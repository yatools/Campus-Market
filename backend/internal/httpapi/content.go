package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	_ "golang.org/x/image/webp"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"math/big"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	domain "github.com/yatools/wutong-campus-wall/backend/internal/app"
	operationalmetrics "github.com/yatools/wutong-campus-wall/backend/internal/metrics"
)

var imageProcessingSlots = make(chan struct{}, 2)

var anonymousDefaults = []string{
	"丹阳子", "小狐狸", "欧阳牛马", "逍遥客", "青衫剑客", "云游道人", "长安故人", "江湖小虾",
	"桃花岛民", "月下书生", "竹林听雨", "白衣少侠", "北冥小鱼", "南山隐士", "星河旅人", "落霞居士",
	"橘子汽水", "苹果派", "草莓麻薯", "蓝莓贝果", "芒果布丁", "葡萄冻冻", "柚子茶", "蜜桃乌龙",
	"西瓜啵啵", "柠檬糖", "山楂雪球", "桂花糕", "红豆年糕", "芝士土豆", "海盐曲奇", "抹茶团子",
	"小浣熊", "雪地松鼠", "圆脸海豹", "长耳兔", "赤狐同学", "树懒学长", "水豚同学", "云朵羊",
	"月光水母", "深海鲸鱼", "银杏小鹿", "竹叶熊猫", "薄荷仓鼠", "夜行猫头鹰", "蒲公英刺猬", "海边企鹅",
	"火箭浣熊", "银河旅客", "木叶丸子", "魔法少女", "机甲小队长", "侦探小熊", "白帽骑士", "风之使者",
	"星星邮差", "月亮船长", "云端画师", "森林向导", "时间旅人", "纸飞机员", "深夜电台", "晨光信使",
	"青提奶盖", "椰子拿铁", "焦糖爆米花", "紫薯芋圆", "番茄锅底", "葱油拌面", "糯米烧麦", "香菇包子",
	"银杏叶子", "梧桐种子", "山茶花", "小雏菊", "薄荷叶", "四叶草", "蒲公英", "向日葵",
}

type Entity struct {
	ID                           int64
	Type                         string
	OwnerID                      int64
	Status                       string
	AllowComments, SearchVisible bool
	ModerationReason             string
	Revision                     int
	DeletedAt                    *time.Time
	CreatedAt, UpdatedAt         time.Time
}
type Post struct {
	EntityID                         int64
	Board, Title, Body, IdentityMode string
	ExpiresAt                        *time.Time
	Views                            int
}
type Comment struct {
	EntityID, TargetID      int64
	ParentID, ReplyToUserID *int64
	Body, IdentityMode      string
}

func (s *Server) registerContentRoutes(r chi.Router) {
	r.Get("/posts", s.handle(s.listPosts))
	r.Post("/posts", s.handle(s.createPost))
	r.Get("/posts/{postID}", s.handle(s.getPost))
	r.Patch("/posts/{postID}", s.handle(s.updatePost))
	r.Delete("/entities/{entityID}", s.handle(s.deleteEntity))
	r.Get("/entities/{entityID}/revisions", s.handle(s.listRevisions))
	r.Get("/entities/{targetID}/comments", s.handle(s.listComments))
	r.Post("/entities/{targetID}/comments", s.handle(s.createComment))
	r.Put("/entities/{entityID}/reactions/{reactionType}", s.handle(s.addReaction))
	r.Delete("/entities/{entityID}/reactions/{reactionType}", s.handle(s.removeReaction))
	r.Put("/entities/{entityID}/favorite", s.handle(s.favorite))
	r.Delete("/entities/{entityID}/favorite", s.handle(s.unfavorite))
	r.Post("/entities/{entityID}/reports", s.handle(s.reportEntity))
	r.Post("/uploads/images", s.handle(s.uploadImage))
	r.Get("/attachments/{attachmentID}/content", s.handle(s.downloadPrivateAttachment))
	r.Get("/search", s.handle(s.search))
	r.Get("/hot", s.handle(s.hot))
	r.Get("/feed", s.handle(s.listFeed))
	r.Get("/feed/changes", s.handle(s.feedChanges))
}

func (s *Server) downloadPrivateAttachment(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "attachmentID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var path, thumb, scope string
	var buyerID, sellerID int64
	err = s.DB.QueryRow(r.Context(), `SELECT a.path,a.thumbnail_path,a.access_scope,mt.buyer_id,mt.seller_id FROM attachments a JOIN market_dispute_evidence e ON e.attachment_id=a.id JOIN market_disputes d ON d.id=e.dispute_id JOIN market_transactions mt ON mt.id=d.transaction_id WHERE a.id=$1 AND a.status='attached'`, id).Scan(&path, &thumb, &scope, &buyerID, &sellerID)
	if err == pgx.ErrNoRows {
		return apiError(404, "ATTACHMENT_NOT_FOUND", "附件不存在")
	}
	if err != nil {
		return err
	}
	if scope != "market_dispute" {
		return apiError(404, "ATTACHMENT_NOT_FOUND", "附件不存在")
	}
	if user.ID != buyerID && user.ID != sellerID && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "ATTACHMENT_FORBIDDEN", "无权查看该证据")
	}
	if r.URL.Query().Get("thumbnail") == "true" {
		path = thumb
	}
	signed, err := s.Storage.PresignedGet(r.Context(), scope, path, 5*time.Minute, `attachment; filename="evidence.webp"`)
	if err != nil {
		return err
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.Redirect(w, r, signed.String(), http.StatusTemporaryRedirect)
	return nil
}

