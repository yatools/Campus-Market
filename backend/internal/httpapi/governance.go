package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

func (s *Server) registerGovernanceRoutes(r chi.Router) {
	r.Get("/observe-posts", s.handle(s.listObserve))
	r.Post("/observe-posts", s.handle(s.createObserve))
	r.Post("/observe-posts/{observeID}/response", s.handle(s.respondObserve))
	// POST, not GET: revealing writes an audit record, and a side-effecting GET is exempt
	// from CSRF checks — a cross-site link could otherwise forge "user X unmasked post Y"
	// entries in the very log the 不扩散 agreement relies on for accountability.
	r.Post("/observe-posts/{observeID}/reveal", s.handle(s.revealObserve))
	r.Get("/penalties", s.handle(s.listPenalties))
	r.Post("/penalties/{penaltyID}/appeals", s.handle(s.appealPenalty))
	r.Get("/conversations", s.handle(s.listConversations))
	r.Post("/conversations", s.handle(s.createConversation))
	r.Post("/conversations/read-all", s.handle(s.readAllMessages))
	r.Get("/conversations/{conversationID}/messages", s.handle(s.listMessages))
	r.Post("/conversations/{conversationID}/messages", s.handle(s.sendMessage))
	r.Put("/blocks/{blockedID}", s.handle(s.blockUser))
	r.Delete("/blocks/{blockedID}", s.handle(s.unblockUser))
	r.Get("/notifications", s.handle(s.listNotifications))
	r.Get("/notifications/stream", s.handle(s.notificationStream))
	r.Post("/notifications/{notificationID}/read", s.handle(s.readNotification))
	r.Post("/notifications/read-all", s.handle(s.readAllNotifications))
	r.Get("/announcements", s.handle(s.listAnnouncements))
	r.Put("/announcements/{announcementID}/read", s.handle(s.readAnnouncement))
	r.Post("/feedback", s.handle(s.createFeedback))
	r.Get("/campus-services", s.handle(s.listCampusServices))
	r.Get("/campus-services/{serviceID}", s.handle(s.getCampusService))
	r.Post("/campus-services/{serviceID}/ratings", s.handle(s.rateCampusService))
	r.Post("/campus-service-ratings/{ratingID}/response", s.handle(s.respondCampusServiceRating))
	r.Post("/admin/campus-services", s.handle(s.adminCreateCampusService))
	r.Patch("/admin/campus-services/{serviceID}", s.handle(s.adminUpdateCampusService))
	r.Get("/credit-rules", s.handle(s.publicCreditRules))
	r.Get("/admin/credit-rules", s.handle(s.adminCreditRules))
	r.Patch("/admin/credit-rules", s.handle(s.updateCreditRules))
}

// Observe and governance.
type Observe struct {
	ID                 int64
	Title, Masked, Raw string
	Respondent         *int64
	Response           string
	ResponseAt         *time.Time
	AdminNote          string
}

const observeSelect = `SELECT entity_id,title,body_masked,body_raw,respondent_id,response,response_at,admin_note FROM observe_posts`

