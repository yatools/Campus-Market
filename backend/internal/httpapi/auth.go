package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	domain "github.com/yatools/wutong-campus-wall/backend/internal/app"
	"github.com/yatools/wutong-campus-wall/backend/internal/security"
)

type User struct {
	ID             int64
	Email          *string
	PasswordHash   string
	Nickname       string
	Alias          string
	CampusIdentity string
	Role           string
	Status         string
	Credit         int
	XP             int
	AvatarPath     *string
	DMStrangerOff  bool
	HideOnline     bool
	VerifiedAt     time.Time
	CreatedAt      time.Time
}

type Session struct {
	ID                int64
	UserID            int64
	CSRFToken         string
	ExpiresAt         time.Time
	AbsoluteExpiresAt time.Time
	LastSeenAt        time.Time
}

func (s *Server) registerRoutes(r chi.Router) {
	r.Route("/auth", func(auth chi.Router) {
		auth.Post("/request-code", s.handle(s.requestCode))
		auth.Post("/register", s.handle(s.register))
		auth.Post("/login", s.handle(s.login))
		auth.Post("/reset-password", s.handle(s.resetPassword))
		auth.Post("/logout", s.handle(s.logout))
		auth.Post("/logout-all", s.handle(s.logoutAll))
	})
	s.registerContentRoutes(r)
	s.registerTeamRoutes(r)
	s.registerModuleRoutes(r)
	s.registerGovernanceRoutes(r)
	s.registerMeAdminRoutes(r)
}

func decodeBody(r *http.Request, value any) *APIError {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	if err := decoder.Decode(value); err != nil {
		return &APIError{Status: 422, Code: "VALIDATION_ERROR", Message: "提交内容不符合要求", FieldErrors: map[string]string{"request": "请求正文无效"}}
	}
	return nil
}

func clientIP(r *http.Request) string {
	if value, ok := r.Context().Value(clientIPKey).(string); ok && value != "" {
		return value
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) campusEmail(value string) (string, *APIError) {
	email := domain.NormalizeEmail(value)
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return "", apiError(400, "CAMPUS_EMAIL_REQUIRED", "请使用学校邮箱注册")
	}
	if _, ok := s.Config.AllowedCampusEmailDomains[parts[1]]; !ok {
		return "", apiError(400, "CAMPUS_EMAIL_REQUIRED", "请使用学校邮箱注册")
	}
	return email, nil
}

func (s *Server) requestCode(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Email   string `json:"email"`
		Purpose string `json:"purpose"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Purpose == "" {
		body.Purpose = "register"
	}
	if len(body.Email) < 5 || len(body.Email) > 320 {
		return validation("email", "String should have at least 5 characters")
	}
	if body.Purpose != "register" && body.Purpose != "reset_password" && body.Purpose != "change_email" {
		return validation("purpose", "Value error, 不支持的验证码用途")
	}
	email, apiErr := s.campusEmail(body.Email)
	if apiErr != nil {
		return apiErr
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var exists bool
	if err := tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)", email).Scan(&exists); err != nil {
		return err
	}
	if body.Purpose == "register" && exists {
		return apiError(409, "EMAIL_EXISTS", "该邮箱已注册")
	}
	if body.Purpose == "reset_password" && !exists {
		writeJSON(w, 202, map[string]any{"accepted": true, "resend_after": 60})
		return nil
	}
	if err := domain.CheckRateLimit(r.Context(), tx, "email_code_email", body.Purpose+":"+email, 5, 60); err != nil {
		if err == domain.ErrRateLimited {
			return apiError(429, "RATE_LIMITED", "操作过于频繁，请稍后再试")
		}
		return err
	}
	if err := domain.CheckRateLimit(r.Context(), tx, "email_code_ip", body.Purpose+":"+clientIP(r), 200, 60); err != nil {
		if err == domain.ErrRateLimited {
			return apiError(429, "RATE_LIMITED", "操作过于频繁，请稍后再试")
		}
		return err
	}
	code, err := randomDigits(6)
	if err != nil {
		return err
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO verification_codes(email,purpose,code_hash,ip_address,expires_at,created_at)
		VALUES($1,$2,$3,$4,now()+interval '10 minute',now())`, email, body.Purpose, security.CodeHash(s.Config.SecretKey, email, body.Purpose, code), clientIP(r))
	if err != nil {
		return err
	}
	purposeText := map[string]string{"register": "注册", "reset_password": "重置密码", "change_email": "更换邮箱"}[body.Purpose]
	if err := domain.EnqueueEmail(r.Context(), tx, email, "【梧桐墙】"+purposeText+"验证码", "你的验证码是 "+code+"，10 分钟内有效。请勿转发。"); err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 202, map[string]any{"accepted": true, "resend_after": 60})
	return nil
}