func (s *Server) listPosts(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pagination(r, 20, 50)
	if err != nil {
		return err
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if runeLen(q) > 80 {
		return validation("q", "String should have at most 80 characters")
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	where := `e.type='post' AND e.publication_status='published' AND e.moderation_status='approved' AND p.board='treehole' AND (p.expires_at IS NULL OR p.expires_at>now())`
	args := []any{}
	if q != "" {
		where += ` AND e.search_visible=true AND (p.title ILIKE $1 OR p.body ILIKE $1)`
		args = append(args, "%"+q+"%")
	}
	var total int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM content_entities e JOIN posts p ON p.entity_id=e.id WHERE "+where, args...).Scan(&total); err != nil {
		return err
	}
	args = append(args, viewer.ID)
	viewerArg := len(args)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(`SELECT e.id,e.owner_id,e.publication_status,e.allow_comments,e.created_at,e.updated_at,p.board,p.title,p.body,p.identity_mode,p.expires_at,p.views,
		CASE WHEN u.status='deleted' THEN '已注销用户' WHEN p.identity_mode='alias' THEN u.alias WHEN p.identity_mode='anonymous' THEN COALESCE(tai.display_name,'匿名同学') ELSE u.nickname END author,
		(SELECT count(*) FROM reactions WHERE entity_id=e.id AND type='like') likes,(SELECT count(*) FROM favorites WHERE entity_id=e.id) favorites,
		(SELECT count(*) FROM comments c JOIN content_entities ce ON ce.id=c.entity_id WHERE c.target_entity_id=e.id AND ce.publication_status='published' AND ce.moderation_status='approved') comments,
		COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id,'path',a.path,'thumbnail_path',a.thumbnail_path,'width',a.width,'height',a.height) ORDER BY a.id) FROM attachments a WHERE a.entity_id=e.id AND a.status='attached' AND a.access_scope='public'),'[]'::jsonb),
		EXISTS(SELECT 1 FROM reactions WHERE entity_id=e.id AND user_id=$%d AND type='like'),EXISTS(SELECT 1 FROM favorites WHERE entity_id=e.id AND user_id=$%d)
		FROM content_entities e JOIN posts p ON p.entity_id=e.id JOIN users u ON u.id=e.owner_id LEFT JOIN thread_anonymous_identities tai ON tai.thread_id=e.id AND tai.user_id=e.owner_id WHERE %s ORDER BY e.created_at DESC LIMIT $%d OFFSET $%d`, viewerArg, viewerArg, where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, ownerID int64
		var status, board, title, body, identity, author string
		var allow bool
		var created, updated time.Time
		var expires *time.Time
		var views, likes, favorites, comments int
		var raw json.RawMessage
		var liked, favorited bool
		if err := rows.Scan(&id, &ownerID, &status, &allow, &created, &updated, &board, &title, &body, &identity, &expires, &views, &author, &likes, &favorites, &comments, &raw, &liked, &favorited); err != nil {
			return err
		}
		var attachments []map[string]any
		_ = json.Unmarshal(raw, &attachments)
		for _, attachment := range attachments {
			attachment["url"] = "/uploads/" + fmt.Sprint(attachment["path"])
			attachment["thumbnail_url"] = "/uploads/" + fmt.Sprint(attachment["thumbnail_path"])
			delete(attachment, "path")
			delete(attachment, "thumbnail_path")
		}
		items = append(items, map[string]any{"id": id, "type": "post", "status": status, "title": title, "body": body, "board": board, "author": author, "identity_mode": identity, "allow_comments": allow, "expires_at": expires, "views": views, "created_at": created, "updated_at": updated, "mine": viewer.ID != 0 && viewer.ID == ownerID, "attachments": attachments, "likes": likes, "favorites": favorites, "comments": comments, "liked": liked, "favorited": favorited})
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}

func (s *Server) checkSearchRateLimit(ctx context.Context, subject string) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := domain.CheckRateLimit(ctx, tx, "search", subject, 120, 1); err != nil {
		if err == domain.ErrRateLimited {
			return apiError(http.StatusTooManyRequests, "RATE_LIMITED", "Search rate limit exceeded")
		}
		return err
	}
	return tx.Commit(ctx)
}

func (s *Server) createPost(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Title         string  `json:"title"`
		Body          string  `json:"body"`
		IdentityMode  string  `json:"identity_mode"`
		Visibility    string  `json:"visibility"`
		AllowComments *bool   `json:"allow_comments"`
		AttachmentIDs []int64 `json:"attachment_ids"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.IdentityMode == "" {
		body.IdentityMode = "anonymous"
	}
	if body.Visibility == "" {
		body.Visibility = "forever"
	}
	if body.AllowComments == nil {
		v := true
		body.AllowComments = &v
	}
	if runeLen(strings.TrimSpace(body.Title)) > 120 {
		return validation("title", "String should have at most 120 characters")
	}
	if runeLen(strings.TrimSpace(body.Body)) < 1 || runeLen(strings.TrimSpace(body.Body)) > 10000 {
		return validation("body", "String should have at least 1 character")
	}
	if !validIdentity(body.IdentityMode) {
		return validation("identity_mode", "Value error, 身份模式无效")
	}
	if body.Visibility != "24h" && body.Visibility != "7d" && body.Visibility != "forever" {
		return validation("visibility", "Value error, 可见期无效")
	}
	if len(body.AttachmentIDs) > 9 {
		return validation("attachment_ids", "List should have at most 9 items")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if body.IdentityMode == "anonymous" {
		if err := s.requireCredit(r.Context(), tx, user, "threshold.anonymous_post", "匿名发帖"); err != nil {
			return err
		}
	}
	var expires *time.Time
	if body.Visibility != "forever" {
		v := time.Now().UTC().Add(24 * time.Hour)
		if body.Visibility == "7d" {
			v = time.Now().UTC().Add(7 * 24 * time.Hour)
		}
		expires = &v
	}
	entity, care, err := s.createEntity(r.Context(), tx, user.ID, "post", body.Title+"\n"+body.Body, *body.AllowComments, body.IdentityMode != "anonymous", false)
	if err != nil {
		return err
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO posts(entity_id,board,title,body,identity_mode,expires_at,views) VALUES($1,'treehole',$2,$3,$4,$5,0)`, entity.ID, strings.TrimSpace(body.Title), strings.TrimSpace(body.Body), body.IdentityMode, expires)
	if err != nil {
		return err
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, entity.ID, body.AttachmentIDs); err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "post.create", "content", entity.ID, "", nil, nil, requestID(r.Context()))
	p := Post{EntityID: entity.ID, Board: "treehole", Title: strings.TrimSpace(body.Title), Body: strings.TrimSpace(body.Body), IdentityMode: body.IdentityMode, ExpiresAt: expires}
	payload, err := s.postPayloadTx(r.Context(), tx, entity, p, &user)
	if err != nil {
		return err
	}
	payload["care"] = care
	if care {
		payload["care_message"] = "如果你正在经历难以承受的时刻，请联系校心理中心或 24 小时心理援助热线 12356。"
	} else {
		payload["care_message"] = ""
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, payload)
	return nil
}

func (s *Server) getPost(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "postID")
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, p, err := getEntityPost(r.Context(), tx, id, true)
	if err == pgx.ErrNoRows {
		return apiError(404, "POST_NOT_FOUND", "帖子不存在")
	}
	if err != nil {
		return err
	}
	preview := viewer.ID != 0 && (viewer.ID == e.OwnerID || viewer.Role == "moderator" || viewer.Role == "admin")
	if e.Status != "published" && !preview {
		return apiError(404, "POST_NOT_FOUND", "帖子不存在")
	}
	if p.ExpiresAt != nil && !p.ExpiresAt.After(time.Now().UTC()) && !preview {
		return apiError(404, "POST_EXPIRED", "帖子已过期")
	}
	p.Views++
	if _, err := tx.Exec(r.Context(), "UPDATE posts SET views=views+1 WHERE entity_id=$1", id); err != nil {
		return err
	}
	payload, err := s.postPayloadTx(r.Context(), tx, e, p, userOrNil(viewer))
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (s *Server) updatePost(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "postID")
	if err != nil {
		return err
	}
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := decodeBody(r, &raw); err != nil {
		return err
	}
	var title, body *string
	var allow *bool
	var attachments []int64
	if value, ok := raw["title"]; ok {
		var v string
		if json.Unmarshal(value, &v) != nil {
			return validation("title", "Input should be a valid string")
		}
		v = strings.TrimSpace(v)
		if runeLen(v) > 120 {
			return validation("title", "String should have at most 120 characters")
		}
		title = &v
	}
	if value, ok := raw["body"]; ok {
		var v string
		if json.Unmarshal(value, &v) != nil {
			return validation("body", "Input should be a valid string")
		}
		v = strings.TrimSpace(v)
		if runeLen(v) == 0 || runeLen(v) > 10000 {
			return validation("body", "String should have at least 1 character")
		}
		body = &v
	}
	if value, ok := raw["allow_comments"]; ok {
		var v bool
		if json.Unmarshal(value, &v) != nil {
			return validation("allow_comments", "Input should be a valid boolean")
		}
		allow = &v
	}
	if value, ok := raw["attachment_ids"]; ok {
		if json.Unmarshal(value, &attachments) != nil || len(attachments) > 9 {
			return validation("attachment_ids", "List should have at most 9 items")
		}
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, p, err := getEntityPost(r.Context(), tx, id, true)
	if err == pgx.ErrNoRows {
		return apiError(404, "POST_NOT_FOUND", "帖子不存在")
	}
	if err != nil {
		return err
	}
	if e.OwnerID != user.ID && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "NOT_OWNER", "无权修改该帖子")
	}
	if title != nil || body != nil {
		_, err = tx.Exec(r.Context(), `INSERT INTO content_revisions(entity_id,editor_id,revision,title,body,created_at) VALUES($1,$2,$3,$4,$5,now())`, e.ID, user.ID, e.Revision, p.Title, p.Body)
		if err != nil {
			return err
		}
		e.Revision++
		_, err = tx.Exec(r.Context(), "UPDATE content_entities SET revision=$1,updated_at=now() WHERE id=$2", e.Revision, e.ID)
		if err != nil {
			return err
		}
	}
	if title != nil {
		p.Title = strings.TrimSpace(*title)
	}
	if body != nil {
		p.Body = strings.TrimSpace(*body)
	}
	if allow != nil {
		e.AllowComments = *allow
	}
	if _, err := tx.Exec(r.Context(), "UPDATE posts SET title=$1,body=$2 WHERE entity_id=$3", p.Title, p.Body, e.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "UPDATE content_entities SET allow_comments=$1,updated_at=now() WHERE id=$2", e.AllowComments, e.ID); err != nil {
		return err
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, e.ID, attachments); err != nil {
		return err
	}
	if err := s.remoderate(r.Context(), tx, &e, p.Title+"\n"+p.Body); err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "post.update", "content", e.ID, "", nil, nil, requestID(r.Context()))
	e.UpdatedAt = time.Now().UTC()
	payload, err := s.postPayloadTx(r.Context(), tx, e, p, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (s *Server) deleteEntity(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "entityID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	if runeLen(reason) > 1000 {
		return validation("reason", "String should have at most 1000 characters")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var e Entity
	err = tx.QueryRow(r.Context(), entitySelect+" WHERE id=$1 FOR UPDATE", id).Scan(entityScan(&e)...)
	if err == pgx.ErrNoRows {
		return apiError(404, "CONTENT_NOT_FOUND", "内容不存在")
	}
	if err != nil {
		return err
	}
	if e.OwnerID != user.ID && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "NOT_OWNER", "无权删除该内容")
	}
	if e.OwnerID != user.ID && runeLen(strings.TrimSpace(reason)) < 2 {
		return apiError(400, "DELETION_REASON_REQUIRED", "审核人员删除内容时必须填写理由")
	}
	var bounty int
	if err := tx.QueryRow(r.Context(), `UPDATE questions SET bounty_settled=true WHERE entity_id=$1 AND bounty_settled=false AND accepted_answer_id IS NULL RETURNING bounty_xp`, id).Scan(&bounty); err == nil {
		if _, err := tx.Exec(r.Context(), "UPDATE users SET xp=xp+$1,updated_at=now() WHERE id=$2", bounty, e.OwnerID); err != nil {
			return err
		}
	} else if err != nil && err != pgx.ErrNoRows {
		return err
	}
	now := time.Now().UTC()
	if _, err := tx.Exec(r.Context(), "UPDATE content_entities SET publication_status='deleted',deleted_at=$1,search_visible=false,updated_at=$1 WHERE id=$2", now, id); err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "content.delete", e.Type, e.ID, firstNonempty(strings.TrimSpace(reason), "作者自行删除"), map[string]any{"status": e.Status}, map[string]any{"status": "deleted", "deleted_at": now}, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}