func scanObserve(row pgx.Row) (Observe, error) {
	var o Observe
	err := row.Scan(&o.ID, &o.Title, &o.Masked, &o.Raw, &o.Respondent, &o.Response, &o.ResponseAt, &o.AdminNote)
	return o, err
}
func (s *Server) listObserve(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pagination(r, 20, 50)
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	where := "e.publication_status='published'"
	args := []any{}
	if viewer.ID != 0 {
		if viewer.Role == "moderator" || viewer.Role == "admin" {
			where = "true"
		} else {
			where = "(e.publication_status='published' OR e.owner_id=$1 OR o.respondent_id=$1)"
			args = append(args, viewer.ID)
		}
	}
	isMod := viewer.ID != 0 && (viewer.Role == "moderator" || viewer.Role == "admin")
	unmaskThreshold := s.creditThreshold(r.Context(), s.DB, "threshold.observe_unmask")
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM observe_posts o JOIN content_entities e ON e.id=o.entity_id WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(`SELECT e.id,e.owner_id,e.publication_status,e.created_at,e.updated_at,o.title,o.body_masked,o.body_raw,o.respondent_id,o.response,o.admin_note,
		COALESCE((SELECT jsonb_agg(jsonb_build_object('id',att.id,'path',att.path,'thumbnail_path',att.thumbnail_path,'width',att.width,'height',att.height) ORDER BY att.id) FROM attachments att WHERE att.entity_id=e.id AND att.status='attached' AND att.access_scope='public'),'[]'::jsonb)
		FROM content_entities e JOIN observe_posts o ON o.entity_id=e.id WHERE %s ORDER BY e.created_at DESC LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, ownerID int64
		var status, title, masked, raw, response, note string
		var respondent *int64
		var created, updated time.Time
		var attachmentsRaw json.RawMessage
		if err := rows.Scan(&id, &ownerID, &status, &created, &updated, &title, &masked, &raw, &respondent, &response, &note, &attachmentsRaw); err != nil {
			return err
		}
		body := masked
		if isMod {
			body = raw
		}
		// Eligibility to unmask (credit + role). Moderators already see raw, so they
		// have nothing to reveal. The agreement gate and per-view audit are enforced by
		// the reveal endpoint; this flag only tells the client whether to offer it.
		canUnmask := viewer.ID != 0 && !isMod && status == "published" && viewer.Credit >= unmaskThreshold
		attachments, err := publicAttachmentsFromJSON(attachmentsRaw)
		if err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "title": title, "body": body, "status": status, "response": response, "admin_note": note, "mine": viewer.ID != 0 && viewer.ID == ownerID, "respondent": viewer.ID != 0 && respondent != nil && viewer.ID == *respondent, "can_unmask": canUnmask, "unmask_threshold": unmaskThreshold, "created_at": created, "updated_at": updated, "attachments": attachments})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return nil
}
func (s *Server) createObserve(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Title, Body   string
		AttachmentIDs []int64 `json:"attachment_ids"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if runeLen(strings.TrimSpace(body.Title)) < 4 || runeLen(strings.TrimSpace(body.Title)) > 160 {
		return validation("title", "String should have at least 4 characters")
	}
	if runeLen(strings.TrimSpace(body.Body)) < 10 || runeLen(strings.TrimSpace(body.Body)) > 10000 {
		return validation("body", "String should have at least 10 characters")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if err := s.requireCredit(r.Context(), tx, user, "threshold.observe_publish", "发布观察帖"); err != nil {
		return err
	}
	e, _, err := s.createEntity(r.Context(), tx, user.ID, "observe", body.Title+"\n"+body.Body, true, false, true)
	if err != nil {
		return err
	}
	o := Observe{ID: e.ID, Title: strings.TrimSpace(body.Title), Raw: strings.TrimSpace(body.Body), Masked: maskObserve(strings.TrimSpace(body.Body))}
	if _, err := tx.Exec(r.Context(), "INSERT INTO observe_posts(entity_id,title,body_masked,body_raw,response,admin_note) VALUES($1,$2,$3,$4,'','')", o.ID, o.Title, o.Masked, o.Raw); err != nil {
		return err
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, e.ID, body.AttachmentIDs); err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "observe.create", "observe", e.ID, "", nil, nil, requestID(r.Context()))
	p, err := s.observePayload(r.Context(), tx, e, o, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, p)
	return nil
}
func (s *Server) respondObserve(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "observeID")
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Body          string  `json:"body"`
		AttachmentIDs []int64 `json:"attachment_ids"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if runeLen(strings.TrimSpace(body.Body)) < 2 || runeLen(strings.TrimSpace(body.Body)) > 5000 {
		return validation("body", "String should have at least 2 characters")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, err := getEntity(r.Context(), tx, id)
	if err != nil {
		return apiError(404, "OBSERVE_NOT_FOUND", "观察帖不存在")
	}
	o, err := scanObserve(tx.QueryRow(r.Context(), observeSelect+" WHERE entity_id=$1", id))
	if err != nil {
		return apiError(404, "OBSERVE_NOT_FOUND", "观察帖不存在")
	}
	if o.Respondent == nil || *o.Respondent != user.ID {
		return apiError(403, "RESPONDENT_REQUIRED", "只有审核员指定的回应方可以回应")
	}
	o.Response = strings.TrimSpace(body.Body)
	now := time.Now().UTC()
	o.ResponseAt = &now
	if _, err := tx.Exec(r.Context(), "UPDATE observe_posts SET response=$1,response_at=$2 WHERE entity_id=$3", o.Response, now, id); err != nil {
		return err
	}
	// attachUploads replaces the entity's whole public attachment set, and the respondent
	// is a different person from the observe post's author. Calling it unconditionally
	// would detach the author's images on every reply, after which the cleanup worker
	// deletes them (and their objects) 24 hours later.
	if len(body.AttachmentIDs) > 0 {
		if err := s.attachUploads(r.Context(), tx, user.ID, id, body.AttachmentIDs); err != nil {
			return err
		}
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", id)
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "observe.respond", "observe", id, "", nil, nil, requestID(r.Context()))
	_ = notifySQL(r.Context(), tx, e.OwnerID, "观察帖收到回应", o.Title, fmt.Sprintf("/observe/%d", id), "system")
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "response": o.Response})
	return nil
}
func (s *Server) listPenalties(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM penalties").Scan(&total)
	rows, err := s.DB.Query(r.Context(), "SELECT id,public_mask,violation_type,result,rule,created_at FROM penalties ORDER BY created_at DESC LIMIT $1 OFFSET $2", size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id int64
		var user, kind, result, rule string
		var created time.Time
		if err := rows.Scan(&id, &user, &kind, &result, &rule, &created); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "user": user, "violation_type": kind, "result": result, "rule": rule, "created_at": created})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return nil
}
func (s *Server) appealPenalty(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "penaltyID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if runeLen(strings.TrimSpace(body.Reason)) < 10 || runeLen(strings.TrimSpace(body.Reason)) > 5000 {
		return validation("reason", "String should have at least 10 characters")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var owner int64
	if err := tx.QueryRow(r.Context(), "SELECT user_id FROM penalties WHERE id=$1", id).Scan(&owner); err == pgx.ErrNoRows {
		return apiError(404, "PENALTY_NOT_FOUND", "处罚记录不存在")
	} else if err != nil {
		return err
	}
	if owner != user.ID {
		return apiError(403, "PENALTY_OWNER_REQUIRED", "只能申诉自己的处罚记录")
	}
	var appealID int64
	var status string
	// ON CONFLICT rather than check-then-insert: a double-clicked "submit appeal" used to
	// have both transactions see no existing row and the loser hit uq_appeal, which
	// surfaced as a 500 on what should be an idempotent action.
	err = tx.QueryRow(r.Context(), `INSERT INTO appeals(penalty_id,user_id,reason,status,admin_note,created_at)
		VALUES($1,$2,$3,'pending','',now())
		ON CONFLICT(penalty_id,user_id) DO UPDATE SET penalty_id=EXCLUDED.penalty_id
		RETURNING id,status`, id, user.ID, strings.TrimSpace(body.Reason)).Scan(&appealID, &status)
	if err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "appeal.create", "appeal", appealID, "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"id": appealID, "status": status})
	return nil
}

// revealObserve returns an observe post's raw (un-masked) body to a viewer who is a
// moderator/admin, or who meets the credit threshold AND has signed the
// 《吃瓜不扩散协议》. Every reveal is written to the audit log so the "不扩散" promise
// has an accountability trail. List/detail stay masked; this is the explicit opt-in.
func (s *Server) revealObserve(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "observeID")
	if err != nil {
		return err
	}
	// participatingUser, not currentUser: a user restricted for abusing this very feature
	// would otherwise keep full access to it.
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var status, raw string
	var ownerID int64
	if err := s.DB.QueryRow(r.Context(), "SELECT e.publication_status,e.owner_id,o.body_raw FROM observe_posts o JOIN content_entities e ON e.id=o.entity_id WHERE o.entity_id=$1", id).Scan(&status, &ownerID, &raw); err == pgx.ErrNoRows {
		return apiError(404, "OBSERVE_NOT_FOUND", "观察帖不存在")
	} else if err != nil {
		return err
	}
	isMod := user.Role == "moderator" || user.Role == "admin"
	if !isMod {
		if status != "published" && user.ID != ownerID {
			return apiError(404, "OBSERVE_NOT_FOUND", "观察帖不存在")
		}
		threshold := s.creditThreshold(r.Context(), s.DB, "threshold.observe_unmask")
		if user.Credit < threshold {
			return apiError(403, "CREDIT_REQUIRED", fmt.Sprintf("查看观察帖原文需要信用分不低于 %d", threshold))
		}
		agreed, err := s.observeUnmaskAgreed(r.Context(), user.ID)
		if err != nil {
			return err
		}
		if !agreed {
			return apiError(403, "UNMASK_AGREEMENT_REQUIRED", "请先签署《吃瓜不扩散协议》")
		}
		// Per-viewer throttle. Without it a single qualifying account could walk the whole
		// id range and export every observe post in minutes; the audit log would only record
		// the exfiltration after the fact.
		if err := s.rateLimit(r.Context(), "observe_unmask_hour", strconv.FormatInt(user.ID, 10), 10, 60); err != nil {
			return err
		}
		if err := s.rateLimit(r.Context(), "observe_unmask_day", strconv.FormatInt(user.ID, 10), 30, 24*60); err != nil {
			return err
		}
	}
	// Fail closed: the audit entry is the entire accountability story behind 不扩散, so if
	// it cannot be written the raw body is not handed out either.
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	actor := user.ID
	if err := auditSQL(r.Context(), tx, &actor, "observe.unmask", "observe", id, "", nil, map[string]any{"credit": user.Credit, "moderator": isMod}, requestID(r.Context())); err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, 200, map[string]any{"id": id, "body": raw, "unmasked": true})
	return nil
}

// observeUnmaskAgreed reports whether the user has accepted the *current* version of the
// 《吃瓜不扩散协议》. Checking only for the row's existence made the version column
// decorative: bumping the protocol text would not have forced anyone to re-consent.
func (s *Server) observeUnmaskAgreed(ctx context.Context, userID int64) (bool, error) {
	var agreed bool
	err := s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM observe_unmask_agreements WHERE user_id=$1 AND agreed_version=$2)", userID, observeUnmaskAgreementVersion).Scan(&agreed)
	return agreed, err
}

func (s *Server) observePayload(ctx context.Context, q queryer, e Entity, o Observe, viewer *User) (map[string]any, error) {
	body := o.Masked
	isMod := viewer != nil && (viewer.Role == "moderator" || viewer.Role == "admin")
	if isMod {
		body = o.Raw
	}
	canUnmask := viewer != nil && !isMod && e.Status == "published" && viewer.Credit >= s.creditThreshold(ctx, q, "threshold.observe_unmask")
	files, err := attachmentsPayload(ctx, q, e.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": e.ID, "title": o.Title, "body": body, "status": e.Status, "response": o.Response, "admin_note": o.AdminNote, "mine": viewer != nil && viewer.ID == e.OwnerID, "respondent": viewer != nil && o.Respondent != nil && viewer.ID == *o.Respondent, "can_unmask": canUnmask, "created_at": e.CreatedAt, "updated_at": e.UpdatedAt, "attachments": files}, nil
}

// Masking patterns for observe posts. Order matters: the more specific patterns run first
// so a phone number is not swallowed by the generic digit rule before it can be matched.
//
// The previous version only replaced runs of 6-18 digits, which meant a body like
// "张伟，微信 zhangwei_1998，邮箱 a@stu.edu.cn，电话 138-1234-5678，QQ 12345" came through
// entirely in the clear — and the masked body is what anonymous visitors see, so that leak
// sat in front of the credit/agreement gate rather than behind it.
var (
	phoneMask = regexp.MustCompile(`1[3-9]\d[-\s]?\d{4}[-\s]?\d{4}`)
	emailMask = regexp.MustCompile(`[\w.+-]+@[\w-]+(?:\.[\w-]+)+`)
	// 拉丁标签必须整词匹配（\b…\b），否则 "wxyz12345" 会被拆成 wx + yz12345 误遮；
	// 中文标签不受 \b 影响（Go 的 \b 是 ASCII 词边界），单独列出并允许「微信号」写法。
	imAccount   = regexp.MustCompile(`(?i)((?:微信|威信|扣扣)号?|\b(?:weixin|wechat|wx|qq)\b)\s*[:：是]?\s*[A-Za-z0-9_-]{5,20}`)
	idCardMask  = regexp.MustCompile(`\b\d{17}[\dXx]\b`)
	digitMask   = regexp.MustCompile(`\d{5,18}`)
	maskedToken = "▓▓▓▓▓▓"
)

func maskObserve(v string) string {
	v = emailMask.ReplaceAllString(v, maskedToken)
	v = idCardMask.ReplaceAllString(v, maskedToken)
	v = phoneMask.ReplaceAllString(v, "1**********")
	v = imAccount.ReplaceAllString(v, "${1} "+maskedToken)
	return digitMask.ReplaceAllString(v, maskedToken)
}

// Private messaging.
func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	page, size, err := pagination(r, 30, 100)
	if err != nil {
		return err
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM conversation_members WHERE user_id=$1", user.ID).Scan(&total)
	rows, err := s.DB.Query(r.Context(), `SELECT c.id,c.context_type,c.context_id,other.id,other.nickname,COALESCE(last_message.body,''),COALESCE(last_message.created_at,c.created_at),
		EXISTS(SELECT 1 FROM blocks block WHERE block.user_id=$1 AND block.blocked_id=other.id),
		(SELECT count(*) FROM messages message JOIN content_entities entity ON entity.id=message.entity_id WHERE message.conversation_id=c.id AND entity.owner_id<>$1 AND entity.publication_status='published' AND entity.created_at>COALESCE(m.last_read_at,c.created_at))
		FROM conversations c JOIN conversation_members m ON m.conversation_id=c.id
		LEFT JOIN LATERAL (SELECT u.id,u.nickname FROM conversation_members participant JOIN users u ON u.id=participant.user_id WHERE participant.conversation_id=c.id AND participant.user_id<>$1 LIMIT 1) other ON true
		LEFT JOIN LATERAL (SELECT message.body,entity.created_at FROM messages message JOIN content_entities entity ON entity.id=message.entity_id WHERE message.conversation_id=c.id AND entity.publication_status='published' ORDER BY entity.created_at DESC LIMIT 1) last_message ON true
		WHERE m.user_id=$1 ORDER BY c.id DESC LIMIT $2 OFFSET $3`, user.ID, size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id int64
		var kind string
		var contextID *int64
		var otherID *int64
		var otherName *string
		var lastBody string
		var lastAt time.Time
		var blockedByMe bool
		var unread int
		if err := rows.Scan(&id, &kind, &contextID, &otherID, &otherName, &lastBody, &lastAt, &blockedByMe, &unread); err != nil {
			return err
		}
		var other any
		if otherID != nil && otherName != nil {
			other = map[string]any{"id": *otherID, "nickname": *otherName}
		}
		items = append(items, map[string]any{"id": id, "context_type": kind, "context_id": contextID, "other_user": other, "last_message": truncateRunes(lastBody, 100), "last_message_at": lastAt, "unread": unread, "blocked_by_me": blockedByMe})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return nil
}
func (s *Server) createConversation(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Recipient    int64  `json:"recipient_id"`
		ContextType  string `json:"context_type"`
		ContextID    *int64 `json:"context_id"`
		FirstMessage string `json:"first_message"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.ContextType == "" {
		body.ContextType = "direct"
	}
	valid := map[string]bool{"direct": true, "listing": true, "team": true, "activity": true}
	if !valid[body.ContextType] {
		return validation("context_type", "Value error, 会话上下文无效")
	}
	if strings.TrimSpace(body.FirstMessage) == "" || runeLen(strings.TrimSpace(body.FirstMessage)) > 2000 {
		return validation("first_message", "String should have at least 1 character")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var recipient User
	recipient, err = scanUser(tx.QueryRow(r.Context(), `SELECT id,email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,avatar_path,dm_stranger_off,hide_online,verified_at,created_at FROM users WHERE id=$1`, body.Recipient))
	if err != nil || recipient.Status != "active" {
		return apiError(404, "RECIPIENT_NOT_FOUND", "收件人不存在")
	}
	if recipient.ID == user.ID {
		return apiError(400, "SELF_MESSAGE", "不能给自己发私信")
	}
	if blocked, err := isBlocked(r.Context(), tx, user.ID, recipient.ID); err != nil {
		return err
	} else if blocked {
		return apiError(403, "MESSAGE_BLOCKED", "无法向该用户发送私信")
	}
	allowed, err := s.contextContactAllowed(r.Context(), tx, user.ID, recipient.ID, body.ContextType, body.ContextID)
	if err != nil {
		return err
	}
	if body.ContextType == "direct" && body.ContextID != nil {
		return apiError(400, "INVALID_MESSAGE_CONTEXT", "普通私信不能携带业务上下文")
	}
	if body.ContextType != "direct" && !allowed {
		return apiError(403, "INVALID_MESSAGE_CONTEXT", "你们不在该商品、车队或活动上下文中")
	}
	if recipient.DMStrangerOff && !allowed {
		return apiError(403, "STRANGER_MESSAGES_OFF", "对方已关闭陌生人私信")
	}
	conversationID, created, err := findConversation(r.Context(), tx, user.ID, recipient.ID, body.ContextType, body.ContextID)
	if err != nil {
		return err
	}
	if conversationID == 0 {
		err = tx.QueryRow(r.Context(), "INSERT INTO conversations(context_type,context_id,created_at) VALUES($1,$2,now()) RETURNING id,created_at", body.ContextType, body.ContextID).Scan(&conversationID, &created)
		if err != nil {
			return err
		}
		_, err = tx.Exec(r.Context(), "INSERT INTO conversation_members(conversation_id,user_id,last_read_at) VALUES($1,$2,now()),($1,$3,NULL)", conversationID, user.ID, recipient.ID)
		if err != nil {
			return err
		}
	}
	messageID, _, err := s.addMessage(r.Context(), tx, conversationID, user, body.FirstMessage)
	if err != nil {
		return err
	}
	_ = notifySQL(r.Context(), tx, recipient.ID, "收到新私信", "来自 "+user.Nickname+" 的消息", fmt.Sprintf("/messages/%d", conversationID), "message")
	payload, err := s.conversationPayload(r.Context(), tx, conversationID, body.ContextType, body.ContextID, created, user.ID)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"conversation": payload, "message_id": messageID})
	return nil
}
func (s *Server) listMessages(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "conversationID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	page, size, err := pagination(r, 50, 200)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var membership int64
	if err := tx.QueryRow(r.Context(), "SELECT id FROM conversation_members WHERE conversation_id=$1 AND user_id=$2", id, user.ID).Scan(&membership); err != nil {
		return apiError(403, "CONVERSATION_MEMBER_REQUIRED", "无权查看该会话")
	}
	var total int
	_ = tx.QueryRow(r.Context(), `SELECT count(*) FROM messages m JOIN content_entities e ON e.id=m.entity_id WHERE m.conversation_id=$1 AND e.publication_status='published'`, id).Scan(&total)
	rows, err := tx.Query(r.Context(), `SELECT e.id,m.body,e.owner_id,e.created_at FROM content_entities e JOIN messages m ON m.entity_id=e.id WHERE m.conversation_id=$1 AND e.publication_status='published' ORDER BY e.created_at LIMIT $2 OFFSET $3`, id, size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var mid, sender int64
		var body string
		var created time.Time
		if err := rows.Scan(&mid, &body, &sender, &created); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": mid, "body": body, "sender_id": sender, "mine": sender == user.ID, "created_at": created})
	}
	// Without this check a mid-stream failure looks like "the conversation ended here":
	// the client gets a 200 with a silently truncated message list.
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if _, err := tx.Exec(r.Context(), "UPDATE conversation_members SET last_read_at=now() WHERE id=$1", membership); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "UPDATE notifications SET read_at=now() WHERE user_id=$1 AND type='message' AND link=$2 AND read_at IS NULL", user.ID, fmt.Sprintf("/messages/%d", id)); err != nil {
		return err
	}
	var unreadNotifications, unreadMessages int
	if err := tx.QueryRow(r.Context(), "SELECT count(*) FILTER(WHERE read_at IS NULL),count(*) FILTER(WHERE read_at IS NULL AND type='message') FROM notifications WHERE user_id=$1", user.ID).Scan(&unreadNotifications, &unreadMessages); err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	payload := pagePayload(items, page, size, total)
	payload["unread_notifications"] = unreadNotifications
	payload["unread_messages"] = unreadMessages
	writeJSON(w, 200, payload)
	return nil
}