var sixDigits = regexp.MustCompile(`^\d{6}$`)

func (s *Server) register(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Email              string `json:"email"`
		Code               string `json:"code"`
		Password           string `json:"password"`
		Nickname           string `json:"nickname"`
		AgreedTermsVersion string `json:"agreed_terms_version"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	fields := map[string]string{}
	if !sixDigits.MatchString(body.Code) {
		fields["code"] = "String should match pattern '^\\d{6}$'"
	}
	if len(body.Password) < 10 || len(body.Password) > 128 {
		fields["password"] = "String should have at least 10 characters"
	}
	if runeLen(strings.TrimSpace(body.Nickname)) < 2 || runeLen(strings.TrimSpace(body.Nickname)) > 20 {
		fields["nickname"] = "String should have at least 2 characters"
	}
	if body.AgreedTermsVersion == "" || len(body.AgreedTermsVersion) > 30 {
		fields["agreed_terms_version"] = "Field required"
	}
	if len(fields) > 0 {
		return validationFields(fields)
	}
	email, apiErr := s.campusEmail(body.Email)
	if apiErr != nil {
		return apiErr
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if err := domain.CheckRateLimit(r.Context(), tx, "register", clientIP(r), 100, 60); err != nil {
		if err == domain.ErrRateLimited {
			return apiError(429, "RATE_LIMITED", "操作过于频繁，请稍后再试")
		}
		return err
	}
	if err := s.consumeCode(r.Context(), tx, email, "register", body.Code); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)", email).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return apiError(409, "EMAIL_EXISTS", "该邮箱已注册")
	}
	passwordHash, err := security.HashPassword(body.Password)
	if err != nil {
		return err
	}
	alias, err := newAlias()
	if err != nil {
		return err
	}
	credit, err := domain.CreditValue(r.Context(), tx, "baseline.initial_credit")
	if err != nil {
		return err
	}
	var user User
	err = tx.QueryRow(r.Context(), `INSERT INTO users(email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,dm_stranger_off,hide_online,verified_at,created_at,updated_at)
		VALUES($1,$2,$3,$4,'student','user','active',$5,0,false,false,now(),now(),now())
		RETURNING id,email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,avatar_path,dm_stranger_off,hide_online,verified_at,created_at`,
		email, passwordHash, strings.TrimSpace(body.Nickname), alias, credit).Scan(userScan(&user)...)
	if err != nil {
		var pgErr *pgconn.PgError
		if ok := errorAs(err, &pgErr); ok && pgErr.Code == "23505" {
			return apiError(409, "ACCOUNT_CONFLICT", "邮箱或昵称已被使用")
		}
		return err
	}
	raw, session, err := s.createSession(r.Context(), tx, user.ID, r)
	if err != nil {
		return err
	}
	s.setSessionCookies(w, raw, session)
	_ = domain.Notify(r.Context(), tx, user.ID, "欢迎加入梧桐墙", "校园邮箱已验证。请先阅读社区规范，再开始参与讨论。", "/me", "system")
	actor := user.ID
	_ = domain.Audit(r.Context(), tx, &actor, "account.register", "user", strconv.FormatInt(user.ID, 10), "", nil, map[string]any{"terms": body.AgreedTermsVersion}, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"user": userPayload(user), "csrf_token": session.CSRFToken})
	return nil
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Password == "" || len(body.Password) > 128 {
		return validation("password", "String should have at least 1 character")
	}
	email := domain.NormalizeEmail(body.Email)
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	for _, limit := range []struct {
		action, subject string
		count, minutes  int
	}{{"login_ip", clientIP(r), 300, 15}, {"login_email", email, 10, 15}} {
		if err := domain.CheckRateLimit(r.Context(), tx, limit.action, limit.subject, limit.count, limit.minutes); err != nil {
			if err == domain.ErrRateLimited {
				return apiError(429, "RATE_LIMITED", "操作过于频繁，请稍后再试")
			}
			return err
		}
	}
	user, err := scanUser(tx.QueryRow(r.Context(), `SELECT id,email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,avatar_path,dm_stranger_off,hide_online,verified_at,created_at FROM users WHERE email=$1`, email))
	if err == pgx.ErrNoRows || (err == nil && !security.VerifyPassword(body.Password, user.PasswordHash)) {
		return apiError(401, "INVALID_CREDENTIALS", "邮箱或密码错误")
	}
	if err != nil {
		return err
	}
	if user.Status == "disabled" || user.Status == "deleted" {
		return apiError(403, "ACCOUNT_DISABLED", "账号已停用")
	}
	raw, session, err := s.createSession(r.Context(), tx, user.ID, r)
	if err != nil {
		return err
	}
	s.setSessionCookies(w, raw, session)
	actor := user.ID
	_ = domain.Audit(r.Context(), tx, &actor, "account.login", "session", strconv.FormatInt(session.ID, 10), "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"user": userPayload(user), "csrf_token": session.CSRFToken})
	return nil
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Email       string `json:"email"`
		Code        string `json:"code"`
		NewPassword string `json:"new_password"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if !sixDigits.MatchString(body.Code) {
		return validation("code", "String should match pattern '^\\d{6}$'")
	}
	if len(body.NewPassword) < 10 || len(body.NewPassword) > 128 {
		return validation("new_password", "String should have at least 10 characters")
	}
	email, apiErr := s.campusEmail(body.Email)
	if apiErr != nil {
		return apiErr
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var userID int64
	if err := tx.QueryRow(r.Context(), "SELECT id FROM users WHERE email=$1", email).Scan(&userID); err == pgx.ErrNoRows {
		return apiError(400, "INVALID_CODE", "验证码错误或已过期")
	} else if err != nil {
		return err
	}
	if err := s.consumeCode(r.Context(), tx, email, "reset_password", body.Code); err != nil {
		return err
	}
	hash, err := security.HashPassword(body.NewPassword)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "UPDATE users SET password_hash=$1,updated_at=now() WHERE id=$2", hash, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "UPDATE sessions SET revoked_at=now() WHERE user_id=$1", userID); err != nil {
		return err
	}
	_ = domain.Notify(r.Context(), tx, userID, "密码已重置", "所有设备的登录已失效；如非本人操作，请联系管理员。", "", "system")
	actor := userID
	_ = domain.Audit(r.Context(), tx, &actor, "account.password_reset", "user", strconv.FormatInt(userID, 10), "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	_, session, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	if _, err := s.DB.Exec(r.Context(), "UPDATE sessions SET revoked_at=now() WHERE id=$1", session.ID); err != nil {
		return err
	}
	s.clearSessionCookies(w)
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (s *Server) logoutAll(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), "UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL", user.ID); err != nil {
		return err
	}
	actor := user.ID
	_ = domain.Audit(r.Context(), tx, &actor, "account.logout_all", "user", strconv.FormatInt(user.ID, 10), "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	s.clearSessionCookies(w)
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (s *Server) consumeCode(ctx context.Context, tx pgx.Tx, email, purpose, code string) error {
	var id int64
	var codeHash string
	var expires time.Time
	err := tx.QueryRow(ctx, `SELECT id,code_hash,expires_at FROM verification_codes WHERE email=$1 AND purpose=$2 AND consumed_at IS NULL ORDER BY id DESC LIMIT 1 FOR UPDATE`, email, purpose).Scan(&id, &codeHash, &expires)
	if err == pgx.ErrNoRows || err == nil && (expires.Before(time.Now().UTC()) || subtle.ConstantTimeCompare([]byte(codeHash), []byte(security.CodeHash(s.Config.SecretKey, email, purpose, code))) != 1) {
		return apiError(400, "INVALID_CODE", "验证码错误或已过期")
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE verification_codes SET consumed_at=now() WHERE id=$1", id)
	return err
}

func (s *Server) createSession(ctx context.Context, tx pgx.Tx, userID int64, r *http.Request) (string, Session, error) {
	raw, err := security.RandomToken(48)
	if err != nil {
		return "", Session{}, err
	}
	csrf, err := security.RandomToken(32)
	if err != nil {
		return "", Session{}, err
	}
	now := time.Now().UTC()
	absolute := now.Add(s.Config.SessionAbsolute)
	expires := now.Add(s.Config.SessionSliding)
	if expires.After(absolute) {
		expires = absolute
	}
	var session Session
	err = tx.QueryRow(ctx, `INSERT INTO sessions(user_id,token_hash,csrf_token,ip_address,user_agent,expires_at,absolute_expires_at,last_seen_at,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$8) RETURNING id,user_id,csrf_token,expires_at,absolute_expires_at,last_seen_at`,
		userID, security.TokenHash(s.Config.SecretKey, raw), csrf, clientIP(r), truncate(r.UserAgent(), 500), expires, absolute, now).
		Scan(&session.ID, &session.UserID, &session.CSRFToken, &session.ExpiresAt, &session.AbsoluteExpiresAt, &session.LastSeenAt)
	return raw, session, err
}

func (s *Server) currentUser(w http.ResponseWriter, r *http.Request, required bool) (User, Session, error) {
	cookie, err := r.Cookie(s.Config.SessionCookieName)
	if err != nil {
		if required {
			return User{}, Session{}, apiError(401, "AUTH_REQUIRED", "请先登录")
		}
		return User{}, Session{}, nil
	}
	row := s.DB.QueryRow(r.Context(), `SELECT s.id,s.user_id,s.csrf_token,s.expires_at,s.absolute_expires_at,s.last_seen_at,
		u.id,u.email,u.password_hash,u.nickname,u.alias,u.campus_identity,u.role,u.status,u.credit,u.xp,u.avatar_path,u.dm_stranger_off,u.hide_online,u.verified_at,u.created_at
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE s.token_hash=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND s.absolute_expires_at>now()`, security.TokenHash(s.Config.SecretKey, cookie.Value))
	var session Session
	var user User
	scanArgs := []any{&session.ID, &session.UserID, &session.CSRFToken, &session.ExpiresAt, &session.AbsoluteExpiresAt, &session.LastSeenAt}
	scanArgs = append(scanArgs, userScan(&user)...)
	err = row.Scan(scanArgs...)
	if err == pgx.ErrNoRows {
		if required {
			return User{}, Session{}, apiError(401, "SESSION_EXPIRED", "登录已过期，请重新登录")
		}
		return User{}, Session{}, nil
	}
	if err != nil {
		return User{}, Session{}, err
	}
	if user.Status == "disabled" || user.Status == "deleted" {
		return User{}, Session{}, apiError(403, "ACCOUNT_DISABLED", "账号已停用")
	}
	if session.LastSeenAt.Before(time.Now().UTC().Add(-s.Config.SessionRotation)) {
		raw, tokenErr := security.RandomToken(48)
		if tokenErr != nil {
			return User{}, Session{}, tokenErr
		}
		csrf, tokenErr := security.RandomToken(32)
		if tokenErr != nil {
			return User{}, Session{}, tokenErr
		}
		expires := time.Now().UTC().Add(s.Config.SessionSliding)
		if expires.After(session.AbsoluteExpiresAt) {
			expires = session.AbsoluteExpiresAt
		}
		_, err = s.DB.Exec(r.Context(), "UPDATE sessions SET token_hash=$1,csrf_token=$2,last_seen_at=now(),expires_at=$3 WHERE id=$4", security.TokenHash(s.Config.SecretKey, raw), csrf, expires, session.ID)
		if err != nil {
			return User{}, Session{}, err
		}
		session.CSRFToken, session.ExpiresAt, session.LastSeenAt = csrf, expires, time.Now().UTC()
		s.setSessionCookies(w, raw, session)
	}
	return user, session, nil
}

func (s *Server) participatingUser(w http.ResponseWriter, r *http.Request) (User, Session, error) {
	user, session, err := s.currentUser(w, r, true)
	if err == nil && user.Status == "restricted" {
		err = apiError(403, "ACCOUNT_RESTRICTED", "账号当前处于限权状态，不能发布或互动")
	}
	return user, session, err
}

func (s *Server) moderatorUser(w http.ResponseWriter, r *http.Request) (User, error) {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return User{}, err
	}
	if user.Status != "active" {
		return User{}, apiError(403, "ACCOUNT_RESTRICTED", "账号当前状态不能执行审核操作")
	}
	if user.Role != "moderator" && user.Role != "admin" {
		return User{}, apiError(403, "MODERATOR_REQUIRED", "需要审核员权限")
	}
	return user, nil
}

func (s *Server) adminUser(w http.ResponseWriter, r *http.Request) (User, error) {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return User{}, err
	}
	if user.Status != "active" {
		return User{}, apiError(403, "ACCOUNT_RESTRICTED", "账号当前状态不能执行管理操作")
	}
	if user.Role != "admin" {
		return User{}, apiError(403, "ADMIN_REQUIRED", "需要管理员权限")
	}
	return user, nil
}