func (s *Server) listRevisions(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "entityID")
	if err != nil {
		return err
	}
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	var owner int64
	if err := s.DB.QueryRow(r.Context(), "SELECT owner_id FROM content_entities WHERE id=$1", id).Scan(&owner); err == pgx.ErrNoRows {
		return apiError(404, "CONTENT_NOT_FOUND", "内容不存在")
	} else if err != nil {
		return err
	}
	if viewer.ID == 0 || (viewer.ID != owner && viewer.Role != "moderator" && viewer.Role != "admin") {
		return apiError(403, "REVISION_ACCESS_DENIED", "只有作者和审核人员可以查看历史版本")
	}
	var total int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM content_revisions WHERE entity_id=$1", id).Scan(&total); err != nil {
		return err
	}
	rows, err := s.DB.Query(r.Context(), "SELECT id,revision,title,body,editor_id,created_at FROM content_revisions WHERE entity_id=$1 ORDER BY revision DESC LIMIT $2 OFFSET $3", id, size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var rid int64
		var rev int
		var title, body string
		var editor int64
		var created time.Time
		if err := rows.Scan(&rid, &rev, &title, &body, &editor, &created); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": rid, "revision": rev, "title": title, "body": body, "editor_id": editor, "created_at": created})
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}

func (s *Server) listComments(w http.ResponseWriter, r *http.Request) error {
	target, err := pathID(r, "targetID")
	if err != nil {
		return err
	}
	page, size, err := pagination(r, 20, 50)
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	if err := publicEntity(r.Context(), s.DB, target); err != nil {
		return err
	}
	var total int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM comments WHERE target_entity_id=$1 AND parent_id IS NULL", target).Scan(&total); err != nil {
		return err
	}
	rows, err := s.DB.Query(r.Context(), commentSelect+" WHERE c.target_entity_id=$1 AND c.parent_id IS NULL ORDER BY e.created_at LIMIT $2 OFFSET $3", target, size, (page-1)*size)
	if err != nil {
		return err
	}
	roots := []struct {
		e Entity
		c Comment
	}{}
	for rows.Next() {
		e, c, err := scanEntityComment(rows)
		if err != nil {
			return err
		}
		roots = append(roots, struct {
			e Entity
			c Comment
		}{e, c})
	}
	rows.Close()
	items := []any{}
	for _, root := range roots {
		item, err := s.commentPayload(r.Context(), root.e, root.c, userOrNil(viewer))
		if err != nil {
			return err
		}
		replyRows, err := s.DB.Query(r.Context(), commentSelect+" WHERE c.parent_id=$1 ORDER BY e.created_at", root.e.ID)
		if err != nil {
			return err
		}
		replies := []any{}
		for replyRows.Next() {
			e, c, err := scanEntityComment(replyRows)
			if err != nil {
				return err
			}
			p, err := s.commentPayload(r.Context(), e, c, userOrNil(viewer))
			if err != nil {
				return err
			}
			replies = append(replies, p)
		}
		replyRows.Close()
		item["replies"] = replies
		items = append(items, item)
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return nil
}

func (s *Server) createComment(w http.ResponseWriter, r *http.Request) error {
	targetID, err := pathID(r, "targetID")
	if err != nil {
		return err
	}
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Body          string  `json:"body"`
		ParentID      *int64  `json:"parent_id"`
		IdentityMode  string  `json:"identity_mode"`
		AttachmentIDs []int64 `json:"attachment_ids"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.IdentityMode == "" {
		body.IdentityMode = "nickname"
	}
	if strings.TrimSpace(body.Body) == "" || runeLen(strings.TrimSpace(body.Body)) > 3000 {
		return validation("body", "String should have at least 1 character")
	}
	if !validIdentity(body.IdentityMode) {
		return validation("identity_mode", "Value error, 身份模式无效")
	}
	if len(body.AttachmentIDs) > 6 {
		return validation("attachment_ids", "List should have at most 6 items")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var target Entity
	if err := tx.QueryRow(r.Context(), entitySelect+" WHERE id=$1", targetID).Scan(entityScan(&target)...); err == pgx.ErrNoRows || target.Status != "published" {
		return apiError(404, "CONTENT_NOT_FOUND", "评论对象不存在")
	} else if err != nil {
		return err
	}
	if !target.AllowComments {
		return apiError(403, "COMMENTS_CLOSED", "发布者已关闭回帖")
	}
	if body.IdentityMode != "nickname" {
		var board string
		if err := tx.QueryRow(r.Context(), "SELECT board FROM posts WHERE entity_id=$1", targetID).Scan(&board); err != nil || board != "treehole" {
			return apiError(400, "IDENTITY_MODE_NOT_ALLOWED", "该板块回帖需显示昵称")
		}
	}
	var parentRoot, replyTo *int64
	if body.ParentID != nil {
		var c Comment
		var owner int64
		var status string
		err := tx.QueryRow(r.Context(), `SELECT c.entity_id,c.target_entity_id,c.parent_id,c.reply_to_user_id,c.body,c.identity_mode,e.owner_id,e.publication_status FROM comments c JOIN content_entities e ON e.id=c.entity_id WHERE c.entity_id=$1`, *body.ParentID).Scan(&c.EntityID, &c.TargetID, &c.ParentID, &c.ReplyToUserID, &c.Body, &c.IdentityMode, &owner, &status)
		if err != nil || status != "published" || c.TargetID != targetID {
			return apiError(404, "PARENT_NOT_FOUND", "被回复的内容不存在")
		}
		replyTo = &owner
		if c.ParentID != nil {
			parentRoot = c.ParentID
		} else {
			v := c.EntityID
			parentRoot = &v
		}
	}
	entity, _, err := s.createEntity(r.Context(), tx, user.ID, "comment", body.Body, false, false, false)
	if err != nil {
		return err
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO comments(entity_id,target_entity_id,parent_id,reply_to_user_id,body,identity_mode) VALUES($1,$2,$3,$4,$5,$6)`, entity.ID, targetID, parentRoot, replyTo, strings.TrimSpace(body.Body), body.IdentityMode)
	if err != nil {
		return err
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, entity.ID, body.AttachmentIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", targetID); err != nil {
		return err
	}
	recipient := target.OwnerID
	if replyTo != nil {
		recipient = *replyTo
	}
	if recipient != user.ID && entity.Status == "published" {
		author, _ := s.authorName(r.Context(), tx, entity, body.IdentityMode, targetID)
		_ = notifySQL(r.Context(), tx, recipient, "收到新回帖", author+" 回复了你的内容", fmt.Sprintf("/content/%d", targetID), "reply")
	}
	comment := Comment{EntityID: entity.ID, TargetID: targetID, ParentID: parentRoot, ReplyToUserID: replyTo, Body: strings.TrimSpace(body.Body), IdentityMode: body.IdentityMode}
	payload, err := s.commentPayloadTx(r.Context(), tx, entity, comment, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, payload)
	return nil
}