func (s *Server) readAllMessages(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	_, err = tx.Exec(r.Context(), "UPDATE conversation_members SET last_read_at=now() WHERE user_id=$1", user.ID)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(r.Context(), "UPDATE notifications SET read_at=now() WHERE user_id=$1 AND type='message' AND read_at IS NULL", user.ID)
	if err != nil {
		return err
	}
	var unreadNotifications int
	if err := tx.QueryRow(r.Context(), "SELECT count(*) FROM notifications WHERE user_id=$1 AND read_at IS NULL", user.ID).Scan(&unreadNotifications); err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "marked_messages": tag.RowsAffected(), "unread_messages": 0, "unread_notifications": unreadNotifications})
	return nil
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "conversationID")
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Body) == "" || runeLen(strings.TrimSpace(body.Body)) > 2000 {
		return validation("body", "String should have at least 1 character")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	members, err := int64Rows(r.Context(), tx, "SELECT user_id FROM conversation_members WHERE conversation_id=$1", id)
	if err != nil {
		return err
	}
	if len(members) != 2 || members[0] != user.ID && members[1] != user.ID {
		return apiError(403, "CONVERSATION_MEMBER_REQUIRED", "无权向该会话发送消息")
	}
	recipient := members[0]
	if recipient == user.ID {
		recipient = members[1]
	}
	if blocked, err := isBlocked(r.Context(), tx, user.ID, recipient); err != nil {
		return err
	} else if blocked {
		return apiError(403, "MESSAGE_BLOCKED", "无法向该用户发送私信")
	}
	messageID, created, err := s.addMessage(r.Context(), tx, id, user, body.Body)
	if err != nil {
		return err
	}
	_ = notifySQL(r.Context(), tx, recipient, "收到新私信", "来自 "+user.Nickname+" 的消息", fmt.Sprintf("/messages/%d", id), "message")
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"id": messageID, "body": strings.TrimSpace(body.Body), "mine": true, "created_at": created})
	return nil
}
func (s *Server) blockUser(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "blockedID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var exists bool
	_ = s.DB.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)", id).Scan(&exists)
	if id == user.ID || !exists {
		return apiError(400, "INVALID_BLOCK_TARGET", "拉黑对象无效")
	}
	_, err = s.DB.Exec(r.Context(), "INSERT INTO blocks(user_id,blocked_id,created_at) VALUES($1,$2,now()) ON CONFLICT(user_id,blocked_id) DO NOTHING", user.ID, id)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"active": true})
	return nil
}
func (s *Server) unblockUser(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "blockedID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(r.Context(), "DELETE FROM blocks WHERE user_id=$1 AND blocked_id=$2", user.ID, id)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"active": false})
	return nil
}
func isBlocked(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, a, b int64) (bool, error) {
	var result bool
	err := q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM blocks WHERE (user_id=$1 AND blocked_id=$2) OR (user_id=$2 AND blocked_id=$1))", a, b).Scan(&result)
	return result, err
}
func (s *Server) contextContactAllowed(ctx context.Context, q queryer, sender, recipient int64, kind string, id *int64) (bool, error) {
	if id == nil {
		return false, nil
	}
	switch kind {
	case "listing":
		var owner int64
		var status string
		err := q.QueryRow(ctx, "SELECT e.owner_id,l.trade_status FROM content_entities e JOIN listings l ON l.entity_id=e.id WHERE e.id=$1", *id).Scan(&owner, &status)
		return err == nil && owner == recipient && (status == "available" || status == "reserved"), nil
	case "team":
		var count int
		err := q.QueryRow(ctx, "SELECT count(*) FROM team_memberships WHERE team_id=$1 AND user_id=ANY($2) AND status='active'", *id, []int64{sender, recipient}).Scan(&count)
		return count == 2, err
	case "activity":
		var owner int64
		var entityStatus, activityStatus string
		err := q.QueryRow(ctx, "SELECT e.owner_id,e.publication_status,a.status FROM content_entities e JOIN activities a ON a.entity_id=e.id WHERE e.id=$1", *id).Scan(&owner, &entityStatus, &activityStatus)
		if err != nil {
			return false, nil
		}
		var count int
		_ = q.QueryRow(ctx, "SELECT count(*) FROM activity_members WHERE activity_id=$1 AND user_id=ANY($2) AND status='joined'", *id, []int64{sender, recipient}).Scan(&count)
		participants := count
		if owner == sender || owner == recipient {
			participants++
		}
		return entityStatus == "published" && activityStatus == "open" && participants >= 2, nil
	}
	return false, nil
}

