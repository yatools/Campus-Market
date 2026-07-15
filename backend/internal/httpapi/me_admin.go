package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/yatools/wutong-campus-wall/backend/internal/security"
)

func (s *Server) registerMeAdminRoutes(r chi.Router) {
	r.Get("/me", s.handle(s.me))
	r.Patch("/me/profile", s.handle(s.updateProfile))
	r.Patch("/me/privacy", s.handle(s.updatePrivacy))
	r.Post("/me/password", s.handle(s.changePassword))
	r.Post("/me/email", s.handle(s.changeEmail))
	r.Get("/me/sessions", s.handle(s.mySessions))
	r.Delete("/me/sessions/{sessionID}", s.handle(s.revokeSession))
	r.Get("/me/content", s.handle(s.myContent))
	r.Get("/me/favorites", s.handle(s.myFavorites))
	r.Get("/me/reports", s.handle(s.myReports))
	r.Get("/me/appeals", s.handle(s.myAppeals))
	r.Post("/me/deactivate", s.handle(s.deactivateAccount))
	r.Get("/admin/overview", s.handle(s.adminOverview))
	r.Get("/admin/users", s.handle(s.adminUsers))
	r.Patch("/admin/users/{userID}", s.handle(s.adminUpdateUser))
	r.Get("/admin/moderation-cases", s.handle(s.adminModerationCases))
	r.Get("/admin/reports", s.handle(s.adminReports))
	r.Post("/admin/moderation-cases/{caseID}/decision", s.handle(s.adminDecideModeration))
	r.Post("/admin/penalties", s.handle(s.adminCreatePenalty))
	r.Get("/admin/appeals", s.handle(s.adminAppeals))
	r.Post("/admin/appeals/{appealID}/decision", s.handle(s.adminDecideAppeal))
	r.Post("/admin/announcements", s.handle(s.adminCreateAnnouncement))
	r.Get("/admin/settings", s.handle(s.adminSettings))
	r.Put("/admin/settings/{key}", s.handle(s.adminUpdateSetting))
	r.Get("/admin/feedback", s.handle(s.adminFeedback))
	r.Post("/admin/feedback/{feedbackID}/decision", s.handle(s.adminDecideFeedback))
	r.Post("/admin/backups", s.handle(s.adminRequestBackup))
	r.Get("/admin/backups", s.handle(s.adminBackups))
	r.Get("/admin/backups/{jobID}/download", s.handle(s.adminDownloadBackup))
	r.Get("/admin/audit-logs", s.handle(s.adminAuditLogs))
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	p := userPayload(user)
	var unread, sessions int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM notifications WHERE user_id=$1 AND read_at IS NULL", user.ID).Scan(&unread)
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM sessions WHERE user_id=$1 AND revoked_at IS NULL AND expires_at>now()", user.ID).Scan(&sessions)
	p["unread_notifications"] = unread
	p["active_sessions"] = sessions
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := decodeBody(r, &raw); err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if value, ok := raw["nickname"]; ok {
		var x string
		if json.Unmarshal(value, &x) != nil || runeLen(strings.TrimSpace(x)) < 2 || runeLen(strings.TrimSpace(x)) > 20 {
			return validation("nickname", "String should have at least 2 characters")
		}
		user.Nickname = strings.TrimSpace(x)
	}
	if value, ok := raw["alias"]; ok {
		var x string
		if json.Unmarshal(value, &x) != nil {
			return validation("alias", "Input should be a valid string")
		}
		x = strings.TrimSpace(x)
		if runeLen(x) < 2 || runeLen(x) > 20 || containsControl(x) {
			return apiError(400, "ALIAS_INVALID", "固定匿名昵称需为 2–20 个字符，且不能包含换行或控制字符")
		}
		status, reason, _, err := s.moderate(r.Context(), tx, x, false)
		if err != nil {
			return err
		}
		if status != "published" {
			return apiError(400, "ALIAS_REJECTED", firstNonempty(reason, "固定匿名昵称包含不适合公开展示的内容"))
		}
		var exists bool
		_ = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM users WHERE alias=$1 AND id<>$2)", x, user.ID).Scan(&exists)
		if exists {
			return apiError(409, "ALIAS_EXISTS", "这个固定匿名昵称已被使用")
		}
		user.Alias = x
	}
	if value, ok := raw["avatar_attachment_id"]; ok && string(value) != "null" {
		var id int64
		if json.Unmarshal(value, &id) != nil {
			return validation("avatar_attachment_id", "Input should be a valid integer")
		}
		var path, status string
		var owner int64
		if err := tx.QueryRow(r.Context(), "SELECT owner_id,path,status FROM attachments WHERE id=$1 FOR UPDATE", id).Scan(&owner, &path, &status); err != nil || owner != user.ID || status != "pending" {
			return apiError(400, "INVALID_AVATAR", "头像附件无效")
		}
		_, _ = tx.Exec(r.Context(), "UPDATE attachments SET status='avatar' WHERE id=$1", id)
		user.AvatarPath = &path
	}
	_, err = tx.Exec(r.Context(), "UPDATE users SET nickname=$1,alias=$2,avatar_path=$3,updated_at=now() WHERE id=$4", user.Nickname, user.Alias, user.AvatarPath, user.ID)
	if err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "account.profile_update", "user", user.ID, "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, userPayload(user))
	return nil
}
func (s *Server) updatePrivacy(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := decodeBody(r, &raw); err != nil {
		return err
	}
	after := map[string]any{}
	if value, ok := raw["dm_stranger_off"]; ok {
		if json.Unmarshal(value, &user.DMStrangerOff) != nil {
			return validation("dm_stranger_off", "Input should be a valid boolean")
		}
		after["dm_stranger_off"] = user.DMStrangerOff
	}
	if value, ok := raw["hide_online"]; ok {
		if json.Unmarshal(value, &user.HideOnline) != nil {
			return validation("hide_online", "Input should be a valid boolean")
		}
		after["hide_online"] = user.HideOnline
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if _, err := tx.Exec(r.Context(), "UPDATE users SET dm_stranger_off=$1,hide_online=$2,updated_at=now() WHERE id=$3", user.DMStrangerOff, user.HideOnline, user.ID); err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "account.privacy_update", "user", user.ID, "", nil, after, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"dm_stranger_off": user.DMStrangerOff, "hide_online": user.HideOnline})
	return nil
}
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		Old string `json:"old_password"`
		New string `json:"new_password"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if !security.VerifyPassword(body.Old, user.PasswordHash) {
		return apiError(400, "OLD_PASSWORD_INVALID", "原密码错误")
	}
	if len(body.New) < 10 || len(body.New) > 128 {
		return validation("new_password", "String should have at least 10 characters")
	}
	hash, err := security.HashPassword(body.New)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	_, _ = tx.Exec(r.Context(), "UPDATE users SET password_hash=$1,updated_at=now() WHERE id=$2", hash, user.ID)
	_, _ = tx.Exec(r.Context(), "UPDATE sessions SET revoked_at=now() WHERE user_id=$1", user.ID)
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "account.password_change", "user", user.ID, "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "login_required": true})
	return nil
}
func (s *Server) changeEmail(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		Email string `json:"new_email"`
		Code  string `json:"code"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
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
	_ = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM users WHERE email=$1 AND id<>$2)", email, user.ID).Scan(&exists)
	if exists {
		return apiError(409, "EMAIL_EXISTS", "该邮箱已被使用")
	}
	if err := s.consumeCode(r.Context(), tx, email, "change_email", body.Code); err != nil {
		return err
	}
	before := user.Email
	user.Email = &email
	_, _ = tx.Exec(r.Context(), "UPDATE users SET email=$1,verified_at=now(),updated_at=now() WHERE id=$2", email, user.ID)
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "account.email_change", "user", user.ID, "", before, email, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "email": email})
	return nil
}
func (s *Server) mySessions(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM sessions WHERE user_id=$1", user.ID).Scan(&total)
	rows, err := s.DB.Query(r.Context(), "SELECT id,ip_address,user_agent,last_seen_at,expires_at,revoked_at FROM sessions WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3", user.ID, size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id int64
		var ip, agent string
		var seen, expires time.Time
		var revoked *time.Time
		if err := rows.Scan(&id, &ip, &agent, &seen, &expires, &revoked); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "ip_address": ip, "user_agent": agent, "last_seen_at": seen, "expires_at": expires, "revoked": revoked != nil})
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "sessionID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	tag, err := s.DB.Exec(r.Context(), "UPDATE sessions SET revoked_at=COALESCE(revoked_at,now()) WHERE id=$1 AND user_id=$2", id, user.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apiError(404, "SESSION_NOT_FOUND", "会话不存在")
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (s *Server) myContent(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	where := "owner_id=$1"
	args := []any{user.ID}
	if kind := r.URL.Query().Get("type"); kind != "" {
		args = append(args, kind)
		where += fmt.Sprintf(" AND type=$%d", len(args))
	}
	if status := r.URL.Query().Get("status"); status != "" {
		args = append(args, status)
		where += fmt.Sprintf(" AND status=$%d", len(args))
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM content_entities WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(entitySelect+" WHERE %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d", where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var e Entity
		if err := rows.Scan(entityScan(&e)...); err != nil {
			return err
		}
		title, _, err := contentTitlePreview(r.Context(), s.DB, e.ID, e.Type)
		if err != nil {
			return err
		}
		items = append(items, map[string]any{"id": e.ID, "type": e.Type, "title": title, "status": e.Status, "created_at": e.CreatedAt, "updated_at": e.UpdatedAt})
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) myFavorites(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	where := "f.user_id=$1 AND e.status='published'"
	args := []any{user.ID}
	if kind := r.URL.Query().Get("type"); kind != "" {
		args = append(args, kind)
		where += " AND e.type=$2"
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM favorites f JOIN content_entities e ON e.id=f.entity_id WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf("SELECT e.id,e.type,f.created_at FROM favorites f JOIN content_entities e ON e.id=f.entity_id WHERE %s ORDER BY f.created_at DESC LIMIT $%d OFFSET $%d", where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id int64
		var kind string
		var created time.Time
		if err := rows.Scan(&id, &kind, &created); err != nil {
			return err
		}
		title, _, err := contentTitlePreview(r.Context(), s.DB, id, kind)
		if err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "type": kind, "title": title, "favorited_at": created})
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) myReports(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	return s.simpleUserQueue(w, r, "reports", "reporter_id", user.ID, `SELECT id,entity_id,reason,status,created_at FROM reports`, func(rows pgx.Rows) (any, error) {
		var id, entity int64
		var reason, status string
		var created time.Time
		err := rows.Scan(&id, &entity, &reason, &status, &created)
		return map[string]any{"id": id, "entity_id": entity, "reason": reason, "status": status, "created_at": created}, err
	})
}
func (s *Server) myAppeals(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	return s.simpleUserQueue(w, r, "appeals", "user_id", user.ID, `SELECT id,penalty_id,status,admin_note,created_at FROM appeals`, func(rows pgx.Rows) (any, error) {
		var id, penalty int64
		var status, note string
		var created time.Time
		err := rows.Scan(&id, &penalty, &status, &note, &created)
		return map[string]any{"id": id, "penalty_id": penalty, "status": status, "admin_note": note, "created_at": created}, err
	})
}
func (s *Server) simpleUserQueue(w http.ResponseWriter, r *http.Request, table, owner string, userID int64, selectSQL string, scan func(pgx.Rows) (any, error)) error {
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	where := owner + "=$1"
	args := []any{userID}
	if status := r.URL.Query().Get("status"); status != "" {
		args = append(args, status)
		where += " AND status=$2"
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM "+table+" WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf("%s WHERE %s ORDER BY id DESC LIMIT $%d OFFSET $%d", selectSQL, where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return err
		}
		items = append(items, item)
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) deactivateAccount(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct{ Password, Confirmation string }
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Confirmation != "注销我的账号" {
		return apiError(400, "CONFIRMATION_REQUIRED", "请输入“注销我的账号”确认")
	}
	if !security.VerifyPassword(body.Password, user.PasswordHash) {
		return apiError(400, "PASSWORD_INVALID", "密码错误")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	_, _ = tx.Exec(r.Context(), "UPDATE users SET status='disabled',deactivated_at=now(),updated_at=now() WHERE id=$1", user.ID)
	_, _ = tx.Exec(r.Context(), "UPDATE sessions SET revoked_at=now() WHERE user_id=$1", user.ID)
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "account.deactivate", "user", user.ID, "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	s.clearSessionCookies(w)
	writeJSON(w, 200, map[string]any{"ok": true, "anonymize_after_days": 30})
	return nil
}

// Admin dashboard and governance.
func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.moderatorUser(w, r); err != nil {
		return err
	}
	queries := map[string]string{"users": "SELECT count(*) FROM users", "published_content": "SELECT count(*) FROM content_entities WHERE status='published'", "pending_moderation": "SELECT count(*) FROM moderation_cases WHERE status='pending'", "pending_reports": "SELECT count(*) FROM reports WHERE status='pending'", "pending_appeals": "SELECT count(*) FROM appeals WHERE status='pending'", "unread_feedback": "SELECT count(*) FROM feedback WHERE status='pending'", "pending_email": "SELECT count(*) FROM email_outbox WHERE status='pending'", "failed_email": "SELECT count(*) FROM email_outbox WHERE status='failed'"}
	p := map[string]any{}
	for key, query := range queries {
		var count int
		if err := s.DB.QueryRow(r.Context(), query).Scan(&count); err != nil {
			return err
		}
		p[key] = count
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.moderatorUser(w, r); err != nil {
		return err
	}
	page, size, err := pagination(r, 30, 100)
	if err != nil {
		return err
	}
	q := r.URL.Query().Get("q")
	where := "true"
	args := []any{}
	if q != "" {
		args = append(args, "%"+q+"%")
		where = "nickname ILIKE $1 OR email ILIKE $1"
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM users WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(`SELECT id,email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,avatar_path,dm_stranger_off,hide_online,verified_at,created_at FROM users WHERE %s ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var u User
		if err := rows.Scan(userScan(&u)...); err != nil {
			return err
		}
		items = append(items, userPayload(u))
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) error {
	admin, err := s.adminUser(w, r)
	if err != nil {
		return err
	}
	id, _ := pathID(r, "userID")
	var raw map[string]json.RawMessage
	if err := decodeBody(r, &raw); err != nil {
		return err
	}
	var reason string
	if value, ok := raw["reason"]; ok {
		_ = json.Unmarshal(value, &reason)
	}
	if runeLen(strings.TrimSpace(reason)) < 2 || len(reason) > 1000 {
		return validation("reason", "String should have at least 2 characters")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	target, err := scanUser(tx.QueryRow(r.Context(), `SELECT id,email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,avatar_path,dm_stranger_off,hide_online,verified_at,created_at FROM users WHERE id=$1 FOR UPDATE`, id))
	if err != nil {
		return apiError(404, "USER_NOT_FOUND", "用户不存在")
	}
	before := userPayload(target)
	if value, ok := raw["role"]; ok {
		var x string
		_ = json.Unmarshal(value, &x)
		if x != "user" && x != "moderator" && x != "admin" {
			return validation("role", "Value error, 角色无效")
		}
		target.Role = x
	}
	if value, ok := raw["campus_identity"]; ok {
		var x string
		_ = json.Unmarshal(value, &x)
		if x != "student" && x != "alumni" && x != "staff" {
			return validation("campus_identity", "Value error, 校园身份无效")
		}
		target.CampusIdentity = x
	}
	if value, ok := raw["status"]; ok {
		var x string
		_ = json.Unmarshal(value, &x)
		if x != "active" && x != "restricted" && x != "disabled" {
			return validation("status", "Value error, 账号状态无效")
		}
		if target.ID == admin.ID && (x == "disabled" || x == "restricted") {
			return apiError(400, "SELF_LOCKOUT", "不能限制自己的管理员账号")
		}
		target.Status = x
	}
	if value, ok := raw["credit"]; ok {
		var x int
		if json.Unmarshal(value, &x) != nil || x < 0 || x > 1000 {
			return validation("credit", "Input should be between 0 and 1000")
		}
		target.Credit = x
	}
	_, err = tx.Exec(r.Context(), "UPDATE users SET role=$1,campus_identity=$2,status=$3,credit=$4,updated_at=now() WHERE id=$5", target.Role, target.CampusIdentity, target.Status, target.Credit, id)
	if err != nil {
		return err
	}
	if target.Status == "disabled" || target.Role != before["role"] {
		_, _ = tx.Exec(r.Context(), "UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL", id)
	}
	actor := admin.ID
	_ = auditSQL(r.Context(), tx, &actor, "admin.user_update", "user", id, strings.TrimSpace(reason), before, userPayload(target), requestID(r.Context()))
	_ = notifySQL(r.Context(), tx, id, "账号状态已更新", strings.TrimSpace(reason), "/me", "system")
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, userPayload(target))
	return nil
}

func (s *Server) adminModerationCases(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.moderatorUser(w, r); err != nil {
		return err
	}
	page, size, err := pagination(r, 30, 100)
	if err != nil {
		return err
	}
	status := firstNonempty(r.URL.Query().Get("status"), "pending")
	where := "c.status=$1"
	args := []any{status}
	for _, filter := range []struct{ name, column string }{{"source", "c.source"}, {"entity_type", "e.type"}} {
		if value := r.URL.Query().Get(filter.name); value != "" {
			args = append(args, value)
			where += fmt.Sprintf(" AND %s=$%d", filter.column, len(args))
		}
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM moderation_cases c JOIN content_entities e ON e.id=c.entity_id WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(`SELECT c.id,c.entity_id,e.type,c.source,c.status,c.notes,c.created_at FROM moderation_cases c JOIN content_entities e ON e.id=c.entity_id WHERE %s ORDER BY c.created_at LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var caseID, entityID int64
		var kind, source, status, notes string
		var created time.Time
		if err := rows.Scan(&caseID, &entityID, &kind, &source, &status, &notes, &created); err != nil {
			return err
		}
		title, preview, err := contentTitlePreview(r.Context(), s.DB, entityID, kind)
		if err != nil {
			return err
		}
		reportRows, err := s.DB.Query(r.Context(), "SELECT id,reporter_id,reason,detail,created_at FROM reports WHERE entity_id=$1 AND status='pending'", entityID)
		if err != nil {
			return err
		}
		reports := []any{}
		for reportRows.Next() {
			var id, reporter int64
			var reason, detail string
			var at time.Time
			if err := reportRows.Scan(&id, &reporter, &reason, &detail, &at); err != nil {
				return err
			}
			reports = append(reports, map[string]any{"id": id, "reporter_id": reporter, "reason": reason, "detail": detail, "created_at": at})
		}
		reportRows.Close()
		items = append(items, map[string]any{"id": caseID, "entity_id": entityID, "entity_type": kind, "title": title, "preview": preview, "source": source, "status": status, "notes": notes, "reports": reports, "created_at": created})
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) adminReports(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.moderatorUser(w, r); err != nil {
		return err
	}
	page, size, err := pagination(r, 30, 100)
	if err != nil {
		return err
	}
	status := firstNonempty(r.URL.Query().Get("status"), "pending")
	where := "r.status=$1"
	args := []any{status}
	if reason := r.URL.Query().Get("reason"); reason != "" {
		args = append(args, reason)
		where += " AND r.reason=$2"
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM reports r WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(`SELECT r.id,r.entity_id,e.type,r.reporter_id,r.reason,r.detail,r.status,r.created_at FROM reports r JOIN content_entities e ON e.id=r.entity_id WHERE %s ORDER BY r.created_at LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, entity, reporter int64
		var kind, reason, detail, status string
		var created time.Time
		if err := rows.Scan(&id, &entity, &kind, &reporter, &reason, &detail, &status, &created); err != nil {
			return err
		}
		title, preview, err := contentTitlePreview(r.Context(), s.DB, entity, kind)
		if err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "entity_id": entity, "entity_type": kind, "title": title, "preview": preview, "reporter_id": reporter, "reason": reason, "detail": detail, "status": status, "created_at": created})
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) adminDecideModeration(w http.ResponseWriter, r *http.Request) error {
	moderator, err := s.moderatorUser(w, r)
	if err != nil {
		return err
	}
	id, _ := pathID(r, "caseID")
	var body struct {
		Decision, Note string
		Respondent     *int64 `json:"respondent_id"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Decision != "approve" && body.Decision != "reject" && body.Decision != "hide" {
		return validation("decision", "Value error, 审核决定无效")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var entityID int64
	var status string
	if err := tx.QueryRow(r.Context(), "SELECT entity_id,status FROM moderation_cases WHERE id=$1 FOR UPDATE", id).Scan(&entityID, &status); err != nil {
		return apiError(404, "CASE_NOT_FOUND", "审核案件不存在")
	}
	if status != "pending" {
		var decision string
		_ = tx.QueryRow(r.Context(), "SELECT decision FROM moderation_cases WHERE id=$1", id).Scan(&decision)
		writeJSON(w, 200, map[string]any{"id": id, "status": status, "decision": decision})
		return nil
	}
	e, err := getEntityForUpdate(r.Context(), tx, entityID)
	if err != nil {
		return err
	}
	entityStatus := "hidden"
	if body.Decision == "approve" {
		entityStatus = "published"
	}
	_, err = tx.Exec(r.Context(), "UPDATE moderation_cases SET status='resolved',assignee_id=$1,decision=$2,notes=$3,decided_at=now() WHERE id=$4", moderator.ID, body.Decision, body.Note, id)
	if err != nil {
		return err
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET status=$1,updated_at=now() WHERE id=$2", entityStatus, entityID)
	if body.Decision != "approve" {
		var bounty int
		var settled bool
		var accepted *int64
		if tx.QueryRow(r.Context(), "SELECT bounty_xp,bounty_settled,accepted_answer_id FROM questions WHERE entity_id=$1", entityID).Scan(&bounty, &settled, &accepted) == nil && !settled && accepted == nil {
			_, _ = tx.Exec(r.Context(), "UPDATE users SET xp=xp+$1 WHERE id=$2", bounty, e.OwnerID)
			_, _ = tx.Exec(r.Context(), "UPDATE questions SET bounty_settled=true WHERE entity_id=$1", entityID)
		}
	}
	var observeTitle string
	if err := tx.QueryRow(r.Context(), "SELECT title FROM observe_posts WHERE entity_id=$1", entityID).Scan(&observeTitle); err == nil {
		if body.Respondent != nil {
			var active bool
			_ = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active')", *body.Respondent).Scan(&active)
			if !active {
				return apiError(404, "RESPONDENT_NOT_FOUND", "指定回应方不存在")
			}
			_, _ = tx.Exec(r.Context(), "UPDATE observe_posts SET respondent_id=$1 WHERE entity_id=$2", *body.Respondent, entityID)
			_ = notifySQL(r.Context(), tx, *body.Respondent, "你被指定为观察帖回应方", observeTitle, fmt.Sprintf("/observe/%d", entityID), "system")
		}
		_, _ = tx.Exec(r.Context(), "UPDATE observe_posts SET admin_note=$1 WHERE entity_id=$2", body.Note, entityID)
	}
	_, _ = tx.Exec(r.Context(), "UPDATE reports SET status='resolved' WHERE entity_id=$1 AND status='pending'", entityID)
	actor := moderator.ID
	_ = auditSQL(r.Context(), tx, &actor, "moderation.decide", e.Type, e.ID, body.Note, map[string]any{"case_status": "pending", "entity_status": e.Status}, map[string]any{"case_status": "resolved", "entity_status": entityStatus, "decision": body.Decision}, requestID(r.Context()))
	_ = notifySQL(r.Context(), tx, e.OwnerID, "内容审核结果", "审核结果："+body.Decision+"。"+body.Note, fmt.Sprintf("/content/%d", entityID), "system")
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": "resolved", "decision": body.Decision})
	return nil
}
func (s *Server) adminCreatePenalty(w http.ResponseWriter, r *http.Request) error {
	moderator, err := s.moderatorUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		UserID                  int64 `json:"user_id"`
		Violation, Result, Rule string
		Delta                   int `json:"credit_delta"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Delta > 0 || body.Delta < -1000 {
		return validation("credit_delta", "Input should be between -1000 and 0")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var alias string
	var before, after int
	err = tx.QueryRow(r.Context(), "SELECT alias,credit FROM users WHERE id=$1 FOR UPDATE", body.UserID).Scan(&alias, &before)
	if err != nil {
		return apiError(404, "USER_NOT_FOUND", "用户不存在")
	}
	if err = tx.QueryRow(r.Context(), "UPDATE users SET credit=GREATEST(0,LEAST(1000,credit+$1)),updated_at=now() WHERE id=$2 RETURNING credit", body.Delta, body.UserID).Scan(&after); err != nil {
		return err
	}
	mask := "用户 " + lastRunes(alias, 4)
	var id int64
	if err := tx.QueryRow(r.Context(), "INSERT INTO penalties(user_id,public_mask,violation_type,result,rule,created_at) VALUES($1,$2,$3,$4,$5,now()) RETURNING id", body.UserID, mask, body.Violation, body.Result, body.Rule).Scan(&id); err != nil {
		return err
	}
	actor := moderator.ID
	_ = auditSQL(r.Context(), tx, &actor, "penalty.create", "penalty", id, body.Rule, map[string]any{"user_id": body.UserID, "credit": before}, map[string]any{"user_id": body.UserID, "credit": after, "result": body.Result}, requestID(r.Context()))
	_ = notifySQL(r.Context(), tx, body.UserID, "收到治理处理", body.Result, "/governance", "system")
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"id": id, "credit": after})
	return nil
}
func (s *Server) adminAppeals(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.moderatorUser(w, r); err != nil {
		return err
	}
	page, size, err := pagination(r, 30, 100)
	if err != nil {
		return err
	}
	where := "true"
	args := []any{}
	if status := r.URL.Query().Get("status"); status != "" {
		args = append(args, status)
		where = "status=$1"
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM appeals WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf("SELECT id,penalty_id,user_id,reason,status,admin_note FROM appeals WHERE %s ORDER BY created_at LIMIT $%d OFFSET $%d", where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, penalty, user int64
		var reason, status, note string
		if err := rows.Scan(&id, &penalty, &user, &reason, &status, &note); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "penalty_id": penalty, "user_id": user, "reason": reason, "status": status, "admin_note": note})
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) adminDecideAppeal(w http.ResponseWriter, r *http.Request) error {
	moderator, err := s.moderatorUser(w, r)
	if err != nil {
		return err
	}
	id, _ := pathID(r, "appealID")
	var body struct{ Status, Note string }
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Status != "approved" && body.Status != "rejected" {
		return validation("status", "Value error, 申诉决定无效")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var userID int64
	var status string
	if err := tx.QueryRow(r.Context(), "SELECT user_id,status FROM appeals WHERE id=$1 FOR UPDATE", id).Scan(&userID, &status); err != nil {
		return apiError(404, "APPEAL_NOT_FOUND", "申诉不存在")
	}
	if status == "pending" {
		_, _ = tx.Exec(r.Context(), "UPDATE appeals SET status=$1,admin_note=$2 WHERE id=$3", body.Status, body.Note, id)
		actor := moderator.ID
		_ = auditSQL(r.Context(), tx, &actor, "appeal.decide", "appeal", id, body.Note, map[string]any{"status": status}, map[string]any{"status": body.Status}, requestID(r.Context()))
		_ = notifySQL(r.Context(), tx, userID, "申诉处理结果", body.Status+"："+body.Note, "/me", "system")
		status = body.Status
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"id": id, "status": status})
	return nil
}

func (s *Server) adminCreateAnnouncement(w http.ResponseWriter, r *http.Request) error {
	moderator, err := s.moderatorUser(w, r)
	if err != nil {
		return err
	}
	var body struct{ Title, Body, Level, Audience string }
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Level == "" {
		body.Level = "normal"
	}
	if body.Audience == "" {
		body.Audience = "all"
	}
	if body.Level != "normal" && body.Level != "strong" {
		return validation("level", "Value error, 公告级别无效")
	}
	validAudience := map[string]bool{"all": true, "student": true, "alumni": true, "staff": true}
	if !validAudience[body.Audience] {
		return validation("audience", "Value error, 公告目标人群无效")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var id int64
	var published time.Time
	if err := tx.QueryRow(r.Context(), "INSERT INTO announcements(title,body,level,audience,published_at) VALUES($1,$2,$3,$4,now()) RETURNING id,published_at", body.Title, body.Body, body.Level, body.Audience).Scan(&id, &published); err != nil {
		return err
	}
	if body.Level == "strong" {
		where := "status='active' AND email IS NOT NULL"
		args := []any{}
		if body.Audience != "all" {
			where += " AND campus_identity=$1"
			args = append(args, body.Audience)
		}
		rows, err := tx.Query(r.Context(), "SELECT id,email FROM users WHERE "+where, args...)
		if err != nil {
			return err
		}
		type recipient struct {
			id    int64
			email string
		}
		recipients := []recipient{}
		for rows.Next() {
			var item recipient
			if err := rows.Scan(&item.id, &item.email); err != nil {
				rows.Close()
				return err
			}
			recipients = append(recipients, item)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, item := range recipients {
			if err := notifySQL(r.Context(), tx, item.id, body.Title, body.Body, "/explore/announcements", "announcement"); err != nil {
				return err
			}
			if _, err := tx.Exec(r.Context(), "INSERT INTO email_outbox(to_email,subject,body,status,attempts,next_attempt_at,last_error,created_at) VALUES($1,$2,$3,'pending',0,now(),'',now())", item.email, "【梧桐墙公告】"+body.Title, body.Body); err != nil {
				return err
			}
		}
	}
	actor := moderator.ID
	_ = auditSQL(r.Context(), tx, &actor, "announcement.create", "announcement", id, "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"id": id, "title": body.Title, "body": body.Body, "level": body.Level, "audience": body.Audience, "published_at": published})
	return nil
}
func (s *Server) adminSettings(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.adminUser(w, r); err != nil {
		return err
	}
	rows, err := s.DB.Query(r.Context(), "SELECT key,value FROM settings ORDER BY key")
	if err != nil {
		return err
	}
	defer rows.Close()
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return err
		}
		values[key] = value
	}
	if _, ok := values["anonymous_nickname_pool"]; !ok {
		values["anonymous_nickname_pool"] = strings.Join(anonymousDefaults, "\n")
	}
	writeJSON(w, 200, values)
	return rows.Err()
}
func (s *Server) adminUpdateSetting(w http.ResponseWriter, r *http.Request) error {
	admin, err := s.adminUser(w, r)
	if err != nil {
		return err
	}
	key := chi.URLParam(r, "key")
	allowed := map[string]bool{"handbook_categories": true, "risk_words": true, "site_notice": true, "registration_open": true, "anonymous_nickname_pool": true}
	if !allowed[key] {
		return apiError(400, "SETTING_NOT_ALLOWED", "不支持该设置项")
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if len(body.Value) > 20000 {
		return validation("value", "String should have at most 20000 characters")
	}
	if key == "anonymous_nickname_pool" {
		names, err := normalizeNicknamePool(body.Value)
		if err != nil {
			return apiError(400, "ANONYMOUS_POOL_INVALID", err.Error())
		}
		body.Value = strings.Join(names, "\n")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var before string
	_ = tx.QueryRow(r.Context(), "SELECT value FROM settings WHERE key=$1", key).Scan(&before)
	_, err = tx.Exec(r.Context(), `INSERT INTO settings(key,value,updated_by,updated_at) VALUES($1,$2,$3,now()) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value,updated_by=EXCLUDED.updated_by,updated_at=now()`, key, body.Value, admin.ID)
	if err != nil {
		return err
	}
	actor := admin.ID
	_ = auditSQLText(r.Context(), tx, &actor, "setting.update", "setting", key, "", before, body.Value, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"key": key, "value": body.Value})
	return nil
}
func (s *Server) adminFeedback(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.moderatorUser(w, r); err != nil {
		return err
	}
	page, size, err := pagination(r, 30, 100)
	if err != nil {
		return err
	}
	where := "true"
	args := []any{}
	if status := r.URL.Query().Get("status"); status != "" {
		args = append(args, status)
		where = "f.status=$1"
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM feedback f WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf("SELECT e.id,e.owner_id,f.type,f.title,f.body,f.status,f.admin_note FROM content_entities e JOIN feedback f ON f.entity_id=e.id WHERE %s ORDER BY e.created_at DESC LIMIT $%d OFFSET $%d", where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, user int64
		var kind, title, body, status, note string
		if err := rows.Scan(&id, &user, &kind, &title, &body, &status, &note); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "user_id": user, "type": kind, "title": title, "body": body, "status": status, "admin_note": note})
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) adminDecideFeedback(w http.ResponseWriter, r *http.Request) error {
	moderator, err := s.moderatorUser(w, r)
	if err != nil {
		return err
	}
	id, _ := pathID(r, "feedbackID")
	var body struct {
		Status string `json:"status"`
		Note   string `json:"admin_note"`
		Reward int    `json:"reward_xp"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Status != "accepted" && body.Status != "rejected" {
		return validation("status", "Value error, 反馈处理状态无效")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var owner int64
	var status, note string
	if err := tx.QueryRow(r.Context(), "SELECT e.owner_id,f.status,f.admin_note FROM feedback f JOIN content_entities e ON e.id=f.entity_id WHERE f.entity_id=$1 FOR UPDATE OF f", id).Scan(&owner, &status, &note); err != nil {
		return apiError(404, "FEEDBACK_NOT_FOUND", "反馈不存在")
	}
	if status == "accepted" || status == "rejected" {
		writeJSON(w, 200, map[string]any{"ok": true, "status": status})
		return nil
	}
	_, _ = tx.Exec(r.Context(), "UPDATE feedback SET status=$1,admin_note=$2 WHERE entity_id=$3", body.Status, body.Note, id)
	if body.Status == "accepted" {
		_, _ = tx.Exec(r.Context(), "UPDATE users SET xp=xp+$1,updated_at=now() WHERE id=$2", body.Reward, owner)
		_, err = s.applyCredit(r.Context(), tx, owner, "reward.feedback_accepted", "feedback", id)
		if err != nil {
			return err
		}
	}
	_ = notifySQL(r.Context(), tx, owner, "反馈处理结果", body.Status+"："+body.Note, "/me", "system")
	actor := moderator.ID
	_ = auditSQL(r.Context(), tx, &actor, "feedback.decide", "feedback", id, body.Note, map[string]any{"status": status, "admin_note": note}, map[string]any{"status": body.Status, "admin_note": body.Note}, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (s *Server) adminRequestBackup(w http.ResponseWriter, r *http.Request) error {
	admin, err := s.adminUser(w, r)
	if err != nil {
		return err
	}
	token, err := security.RandomToken(32)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var id int64
	var status string
	if err := tx.QueryRow(r.Context(), "INSERT INTO backup_jobs(requested_by,status,file_path,download_token,error,created_at) VALUES($1,'pending','',$2,'',now()) RETURNING id,status", admin.ID, token).Scan(&id, &status); err != nil {
		return err
	}
	actor := admin.ID
	_ = auditSQL(r.Context(), tx, &actor, "backup.request", "backup", id, "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 202, map[string]any{"id": id, "status": status})
	return nil
}
func (s *Server) adminBackups(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.adminUser(w, r); err != nil {
		return err
	}
	rows, err := s.DB.Query(r.Context(), "SELECT id,status,created_at,finished_at,download_token,error FROM backup_jobs ORDER BY created_at DESC LIMIT 20")
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id int64
		var status, token, errorText string
		var created time.Time
		var finished *time.Time
		if err := rows.Scan(&id, &status, &created, &finished, &token, &errorText); err != nil {
			return err
		}
		var download any
		if status == "ready" {
			download = fmt.Sprintf("/api/v1/admin/backups/%d/download?token=%s", id, token)
		}
		items = append(items, map[string]any{"id": id, "status": status, "created_at": created, "finished_at": finished, "download_url": download, "error": errorText})
	}
	writeJSON(w, 200, pagePayload(items, 1, 20, len(items)))
	return rows.Err()
}
func (s *Server) adminDownloadBackup(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.adminUser(w, r); err != nil {
		return err
	}
	id, _ := pathID(r, "jobID")
	token := r.URL.Query().Get("token")
	var status, path, stored string
	if err := s.DB.QueryRow(r.Context(), "SELECT status,file_path,download_token FROM backup_jobs WHERE id=$1", id).Scan(&status, &path, &stored); err != nil || status != "ready" || subtle.ConstantTimeCompare([]byte(token), []byte(stored)) != 1 {
		return apiError(404, "BACKUP_NOT_FOUND", "备份不存在或尚未完成")
	}
	base, _ := filepath.Abs(s.Config.BackupDir)
	target, _ := filepath.Abs(path)
	relative, relErr := filepath.Rel(base, target)
	if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return apiError(404, "BACKUP_FILE_MISSING", "备份文件不存在")
	}
	info, err := os.Stat(target)
	if err != nil || info.IsDir() {
		return apiError(404, "BACKUP_FILE_MISSING", "备份文件不存在")
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(target)))
	http.ServeFile(w, r, target)
	return nil
}
func (s *Server) adminAuditLogs(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.adminUser(w, r); err != nil {
		return err
	}
	page, size, err := pagination(r, 50, 100)
	if err != nil {
		return err
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM audit_logs").Scan(&total)
	rows, err := s.DB.Query(r.Context(), "SELECT id,actor_id,action,target_type,target_id,reason,created_at FROM audit_logs ORDER BY created_at DESC LIMIT $1 OFFSET $2", size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id int64
		var actor *int64
		var action, kind, target, reason string
		var created time.Time
		if err := rows.Scan(&id, &actor, &action, &kind, &target, &reason, &created); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "actor_id": actor, "action": action, "target_type": kind, "target_id": target, "reason": reason, "created_at": created})
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}

func contentTitlePreview(ctx context.Context, q queryer, id int64, kind string) (string, string, error) {
	var title, preview string
	queries := map[string]string{"post": "SELECT COALESCE(NULLIF(title,''),substr(body,1,50)),substr(body,1,2000) FROM posts WHERE entity_id=$1", "question": "SELECT title,substr(body,1,2000) FROM questions WHERE entity_id=$1", "handbook": "SELECT title,substr(body,1,2000) FROM handbook_articles WHERE entity_id=$1", "listing": "SELECT title,substr(description,1,2000) FROM listings WHERE entity_id=$1", "team": "SELECT game||' · '||mode,substr(notes,1,2000) FROM teams WHERE entity_id=$1", "course_review": "SELECT substr(body,1,50),substr(body,1,2000) FROM course_reviews WHERE entity_id=$1", "feedback": "SELECT title,substr(body,1,2000) FROM feedback WHERE entity_id=$1", "lost_item": "SELECT item_name,substr(description,1,2000) FROM lost_items WHERE entity_id=$1", "lost": "SELECT item_name,substr(description,1,2000) FROM lost_items WHERE entity_id=$1", "observe": "SELECT title,substr(body_raw,1,2000) FROM observe_posts WHERE entity_id=$1", "comment": "SELECT '回帖',substr(body,1,2000) FROM comments WHERE entity_id=$1", "answer": "SELECT '回答',substr(body,1,2000) FROM answers WHERE entity_id=$1", "message": "SELECT '被举报的私信',substr(body,1,2000) FROM messages WHERE entity_id=$1"}
	query, ok := queries[kind]
	if !ok {
		return kind, "", nil
	}
	err := q.QueryRow(ctx, query, id).Scan(&title, &preview)
	if err == pgx.ErrNoRows {
		return kind, "", nil
	}
	return title, preview, err
}
func normalizeNicknamePool(value string) ([]string, error) {
	seen := map[string]bool{}
	names := []string{}
	for _, raw := range strings.Split(value, "\n") {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		if runeLen(name) < 2 || runeLen(name) > 20 || containsControl(name) {
			return nil, fmt.Errorf("匿名昵称每行需为 2–20 个字符，且不能包含控制字符")
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("匿名昵称池至少需要一个昵称")
	}
	if len(names) > 5000 {
		return nil, fmt.Errorf("匿名昵称池最多支持 5000 个昵称")
	}
	return names, nil
}
func containsControl(value string) bool {
	for _, r := range value {
		if r < 32 {
			return true
		}
	}
	return false
}
func lastRunes(value string, n int) string {
	r := []rune(value)
	if len(r) <= n {
		return value
	}
	return string(r[len(r)-n:])
}

var _ = strconv.Itoa