func (s *Server) addReaction(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "entityID")
	if err != nil {
		return err
	}
	kind := chi.URLParam(r, "reactionType")
	if kind != "like" {
		return apiError(400, "REACTION_NOT_SUPPORTED", "暂不支持该互动类型")
	}
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	if err := publicEntity(r.Context(), s.DB, id); err != nil {
		return err
	}
	_, err = s.DB.Exec(r.Context(), "INSERT INTO reactions(entity_id,user_id,type,created_at) VALUES($1,$2,$3,now()) ON CONFLICT(entity_id,user_id,type) DO NOTHING", id, user.ID, kind)
	if err != nil {
		return err
	}
	var count int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM reactions WHERE entity_id=$1 AND type=$2", id, kind).Scan(&count); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"active": true, "count": count})
	return nil
}
func (s *Server) removeReaction(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "entityID")
	if err != nil {
		return err
	}
	kind := chi.URLParam(r, "reactionType")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	if _, err := s.DB.Exec(r.Context(), "DELETE FROM reactions WHERE entity_id=$1 AND user_id=$2 AND type=$3", id, user.ID, kind); err != nil {
		return err
	}
	var count int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM reactions WHERE entity_id=$1 AND type=$2", id, kind).Scan(&count); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"active": false, "count": count})
	return nil
}
func (s *Server) favorite(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "entityID")
	if err != nil {
		return err
	}
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	if err := publicEntity(r.Context(), s.DB, id); err != nil {
		return err
	}
	_, err = s.DB.Exec(r.Context(), "INSERT INTO favorites(entity_id,user_id,created_at) VALUES($1,$2,now()) ON CONFLICT(entity_id,user_id) DO NOTHING", id, user.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"active": true})
	return nil
}
func (s *Server) unfavorite(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "entityID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	_, err = s.DB.Exec(r.Context(), "DELETE FROM favorites WHERE entity_id=$1 AND user_id=$2", id, user.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"active": false})
	return nil
}

func (s *Server) reportEntity(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "entityID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		Reason string `json:"reason"`
		Detail string `json:"detail"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if runeLen(strings.TrimSpace(body.Reason)) < 2 || runeLen(strings.TrimSpace(body.Reason)) > 80 {
		return validation("reason", "String should have at least 2 characters")
	}
	if runeLen(strings.TrimSpace(body.Detail)) > 2000 {
		return validation("detail", "String should have at most 2000 characters")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var owner int64
	var status string
	if err := tx.QueryRow(r.Context(), "SELECT owner_id,publication_status FROM content_entities WHERE id=$1", id).Scan(&owner, &status); err == pgx.ErrNoRows || status != "published" {
		return apiError(404, "CONTENT_NOT_FOUND", "内容不存在")
	} else if err != nil {
		return err
	}
	if owner == user.ID {
		return apiError(400, "SELF_REPORT", "不能举报自己的内容")
	}
	tag, err := tx.Exec(r.Context(), `INSERT INTO reports(entity_id,reporter_id,reason,detail,status,created_at) VALUES($1,$2,$3,$4,'pending',now()) ON CONFLICT(entity_id,reporter_id) DO NOTHING`, id, user.ID, body.Reason, body.Detail)
	if err != nil {
		return err
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO moderation_cases(entity_id,source,status,decision,notes,created_at) VALUES($1,'report','pending','','',now()) ON CONFLICT(entity_id) DO NOTHING`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		_, err = tx.Exec(r.Context(), "UPDATE moderation_cases SET status='pending',source='report',assignee_id=NULL,decision='',decided_at=NULL WHERE entity_id=$1 AND status<>'pending'", id)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"accepted": true})
	return nil
}

func (s *Server) uploadImage(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.Config.MaxUploadBytes+1<<20)
	if err := r.ParseMultipartForm(s.Config.MaxUploadBytes + 1<<20); err != nil {
		return apiError(413, "IMAGE_TOO_LARGE", fmt.Sprintf("图片不能超过 %dMB", s.Config.MaxUploadBytes/1024/1024))
	}
	scope := strings.TrimSpace(r.FormValue("scope"))
	if scope == "" {
		scope = "public"
	}
	if scope != "public" && scope != "market_dispute" {
		return validation("scope", "上传范围无效")
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		return validation("file", "Field required")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, s.Config.MaxUploadBytes+1))
	if err != nil {
		return err
	}
	if int64(len(raw)) > s.Config.MaxUploadBytes {
		return apiError(413, "IMAGE_TOO_LARGE", fmt.Sprintf("图片不能超过 %dMB", s.Config.MaxUploadBytes/1024/1024))
	}
	declared := header.Header.Get("Content-Type")
	allowed := map[string]string{"image/jpeg": "jpeg", "image/png": "png", "image/webp": "webp"}
	want, ok := allowed[declared]
	if !ok {
		return apiError(400, "UNSAFE_IMAGE_TYPE", "仅支持 JPG、PNG 和 WebP 图片")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil {
		return apiError(400, "INVALID_IMAGE", "图片文件无效")
	}
	if format != want {
		return apiError(400, "IMAGE_MIME_MISMATCH", "图片声明类型与实际格式不一致")
	}
	if cfg.Width*cfg.Height > 40_000_000 || cfg.Width > 16_384 || cfg.Height > 16_384 {
		return apiError(400, "IMAGE_DIMENSIONS_TOO_LARGE", "图片像素尺寸过大")
	}
	day := time.Now().UTC().Format("2006/01/02")
	dir, err := os.MkdirTemp("", "wutong-image-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	token := make([]byte, 20)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	key := hex.EncodeToString(token)
	input := filepath.Join(dir, key+"."+format)
	if err := os.WriteFile(input, raw, 0o640); err != nil {
		return err
	}
	defer os.Remove(input)
	full := filepath.Join(dir, key+".webp")
	thumb := filepath.Join(dir, key+"-thumb.webp")
	if err := vipsThumbnail(r.Context(), input, full, "16384x16384>", 86); err != nil {
		return err
	}
	if err := vipsThumbnail(r.Context(), input, thumb, "640x640>", 80); err != nil {
		os.Remove(full)
		return err
	}
	info, err := os.Stat(full)
	if err != nil {
		return err
	}
	relative := filepath.ToSlash(filepath.Join(day, key+".webp"))
	thumbRelative := filepath.ToSlash(filepath.Join(day, key+"-thumb.webp"))
	fullReader, err := os.Open(full)
	if err != nil {
		return err
	}
	if err := s.Storage.Put(r.Context(), scope, relative, "image/webp", map[bool]string{true: "public, max-age=300", false: "private, no-store"}[scope == "public"], fullReader, info.Size()); err != nil {
		fullReader.Close()
		return err
	}
	fullReader.Close()
	thumbInfo, err := os.Stat(thumb)
	if err != nil {
		_ = s.Storage.Remove(r.Context(), scope, relative)
		return err
	}
	thumbReader, err := os.Open(thumb)
	if err != nil {
		_ = s.Storage.Remove(r.Context(), scope, relative)
		return err
	}
	if err := s.Storage.Put(r.Context(), scope, thumbRelative, "image/webp", map[bool]string{true: "public, max-age=300", false: "private, no-store"}[scope == "public"], thumbReader, thumbInfo.Size()); err != nil {
		thumbReader.Close()
		_ = s.Storage.Remove(r.Context(), scope, relative)
		return err
	}
	thumbReader.Close()
	var id int64
	err = s.DB.QueryRow(r.Context(), `INSERT INTO attachments(owner_id,path,thumbnail_path,storage_bucket,access_scope,mime_type,size_bytes,width,height,status,created_at) VALUES($1,$2,$3,$4,$5,'image/webp',$6,$7,$8,'pending',now()) RETURNING id`, user.ID, relative, thumbRelative, s.Storage.BucketName(scope), scope, info.Size(), cfg.Width, cfg.Height).Scan(&id)
	if err != nil {
		_ = s.Storage.Remove(r.Context(), scope, relative)
		_ = s.Storage.Remove(r.Context(), scope, thumbRelative)
		return err
	}
	result := map[string]any{"id": id, "scope": scope, "width": cfg.Width, "height": cfg.Height}
	if scope == "public" {
		result["url"] = "/uploads/" + relative
		result["thumbnail_url"] = "/uploads/" + thumbRelative
	} else {
		result["content_url"] = fmt.Sprintf("%s/attachments/%d/content", s.Config.APIPrefix, id)
	}
	writeJSON(w, 201, result)
	return nil
}

func (s *Server) search(w http.ResponseWriter, r *http.Request) error {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if runeLen(q) < 2 || runeLen(q) > 80 {
		return validation("q", "String should have at least 2 characters")
	}
	page, size, err := pagination(r, 20, 50)
	if err != nil {
		return err
	}
	if page > 100 {
		return validation("page", "Search results are limited to the first 100 pages")
	}
	if err := s.checkSearchRateLimit(r.Context(), clientIP(r)); err != nil {
		return err
	}
	pattern := "%" + q + "%"
	union := `SELECT e.id,'post' type,COALESCE(NULLIF(p.title,''),substr(p.body,1,40)) title,substr(p.body,1,120) summary,e.created_at FROM content_entities e JOIN posts p ON p.entity_id=e.id WHERE e.publication_status='published' AND e.search_visible=true AND (p.title ILIKE $1 OR p.body ILIKE $1)
	UNION ALL SELECT e.id,'question',q.title,substr(q.body,1,120),e.created_at FROM content_entities e JOIN questions q ON q.entity_id=e.id WHERE e.publication_status='published' AND e.search_visible=true AND (q.title ILIKE $1 OR q.body ILIKE $1)
	UNION ALL SELECT e.id,'handbook',h.title,substr(h.body,1,120),e.created_at FROM content_entities e JOIN handbook_articles h ON h.entity_id=e.id WHERE e.publication_status='published' AND e.search_visible=true AND (h.title ILIKE $1 OR h.body ILIKE $1)
	UNION ALL SELECT e.id,'listing',l.title,substr(l.description,1,120),e.created_at FROM content_entities e JOIN listings l ON l.entity_id=e.id WHERE e.publication_status='published' AND e.search_visible=true AND (l.title ILIKE $1 OR l.description ILIKE $1)
	UNION ALL SELECT e.id,'activity',a.title,substr(a.body,1,120),e.created_at FROM content_entities e JOIN activities a ON a.entity_id=e.id WHERE e.publication_status='published' AND e.search_visible=true AND (a.title ILIKE $1 OR a.body ILIKE $1)
	UNION ALL SELECT e.id,'lost',l.item_name,substr(l.description,1,120),e.created_at FROM content_entities e JOIN lost_items l ON l.entity_id=e.id WHERE e.publication_status='published' AND e.search_visible=true AND (l.item_name ILIKE $1 OR l.description ILIKE $1)`
	var total int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM ("+union+") x", pattern).Scan(&total); err != nil {
		return err
	}
	rows, err := s.DB.Query(r.Context(), "SELECT id,type,title,summary FROM ("+union+") x ORDER BY created_at DESC LIMIT $2 OFFSET $3", pattern, size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id int64
		var kind, title, summary string
		if err := rows.Scan(&id, &kind, &title, &summary); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "type": kind, "title": title, "summary": summary})
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}