// findConversation locates the existing two-party conversation between a and b, if any.
//
// This used to walk every conversation of the given kind and issue a follow-up query per
// row. On a pgx.Tx that is a single connection, so the inner query ran while the outer
// result set was still open and failed with "conn busy" — i.e. starting a second direct
// conversation site-wide always returned 500. It was also O(all conversations) per call.
func findConversation(ctx context.Context, q queryer, a, b int64, kind string, contextID *int64) (int64, time.Time, error) {
	var id int64
	var created time.Time
	err := q.QueryRow(ctx, `SELECT c.id,c.created_at FROM conversations c
		JOIN conversation_members m1 ON m1.conversation_id=c.id AND m1.user_id=$3
		JOIN conversation_members m2 ON m2.conversation_id=c.id AND m2.user_id=$4
		WHERE c.context_type=$1 AND c.context_id IS NOT DISTINCT FROM $2
		  AND (SELECT count(*) FROM conversation_members x WHERE x.conversation_id=c.id)=2
		ORDER BY c.id LIMIT 1`, kind, contextID, a, b).Scan(&id, &created)
	if err == pgx.ErrNoRows {
		return 0, time.Time{}, nil
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	return id, created, nil
}
func (s *Server) addMessage(ctx context.Context, tx pgx.Tx, conversationID int64, sender User, body string) (int64, time.Time, error) {
	if err := checkRateLimitSQL(ctx, tx, "message_send_minute", strconv.FormatInt(sender.ID, 10), 30, 1); err != nil {
		return 0, time.Time{}, err
	}
	if err := checkRateLimitSQL(ctx, tx, "message_send_day", strconv.FormatInt(sender.ID, 10), 300, 24*60); err != nil {
		return 0, time.Time{}, err
	}
	threshold := creditDefault("threshold.dm_unlimited")
	_ = tx.QueryRow(ctx, "SELECT value FROM credit_rules WHERE key='threshold.dm_unlimited'").Scan(&threshold)
	if sender.Credit < threshold {
		var sent int
		_ = tx.QueryRow(ctx, "SELECT count(*) FROM content_entities WHERE type='message' AND owner_id=$1 AND created_at>=date_trunc('day',now())", sender.ID).Scan(&sent)
		limit := 20
		if sender.CreatedAt.After(time.Now().UTC().Add(-7 * 24 * time.Hour)) {
			limit = 5
		}
		if sent >= limit {
			return 0, time.Time{}, apiError(429, "MESSAGE_LIMIT_REACHED", fmt.Sprintf("已达到今日私信上限（%d 条）", limit))
		}
	}
	e, _, err := s.createEntity(ctx, tx, sender.ID, "message", body, false, false, false)
	if err != nil {
		return 0, time.Time{}, err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO messages(entity_id,conversation_id,body) VALUES($1,$2,$3)", e.ID, conversationID, strings.TrimSpace(body)); err != nil {
		return 0, time.Time{}, err
	}
	return e.ID, e.CreatedAt, nil
}
func (s *Server) conversationPayload(ctx context.Context, q queryer, id int64, kind string, contextID *int64, created time.Time, viewer int64) (map[string]any, error) {
	var otherID *int64
	var otherName *string
	_ = q.QueryRow(ctx, `SELECT u.id,u.nickname FROM conversation_members m JOIN users u ON u.id=m.user_id WHERE m.conversation_id=$1 AND m.user_id<>$2 LIMIT 1`, id, viewer).Scan(&otherID, &otherName)
	var lastBody string
	var lastAt time.Time
	err := q.QueryRow(ctx, `SELECT m.body,e.created_at FROM messages m JOIN content_entities e ON e.id=m.entity_id WHERE m.conversation_id=$1 AND e.publication_status='published' ORDER BY e.created_at DESC LIMIT 1`, id).Scan(&lastBody, &lastAt)
	if err == pgx.ErrNoRows {
		lastAt = created
	} else if err != nil {
		return nil, err
	}
	var readAt *time.Time
	_ = q.QueryRow(ctx, "SELECT last_read_at FROM conversation_members WHERE conversation_id=$1 AND user_id=$2", id, viewer).Scan(&readAt)
	since := created
	if readAt != nil {
		since = *readAt
	}
	var unread int
	_ = q.QueryRow(ctx, `SELECT count(*) FROM messages m JOIN content_entities e ON e.id=m.entity_id WHERE m.conversation_id=$1 AND e.owner_id<>$2 AND e.publication_status='published' AND e.created_at>$3`, id, viewer, since).Scan(&unread)
	var other any
	blockedByMe := false
	if otherID != nil {
		other = map[string]any{"id": *otherID, "nickname": *otherName}
		_ = q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM blocks WHERE user_id=$1 AND blocked_id=$2)", viewer, *otherID).Scan(&blockedByMe)
	}
	return map[string]any{"id": id, "context_type": kind, "context_id": contextID, "other_user": other, "last_message": truncateRunes(lastBody, 100), "last_message_at": lastAt, "unread": unread, "blocked_by_me": blockedByMe}, nil
}