func (s *Server) setSessionCookies(w http.ResponseWriter, raw string, session Session) {
	maxAge := int(time.Until(session.AbsoluteExpiresAt).Seconds())
	if maxAge < 0 {
		maxAge = 0
	}
	http.SetCookie(w, &http.Cookie{Name: s.Config.SessionCookieName, Value: raw, Path: "/", MaxAge: maxAge, HttpOnly: true, Secure: s.Config.CookieSecure, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: s.Config.CSRFCookieName, Value: session.CSRFToken, Path: "/", MaxAge: maxAge, HttpOnly: false, Secure: s.Config.CookieSecure, SameSite: http.SameSiteLaxMode})
}

func (s *Server) clearSessionCookies(w http.ResponseWriter) {
	for _, name := range []string{s.Config.SessionCookieName, s.Config.CSRFCookieName} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(1, 0), HttpOnly: name == s.Config.SessionCookieName, Secure: s.Config.CookieSecure, SameSite: http.SameSiteLaxMode})
	}
}

func userScan(u *User) []any {
	return []any{&u.ID, &u.Email, &u.PasswordHash, &u.Nickname, &u.Alias, &u.CampusIdentity, &u.Role, &u.Status, &u.Credit, &u.XP, &u.AvatarPath, &u.DMStrangerOff, &u.HideOnline, &u.VerifiedAt, &u.CreatedAt}
}
func scanUser(row pgx.Row) (User, error) { var u User; err := row.Scan(userScan(&u)...); return u, err }