func (s *Server) hot(w http.ResponseWriter, r *http.Request) error {
	rows, err := s.DB.Query(r.Context(), `SELECT e.id,e.type,e.created_at,COALESCE(rc.likes,0),COALESCE(fc.favorites,0),COALESCE(cc.comments,0),COALESCE(p.title,NULL),COALESCE(p.body,NULL),COALESCE(q.title,NULL),COALESCE(l.title,NULL),COALESCE(a.title,NULL) FROM content_entities e LEFT JOIN (SELECT entity_id,count(*) likes FROM reactions WHERE type='like' GROUP BY entity_id) rc ON rc.entity_id=e.id LEFT JOIN (SELECT entity_id,count(*) favorites FROM favorites GROUP BY entity_id) fc ON fc.entity_id=e.id LEFT JOIN (SELECT c.target_entity_id entity_id,count(*) comments FROM comments c JOIN content_entities ce ON ce.id=c.entity_id WHERE ce.publication_status='published' GROUP BY c.target_entity_id) cc ON cc.entity_id=e.id LEFT JOIN posts p ON p.entity_id=e.id LEFT JOIN questions q ON q.entity_id=e.id LEFT JOIN listings l ON l.entity_id=e.id LEFT JOIN activities a ON a.entity_id=e.id WHERE e.publication_status='published' AND e.created_at>=now()-interval '14 days' ORDER BY e.created_at DESC LIMIT 200`)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []map[string]any{}
	for rows.Next() {
		var id int64
		var kind string
		var created time.Time
		var likes, favorites, comments int
		var pt, pb, qt, lt, at *string
		if err := rows.Scan(&id, &kind, &created, &likes, &favorites, &comments, &pt, &pb, &qt, &lt, &at); err != nil {
			return err
		}
		title := kind
		for _, candidate := range []*string{pt, qt, lt, at} {
			if candidate != nil && *candidate != "" {
				title = *candidate
				break
			}
		}
		if title == kind && pb != nil {
			title = truncateRunes(*pb, 40)
		}
		age := math.Max(0, time.Since(created).Hours())
		score := float64(likes+favorites*3+comments*2) / (1 + age/24)
		items = append(items, map[string]any{"id": id, "type": kind, "title": title, "score": math.Round(score*100) / 100, "likes": likes, "favorites": favorites, "comments": comments})
	}
	sort.Slice(items, func(i, j int) bool { return items[i]["score"].(float64) > items[j]["score"].(float64) })
	if len(items) > 20 {
		items = items[:20]
	}
	out := make([]any, len(items))
	for i := range items {
		out[i] = items[i]
	}
	writeJSON(w, 200, pagePayload(out, 1, 20, len(items)))
	return rows.Err()
}

func (s *Server) listFeed(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pagination(r, 20, 50)
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	rows, err := s.DB.Query(r.Context(), `SELECT e.id,e.type,e.owner_id,e.publication_status,e.allow_comments,e.search_visible,e.moderation_reason,e.revision,e.deleted_at,e.created_at,e.updated_at FROM content_entities e WHERE e.publication_status='published' AND e.type IN ('post','team','question','handbook','course_review','listing','activity','lost_item','observe') ORDER BY e.updated_at DESC,e.id DESC LIMIT $1 OFFSET $2`, size, (page-1)*size)
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
		item, err := s.feedPayload(r.Context(), e, userOrNil(viewer))
		if err != nil {
			return err
		}
		if item != nil {
			items = append(items, item)
		}
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM content_entities WHERE publication_status='published' AND type=ANY($1)", []string{"post", "team", "question", "handbook", "course_review", "listing", "activity", "lost_item", "observe"}).Scan(&total)
	writeJSON(w, 200, map[string]any{"items": items, "page": page, "page_size": size, "total": total, "watermark": time.Now().UTC()})
	return rows.Err()
}
func (s *Server) feedChanges(w http.ResponseWriter, r *http.Request) error {
	afterRaw := r.URL.Query().Get("after")
	after, err := time.Parse(time.RFC3339, afterRaw)
	if err != nil {
		return validation("after", "Input should be a valid datetime")
	}
	var count int
	watermark := time.Now().UTC()
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM content_entities WHERE publication_status='published' AND updated_at>$1 AND updated_at<=$2 AND type=ANY($3)", after, watermark, []string{"post", "team", "question", "handbook", "course_review", "listing", "activity", "lost_item", "observe"}).Scan(&count); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"count": count, "watermark": watermark})
	return nil
}

// Shared content helpers.
const entitySelect = `SELECT id,type,owner_id,publication_status,allow_comments,search_visible,moderation_reason,revision,deleted_at,created_at,updated_at FROM content_entities`
const entityPostSelect = `SELECT e.id,e.type,e.owner_id,e.publication_status,e.allow_comments,e.search_visible,e.moderation_reason,e.revision,e.deleted_at,e.created_at,e.updated_at,p.entity_id,p.board,p.title,p.body,p.identity_mode,p.expires_at,p.views FROM content_entities e JOIN posts p ON p.entity_id=e.id`
const commentSelect = `SELECT e.id,e.type,e.owner_id,e.publication_status,e.allow_comments,e.search_visible,e.moderation_reason,e.revision,e.deleted_at,e.created_at,e.updated_at,c.entity_id,c.target_entity_id,c.parent_id,c.reply_to_user_id,c.body,c.identity_mode FROM content_entities e JOIN comments c ON c.entity_id=e.id`