// Notifications, announcements and feedback.
func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	page, size, err := pagination(r, 30, 100)
	if err != nil {
		return err
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM notifications WHERE user_id=$1", user.ID).Scan(&total)
	rows, err := s.DB.Query(r.Context(), "SELECT id,type,title,body,link,read_at,created_at FROM notifications WHERE user_id=$1 ORDER BY created_at DESC LIMIT $2 OFFSET $3", user.ID, size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id int64
		var kind, title, body, link string
		var read *time.Time
		var created time.Time
		if err := rows.Scan(&id, &kind, &title, &body, &link, &read, &created); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "type": kind, "title": title, "body": body, "link": link, "read_at": read, "created_at": created})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return nil
}
func (s *Server) notificationStream(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming unsupported")
	}
	// Clear the server's 90s WriteTimeout for this long-lived stream. The absolute
	// write deadline is not reset by the 30s heartbeat, so without this every SSE
	// connection is severed ~90s after it opens, degrading realtime notifications to
	// a reconnect-driven poll (and dropping events in each reconnect gap).
	// Clear the server's WriteTimeout for this connection: an SSE stream is long-lived by
	// design and would otherwise be cut every 90 seconds. Log on failure — a silently
	// discarded error here is exactly how this fix went unnoticed the first time.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		slog.Warn("sse_write_deadline_unsupported", "error", err, "request_id", requestID(r.Context()))
	}
	events, unsubscribe := s.Hub.subscribe(user.ID)
	defer unsubscribe()
	writeUnread := func() error {
		var count, messages int
		if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FILTER(WHERE read_at IS NULL),count(*) FILTER(WHERE read_at IS NULL AND type='message') FROM notifications WHERE user_id=$1", user.ID).Scan(&count, &messages); err != nil {
			return err
		}
		_, err := fmt.Fprintf(w, "event: unread\ndata: {\"count\":%d,\"messages\":%d}\n\n", count, messages)
		flusher.Flush()
		return err
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	// Tell nginx not to buffer this response; with buffering on, events sit in the proxy
	// until the buffer fills and the stream stops being real time.
	w.Header().Set("X-Accel-Buffering", "no")
	if err := writeUnread(); err != nil {
		return err
	}
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return nil
		case <-heartbeat.C:
			// A failed heartbeat write means the peer is gone; returning releases the
			// subscription instead of looping on a dead connection.
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return nil
			}
			flusher.Flush()
		case <-events:
			if err := writeUnread(); err != nil {
				return err
			}
		}
	}
}

func (s *Server) readNotification(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "notificationID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	tag, err := s.DB.Exec(r.Context(), "UPDATE notifications SET read_at=COALESCE(read_at,now()) WHERE id=$1 AND user_id=$2", id, user.ID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apiError(404, "NOTIFICATION_NOT_FOUND", "Notification not found")
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (s *Server) readAllNotifications(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	if _, err := s.DB.Exec(r.Context(), "UPDATE notifications SET read_at=now() WHERE user_id=$1 AND read_at IS NULL", user.ID); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (s *Server) listAnnouncements(w http.ResponseWriter, r *http.Request) error {
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	where := "a.audience='all'"
	visibilityArgs := []any{}
	if viewer.ID != 0 {
		where = "a.audience='all' OR a.audience=$1"
		visibilityArgs = append(visibilityArgs, viewer.CampusIdentity)
	}
	var total int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM announcements a WHERE "+where, visibilityArgs...).Scan(&total); err != nil {
		return err
	}
	selectWhere := where
	if viewer.ID != 0 {
		selectWhere = "a.audience='all' OR a.audience=$2"
	}
	args := append([]any{viewer.ID}, visibilityArgs...)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(`SELECT a.id,a.title,a.body,a.level,a.audience,a.published_at,
		vr.user_id IS NOT NULL AS read,COALESCE(rc.read_count,0) AS read_count
		FROM announcements a
		LEFT JOIN announcement_reads vr ON vr.announcement_id=a.id AND vr.user_id=$1
		LEFT JOIN (SELECT announcement_id,count(*) AS read_count FROM announcement_reads GROUP BY announcement_id) rc ON rc.announcement_id=a.id
		WHERE %s ORDER BY a.published_at DESC LIMIT $%d OFFSET $%d`, selectWhere, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id int64
		var title, body, level, audience string
		var published time.Time
		var read bool
		var count int
		if err := rows.Scan(&id, &title, &body, &level, &audience, &published, &read, &count); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "title": title, "body": body, "level": level, "audience": audience, "read": read, "read_count": count, "published_at": published})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return nil
}