func userPayload(u User) map[string]any {
	var avatar any
	if u.AvatarPath != nil && *u.AvatarPath != "" {
		avatar = "/uploads/" + *u.AvatarPath
	}
	return map[string]any{"id": u.ID, "email": u.Email, "nickname": u.Nickname, "alias": u.Alias, "campus_identity": u.CampusIdentity, "role": u.Role, "status": u.Status, "credit": u.Credit, "xp": u.XP, "avatar_url": avatar, "dm_stranger_off": u.DMStrangerOff, "hide_online": u.HideOnline, "verified_at": u.VerifiedAt, "created_at": u.CreatedAt}
}

func validation(field, message string) *APIError {
	return validationFields(map[string]string{field: message})
}
func validationFields(fields map[string]string) *APIError {
	return &APIError{Status: 422, Code: "VALIDATION_ERROR", Message: "提交内容不符合要求", FieldErrors: fields}
}
func runeLen(v string) int { return len([]rune(v)) }
func truncate(v string, n int) string {
	if len(v) <= n {
		return v
	}
	return v[:n]
}

func randomDigits(length int) (string, error) {
	max := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(length)), nil)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%0*d", length, n), nil
}
func newAlias() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(900000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("梧桐#%06d", n.Int64()+100000), nil
}

func errorAs(err error, target any) bool {
	switch t := target.(type) {
	case **pgconn.PgError:
		if e, ok := err.(*pgconn.PgError); ok {
			*t = e
			return true
		}
	}
	return false
}