func entityScan(e *Entity) []any {
	return []any{&e.ID, &e.Type, &e.OwnerID, &e.Status, &e.AllowComments, &e.SearchVisible, &e.ModerationReason, &e.Revision, &e.DeletedAt, &e.CreatedAt, &e.UpdatedAt}
}
func scanEntityPost(row pgx.Row) (Entity, Post, error) {
	var e Entity
	var p Post
	args := append(entityScan(&e), &p.EntityID, &p.Board, &p.Title, &p.Body, &p.IdentityMode, &p.ExpiresAt, &p.Views)
	err := row.Scan(args...)
	return e, p, err
}
func getEntityPost(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64, lock bool) (Entity, Post, error) {
	suffix := ""
	if lock {
		suffix = " FOR UPDATE OF e"
	}
	return scanEntityPost(q.QueryRow(ctx, entityPostSelect+" WHERE e.id=$1"+suffix, id))
}
func scanEntityComment(row pgx.Row) (Entity, Comment, error) {
	var e Entity
	var c Comment
	args := append(entityScan(&e), &c.EntityID, &c.TargetID, &c.ParentID, &c.ReplyToUserID, &c.Body, &c.IdentityMode)
	err := row.Scan(args...)
	return e, c, err
}
func pathID(r *http.Request, key string) (int64, error) {
	value, err := strconv.ParseInt(chi.URLParam(r, key), 10, 64)
	if err != nil || value < 1 {
		return 0, apiError(422, "VALIDATION_ERROR", "提交内容不符合要求")
	}
	return value, nil
}
func pagination(r *http.Request, defaultSize, maxSize int) (int, int, error) {
	page := 1
	size := defaultSize
	var err error
	if raw := r.URL.Query().Get("page"); raw != "" {
		page, err = strconv.Atoi(raw)
		if err != nil || page < 1 {
			return 0, 0, validation("page", "Input should be greater than or equal to 1")
		}
	}
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		size, err = strconv.Atoi(raw)
		if err != nil || size < 1 || size > maxSize {
			return 0, 0, validation("page_size", fmt.Sprintf("Input should be less than or equal to %d", maxSize))
		}
	}
	return page, size, nil
}
func pagePayload(items []any, page, size, total int) map[string]any {
	return map[string]any{"items": items, "page": page, "page_size": size, "total": total}
}
func validIdentity(v string) bool { return v == "nickname" || v == "alias" || v == "anonymous" }
func userOrNil(u User) *User {
	if u.ID == 0 {
		return nil
	}
	return &u
}