func (s *Server) readAnnouncement(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "announcementID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var audience string
	if err := s.DB.QueryRow(r.Context(), "SELECT audience FROM announcements WHERE id=$1", id).Scan(&audience); err != nil || audience != "all" && audience != user.CampusIdentity {
		return apiError(404, "ANNOUNCEMENT_NOT_FOUND", "公告不存在")
	}
	_, err = s.DB.Exec(r.Context(), "INSERT INTO announcement_reads(announcement_id,user_id,read_at) VALUES($1,$2,now()) ON CONFLICT(announcement_id,user_id) DO NOTHING", id, user.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"active": true})
	return nil
}
func (s *Server) createFeedback(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct{ Type, Title, Body string }
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Type == "" {
		body.Type = "suggestion"
	}
	if runeLen(strings.TrimSpace(body.Title)) < 3 || runeLen(strings.TrimSpace(body.Body)) < 10 {
		return validation("request", "反馈内容不符合要求")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, _, err := s.createEntity(r.Context(), tx, user.ID, "feedback", body.Title+"\n"+body.Body, true, false, false)
	if err != nil {
		return err
	}
	var status string
	if err := tx.QueryRow(r.Context(), "INSERT INTO feedback(entity_id,type,title,body,status,admin_note) VALUES($1,$2,$3,$4,'pending','') RETURNING status", e.ID, body.Type, strings.TrimSpace(body.Title), strings.TrimSpace(body.Body)).Scan(&status); err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"id": e.ID, "status": status})
	return nil
}

// Campus services.
type CampusService struct {
	ID               int64
	Name, Category   string
	Manager          *int64
	Active           bool
	Created, Updated time.Time
}

