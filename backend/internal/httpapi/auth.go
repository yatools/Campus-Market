package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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

const maxJSONBodyBytes int64 = 2 << 20

func decodeBody(r *http.Request, value any) *APIError {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return validation("content_type", "Content-Type must be application/json")
	}
	limited := &io.LimitedReader{R: r.Body, N: maxJSONBodyBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return &APIError{Status: 422, Code: "VALIDATION_ERROR", Message: "提交内容不符合要求", FieldErrors: map[string]string{"request": "请求正文无效"}}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return validation("request", "Request body must contain one JSON value")
	}
	if limited.N == 0 {
		return validation("request", "Request body exceeds the maximum size")
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

// rateLimit enforces a counter in its own committed transaction so the increment
// survives even when the surrounding request later fails (e.g. a wrong password
// or a wrong verification code). Doing the check inside the request transaction
// lets the failure path's rollback discard the increment, which would make the
// throttle count only successful attempts — useless against brute force.
func (s *Server) rateLimit(ctx context.Context, action, subject string, limit, minutes int) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := domain.CheckRateLimit(ctx, tx, action, subject, limit, minutes); err != nil {
		if err == domain.ErrRateLimited {
			return apiError(http.StatusTooManyRequests, "RATE_LIMITED", "操作过于频繁，请稍后再试")
		}
		return err
	}
	return tx.Commit(ctx)
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
	if runeLen(body.Email) < 5 || runeLen(body.Email) > 320 {
		return validation("email", "String should have at least 5 characters")
	}
	if body.Purpose != "register" && body.Purpose != "reset_password" && body.Purpose != "change_email" {
		return validation("purpose", "Value error, 不支持的验证码用途")
	}
	email, apiErr := s.campusEmail(body.Email)
	if apiErr != nil {
		return apiErr
	}
	// change_email codes are only ever consumed by an authenticated /me handler, so an
	// anonymous caller has no business minting them. Leaving it open turned this endpoint
	// into a mail relay that would send an official-looking message to any campus address.
	if body.Purpose == "change_email" {
		user, _, err := s.participatingUser(w, r)
		if err != nil {
			return err
		}
		if user.Email != nil && email == *user.Email {
			return validation("email", "新邮箱不能与当前邮箱相同")
		}
	}
	// Throttle before touching the users table. Both the existence probe and the early
	// returns below are observable, so they must sit behind the same limiter as the send
	// path; otherwise the 409/202 split (and the 429 it eventually produces) enumerates
	// which campus addresses are registered.
	if err := s.rateLimit(r.Context(), "email_code_ip", body.Purpose+":"+clientIP(r), 200, 60); err != nil {
		return err
	}
	if err := s.rateLimit(r.Context(), "email_code_email", body.Purpose+":"+email, 5, 60); err != nil {
		return err
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
	accepted := map[string]any{"accepted": true, "resend_after": 60}
	if body.Purpose == "reset_password" && !exists {
		// Same response as the success path: never confirm whether an address is registered.
		writeJSON(w, 202, accepted)
		return nil
	}
	if body.Purpose == "register" && exists {
		// Also uniform, but tell the real owner of the mailbox what happened instead of
		// leaving them with a code that never arrives.
		if err := domain.EnqueueEmail(r.Context(), tx, email, "【梧桐墙】该邮箱已注册",
			"有人用这个邮箱申请注册梧桐墙，但它已经注册过了。如果是你本人，请直接登录；忘记密码请使用「找回密码」。"); err != nil {
			return err
		}
		if err := tx.Commit(r.Context()); err != nil {
			return err
		}
		writeJSON(w, 202, accepted)
		return nil
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
	writeJSON(w, 202, accepted)
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
	if rawRuneLen(body.Password) < 10 || rawRuneLen(body.Password) > 128 {
		fields["password"] = "String should have at least 10 characters"
	}
	if runeLen(body.Nickname) < 2 || runeLen(body.Nickname) > 20 {
		fields["nickname"] = "String should have at least 2 characters"
	}
	body.AgreedTermsVersion = strings.TrimSpace(body.AgreedTermsVersion)
	if body.AgreedTermsVersion == "" || runeLen(body.AgreedTermsVersion) > 30 {
		fields["agreed_terms_version"] = "Field required"
	}
	if len(fields) > 0 {
		return validationFields(fields)
	}
	email, apiErr := s.campusEmail(body.Email)
	if apiErr != nil {
		return apiErr
	}
	if err := s.rateLimit(r.Context(), "register", clientIP(r), 100, 60); err != nil {
		return err
	}
	if err := s.guardCode(r.Context(), email, "register"); err != nil {
		return err
	}
	// Hash before opening the transaction: Argon2id is deliberately slow and memory hungry,
	// and holding a pool connection for its duration is what turns a login/signup burst
	// into pool exhaustion.
	passwordHash, err := security.HashPassword(body.Password)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
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
	credit, err := domain.CreditValue(r.Context(), tx, "baseline.initial_credit")
	if err != nil {
		return err
	}
	var user User
	// Aliases are random 6-digit handles behind a UNIQUE constraint, so collisions become
	// common as the site grows (~1 in 180 at 5k users). Retry on a fresh alias instead of
	// reporting the misleading "email or nickname already taken" and failing the signup.
	//
	// Each attempt runs in a nested transaction: a unique-violation aborts the enclosing
	// transaction, and every later statement in it fails with 25P02 until it is rolled
	// back. pgx implements nested Begin as a SAVEPOINT, so only the failed attempt is
	// discarded and the retry can actually succeed.
	for attempt := 0; ; attempt++ {
		alias, aliasErr := newAlias()
		if aliasErr != nil {
			return aliasErr
		}
		attemptTx, beginErr := tx.Begin(r.Context())
		if beginErr != nil {
			return beginErr
		}
		err = attemptTx.QueryRow(r.Context(), `INSERT INTO users(email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,dm_stranger_off,hide_online,verified_at,created_at,updated_at)
			VALUES($1,$2,$3,$4,'student','user','active',$5,0,false,false,now(),now(),now())
			RETURNING id,email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,avatar_path,dm_stranger_off,hide_online,verified_at,created_at`,
			email, passwordHash, strings.TrimSpace(body.Nickname), alias, credit).Scan(userScan(&user)...)
		if err == nil {
			if err = attemptTx.Commit(r.Context()); err != nil {
				return err
			}
			break
		}
		_ = attemptTx.Rollback(r.Context())
		var pgErr *pgconn.PgError
		if ok := errorAs(err, &pgErr); ok && pgErr.Code == "23505" {
			if attempt < 4 && strings.Contains(pgErr.ConstraintName, "alias") {
				continue
			}
			return apiError(409, "ACCOUNT_CONFLICT", "邮箱或昵称已被使用")
		}
		return err
	}
	raw, session, err := s.createSession(r.Context(), tx, user.ID, r)
	if err != nil {
		return err
	}
	_ = domain.Notify(r.Context(), tx, user.ID, "欢迎加入梧桐墙", "校园邮箱已验证。请先阅读社区规范，再开始参与讨论。", "/me", "system")
	actor := user.ID
	_ = domain.Audit(r.Context(), tx, &actor, "account.register", "user", strconv.FormatInt(user.ID, 10), "", nil, map[string]any{"terms": body.AgreedTermsVersion}, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	// Cookies only after the session row is committed (see login for the rationale).
	s.setSessionCookies(w, raw, session)
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
	if strings.TrimSpace(body.Password) == "" || rawRuneLen(body.Password) > 128 {
		return validation("password", "String should have at least 1 character")
	}
	// rate_limit_counters.subject is VARCHAR(320); without this bound an over-long address
	// makes the throttle INSERT fail with 22001 and surfaces as a 500 instead of a 401.
	if runeLen(body.Email) < 5 || runeLen(body.Email) > 320 {
		return apiError(401, "INVALID_CREDENTIALS", "邮箱或密码错误")
	}
	email := domain.NormalizeEmail(body.Email)
	// Rate-limit counters are committed independently so failed attempts persist;
	// counting inside the request transaction let the 401 rollback discard them,
	// leaving online password brute force effectively unthrottled.
	if err := s.rateLimit(r.Context(), "login_ip", clientIP(r), 300, 15); err != nil {
		return err
	}
	if err := s.rateLimit(r.Context(), "login_email", email, 10, 15); err != nil {
		return err
	}
	// Look the account up on a pooled connection and verify the password before opening a
	// transaction. Argon2id here costs ~64MiB and tens of milliseconds; doing it inside a
	// transaction pinned a pool connection for the whole computation, so a few dozen
	// concurrent logins could exhaust the pool and stall every other request.
	user, err := scanUser(s.DB.QueryRow(r.Context(), `SELECT id,email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,avatar_path,dm_stranger_off,hide_online,verified_at,created_at FROM users WHERE email=$1`, email))
	if err == pgx.ErrNoRows {
		// Spend the same work on a decoy hash. Skipping verification for unknown accounts
		// made the response time differ by an order of magnitude, which enumerates accounts
		// just as effectively as a distinct status code would.
		security.VerifyPassword(body.Password, decoyPasswordHash())
		return apiError(401, "INVALID_CREDENTIALS", "邮箱或密码错误")
	}
	if err != nil {
		return err
	}
	if !security.VerifyPassword(body.Password, user.PasswordHash) {
		return apiError(401, "INVALID_CREDENTIALS", "邮箱或密码错误")
	}
	if user.Status == "disabled" || user.Status == "deleted" {
		return apiError(403, "ACCOUNT_DISABLED", "账号已停用")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	raw, session, err := s.createSession(r.Context(), tx, user.ID, r)
	if err != nil {
		return err
	}
	actor := user.ID
	_ = domain.Audit(r.Context(), tx, &actor, "account.login", "session", strconv.FormatInt(session.ID, 10), "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	// Only hand out cookies once the session row is durable; setting them earlier meant a
	// failed commit still shipped a session the database had never heard of, and every
	// later state-changing request then failed CSRF validation.
	s.setSessionCookies(w, raw, session)
	writeJSON(w, 200, map[string]any{"user": userPayload(user), "csrf_token": session.CSRFToken})
	return nil
}

// decoyPasswordHash is an Argon2id hash of a random secret, computed once, used purely to
// equalise the cost of a login attempt against a non-existent account.
var decoyPasswordHash = sync.OnceValue(func() string {
	secret, err := randomDigits(32)
	if err != nil {
		secret = "decoy-password-placeholder"
	}
	hash, err := security.HashPassword(secret)
	if err != nil {
		return ""
	}
	return hash
})

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
	if rawRuneLen(body.NewPassword) < 10 || rawRuneLen(body.NewPassword) > 128 {
		return validation("new_password", "String should have at least 10 characters")
	}
	email, apiErr := s.campusEmail(body.Email)
	if apiErr != nil {
		return apiErr
	}
	// Throttle reset attempts so the single 6-digit code (valid 10 minutes) cannot
	// be brute-forced over its ~10^6 space. Counters persist across failed guesses.
	if err := s.rateLimit(r.Context(), "reset_password_ip", clientIP(r), 60, 60); err != nil {
		return err
	}
	if err := s.rateLimit(r.Context(), "reset_password_email", email, 8, 60); err != nil {
		return err
	}
	if err := s.guardCode(r.Context(), email, "reset_password"); err != nil {
		return err
	}
	// Hash outside the transaction (see register).
	hash, err := security.HashPassword(body.NewPassword)
	if err != nil {
		return err
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

// maxCodeAttempts bounds guesses against a single issued verification code.
const maxCodeAttempts = 5

// guardCode charges one attempt against the code currently outstanding for this
// email/purpose, in a transaction of its own so the count survives the caller's rollback.
// Without it a wrong guess left no trace — the 400 path rolls everything back — so a single
// 6-digit code could be walked through its whole 10^6 space during its 10-minute lifetime.
//
// It must be called *before* the caller opens its own transaction: acquiring a second pool
// connection while holding one risks deadlocking the pool under load.
func (s *Server) guardCode(ctx context.Context, email, purpose string) error {
	var candidate int64
	err := s.DB.QueryRow(ctx, `SELECT id FROM verification_codes WHERE email=$1 AND purpose=$2 AND consumed_at IS NULL ORDER BY id DESC LIMIT 1`, email, purpose).Scan(&candidate)
	if err == pgx.ErrNoRows {
		return apiError(400, "INVALID_CODE", "验证码错误或已过期")
	}
	if err != nil {
		return err
	}
	return s.rateLimit(ctx, "verify_code", strconv.FormatInt(candidate, 10), maxCodeAttempts, 60)
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
	currentTokenHash := security.TokenHash(s.Config.SecretKey, cookie.Value)
	row := s.DB.QueryRow(r.Context(), `SELECT s.id,s.user_id,s.csrf_token,s.expires_at,s.absolute_expires_at,s.last_seen_at,
		u.id,u.email,u.password_hash,u.nickname,u.alias,u.campus_identity,u.role,u.status,u.credit,u.xp,u.avatar_path,u.dm_stranger_off,u.hide_online,u.verified_at,u.created_at
		FROM sessions s JOIN users u ON u.id=s.user_id
		WHERE (s.token_hash=$1 OR (s.previous_token_hash=$1 AND s.previous_token_expires_at>now()))
		  AND s.revoked_at IS NULL AND s.expires_at>now() AND s.absolute_expires_at>now()`, currentTokenHash)
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
		expires := time.Now().UTC().Add(s.Config.SessionSliding)
		if expires.After(session.AbsoluteExpiresAt) {
			expires = session.AbsoluteExpiresAt
		}
		tag, updateErr := s.DB.Exec(r.Context(), `UPDATE sessions
			SET previous_token_hash=token_hash,previous_token_expires_at=now()+interval '30 seconds',
				token_hash=$1,last_seen_at=now(),expires_at=$2
			WHERE id=$3 AND token_hash=$4`, security.TokenHash(s.Config.SecretKey, raw), expires, session.ID, currentTokenHash)
		if updateErr != nil {
			return User{}, Session{}, updateErr
		}
		if tag.RowsAffected() == 1 {
			session.ExpiresAt, session.LastSeenAt = expires, time.Now().UTC()
			s.setSessionCookies(w, raw, session)
			return user, session, nil
		}
		// Another response won the rotation race. It owns the browser cookie update.
		err = s.DB.QueryRow(r.Context(), `SELECT csrf_token,expires_at,absolute_expires_at,last_seen_at
			FROM sessions WHERE id=$1 AND user_id=$2 AND revoked_at IS NULL AND expires_at>now() AND absolute_expires_at>now()`, session.ID, user.ID).
			Scan(&session.CSRFToken, &session.ExpiresAt, &session.AbsoluteExpiresAt, &session.LastSeenAt)
		if err != nil {
			return User{}, Session{}, err
		}
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
func runeLen(v string) int    { return len([]rune(strings.TrimSpace(v))) }
func rawRuneLen(v string) int { return len([]rune(v)) }

// truncate cuts on a UTF-8 boundary. Slicing raw bytes could split a multi-byte rune, and
// the resulting invalid sequence is rejected by PostgreSQL — a long Chinese User-Agent was
// enough to make every login and signup from that client fail with a 500.
func truncate(v string, n int) string {
	if len(v) <= n {
		return v
	}
	for n > 0 && !utf8.RuneStart(v[n]) {
		n--
	}
	return strings.ToValidUTF8(v[:n], "")
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