func (s *Server) createEntity(ctx context.Context, tx pgx.Tx, owner int64, kind, text string, allow, searchVisible, forceReview bool) (Entity, bool, error) {
	moderationStatus, reason, care, err := s.moderate(ctx, tx, text, forceReview)
	if err != nil {
		return Entity{}, false, err
	}
	publicationStatus := "published"
	storedModerationStatus := "approved"
	if moderationStatus == "pending" {
		publicationStatus = "hidden"
		storedModerationStatus = "pending"
	}
	var e Entity
	err = tx.QueryRow(ctx, `INSERT INTO content_entities(type,owner_id,publication_status,moderation_status,allow_comments,search_visible,moderation_reason,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,1,now(),now()) RETURNING id,type,owner_id,publication_status,allow_comments,search_visible,moderation_reason,revision,deleted_at,created_at,updated_at`, kind, owner, publicationStatus, storedModerationStatus, allow, searchVisible, reason).Scan(entityScan(&e)...)
	if err != nil {
		return Entity{}, false, err
	}
	if moderationStatus == "pending" {
		_, err = tx.Exec(ctx, "INSERT INTO moderation_cases(entity_id,source,status,decision,notes,created_at) VALUES($1,'risk_rule','pending','','',now())", e.ID)
		if err != nil {
			return Entity{}, false, err
		}
	}
	return e, care, nil
}
func (s *Server) moderate(ctx context.Context, tx pgx.Tx, text string, force bool) (string, string, bool, error) {
	reject := []string{"傻逼", "去死吧", "全家死", "人肉", "代考", "代写"}
	review := []string{"电子烟", "处方药", "管制刀", "求扩散", "避雷", "学号", "身份证"}
	var setting string
	err := tx.QueryRow(ctx, "SELECT value FROM settings WHERE key='risk_words'").Scan(&setting)
	if err != nil && err != pgx.ErrNoRows {
		return "", "", false, err
	}
	if setting != "" {
		var value any
		if json.Unmarshal([]byte(setting), &value) == nil {
			switch v := value.(type) {
			case []any:
				for _, x := range v {
					review = append(review, fmt.Sprint(x))
				}
			case map[string]any:
				for _, x := range listAny(v["reject"]) {
					reject = append(reject, fmt.Sprint(x))
				}
				for _, x := range listAny(v["review"]) {
					review = append(review, fmt.Sprint(x))
				}
			}
		} else {
			review = append(review, strings.FieldsFunc(setting, func(r rune) bool { return r == ',' || r == '，' || r == '\n' })...)
		}
	}
	care := containsAny(text, []string{"自杀", "轻生", "不想活", "自残", "活不下去", "想死"})
	for _, word := range reject {
		if word != "" && strings.Contains(strings.ToLower(text), strings.ToLower(word)) {
			return "", "", care, apiError(400, "CONTENT_REJECTED", "命中禁止内容："+word)
		}
	}
	for _, word := range review {
		if word != "" && strings.Contains(strings.ToLower(text), strings.ToLower(word)) {
			return "pending", "需要人工审核：" + word, care, nil
		}
	}
	if force {
		return "pending", "需要人工审核：风险板块", care, nil
	}
	return "published", "", care, nil
}
func (s *Server) remoderate(ctx context.Context, tx pgx.Tx, e *Entity, text string) error {
	status, reason, _, err := s.moderate(ctx, tx, text, false)
	if err != nil {
		return err
	}
	if status != "pending" {
		return nil
	}
	e.Status = "hidden"
	e.ModerationReason = reason
	if _, err := tx.Exec(ctx, "UPDATE content_entities SET publication_status='hidden',moderation_status='pending',moderation_reason=$1 WHERE id=$2", reason, e.ID); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO moderation_cases(entity_id,source,status,decision,notes,created_at) VALUES($1,'edit_risk','pending','','',now()) ON CONFLICT(entity_id) DO UPDATE SET status='pending',source='edit_risk',assignee_id=NULL,decision='',notes='',decided_at=NULL`, e.ID)
	return err
}
func containsAny(value string, words []string) bool {
	for _, w := range words {
		if strings.Contains(value, w) {
			return true
		}
	}
	return false
}
func listAny(v any) []any {
	if x, ok := v.([]any); ok {
		return x
	}
	return nil
}

func (s *Server) requireCredit(ctx context.Context, tx pgx.Tx, user User, key, action string) error {
	value := creditDefault(key)
	_ = tx.QueryRow(ctx, "SELECT value FROM credit_rules WHERE key=$1", key).Scan(&value)
	if user.Credit < value {
		return apiError(403, "CREDIT_REQUIRED", fmt.Sprintf("%s需要信用分不低于 %d", action, value))
	}
	return nil
}
func creditDefault(key string) int {
	values := map[string]int{"baseline.initial_credit": 800, "threshold.anonymous_post": 600, "threshold.team_create": 600, "threshold.course_review": 600, "threshold.listing_publish": 700, "threshold.contact_publish": 700, "threshold.observe_publish": 750, "threshold.observe_unmask": 800, "threshold.high_credit": 800, "threshold.dm_unlimited": 850, "reward.team_check_in": 2, "reward.lost_claim": 5, "reward.feedback_accepted": 5, "penalty.team_late_leave": -20}
	return values[key]
}

// creditThreshold returns the configured value for a threshold credit rule, falling
// back to the built-in default when the credit_rules row has not been seeded yet.
func (s *Server) creditThreshold(ctx context.Context, q queryer, key string) int {
	value := creditDefault(key)
	_ = q.QueryRow(ctx, "SELECT value FROM credit_rules WHERE key=$1", key).Scan(&value)
	return value
}
func (s *Server) attachUploads(ctx context.Context, tx pgx.Tx, userID, entityID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, "SELECT id,status FROM attachments WHERE id=ANY($1) AND owner_id=$2 AND access_scope='public' FOR UPDATE", ids, userID)
	if err != nil {
		return err
	}
	found := map[int64]string{}
	for rows.Next() {
		var id int64
		var status string
		if err := rows.Scan(&id, &status); err != nil {
			return err
		}
		found[id] = status
	}
	rows.Close()
	unique := map[int64]bool{}
	for _, id := range ids {
		unique[id] = true
	}
	if len(found) != len(unique) {
		return apiError(400, "INVALID_ATTACHMENTS", "附件不存在、已使用或不属于当前用户")
	}
	for id, status := range found {
		if status != "pending" {
			return apiError(400, "INVALID_ATTACHMENTS", "附件不存在、已使用或不属于当前用户")
		}
		if _, err := tx.Exec(ctx, "UPDATE attachments SET entity_id=$1,status='attached' WHERE id=$2", entityID, id); err != nil {
			return err
		}
	}
	return nil
}

type queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

func (s *Server) postPayloadTx(ctx context.Context, tx pgx.Tx, e Entity, p Post, viewer *User) (map[string]any, error) {
	return s.postPayloadQ(ctx, tx, e, p, viewer)
}
func (s *Server) postPayloadQ(ctx context.Context, q queryer, e Entity, p Post, viewer *User) (map[string]any, error) {
	author, err := s.authorName(ctx, q, e, p.IdentityMode, e.ID)
	if err != nil {
		return nil, err
	}
	attachments, err := attachmentsPayload(ctx, q, e.ID)
	if err != nil {
		return nil, err
	}
	likes, favorites, comments, err := metrics(ctx, q, e.ID)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{"id": e.ID, "type": e.Type, "status": e.Status, "title": p.Title, "body": p.Body, "board": p.Board, "author": author, "identity_mode": p.IdentityMode, "allow_comments": e.AllowComments, "expires_at": p.ExpiresAt, "views": p.Views, "created_at": e.CreatedAt, "updated_at": e.UpdatedAt, "mine": viewer != nil && viewer.ID == e.OwnerID, "attachments": attachments, "likes": likes, "favorites": favorites, "comments": comments}
	if viewer != nil {
		var liked, favorited bool
		_ = q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM reactions WHERE entity_id=$1 AND user_id=$2 AND type='like'),EXISTS(SELECT 1 FROM favorites WHERE entity_id=$1 AND user_id=$2)", e.ID, viewer.ID).Scan(&liked, &favorited)
		payload["liked"] = liked
		payload["favorited"] = favorited
	}
	return payload, nil
}
func attachmentsPayload(ctx context.Context, q queryer, entityID int64) ([]any, error) {
	rows, err := q.Query(ctx, "SELECT id,path,thumbnail_path,width,height FROM attachments WHERE entity_id=$1 AND status='attached' ORDER BY id", entityID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id int64
		var path, thumb string
		var width, height int
		if err := rows.Scan(&id, &path, &thumb, &width, &height); err != nil {
			return nil, err
		}
		if thumb == "" {
			thumb = path
		}
		items = append(items, map[string]any{"id": id, "url": "/uploads/" + path, "thumbnail_url": "/uploads/" + thumb, "width": width, "height": height})
	}
	return items, rows.Err()
}
func metrics(ctx context.Context, q queryer, id int64) (int, int, int, error) {
	var likes, favorites, comments int
	err := q.QueryRow(ctx, `SELECT (SELECT count(*) FROM reactions WHERE entity_id=$1 AND type='like'),(SELECT count(*) FROM favorites WHERE entity_id=$1),(SELECT count(*) FROM comments c JOIN content_entities e ON e.id=c.entity_id WHERE c.target_entity_id=$1 AND e.publication_status='published')`, id).Scan(&likes, &favorites, &comments)
	return likes, favorites, comments, err
}

func (s *Server) authorName(ctx context.Context, q queryer, e Entity, mode string, threadID int64) (string, error) {
	var nickname, alias, status string
	if err := q.QueryRow(ctx, "SELECT nickname,alias,status FROM users WHERE id=$1", e.OwnerID).Scan(&nickname, &alias, &status); err != nil {
		return "", err
	}
	if status == "deleted" {
		return "已注销用户", nil
	}
	if mode == "alias" {
		return alias, nil
	}
	if mode != "anonymous" {
		return nickname, nil
	}
	var existing string
	err := q.QueryRow(ctx, "SELECT display_name FROM thread_anonymous_identities WHERE thread_id=$1 AND user_id=$2", threadID, e.OwnerID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if err != pgx.ErrNoRows {
		return "", err
	}
	tx, ok := q.(pgx.Tx)
	if !ok {
		return s.anonymousNamePoolFallback(ctx, threadID, e.OwnerID)
	}
	pool := anonymousDefaults
	var setting string
	if tx.QueryRow(ctx, "SELECT value FROM settings WHERE key='anonymous_nickname_pool'").Scan(&setting) == nil {
		var parsed []string
		for _, line := range strings.Split(setting, "\n") {
			line = strings.TrimSpace(line)
			if runeLen(line) >= 2 && runeLen(line) <= 20 {
				parsed = append(parsed, line)
			}
		}
		if len(parsed) > 0 {
			pool = parsed
		}
	}
	for attempt := 0; attempt < 100; attempt++ {
		var used []string
		rows, err := tx.Query(ctx, "SELECT display_name FROM thread_anonymous_identities WHERE thread_id=$1", threadID)
		if err != nil {
			return "", err
		}
		for rows.Next() {
			var x string
			rows.Scan(&x)
			used = append(used, x)
		}
		rows.Close()
		candidate := chooseUnused(pool, used, attempt)
		_, err = tx.Exec(ctx, "INSERT INTO thread_anonymous_identities(thread_id,user_id,display_name,created_at) VALUES($1,$2,$3,now()) ON CONFLICT DO NOTHING", threadID, e.OwnerID, candidate)
		if err != nil {
			return "", err
		}
		if err := tx.QueryRow(ctx, "SELECT display_name FROM thread_anonymous_identities WHERE thread_id=$1 AND user_id=$2", threadID, e.OwnerID).Scan(&existing); err == nil {
			return existing, nil
		}
	}
	return "", fmt.Errorf("无法分配树洞匿名昵称")
}
func (s *Server) anonymousNamePoolFallback(ctx context.Context, thread, user int64) (string, error) {
	var name string
	err := s.DB.QueryRow(ctx, "SELECT display_name FROM thread_anonymous_identities WHERE thread_id=$1 AND user_id=$2", thread, user).Scan(&name)
	return name, err
}
func chooseUnused(pool, used []string, attempt int) string {
	set := map[string]bool{}
	for _, x := range used {
		set[x] = true
	}
	available := []string{}
	for _, x := range pool {
		if !set[x] {
			available = append(available, x)
		}
	}
	if len(available) > 0 {
		return available[randomIndex(len(available))]
	}
	base := pool[randomIndex(len(pool))]
	return truncateRunes(base, 35) + "·" + strconv.Itoa(attempt+2)
}
func randomIndex(max int) int {
	if max <= 1 {
		return 0
	}
	n, _ := rand.Int(rand.Reader, bigInt(int64(max)))
	return int(n.Int64())
}
func bigInt(value int64) *big.Int { return big.NewInt(value) }

func (s *Server) commentPayload(ctx context.Context, e Entity, c Comment, viewer *User) (map[string]any, error) {
	return s.commentPayloadQ(ctx, s.DB, e, c, viewer)
}
func (s *Server) commentPayloadTx(ctx context.Context, tx pgx.Tx, e Entity, c Comment, viewer *User) (map[string]any, error) {
	return s.commentPayloadQ(ctx, tx, e, c, viewer)
}
func (s *Server) commentPayloadQ(ctx context.Context, q queryer, e Entity, c Comment, viewer *User) (map[string]any, error) {
	author, err := s.authorName(ctx, q, e, c.IdentityMode, c.TargetID)
	if err != nil {
		return nil, err
	}
	attachments, err := attachmentsPayload(ctx, q, e.ID)
	if err != nil {
		return nil, err
	}
	var likes int
	if err := q.QueryRow(ctx, "SELECT count(*) FROM reactions WHERE entity_id=$1 AND type='like'", e.ID).Scan(&likes); err != nil {
		return nil, err
	}
	body := c.Body
	if e.Status != "published" {
		// Tombstone: a hidden/deleted comment must not leak its body, its author,
		// or its attachments — the moderated content may be the very material that
		// got it removed (e.g. a doxxing screenshot).
		body = "该回帖已隐藏"
		author = "—"
		attachments = []any{}
	}
	// reply_to_user_id is intentionally NOT exposed: emitting the replied-to author's
	// real numeric user id de-anonymises tree-hole comments (it can be joined across
	// threads and against any endpoint that maps a user id to a name). The key is kept
	// as null for response-schema compatibility; server-side reply notifications use
	// the separately-computed replyTo, not this field.
	payload := map[string]any{"id": e.ID, "target_entity_id": c.TargetID, "parent_id": c.ParentID, "reply_to_user_id": nil, "body": body, "author": author, "identity_mode": c.IdentityMode, "status": e.Status, "mine": viewer != nil && e.OwnerID == viewer.ID, "created_at": e.CreatedAt, "updated_at": e.UpdatedAt, "attachments": attachments, "likes": likes}
	if viewer != nil {
		var liked bool
		_ = q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM reactions WHERE entity_id=$1 AND user_id=$2 AND type='like')", e.ID, viewer.ID).Scan(&liked)
		payload["liked"] = liked
	}
	return payload, nil
}

func publicEntity(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64) error {
	var exists bool
	if err := q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM content_entities WHERE id=$1 AND publication_status='published' AND moderation_status='approved')", id).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return apiError(404, "CONTENT_NOT_FOUND", "内容不存在")
	}
	return nil
}
func auditSQL(ctx context.Context, tx pgx.Tx, actor *int64, action, targetType string, targetID int64, reason string, before, after any, requestID string) error {
	encode := func(v any) string {
		if v == nil {
			return ""
		}
		data, _ := json.Marshal(v)
		return string(data)
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,action,target_type,target_id,reason,before_json,after_json,request_id,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now())`, actor, action, targetType, strconv.FormatInt(targetID, 10), reason, encode(before), encode(after), requestID)
	return err
}
func notifySQL(ctx context.Context, tx pgx.Tx, userID int64, title, body, link, kind string) error {
	_, err := tx.Exec(ctx, "INSERT INTO notifications(user_id,type,title,body,link,created_at) VALUES($1,$2,$3,$4,$5,now())", userID, kind, title, body, link)
	return err
}