func (s *Server) listCampusServices(w http.ResponseWriter, r *http.Request) error {
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	where := "active=true"
	args := []any{}
	if category := r.URL.Query().Get("category"); category != "" {
		args = append(args, category)
		where += " AND category=$1"
	}
	args = append(args, viewer.ID)
	viewerParam := len(args)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(`SELECT service.id,service.name,service.category,service.manager_user_id,service.active,service.created_at,service.updated_at,
		(SELECT count(*) FROM campus_service_ratings rating WHERE rating.service_id=service.id),
		(SELECT avg(rating.rating)::float8 FROM campus_service_ratings rating WHERE rating.service_id=service.id),
		(SELECT max(rating.created_at) FROM campus_service_ratings rating WHERE rating.service_id=service.id AND rating.user_id=$%d)
		FROM campus_services service WHERE %s ORDER BY service.name`, viewerParam, strings.ReplaceAll(where, "active", "service.active")), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var service CampusService
		var count int
		var average *float64
		var latest *time.Time
		if err := rows.Scan(&service.ID, &service.Name, &service.Category, &service.Manager, &service.Active, &service.Created, &service.Updated, &count, &average, &latest); err != nil {
			return err
		}
		var score any
		if average != nil {
			score = mathRound(*average*10) / 10
		}
		var next any
		if latest != nil {
			next = latest.Add(30 * 24 * time.Hour)
		}
		managed := viewer.ID != 0 && ((service.Manager != nil && *service.Manager == viewer.ID) || viewer.Role == "moderator" || viewer.Role == "admin")
		items = append(items, map[string]any{"id": service.ID, "name": service.Name, "category": service.Category, "score": score, "rating_count": count, "managed_by_me": managed, "next_rating_at": next})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": items, "total": len(items)})
	return nil
}
func (s *Server) getCampusService(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "serviceID")
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	service, err := scanCampusService(s.DB.QueryRow(r.Context(), "SELECT id,name,category,manager_user_id,active,created_at,updated_at FROM campus_services WHERE id=$1", id))
	if err != nil || !service.Active {
		return apiError(404, "CAMPUS_SERVICE_NOT_FOUND", "校园服务不存在")
	}
	p, err := s.campusServicePayload(r.Context(), s.DB, service, userOrNil(viewer), true)
	if err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) rateCampusService(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "serviceID")
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Rating int    `json:"rating"`
		Body   string `json:"body"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	body.Body = strings.TrimSpace(body.Body)
	if body.Rating < 1 || body.Rating > 5 {
		return validation("rating", "Input should be between 1 and 5")
	}
	if body.Rating <= 2 && runeLen(body.Body) < 10 {
		return apiError(400, "LOW_RATING_REASON_REQUIRED", "低分评价请填写至少 10 个字的具体事由")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	// FOR UPDATE serialises concurrent ratings of the same service on the service row.
	// campus_service_ratings has no uniqueness constraint (unlike every sibling rating
	// table), so without it two simultaneous submissions both read "no previous rating"
	// and both pass the 30-day cooldown.
	service, err := scanCampusService(tx.QueryRow(r.Context(), "SELECT id,name,category,manager_user_id,active,created_at,updated_at FROM campus_services WHERE id=$1 FOR UPDATE", id))
	if err != nil || !service.Active {
		return apiError(404, "CAMPUS_SERVICE_NOT_FOUND", "校园服务不存在")
	}
	var latest *time.Time
	_ = tx.QueryRow(r.Context(), "SELECT max(created_at) FROM campus_service_ratings WHERE service_id=$1 AND user_id=$2", id, user.ID).Scan(&latest)
	if latest != nil && latest.After(time.Now().UTC().Add(-30*24*time.Hour)) {
		return apiError(409, "SERVICE_RATING_COOLDOWN", "同一服务 30 天内只能评价一次")
	}
	var ratingID int64
	if err := tx.QueryRow(r.Context(), "INSERT INTO campus_service_ratings(service_id,user_id,rating,body,response,created_at,updated_at) VALUES($1,$2,$3,$4,'',now(),now()) RETURNING id", id, user.ID, body.Rating, body.Body).Scan(&ratingID); err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "campus_service.rate", "campus_service", id, "", nil, map[string]any{"rating": body.Rating}, requestID(r.Context()))
	p, err := s.campusRatingPayload(r.Context(), tx, ratingID)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, p)
	return nil
}
func (s *Server) respondCampusServiceRating(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "ratingID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		Body string `json:"body"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if runeLen(strings.TrimSpace(body.Body)) < 2 || runeLen(strings.TrimSpace(body.Body)) > 2000 {
		return validation("body", "String should have at least 2 characters")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var serviceID, author int64
	var response, serviceName string
	var manager *int64
	if err := tx.QueryRow(r.Context(), `SELECT r.service_id,r.user_id,r.response,s.name,s.manager_user_id FROM campus_service_ratings r JOIN campus_services s ON s.id=r.service_id WHERE r.id=$1`, id).Scan(&serviceID, &author, &response, &serviceName, &manager); err != nil {
		return apiError(404, "SERVICE_RATING_NOT_FOUND", "服务评价不存在")
	}
	if (manager == nil || *manager != user.ID) && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "SERVICE_MANAGER_REQUIRED", "只有服务管理者或管理员可以回应")
	}
	if response != "" {
		return apiError(409, "SERVICE_RATING_RESPONDED", "该评价已经回应")
	}
	_, err = tx.Exec(r.Context(), "UPDATE campus_service_ratings SET response=$1,responder_id=$2,responded_at=now(),updated_at=now() WHERE id=$3", strings.TrimSpace(body.Body), user.ID, id)
	if err != nil {
		return err
	}
	_ = notifySQL(r.Context(), tx, author, "校园服务评价收到回应", "“"+serviceName+"”回应了你的评价", fmt.Sprintf("/explore/handbook?service=%d", serviceID), "system")
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "campus_service.respond", "campus_service_rating", id, "", nil, nil, requestID(r.Context()))
	p, err := s.campusRatingPayload(r.Context(), tx, id)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) adminCreateCampusService(w http.ResponseWriter, r *http.Request) error {
	admin, err := s.adminUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Name, Category string
		Manager        *int64 `json:"manager_user_id"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Category == "" {
		body.Category = "校园服务"
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var exists bool
	_ = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM campus_services WHERE name=$1)", strings.TrimSpace(body.Name)).Scan(&exists)
	if exists {
		return apiError(409, "CAMPUS_SERVICE_EXISTS", "该校园服务已存在")
	}
	if body.Manager != nil {
		_ = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)", *body.Manager).Scan(&exists)
		if !exists {
			return apiError(404, "USER_NOT_FOUND", "服务管理者不存在")
		}
	}
	var service CampusService
	err = tx.QueryRow(r.Context(), "INSERT INTO campus_services(name,category,manager_user_id,active,created_at,updated_at) VALUES($1,$2,$3,true,now(),now()) RETURNING id,name,category,manager_user_id,active,created_at,updated_at", strings.TrimSpace(body.Name), strings.TrimSpace(body.Category), body.Manager).Scan(&service.ID, &service.Name, &service.Category, &service.Manager, &service.Active, &service.Created, &service.Updated)
	if err != nil {
		return err
	}
	actor := admin.ID
	_ = auditSQL(r.Context(), tx, &actor, "campus_service.create", "campus_service", service.ID, "", nil, nil, requestID(r.Context()))
	p, err := s.campusServicePayload(r.Context(), tx, service, &admin, false)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, p)
	return nil
}
func (s *Server) adminUpdateCampusService(w http.ResponseWriter, r *http.Request) error {
	admin, err := s.adminUser(w, r)
	if err != nil {
		return err
	}
	id, _ := pathID(r, "serviceID")
	var raw map[string]json.RawMessage
	if err := decodeBody(r, &raw); err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	service, err := scanCampusService(tx.QueryRow(r.Context(), "SELECT id,name,category,manager_user_id,active,created_at,updated_at FROM campus_services WHERE id=$1", id))
	if err != nil {
		return apiError(404, "CAMPUS_SERVICE_NOT_FOUND", "校园服务不存在")
	}
	for key, dest := range map[string]*string{"name": &service.Name, "category": &service.Category} {
		if v, ok := raw[key]; ok {
			var x string
			if json.Unmarshal(v, &x) != nil {
				return validation(key, "Input should be a valid string")
			}
			*dest = strings.TrimSpace(x)
		}
	}
	if v, ok := raw["manager_user_id"]; ok {
		if string(v) == "null" {
			service.Manager = nil
		} else {
			var x int64
			if json.Unmarshal(v, &x) != nil {
				return validation("manager_user_id", "Input should be a valid integer")
			}
			var exists bool
			_ = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM users WHERE id=$1)", x).Scan(&exists)
			if !exists {
				return apiError(404, "USER_NOT_FOUND", "服务管理者不存在")
			}
			service.Manager = &x
		}
	}
	if v, ok := raw["active"]; ok {
		if json.Unmarshal(v, &service.Active) != nil {
			return validation("active", "Input should be a valid boolean")
		}
	}
	_, err = tx.Exec(r.Context(), "UPDATE campus_services SET name=$1,category=$2,manager_user_id=$3,active=$4,updated_at=now() WHERE id=$5", service.Name, service.Category, service.Manager, service.Active, id)
	if err != nil {
		return err
	}
	actor := admin.ID
	_ = auditSQL(r.Context(), tx, &actor, "campus_service.update", "campus_service", id, "", nil, raw, requestID(r.Context()))
	p, err := s.campusServicePayload(r.Context(), tx, service, &admin, false)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func scanCampusService(row pgx.Row) (CampusService, error) {
	var s CampusService
	err := row.Scan(&s.ID, &s.Name, &s.Category, &s.Manager, &s.Active, &s.Created, &s.Updated)
	return s, err
}
func (s *Server) campusServicePayload(ctx context.Context, q queryer, service CampusService, viewer *User, include bool) (map[string]any, error) {
	var count int
	var average *float64
	if err := q.QueryRow(ctx, "SELECT count(*),avg(rating)::float8 FROM campus_service_ratings WHERE service_id=$1", service.ID).Scan(&count, &average); err != nil {
		return nil, err
	}
	var next any
	if viewer != nil {
		var latest *time.Time
		_ = q.QueryRow(ctx, "SELECT max(created_at) FROM campus_service_ratings WHERE service_id=$1 AND user_id=$2", service.ID, viewer.ID).Scan(&latest)
		if latest != nil {
			next = latest.Add(30 * 24 * time.Hour)
		}
	}
	var score any
	if average != nil {
		score = mathRound(*average*10) / 10
	}
	p := map[string]any{"id": service.ID, "name": service.Name, "category": service.Category, "score": score, "rating_count": count, "managed_by_me": viewer != nil && ((service.Manager != nil && *service.Manager == viewer.ID) || viewer.Role == "moderator" || viewer.Role == "admin"), "next_rating_at": next}
	if include {
		rows, err := q.Query(ctx, `SELECT rating.id,rating.rating,rating.body,COALESCE(author.nickname,'已注销用户'),rating.response,COALESCE(responder.nickname,''),rating.created_at,rating.responded_at
			FROM campus_service_ratings rating LEFT JOIN users author ON author.id=rating.user_id LEFT JOIN users responder ON responder.id=rating.responder_id
			WHERE rating.service_id=$1 ORDER BY rating.created_at DESC LIMIT 50`, service.ID)
		if err != nil {
			return nil, err
		}
		// defer Close so an error mid-iteration cannot leak the pooled connection; without
		// it every failing request permanently shrank the pool.
		defer rows.Close()
		ratings := []any{}
		for rows.Next() {
			var id int64
			var rating int
			var body, author, response, responder string
			var created time.Time
			var responded *time.Time
			if err := rows.Scan(&id, &rating, &body, &author, &response, &responder, &created, &responded); err != nil {
				return nil, err
			}
			ratings = append(ratings, map[string]any{"id": id, "rating": rating, "body": body, "author": author, "response": response, "responder": responder, "created_at": created, "responded_at": responded})
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		rows.Close()
		p["ratings"] = ratings
	}
	return p, nil
}
func (s *Server) campusRatingPayload(ctx context.Context, q queryer, id int64) (map[string]any, error) {
	var rating int
	var body, response string
	var userID int64
	var responder *int64
	var created time.Time
	var responded *time.Time
	if err := q.QueryRow(ctx, "SELECT rating,body,user_id,response,responder_id,created_at,responded_at FROM campus_service_ratings WHERE id=$1", id).Scan(&rating, &body, &userID, &response, &responder, &created, &responded); err != nil {
		return nil, err
	}
	var author string
	_ = q.QueryRow(ctx, "SELECT nickname FROM users WHERE id=$1", userID).Scan(&author)
	if author == "" {
		author = "已注销用户"
	}
	responderName := ""
	if responder != nil {
		_ = q.QueryRow(ctx, "SELECT nickname FROM users WHERE id=$1", *responder).Scan(&responderName)
	}
	return map[string]any{"id": id, "rating": rating, "body": body, "author": author, "response": response, "responder": responderName, "created_at": created, "responded_at": responded}, nil
}

// Credit rules.
func (s *Server) publicCreditRules(w http.ResponseWriter, r *http.Request) error {
	p, err := s.creditRulesPayload(r.Context(), s.DB)
	if err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) adminCreditRules(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.adminUser(w, r); err != nil {
		return err
	}
	return s.publicCreditRules(w, r)
}
func (s *Server) updateCreditRules(w http.ResponseWriter, r *http.Request) error {
	admin, err := s.adminUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Rules []creditRuleUpdate `json:"rules"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if len(body.Rules) < 1 || len(body.Rules) > 50 {
		return validation("rules", "List should have at least 1 item")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if err := ensureCreditRulesSQL(r.Context(), tx); err != nil {
		return err
	}
	seen := map[string]bool{}
	before := map[string]int{}
	after := map[string]int{}
	rules := make([]creditRuleUpdate, len(body.Rules))
	copy(rules, body.Rules)
	// Lock rows in a deterministic order. Two admins submitting overlapping key sets in
	// different orders would otherwise take the same row locks in opposite order and
	// deadlock, aborting one of them with a 500.
	sort.Slice(rules, func(i, j int) bool { return rules[i].Key < rules[j].Key })
	for _, item := range rules {
		if seen[item.Key] {
			return validation("rules", "Value error, 信用规则键不能重复")
		}
		seen[item.Key] = true
		def, ok := creditRuleDefs[item.Key]
		if !ok {
			return apiError(400, "CREDIT_RULE_UNKNOWN", "不支持的信用规则："+item.Key)
		}
		if item.Value < -1000 || item.Value > 1000 {
			return validation("value", "Input should be between -1000 and 1000")
		}
		if def.Kind != "penalty" && item.Value < 0 {
			return apiError(400, "CREDIT_RULE_SIGN", def.Label+"不能设置为负数")
		}
		if def.Kind == "penalty" && item.Value > 0 {
			return apiError(400, "CREDIT_RULE_SIGN", def.Label+"不能设置为正数")
		}
		var old int
		if err := tx.QueryRow(r.Context(), "SELECT value FROM credit_rules WHERE key=$1 FOR UPDATE", item.Key).Scan(&old); err != nil {
			return apiError(500, "CREDIT_RULE_MISSING", "信用规则初始化失败")
		}
		if _, err := tx.Exec(r.Context(), "UPDATE credit_rules SET value=$1,updated_by=$2,updated_at=now() WHERE key=$3", item.Value, admin.ID, item.Key); err != nil {
			return err
		}
		before[item.Key] = old
		after[item.Key] = item.Value
	}
	// The unmask gate is only a gate if it sits strictly above the credit every new account
	// starts with. Leaving them equal (both defaulted to 800) meant a freshly registered
	// user could sign the agreement and read every observe post in the clear.
	//
	// Only enforced when this request actually touches one of the two keys: an existing
	// deployment may still hold the old 800/800 pair, and rejecting every unrelated rule
	// change until it is fixed would be a surprising way to find that out.
	if seen["baseline.initial_credit"] || seen["threshold.observe_unmask"] {
		var initialCredit, unmaskThreshold int
		if err := tx.QueryRow(r.Context(), "SELECT (SELECT value FROM credit_rules WHERE key='baseline.initial_credit'),(SELECT value FROM credit_rules WHERE key='threshold.observe_unmask')").Scan(&initialCredit, &unmaskThreshold); err != nil {
			return err
		}
		if unmaskThreshold <= initialCredit {
			return apiError(400, "CREDIT_RULE_CONFLICT", "观察台去码门槛必须高于新用户初始信用")
		}
	}
	actor := admin.ID
	_ = auditSQLText(r.Context(), tx, &actor, "credit_rules.update", "credit_rules", "global", "", before, after, requestID(r.Context()))
	p, err := s.creditRulesPayload(r.Context(), tx)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}

type creditRuleUpdate struct {
	Key   string `json:"key"`
	Value int    `json:"value"`
}

type creditRuleDef struct {
	Label, Kind string
	Value       int
	Description string
}

var creditRuleDefs = map[string]creditRuleDef{"baseline.initial_credit": {"新用户初始信用", "baseline", 800, "仅影响新注册用户"}, "threshold.anonymous_post": {"完全匿名发帖", "threshold", 600, "树洞完全匿名发帖门槛"}, "threshold.team_create": {"创建游戏车队", "threshold", 600, "发布开车门槛"}, "threshold.course_review": {"评价课程", "threshold", 600, "提交课程评价门槛"}, "threshold.listing_publish": {"发布交易帖", "threshold", 700, "二手集市发布门槛"}, "threshold.contact_publish": {"发布联系方式", "threshold", 700, "公开联系方式门槛"}, "threshold.observe_publish": {"观察台发帖", "threshold", 750, "校园文明观察台发帖门槛"}, "threshold.observe_unmask": {"观察台去码查看", "threshold", 900, "满足信用分并签署吃瓜不扩散协议后可查看观察帖原文"}, "threshold.high_credit": {"高信用用户", "threshold", 800, "高信用身份标签门槛"}, "threshold.dm_unlimited": {"私信不限量", "threshold", 850, "解除新用户私信频率限制"}, "reward.team_check_in": {"车队准时签到", "reward", 2, "每场车队首次有效签到奖励"}, "reward.lost_claim": {"失物成功认领", "reward", 5, "失主确认认领完成奖励"}, "reward.feedback_accepted": {"反馈被采纳", "reward", 5, "管理员采纳有效反馈奖励"}, "penalty.team_late_leave": {"临近发车退出", "penalty", -20, "发车前半小时内未请假退出扣分"}}

func ensureCreditRulesSQL(ctx context.Context, tx pgx.Tx) error {
	for key, def := range creditRuleDefs {
		_, err := tx.Exec(ctx, `INSERT INTO credit_rules(key,label,kind,value,description,updated_at) VALUES($1,$2,$3,$4,$5,now()) ON CONFLICT(key) DO NOTHING`, key, def.Label, def.Kind, def.Value, def.Description)
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Server) creditRulesPayload(ctx context.Context, q queryer) (map[string]any, error) {
	rows, err := q.Query(ctx, "SELECT key,label,kind,value,description,updated_at FROM credit_rules ORDER BY kind,key")
	if err != nil {
		return nil, err
	}
	items := []any{}
	values := map[string]int{}
	for rows.Next() {
		var key, label, kind, description string
		var value int
		var updated time.Time
		if err := rows.Scan(&key, &label, &kind, &value, &description, &updated); err != nil {
			return nil, err
		}
		values[key] = value
		items = append(items, map[string]any{"key": key, "label": label, "kind": kind, "value": value, "description": description, "updated_at": updated})
	}
	rows.Close()
	initial := 800
	if value, ok := values["baseline.initial_credit"]; ok {
		initial = value
	}
	return map[string]any{"max_score": 1000, "initial_score": initial, "values": values, "rules": items}, rows.Err()
}
func auditSQLText(ctx context.Context, tx pgx.Tx, actor *int64, action, targetType, targetID, reason string, before, after any, requestID string) error {
	encode := func(v any) string {
		if v == nil {
			return ""
		}
		data, _ := json.Marshal(v)
		return string(data)
	}
	_, err := tx.Exec(ctx, "INSERT INTO audit_logs(actor_id,action,target_type,target_id,reason,before_json,after_json,request_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now())", actor, action, targetType, targetID, reason, encode(before), encode(after), requestID)
	return err
}