func vipsThumbnail(ctx context.Context, input, output, size string, quality int) error {
	select {
	case imageProcessingSlots <- struct{}{}:
		defer func() { <-imageProcessingSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	processCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	started := time.Now()
	defer func() { operationalmetrics.Default.Observe("image_processing_duration_seconds", time.Since(started)) }()
	bin := os.Getenv("VIPS_THUMBNAIL_BIN")
	if bin == "" {
		bin = "vipsthumbnail"
	}
	command := exec.CommandContext(processCtx, bin, input, "--size", size, "--rotate", "--output", fmt.Sprintf("%s[Q=%d,strip]", output, quality))
	if data, err := command.CombinedOutput(); err != nil {
		operationalmetrics.Default.Inc("image_processing_failures_total")
		return fmt.Errorf("图片处理失败: %w: %s", err, truncateRunes(string(data), 300))
	}
	return nil
}
func firstNonempty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
func truncateRunes(value string, n int) string {
	r := []rune(value)
	if len(r) <= n {
		return value
	}
	return string(r[:n])
}

func (s *Server) feedPayload(ctx context.Context, e Entity, viewer *User) (map[string]any, error) {
	base := map[string]any{"id": e.ID, "type": e.Type, "created_at": e.CreatedAt, "updated_at": e.UpdatedAt, "meta": map[string]any{}, "route": "/", "author": ""}
	var title, body string
	switch e.Type {
	case "post":
		var p Post
		err := s.DB.QueryRow(ctx, "SELECT entity_id,board,title,body,identity_mode,expires_at,views FROM posts WHERE entity_id=$1", e.ID).Scan(&p.EntityID, &p.Board, &p.Title, &p.Body, &p.IdentityMode, &p.ExpiresAt, &p.Views)
		if err != nil {
			return nil, err
		}
		title, body = p.Title, p.Body
		author, err := s.authorName(ctx, s.DB, e, p.IdentityMode, e.ID)
		if err != nil {
			return nil, err
		}
		base["author"], base["route"] = author, "/treehole"
		base["meta"] = map[string]any{"identity_mode": p.IdentityMode, "expires_at": p.ExpiresAt, "views": p.Views}
	case "team":
		var game, mode, notes, status, newbie, vibe, owner string
		var gameID *int64
		var capacity int
		err := s.DB.QueryRow(ctx, `SELECT t.game,t.mode,t.notes,t.status,t.game_id,t.capacity,t.newbie_level,t.vibe,u.nickname FROM teams t JOIN users u ON u.id=t.owner_id WHERE t.entity_id=$1`, e.ID).Scan(&game, &mode, &notes, &status, &gameID, &capacity, &newbie, &vibe, &owner)
		if err != nil {
			return nil, err
		}
		if status != "active" {
			return nil, nil
		}
		title, body = game+" · "+mode, notes
		base["author"], base["route"] = owner, fmt.Sprintf("/teams/%d", e.ID)
		base["meta"] = map[string]any{"game": game, "game_id": gameID, "capacity": capacity, "newbie_level": newbie, "vibe": vibe}
	case "question":
		var category string
		var bounty int
		var accepted *int64
		if err := s.DB.QueryRow(ctx, "SELECT title,body,category,bounty_xp,accepted_answer_id FROM questions WHERE entity_id=$1", e.ID).Scan(&title, &body, &category, &bounty, &accepted); err != nil {
			return nil, err
		}
		base["route"] = "/explore/questions"
		base["meta"] = map[string]any{"category": category, "bounty_xp": bounty, "accepted": accepted != nil}
	case "handbook":
		var category string
		var featured *time.Time
		if err := s.DB.QueryRow(ctx, "SELECT title,body,category,featured_at FROM handbook_articles WHERE entity_id=$1", e.ID).Scan(&title, &body, &category, &featured); err != nil {
			return nil, err
		}
		base["route"] = "/explore/handbook"
		base["meta"] = map[string]any{"category": category, "featured": featured != nil}
	case "course_review":
		var course, teacher, semester, tags string
		var rating int
		if err := s.DB.QueryRow(ctx, `SELECT c.name,c.teacher,o.semester,r.body,r.rating,r.tags FROM course_reviews r JOIN course_offerings o ON o.id=r.offering_id JOIN courses c ON c.id=o.course_id WHERE r.entity_id=$1`, e.ID).Scan(&course, &teacher, &semester, &body, &rating, &tags); err != nil {
			return nil, err
		}
		title = course + " · " + teacher
		base["author"], base["route"] = "匿名课评", "/explore/courses"
		base["meta"] = map[string]any{"rating": rating, "semester": semester, "tags": splitCSV(tags)}
	case "listing":
		var status, category, condition, location string
		var priceCents int64
		var negotiable bool
		if err := s.DB.QueryRow(ctx, `SELECT l.title,l.description,l.trade_status,c.name,l.price_cents,l.condition,loc.name,l.negotiable FROM listings l JOIN market_categories c ON c.id=l.category_id JOIN market_locations loc ON loc.id=l.location_id WHERE l.entity_id=$1`, e.ID).Scan(&title, &body, &status, &category, &priceCents, &condition, &location, &negotiable); err != nil {
			return nil, err
		}
		if status != "available" && status != "reserved" {
			return nil, nil
		}
		base["route"] = "/explore/listings"
		base["meta"] = map[string]any{"category": category, "price_cents": priceCents, "condition": condition, "location": location, "negotiable": negotiable}
	case "activity":
		var status, category, location string
		var starts time.Time
		var capacity *int
		if err := s.DB.QueryRow(ctx, "SELECT title,body,status,category,location,starts_at,capacity FROM activities WHERE entity_id=$1", e.ID).Scan(&title, &body, &status, &category, &location, &starts, &capacity); err != nil {
			return nil, err
		}
		if status != "open" {
			return nil, nil
		}
		base["route"] = "/explore/activities"
		base["meta"] = map[string]any{"category": category, "location": location, "starts_at": starts, "capacity": capacity}
	case "lost_item":
		var kind, location, status string
		if err := s.DB.QueryRow(ctx, "SELECT item_name,description,kind,location,status FROM lost_items WHERE entity_id=$1", e.ID).Scan(&title, &body, &kind, &location, &status); err != nil {
			return nil, err
		}
		base["route"] = "/explore/lost"
		base["meta"] = map[string]any{"kind": kind, "location": location, "status": status}
	case "observe":
		var response string
		if err := s.DB.QueryRow(ctx, "SELECT title,body_masked,response FROM observe_posts WHERE entity_id=$1", e.ID).Scan(&title, &body, &response); err != nil {
			return nil, err
		}
		base["author"], base["route"] = "文明观察员", "/explore/observe"
		base["meta"] = map[string]any{"responded": response != ""}
	default:
		return nil, nil
	}
	if base["author"] == "" {
		author, err := s.authorName(ctx, s.DB, e, "nickname", e.ID)
		if err != nil {
			return nil, err
		}
		base["author"] = author
	}
	base["title"], base["body"] = title, body
	likes, favorites, comments, err := metrics(ctx, s.DB, e.ID)
	if err != nil {
		return nil, err
	}
	base["likes"], base["favorites"], base["comments"] = likes, favorites, comments
	attachments, err := attachmentsPayload(ctx, s.DB, e.ID)
	if err != nil {
		return nil, err
	}
	base["attachments"] = attachments
	if viewer != nil {
		var liked, favorited bool
		_ = s.DB.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM reactions WHERE entity_id=$1 AND user_id=$2 AND type='like'),EXISTS(SELECT 1 FROM favorites WHERE entity_id=$1 AND user_id=$2)", e.ID, viewer.ID).Scan(&liked, &favorited)
		base["liked"], base["favorited"] = liked, favorited
	}
	return base, nil
}
