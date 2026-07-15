package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
)

type JSONDate struct{ time.Time }

func (d *JSONDate) UnmarshalJSON(data []byte) error {
	value, err := strconv.Unquote(string(data))
	if err != nil {
		return err
	}
	parsed, err := time.Parse("2006-01-02", value)
	if err != nil {
		return err
	}
	d.Time = parsed
	return nil
}

func (s *Server) registerModuleRoutes(r chi.Router) {
	r.Get("/questions", s.handle(s.listQuestions))
	r.Post("/questions", s.handle(s.createQuestion))
	r.Patch("/questions/{questionID}", s.handle(s.updateQuestion))
	r.Get("/questions/{questionID}", s.handle(s.getQuestion))
	r.Post("/questions/{questionID}/answers", s.handle(s.createAnswer))
	r.Post("/answers/{answerID}/accept", s.handle(s.acceptAnswer))
	r.Get("/handbook", s.handle(s.listHandbook))
	r.Post("/handbook", s.handle(s.createHandbook))
	r.Patch("/handbook/{articleID}", s.handle(s.updateHandbook))
	r.Get("/handbook/{articleID}", s.handle(s.getHandbook))
	r.Post("/handbook/{articleID}/publish", s.handle(s.publishHandbook))
	r.Post("/handbook/{articleID}/feature", s.handle(s.featureHandbook))
	r.Get("/course-offerings", s.handle(s.listCourseOfferings))
	r.Post("/courses", s.handle(s.createCourse))
	r.Post("/course-offerings", s.handle(s.createCourseOffering))
	r.Post("/course-reviews", s.handle(s.createCourseReview))
	r.Post("/course-reviews/{reviewID}/correction", s.handle(s.correctCourseReview))
	r.Get("/listings", s.handle(s.listListings))
	r.Post("/listings", s.handle(s.createListing))
	r.Patch("/listings/{listingID}/status", s.handle(s.updateListingStatus))
	r.Patch("/listings/{listingID}", s.handle(s.updateListing))
	r.Get("/activities", s.handle(s.listActivities))
	r.Post("/activities", s.handle(s.createActivity))
	r.Patch("/activities/{activityID}", s.handle(s.updateActivity))
	r.Put("/activities/{activityID}/membership", s.handle(s.joinActivity))
	r.Delete("/activities/{activityID}/membership", s.handle(s.leaveActivity))
	r.Post("/activities/{activityID}/cancel", s.handle(s.cancelActivity))
	r.Get("/lost-items", s.handle(s.listLostItems))
	r.Post("/lost-items", s.handle(s.createLostItem))
	r.Patch("/lost-items/{itemID}", s.handle(s.updateLostItem))
	r.Post("/lost-items/{itemID}/claims", s.handle(s.createLostClaim))
	r.Get("/lost-items/{itemID}/claims", s.handle(s.listLostClaims))
	r.Post("/lost-items/{itemID}/claims/{claimID}/decision", s.handle(s.decideLostClaim))
}

// Questions.
type Question struct {
	ID                          int64
	Title, Body, Category, Tags string
	Bounty                      int
	Settled                     bool
	Accepted                    *int64
}
type Answer struct {
	ID, QuestionID int64
	Body           string
}

const questionSelect = `SELECT entity_id,title,body,category,tags,bounty_xp,bounty_settled,accepted_answer_id FROM questions`

func scanQuestion(row pgx.Row) (Question, error) {
	var q Question
	err := row.Scan(&q.ID, &q.Title, &q.Body, &q.Category, &q.Tags, &q.Bounty, &q.Settled, &q.Accepted)
	return q, err
}

func (s *Server) listQuestions(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pagination(r, 20, 50)
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	where := "e.status='published'"
	args := []any{}
	if category := r.URL.Query().Get("category"); category != "" {
		if len(category) > 60 {
			return validation("category", "String should have at most 60 characters")
		}
		args = append(args, category)
		where += " AND q.category=$1"
	}
	var total int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM questions q JOIN content_entities e ON e.id=q.entity_id WHERE "+where, args...).Scan(&total); err != nil {
		return err
	}
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(`SELECT e.id,e.type,e.owner_id,e.status,e.allow_comments,e.search_visible,e.moderation_reason,e.revision,e.deleted_at,e.created_at,e.updated_at,q.entity_id,q.title,q.body,q.category,q.tags,q.bounty_xp,q.bounty_settled,q.accepted_answer_id FROM content_entities e JOIN questions q ON q.entity_id=e.id WHERE %s ORDER BY e.created_at DESC LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		e, q, err := scanEntityQuestion(rows)
		if err != nil {
			return err
		}
		p, err := s.questionPayload(r.Context(), s.DB, e, q, userOrNil(viewer), false)
		if err != nil {
			return err
		}
		items = append(items, p)
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) createQuestion(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Title         string   `json:"title"`
		Body          string   `json:"body"`
		Category      string   `json:"category"`
		Tags          []string `json:"tags"`
		Bounty        int      `json:"bounty_xp"`
		AttachmentIDs []int64  `json:"attachment_ids"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Category == "" {
		body.Category = "其他"
	}
	fields := map[string]string{}
	if runeLen(strings.TrimSpace(body.Title)) < 4 || len(body.Title) > 160 {
		fields["title"] = "String should have at least 4 characters"
	}
	if len(body.Body) > 10000 {
		fields["body"] = "String should have at most 10000 characters"
	}
	if len(body.Tags) > 8 {
		fields["tags"] = "List should have at most 8 items"
	}
	if body.Bounty < 0 || body.Bounty > 500 {
		fields["bounty_xp"] = "Input should be between 0 and 500"
	}
	if len(body.AttachmentIDs) > 9 {
		fields["attachment_ids"] = "List should have at most 9 items"
	}
	if len(fields) > 0 {
		return validationFields(fields)
	}
	if body.Bounty > user.XP {
		return apiError(400, "XP_NOT_ENOUGH", "经验余额不足以支付悬赏")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, _, err := s.createEntity(r.Context(), tx, user.ID, "question", body.Title+"\n"+body.Body, true, true, false)
	if err != nil {
		return err
	}
	tags := strings.Join(cleanStrings(body.Tags, 80), ",")
	if _, err := tx.Exec(r.Context(), `INSERT INTO questions(entity_id,title,body,category,tags,bounty_xp,bounty_settled) VALUES($1,$2,$3,$4,$5,$6,false)`, e.ID, strings.TrimSpace(body.Title), strings.TrimSpace(body.Body), strings.TrimSpace(body.Category), tags, body.Bounty); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "UPDATE users SET xp=xp-$1,updated_at=now() WHERE id=$2", body.Bounty, user.ID); err != nil {
		return err
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, e.ID, body.AttachmentIDs); err != nil {
		return err
	}
	q := Question{ID: e.ID, Title: strings.TrimSpace(body.Title), Body: strings.TrimSpace(body.Body), Category: strings.TrimSpace(body.Category), Tags: tags, Bounty: body.Bounty}
	p, err := s.questionPayload(r.Context(), tx, e, q, &user, true)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, p)
	return nil
}
func (s *Server) updateQuestion(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "questionID")
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
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, err := getEntityForUpdate(r.Context(), tx, id)
	if err == pgx.ErrNoRows {
		return apiError(404, "QUESTION_NOT_FOUND", "问题不存在")
	}
	if err != nil {
		return err
	}
	q, err := scanQuestion(tx.QueryRow(r.Context(), questionSelect+" WHERE entity_id=$1", id))
	if err != nil {
		return apiError(404, "QUESTION_NOT_FOUND", "问题不存在")
	}
	if e.OwnerID != user.ID {
		return apiError(403, "NOT_OWNER", "只有提问者可以编辑问题")
	}
	if q.Accepted != nil {
		return apiError(409, "QUESTION_SETTLED", "已有采纳回答的问题不能再编辑")
	}
	changed := false
	if v, ok := raw["title"]; ok {
		var x string
		if json.Unmarshal(v, &x) != nil || runeLen(strings.TrimSpace(x)) < 4 || len(x) > 160 {
			return validation("title", "String should have at least 4 characters")
		}
		q.Title = strings.TrimSpace(x)
		changed = true
	}
	if v, ok := raw["body"]; ok {
		var x string
		if json.Unmarshal(v, &x) != nil || len(x) > 10000 {
			return validation("body", "String should have at most 10000 characters")
		}
		q.Body = strings.TrimSpace(x)
		changed = true
	}
	if v, ok := raw["category"]; ok {
		var x string
		if json.Unmarshal(v, &x) != nil || len(x) > 60 {
			return validation("category", "String should have at most 60 characters")
		}
		q.Category = strings.TrimSpace(x)
		changed = true
	}
	if v, ok := raw["tags"]; ok {
		var x []string
		if json.Unmarshal(v, &x) != nil || len(x) > 8 {
			return validation("tags", "List should have at most 8 items")
		}
		q.Tags = strings.Join(cleanStrings(x, 80), ",")
		changed = true
	}
	var attachments []int64
	if v, ok := raw["attachment_ids"]; ok {
		if json.Unmarshal(v, &attachments) != nil || len(attachments) > 9 {
			return validation("attachment_ids", "List should have at most 9 items")
		}
	}
	if changed {
		if err := recordRevision(r.Context(), tx, e, user.ID, q.Title, q.Body); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), "UPDATE questions SET title=$1,body=$2,category=$3,tags=$4 WHERE entity_id=$5", q.Title, q.Body, q.Category, q.Tags, id); err != nil {
			return err
		}
		if err := s.remoderate(r.Context(), tx, &e, q.Title+"\n"+q.Body); err != nil {
			return err
		}
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, id, attachments); err != nil {
		return err
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", id)
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "question.update", "question", id, "", nil, nil, requestID(r.Context()))
	e.UpdatedAt = time.Now().UTC()
	p, err := s.questionPayload(r.Context(), tx, e, q, &user, true)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) getQuestion(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "questionID")
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	e, err := getEntity(r.Context(), s.DB, id)
	if err == pgx.ErrNoRows {
		return apiError(404, "QUESTION_NOT_FOUND", "问题不存在")
	}
	if err != nil {
		return err
	}
	q, err := scanQuestion(s.DB.QueryRow(r.Context(), questionSelect+" WHERE entity_id=$1", id))
	if err != nil {
		return apiError(404, "QUESTION_NOT_FOUND", "问题不存在")
	}
	if e.Status != "published" && (viewer.ID == 0 || viewer.ID != e.OwnerID) {
		return apiError(404, "QUESTION_NOT_FOUND", "问题不存在")
	}
	p, err := s.questionPayload(r.Context(), s.DB, e, q, userOrNil(viewer), true)
	if err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) createAnswer(w http.ResponseWriter, r *http.Request) error {
	questionID, err := pathID(r, "questionID")
	if err != nil {
		return err
	}
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
	if runeLen(strings.TrimSpace(body.Body)) < 2 || len(body.Body) > 10000 {
		return validation("body", "String should have at least 2 characters")
	}
	if len(body.AttachmentIDs) > 6 {
		return validation("attachment_ids", "List should have at most 6 items")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	parent, err := getEntity(r.Context(), tx, questionID)
	if err != nil || parent.Status != "published" {
		return apiError(404, "QUESTION_NOT_FOUND", "问题不存在")
	}
	q, err := scanQuestion(tx.QueryRow(r.Context(), questionSelect+" WHERE entity_id=$1", questionID))
	if err != nil {
		return apiError(404, "QUESTION_NOT_FOUND", "问题不存在")
	}
	e, _, err := s.createEntity(r.Context(), tx, user.ID, "answer", body.Body, false, false, false)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "INSERT INTO answers(entity_id,question_id,body) VALUES($1,$2,$3)", e.ID, questionID, strings.TrimSpace(body.Body)); err != nil {
		return err
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, e.ID, body.AttachmentIDs); err != nil {
		return err
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", questionID)
	if parent.OwnerID != user.ID && e.Status == "published" {
		_ = notifySQL(r.Context(), tx, parent.OwnerID, "问题有了新回答", q.Title, fmt.Sprintf("/questions/%d", questionID), "answer")
	}
	p, err := s.answerPayload(r.Context(), tx, e, Answer{ID: e.ID, QuestionID: questionID, Body: strings.TrimSpace(body.Body)}, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, p)
	return nil
}
func (s *Server) acceptAnswer(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "answerID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var answer Answer
	if err := tx.QueryRow(r.Context(), "SELECT entity_id,question_id,body FROM answers WHERE entity_id=$1", id).Scan(&answer.ID, &answer.QuestionID, &answer.Body); err == pgx.ErrNoRows {
		return apiError(404, "ANSWER_NOT_FOUND", "回答不存在")
	} else if err != nil {
		return err
	}
	q, err := scanQuestion(tx.QueryRow(r.Context(), questionSelect+" WHERE entity_id=$1 FOR UPDATE", answer.QuestionID))
	if err != nil {
		return apiError(404, "QUESTION_NOT_FOUND", "问题不存在")
	}
	questionEntity, err := getEntity(r.Context(), tx, answer.QuestionID)
	if err != nil {
		return apiError(404, "QUESTION_NOT_FOUND", "问题不存在")
	}
	answerEntity, err := getEntity(r.Context(), tx, id)
	if err != nil || answerEntity.Status != "published" {
		return apiError(404, "QUESTION_NOT_FOUND", "问题不存在")
	}
	if questionEntity.OwnerID != user.ID {
		return apiError(403, "ASKER_REQUIRED", "只有提问者可以采纳")
	}
	if answerEntity.OwnerID == user.ID {
		return apiError(400, "SELF_ACCEPT_NOT_ALLOWED", "不能采纳自己的回答")
	}
	if q.Accepted != nil {
		if *q.Accepted == id {
			writeJSON(w, 200, map[string]any{"ok": true, "awarded_xp": 0})
			return nil
		}
		return apiError(409, "ALREADY_ACCEPTED", "该问题已经采纳过回答")
	}
	reward := 20 + q.Bounty
	if _, err := tx.Exec(r.Context(), "UPDATE questions SET accepted_answer_id=$1,bounty_settled=true WHERE entity_id=$2", id, q.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "UPDATE users SET xp=xp+$1,updated_at=now() WHERE id=$2", reward, answerEntity.OwnerID); err != nil {
		return err
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", q.ID)
	_ = notifySQL(r.Context(), tx, answerEntity.OwnerID, "回答被采纳", fmt.Sprintf("获得 %d 经验", reward), fmt.Sprintf("/questions/%d", q.ID), "answer")
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "awarded_xp": reward})
	return nil
}

func scanEntityQuestion(row pgx.Row) (Entity, Question, error) {
	var e Entity
	var q Question
	args := append(entityScan(&e), &q.ID, &q.Title, &q.Body, &q.Category, &q.Tags, &q.Bounty, &q.Settled, &q.Accepted)
	err := row.Scan(args...)
	return e, q, err
}
func (s *Server) answerPayload(ctx context.Context, qry queryer, e Entity, a Answer, viewer *User) (map[string]any, error) {
	author, err := s.authorName(ctx, qry, e, "nickname", e.ID)
	if err != nil {
		return nil, err
	}
	files, err := attachmentsPayload(ctx, qry, e.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": e.ID, "body": a.Body, "author": author, "mine": viewer != nil && e.OwnerID == viewer.ID, "status": e.Status, "created_at": e.CreatedAt, "updated_at": e.UpdatedAt, "attachments": files}, nil
}
func (s *Server) questionPayload(ctx context.Context, qry queryer, e Entity, q Question, viewer *User, detail bool) (map[string]any, error) {
	rows, err := qry.Query(ctx, `SELECT e.id,e.type,e.owner_id,e.status,e.allow_comments,e.search_visible,e.moderation_reason,e.revision,e.deleted_at,e.created_at,e.updated_at,a.entity_id,a.question_id,a.body FROM content_entities e JOIN answers a ON a.entity_id=e.id WHERE a.question_id=$1 AND e.status='published' ORDER BY e.created_at`, e.ID)
	if err != nil {
		return nil, err
	}
	answers := []any{}
	for rows.Next() {
		var ae Entity
		var a Answer
		args := append(entityScan(&ae), &a.ID, &a.QuestionID, &a.Body)
		if err := rows.Scan(args...); err != nil {
			return nil, err
		}
		p, err := s.answerPayload(ctx, qry, ae, a, viewer)
		if err != nil {
			return nil, err
		}
		answers = append(answers, p)
	}
	rows.Close()
	author, err := s.authorName(ctx, qry, e, "nickname", e.ID)
	if err != nil {
		return nil, err
	}
	files, err := attachmentsPayload(ctx, qry, e.ID)
	if err != nil {
		return nil, err
	}
	p := map[string]any{"id": e.ID, "title": q.Title, "body": q.Body, "category": q.Category, "tags": splitCSV(q.Tags), "bounty_xp": q.Bounty, "accepted_answer_id": q.Accepted, "author": author, "answer_count": len(answers), "mine": viewer != nil && e.OwnerID == viewer.ID, "status": e.Status, "created_at": e.CreatedAt, "updated_at": e.UpdatedAt, "attachments": files}
	if detail {
		p["answers"] = answers
	}
	return p, nil
}

// Handbook.
type Article struct {
	ID                    int64
	Category, Title, Body string
	Featured              *time.Time
	Rewarded              bool
}

const articleSelect = `SELECT entity_id,category,title,body,featured_at,featured_rewarded FROM handbook_articles`

func scanArticle(row pgx.Row) (Article, error) {
	var a Article
	err := row.Scan(&a.ID, &a.Category, &a.Title, &a.Body, &a.Featured, &a.Rewarded)
	return a, err
}
func (s *Server) listHandbook(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pagination(r, 20, 50)
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	where := "e.status='published'"
	args := []any{}
	if category := r.URL.Query().Get("category"); category != "" {
		args = append(args, category)
		where += " AND a.category=$1"
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM handbook_articles a JOIN content_entities e ON e.id=a.entity_id WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(`SELECT e.id,e.type,e.owner_id,e.status,e.allow_comments,e.search_visible,e.moderation_reason,e.revision,e.deleted_at,e.created_at,e.updated_at,a.entity_id,a.category,a.title,a.body,a.featured_at,a.featured_rewarded FROM content_entities e JOIN handbook_articles a ON a.entity_id=e.id WHERE %s ORDER BY a.featured_at DESC NULLS LAST,e.created_at DESC LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		e, a, err := scanEntityArticle(rows)
		if err != nil {
			return err
		}
		p, err := s.articlePayload(r.Context(), s.DB, e, a, userOrNil(viewer))
		if err != nil {
			return err
		}
		items = append(items, p)
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) createHandbook(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Category, Title, Body string
		Draft                 bool    `json:"draft"`
		AttachmentIDs         []int64 `json:"attachment_ids"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if runeLen(strings.TrimSpace(body.Category)) < 1 || len(body.Category) > 80 {
		return validation("category", "String should have at least 1 character")
	}
	if runeLen(strings.TrimSpace(body.Title)) < 4 || len(body.Title) > 160 {
		return validation("title", "String should have at least 4 characters")
	}
	if runeLen(strings.TrimSpace(body.Body)) < 20 || len(body.Body) > 30000 {
		return validation("body", "String should have at least 20 characters")
	}
	if len(body.AttachmentIDs) > 9 {
		return validation("attachment_ids", "List should have at most 9 items")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, _, err := s.createEntity(r.Context(), tx, user.ID, "handbook", body.Title+"\n"+body.Body, true, true, false)
	if err != nil {
		return err
	}
	if body.Draft {
		e.Status = "draft"
		_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET status='draft' WHERE id=$1", e.ID)
	}
	a := Article{ID: e.ID, Category: strings.TrimSpace(body.Category), Title: strings.TrimSpace(body.Title), Body: strings.TrimSpace(body.Body)}
	if _, err := tx.Exec(r.Context(), "INSERT INTO handbook_articles(entity_id,category,title,body,featured_rewarded) VALUES($1,$2,$3,$4,false)", e.ID, a.Category, a.Title, a.Body); err != nil {
		return err
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, e.ID, body.AttachmentIDs); err != nil {
		return err
	}
	p, err := s.articlePayload(r.Context(), tx, e, a, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, p)
	return nil
}
func (s *Server) updateHandbook(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "articleID")
	user, _, err := s.participatingUser(w, r)
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
	e, err := getEntityForUpdate(r.Context(), tx, id)
	if err != nil {
		return apiError(404, "ARTICLE_NOT_FOUND", "手册文章不存在")
	}
	a, err := scanArticle(tx.QueryRow(r.Context(), articleSelect+" WHERE entity_id=$1", id))
	if err != nil {
		return apiError(404, "ARTICLE_NOT_FOUND", "手册文章不存在")
	}
	if e.OwnerID != user.ID {
		return apiError(403, "NOT_OWNER", "只有作者可以编辑手册")
	}
	changed := false
	for key, dest := range map[string]*string{"category": &a.Category, "title": &a.Title, "body": &a.Body} {
		if value, ok := raw[key]; ok {
			var x string
			if json.Unmarshal(value, &x) != nil {
				return validation(key, "Input should be a valid string")
			}
			*dest = strings.TrimSpace(x)
			changed = true
		}
	}
	var attachments []int64
	if v, ok := raw["attachment_ids"]; ok {
		_ = json.Unmarshal(v, &attachments)
	}
	if changed {
		if err := recordRevision(r.Context(), tx, e, user.ID, a.Title, a.Body); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), "UPDATE handbook_articles SET category=$1,title=$2,body=$3 WHERE entity_id=$4", a.Category, a.Title, a.Body, id); err != nil {
			return err
		}
		if e.Status != "draft" {
			if err := s.remoderate(r.Context(), tx, &e, a.Title+"\n"+a.Body); err != nil {
				return err
			}
		}
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, id, attachments); err != nil {
		return err
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", id)
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "handbook.update", "handbook", id, "", nil, nil, requestID(r.Context()))
	e.UpdatedAt = time.Now().UTC()
	p, err := s.articlePayload(r.Context(), tx, e, a, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) getHandbook(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "articleID")
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	e, err := getEntity(r.Context(), s.DB, id)
	if err != nil {
		return apiError(404, "ARTICLE_NOT_FOUND", "文章不存在")
	}
	a, err := scanArticle(s.DB.QueryRow(r.Context(), articleSelect+" WHERE entity_id=$1", id))
	if err != nil {
		return apiError(404, "ARTICLE_NOT_FOUND", "文章不存在")
	}
	if e.Status != "published" && (viewer.ID == 0 || viewer.ID != e.OwnerID) {
		return apiError(404, "ARTICLE_NOT_FOUND", "文章不存在")
	}
	p, err := s.articlePayload(r.Context(), s.DB, e, a, userOrNil(viewer))
	if err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) publishHandbook(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "articleID")
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, err := getEntityForUpdate(r.Context(), tx, id)
	if err != nil {
		return apiError(404, "ARTICLE_NOT_FOUND", "文章不存在")
	}
	a, err := scanArticle(tx.QueryRow(r.Context(), articleSelect+" WHERE entity_id=$1", id))
	if err != nil {
		return apiError(404, "ARTICLE_NOT_FOUND", "文章不存在")
	}
	if e.OwnerID != user.ID {
		return apiError(403, "NOT_OWNER", "只有作者可以发布草稿")
	}
	if e.Status == "draft" {
		status, reason, _, err := s.moderate(r.Context(), tx, a.Title+"\n"+a.Body, false)
		if err != nil {
			return err
		}
		e.Status = status
		e.ModerationReason = reason
		_, err = tx.Exec(r.Context(), "UPDATE content_entities SET status=$1,moderation_reason=$2,updated_at=now() WHERE id=$3", status, reason, id)
		if err != nil {
			return err
		}
	}
	p, err := s.articlePayload(r.Context(), tx, e, a, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) featureHandbook(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "articleID")
	moderator, err := s.moderatorUser(w, r)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, err := getEntity(r.Context(), tx, id)
	if err != nil {
		return apiError(404, "ARTICLE_NOT_FOUND", "文章不存在")
	}
	a, err := scanArticle(tx.QueryRow(r.Context(), articleSelect+" WHERE entity_id=$1 FOR UPDATE", id))
	if err != nil {
		return apiError(404, "ARTICLE_NOT_FOUND", "文章不存在")
	}
	if a.Featured == nil {
		now := time.Now().UTC()
		a.Featured = &now
	}
	if !a.Rewarded {
		_, _ = tx.Exec(r.Context(), "UPDATE users SET xp=xp+50,updated_at=now() WHERE id=$1", e.OwnerID)
		a.Rewarded = true
		_ = notifySQL(r.Context(), tx, e.OwnerID, "文章被加精", "《"+a.Title+"》被加精，经验 +50", fmt.Sprintf("/handbook/%d", id), "system")
	}
	_, err = tx.Exec(r.Context(), "UPDATE handbook_articles SET featured_at=$1,featured_rewarded=$2 WHERE entity_id=$3", a.Featured, a.Rewarded, id)
	if err != nil {
		return err
	}
	actor := moderator.ID
	_ = auditSQL(r.Context(), tx, &actor, "handbook.feature", "handbook", id, "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}
func scanEntityArticle(row pgx.Row) (Entity, Article, error) {
	var e Entity
	var a Article
	args := append(entityScan(&e), &a.ID, &a.Category, &a.Title, &a.Body, &a.Featured, &a.Rewarded)
	err := row.Scan(args...)
	return e, a, err
}
func (s *Server) articlePayload(ctx context.Context, q queryer, e Entity, a Article, viewer *User) (map[string]any, error) {
	author, err := s.authorName(ctx, q, e, "nickname", e.ID)
	if err != nil {
		return nil, err
	}
	files, err := attachmentsPayload(ctx, q, e.ID)
	if err != nil {
		return nil, err
	}
	var favorites int
	_ = q.QueryRow(ctx, "SELECT count(*) FROM favorites WHERE entity_id=$1", e.ID).Scan(&favorites)
	return map[string]any{"id": e.ID, "category": a.Category, "title": a.Title, "body": a.Body, "featured": a.Featured != nil, "author": author, "mine": viewer != nil && viewer.ID == e.OwnerID, "status": e.Status, "favorite_count": favorites, "attachments": files, "created_at": e.CreatedAt, "updated_at": e.UpdatedAt}, nil
}

// Courses.
func (s *Server) listCourseOfferings(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM course_offerings").Scan(&total)
	rows, err := s.DB.Query(r.Context(), "SELECT id,course_id,semester,section FROM course_offerings ORDER BY semester DESC LIMIT $1 OFFSET $2", size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, course int64
		var semester, section string
		if err := rows.Scan(&id, &course, &semester, &section); err != nil {
			return err
		}
		p, err := s.offeringPayload(r.Context(), s.DB, id, course, semester, section)
		if err != nil {
			return err
		}
		items = append(items, p)
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) createCourse(w http.ResponseWriter, r *http.Request) error {
	moderator, err := s.moderatorUser(w, r)
	if err != nil {
		return err
	}
	var body struct{ Name, Teacher string }
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if runeLen(strings.TrimSpace(body.Name)) < 2 || len(body.Name) > 160 {
		return validation("name", "String should have at least 2 characters")
	}
	if strings.TrimSpace(body.Teacher) == "" || len(body.Teacher) > 100 {
		return validation("teacher", "String should have at least 1 character")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var id int64
	err = tx.QueryRow(r.Context(), "SELECT id FROM courses WHERE name=$1 AND teacher=$2", strings.TrimSpace(body.Name), strings.TrimSpace(body.Teacher)).Scan(&id)
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(r.Context(), "INSERT INTO courses(name,teacher,active) VALUES($1,$2,true) RETURNING id", strings.TrimSpace(body.Name), strings.TrimSpace(body.Teacher)).Scan(&id)
		if err != nil {
			return err
		}
		actor := moderator.ID
		_ = auditSQL(r.Context(), tx, &actor, "course.create", "course", id, "", nil, nil, requestID(r.Context()))
	} else if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"id": id, "name": strings.TrimSpace(body.Name), "teacher": strings.TrimSpace(body.Teacher)})
	return nil
}
func (s *Server) createCourseOffering(w http.ResponseWriter, r *http.Request) error {
	moderator, err := s.moderatorUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		CourseID int64  `json:"course_id"`
		Semester string `json:"semester"`
		Section  string `json:"section"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var exists bool
	_ = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM courses WHERE id=$1)", body.CourseID).Scan(&exists)
	if !exists {
		return apiError(404, "COURSE_NOT_FOUND", "课程不存在")
	}
	var id int64
	err = tx.QueryRow(r.Context(), "SELECT id FROM course_offerings WHERE course_id=$1 AND semester=$2 AND section=$3", body.CourseID, strings.TrimSpace(body.Semester), strings.TrimSpace(body.Section)).Scan(&id)
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(r.Context(), "INSERT INTO course_offerings(course_id,semester,section) VALUES($1,$2,$3) RETURNING id", body.CourseID, strings.TrimSpace(body.Semester), strings.TrimSpace(body.Section)).Scan(&id)
		if err != nil {
			return err
		}
		actor := moderator.ID
		_ = auditSQL(r.Context(), tx, &actor, "course_offering.create", "course_offering", id, "", nil, nil, requestID(r.Context()))
	} else if err != nil {
		return err
	}
	p, err := s.offeringPayload(r.Context(), tx, id, body.CourseID, strings.TrimSpace(body.Semester), strings.TrimSpace(body.Section))
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, p)
	return nil
}
func (s *Server) createCourseReview(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		OfferingID    int64    `json:"offering_id"`
		Rating        int      `json:"rating"`
		Tags          []string `json:"tags"`
		Body          string   `json:"body"`
		AttachmentIDs []int64  `json:"attachment_ids"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Rating < 1 || body.Rating > 5 {
		return validation("rating", "Input should be between 1 and 5")
	}
	if runeLen(strings.TrimSpace(body.Body)) < 5 || len(body.Body) > 5000 {
		return validation("body", "String should have at least 5 characters")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if err := s.requireCredit(r.Context(), tx, user, "threshold.course_review", "评价课程"); err != nil {
		return err
	}
	var exists bool
	_ = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM course_offerings WHERE id=$1)", body.OfferingID).Scan(&exists)
	if !exists {
		return apiError(404, "OFFERING_NOT_FOUND", "课程班次不存在")
	}
	_ = tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM course_reviews WHERE offering_id=$1 AND user_id=$2)", body.OfferingID, user.ID).Scan(&exists)
	if exists {
		return apiError(409, "REVIEW_EXISTS", "你已经评价过该课程班次")
	}
	e, _, err := s.createEntity(r.Context(), tx, user.ID, "course_review", body.Body, true, false, false)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "INSERT INTO course_reviews(entity_id,offering_id,user_id,rating,tags,body,correction) VALUES($1,$2,$3,$4,$5,$6,'')", e.ID, body.OfferingID, user.ID, body.Rating, strings.Join(cleanStrings(body.Tags, 80), ","), strings.TrimSpace(body.Body)); err != nil {
		return err
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, e.ID, body.AttachmentIDs); err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"id": e.ID, "status": e.Status})
	return nil
}
func (s *Server) correctCourseReview(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "reviewID")
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	if user.Role != "moderator" && user.Role != "admin" && user.CampusIdentity != "staff" {
		return apiError(403, "STAFF_REQUIRED", "只有教职工或审核员可以提交更正")
	}
	var body struct {
		Text string `json:"text"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if runeLen(strings.TrimSpace(body.Text)) < 2 || len(body.Text) > 3000 {
		return validation("text", "String should have at least 2 characters")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var before string
	if err := tx.QueryRow(r.Context(), "SELECT correction FROM course_reviews WHERE entity_id=$1", id).Scan(&before); err == pgx.ErrNoRows {
		return apiError(404, "REVIEW_NOT_FOUND", "评价不存在")
	} else if err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "UPDATE course_reviews SET correction=$1 WHERE entity_id=$2", strings.TrimSpace(body.Text), id); err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "course_review.correct", "course_review", id, "", before, strings.TrimSpace(body.Text), requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}
func (s *Server) offeringPayload(ctx context.Context, q queryer, id, courseID int64, semester, section string) (map[string]any, error) {
	var course, teacher string
	if err := q.QueryRow(ctx, "SELECT name,teacher FROM courses WHERE id=$1", courseID).Scan(&course, &teacher); err != nil {
		return nil, err
	}
	rows, err := q.Query(ctx, `SELECT r.entity_id,r.rating,r.tags,r.body,r.correction FROM course_reviews r JOIN content_entities e ON e.id=r.entity_id WHERE r.offering_id=$1 AND e.status='published' ORDER BY e.created_at`, id)
	if err != nil {
		return nil, err
	}
	reviews := []any{}
	tagCounts := map[string]int{}
	sum, count := 0, 0
	for rows.Next() {
		var rid int64
		var rating int
		var tags, body, correction string
		if err := rows.Scan(&rid, &rating, &tags, &body, &correction); err != nil {
			return nil, err
		}
		files, err := attachmentsPayload(ctx, q, rid)
		if err != nil {
			return nil, err
		}
		reviews = append(reviews, map[string]any{"id": rid, "rating": rating, "tags": splitCSV(tags), "body": body, "correction": correction, "attachments": files})
		sum += rating
		count++
		for _, tag := range splitCSV(tags) {
			tagCounts[tag]++
		}
	}
	rows.Close()
	if len(reviews) > 10 {
		reviews = reviews[len(reviews)-10:]
	}
	pairs := make([]struct {
		Tag   string
		Count int
	}, 0, len(tagCounts))
	for tag, n := range tagCounts {
		pairs = append(pairs, struct {
			Tag   string
			Count int
		}{tag, n})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].Count > pairs[j].Count })
	tags := []string{}
	for i, p := range pairs {
		if i == 5 {
			break
		}
		tags = append(tags, p.Tag)
	}
	var score any
	var reason any
	if count >= 5 {
		score = mathRound(float64(sum)*10/float64(count)) / 10
	} else {
		reason = "评价不足 5 条，暂不显示分数"
	}
	return map[string]any{"id": id, "course": course, "teacher": teacher, "semester": semester, "section": section, "review_count": count, "tags": tags, "score": score, "score_hidden_reason": reason, "reviews": reviews}, nil
}

// Listings.
type Listing struct {
	ID                           int64
	Category, Title, Description string
	Price                        float64
	Condition                    string
	Negotiable                   bool
	Purchased                    *time.Time
	Location, Status             string
}

const listingSelect = `SELECT entity_id,category,title,description,price,condition,negotiable,purchased_at,location,trade_status FROM listings`

func scanListing(row pgx.Row) (Listing, error) {
	var l Listing
	err := row.Scan(&l.ID, &l.Category, &l.Title, &l.Description, &l.Price, &l.Condition, &l.Negotiable, &l.Purchased, &l.Location, &l.Status)
	return l, err
}
func (s *Server) listListings(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pagination(r, 20, 50)
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	where := "e.status='published' AND l.trade_status=ANY($1)"
	args := []any{[]string{"available", "reserved"}}
	if category := r.URL.Query().Get("category"); category != "" {
		args = append(args, category)
		where += " AND l.category=$2"
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM listings l JOIN content_entities e ON e.id=l.entity_id WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(`SELECT e.id,e.type,e.owner_id,e.status,e.allow_comments,e.search_visible,e.moderation_reason,e.revision,e.deleted_at,e.created_at,e.updated_at,l.entity_id,l.category,l.title,l.description,l.price,l.condition,l.negotiable,l.purchased_at,l.location,l.trade_status FROM content_entities e JOIN listings l ON l.entity_id=e.id WHERE %s ORDER BY e.created_at DESC LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		e, l, err := scanEntityListing(rows)
		if err != nil {
			return err
		}
		p, err := s.listingPayload(r.Context(), s.DB, e, l, userOrNil(viewer))
		if err != nil {
			return err
		}
		items = append(items, p)
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) createListing(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Category, Title, Description string
		Price                        float64   `json:"price"`
		Condition                    string    `json:"condition"`
		Negotiable                   *bool     `json:"negotiable"`
		Purchased                    *JSONDate `json:"purchased_at"`
		Location                     string    `json:"location"`
		AttachmentIDs                []int64   `json:"attachment_ids"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	negotiable := true
	if body.Negotiable != nil {
		negotiable = *body.Negotiable
	}
	if runeLen(strings.TrimSpace(body.Title)) < 3 || runeLen(strings.TrimSpace(body.Description)) < 5 || body.Price < 0 || body.Price > 1_000_000 {
		return validation("request", "商品信息无效")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if err := s.requireCredit(r.Context(), tx, user, "threshold.listing_publish", "发布商品"); err != nil {
		return err
	}
	e, _, err := s.createEntity(r.Context(), tx, user.ID, "listing", body.Title+"\n"+body.Description, true, true, false)
	if err != nil {
		return err
	}
	var purchased *time.Time
	if body.Purchased != nil {
		value := body.Purchased.Time
		purchased = &value
	}
	l := Listing{ID: e.ID, Category: strings.TrimSpace(body.Category), Title: strings.TrimSpace(body.Title), Description: strings.TrimSpace(body.Description), Price: body.Price, Condition: strings.TrimSpace(body.Condition), Negotiable: negotiable, Purchased: purchased, Location: strings.TrimSpace(body.Location), Status: "available"}
	if _, err := tx.Exec(r.Context(), `INSERT INTO listings(entity_id,category,title,description,price,condition,negotiable,purchased_at,location,trade_status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'available')`, l.ID, l.Category, l.Title, l.Description, l.Price, l.Condition, l.Negotiable, l.Purchased, l.Location); err != nil {
		return err
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, e.ID, body.AttachmentIDs); err != nil {
		return err
	}
	p, err := s.listingPayload(r.Context(), tx, e, l, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, p)
	return nil
}
func (s *Server) updateListingStatus(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "listingID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	valid := map[string]bool{"available": true, "reserved": true, "sold": true, "offline": true}
	if !valid[body.Status] {
		return validation("status", "Value error, 商品状态无效")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, err := getEntityForUpdate(r.Context(), tx, id)
	if err != nil {
		return apiError(404, "LISTING_NOT_FOUND", "商品不存在")
	}
	l, err := scanListing(tx.QueryRow(r.Context(), listingSelect+" WHERE entity_id=$1", id))
	if err != nil {
		return apiError(404, "LISTING_NOT_FOUND", "商品不存在")
	}
	if e.OwnerID != user.ID && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "NOT_SELLER", "只有卖家可以修改商品状态")
	}
	l.Status = body.Status
	if _, err := tx.Exec(r.Context(), "UPDATE listings SET trade_status=$1 WHERE entity_id=$2", body.Status, id); err != nil {
		return err
	}
	if body.Status == "offline" {
		e.Status = "hidden"
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET status=$1,updated_at=now() WHERE id=$2", e.Status, id)
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "listing.status", "listing", id, "", nil, map[string]any{"status": body.Status}, requestID(r.Context()))
	p, err := s.listingPayload(r.Context(), tx, e, l, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) updateListing(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "listingID")
	user, _, err := s.participatingUser(w, r)
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
	e, err := getEntityForUpdate(r.Context(), tx, id)
	if err != nil {
		return apiError(404, "LISTING_NOT_FOUND", "商品不存在")
	}
	l, err := scanListing(tx.QueryRow(r.Context(), listingSelect+" WHERE entity_id=$1", id))
	if err != nil {
		return apiError(404, "LISTING_NOT_FOUND", "商品不存在")
	}
	if e.OwnerID != user.ID {
		return apiError(403, "NOT_SELLER", "只有卖家可以修改商品")
	}
	changed := false
	for key, dest := range map[string]*string{"category": &l.Category, "title": &l.Title, "description": &l.Description, "condition": &l.Condition, "location": &l.Location} {
		if v, ok := raw[key]; ok {
			var x string
			if json.Unmarshal(v, &x) != nil {
				return validation(key, "Input should be a valid string")
			}
			*dest = strings.TrimSpace(x)
			changed = true
		}
	}
	if v, ok := raw["price"]; ok {
		if json.Unmarshal(v, &l.Price) != nil || l.Price < 0 || l.Price > 1_000_000 {
			return validation("price", "Input should be between 0 and 1000000")
		}
		changed = true
	}
	if v, ok := raw["negotiable"]; ok {
		if json.Unmarshal(v, &l.Negotiable) != nil {
			return validation("negotiable", "Input should be a valid boolean")
		}
		changed = true
	}
	if v, ok := raw["purchased_at"]; ok {
		if string(v) == "null" {
			l.Purchased = nil
		} else {
			var x JSONDate
			if json.Unmarshal(v, &x) != nil {
				return validation("purchased_at", "Input should be a valid date")
			}
			l.Purchased = &x.Time
		}
		changed = true
	}
	var attachments []int64
	if v, ok := raw["attachment_ids"]; ok {
		_ = json.Unmarshal(v, &attachments)
	}
	if changed {
		if err := recordRevision(r.Context(), tx, e, user.ID, l.Title, l.Description); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), "UPDATE listings SET category=$1,title=$2,description=$3,price=$4,condition=$5,negotiable=$6,purchased_at=$7,location=$8 WHERE entity_id=$9", l.Category, l.Title, l.Description, l.Price, l.Condition, l.Negotiable, l.Purchased, l.Location, id); err != nil {
			return err
		}
		if err := s.remoderate(r.Context(), tx, &e, l.Title+"\n"+l.Description); err != nil {
			return err
		}
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, id, attachments); err != nil {
		return err
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", id)
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "listing.update", "listing", id, "", nil, nil, requestID(r.Context()))
	p, err := s.listingPayload(r.Context(), tx, e, l, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func scanEntityListing(row pgx.Row) (Entity, Listing, error) {
	var e Entity
	var l Listing
	args := append(entityScan(&e), &l.ID, &l.Category, &l.Title, &l.Description, &l.Price, &l.Condition, &l.Negotiable, &l.Purchased, &l.Location, &l.Status)
	err := row.Scan(args...)
	return e, l, err
}
func (s *Server) listingPayload(ctx context.Context, q queryer, e Entity, l Listing, viewer *User) (map[string]any, error) {
	var sellerID int64
	var nickname string
	var credit int
	var verified *time.Time
	if err := q.QueryRow(ctx, "SELECT id,nickname,credit,verified_at FROM users WHERE id=$1", e.OwnerID).Scan(&sellerID, &nickname, &credit, &verified); err != nil && err != pgx.ErrNoRows {
		return nil, err
	}
	var sales, favorites int
	_ = q.QueryRow(ctx, "SELECT count(*) FROM listings l JOIN content_entities e ON e.id=l.entity_id WHERE e.owner_id=$1 AND l.trade_status='sold'", e.OwnerID).Scan(&sales)
	_ = q.QueryRow(ctx, "SELECT count(*) FROM favorites WHERE entity_id=$1", e.ID).Scan(&favorites)
	files, err := attachmentsPayload(ctx, q, e.ID)
	if err != nil {
		return nil, err
	}
	var seller any
	if sellerID != 0 {
		seller = map[string]any{"id": sellerID, "nickname": nickname, "credit": credit, "verified": verified != nil, "completed_sales": sales}
	}
	var purchased any
	if l.Purchased != nil {
		purchased = l.Purchased.Format("2006-01-02")
	}
	return map[string]any{"id": e.ID, "category": l.Category, "title": l.Title, "description": l.Description, "price": l.Price, "condition": l.Condition, "negotiable": l.Negotiable, "purchased_at": purchased, "location": l.Location, "trade_status": l.Status, "trade_mode": "offline_only", "seller": seller, "mine": viewer != nil && e.OwnerID == viewer.ID, "attachments": files, "favorite_count": favorites, "created_at": e.CreatedAt, "updated_at": e.UpdatedAt}, nil
}

// Activities.
type Activity struct {
	ID                              int64
	Category, Title, Body, Location string
	Starts                          time.Time
	Ends                            *time.Time
	Capacity                        *int
	Status                          string
}

const activitySelect = `SELECT entity_id,category,title,body,location,starts_at,ends_at,capacity,status FROM activities`

func scanActivity(row pgx.Row) (Activity, error) {
	var a Activity
	err := row.Scan(&a.ID, &a.Category, &a.Title, &a.Body, &a.Location, &a.Starts, &a.Ends, &a.Capacity, &a.Status)
	return a, err
}
func (s *Server) listActivities(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	where := "e.status='published' AND a.status='open'"
	args := []any{}
	if category := r.URL.Query().Get("category"); category != "" {
		args = append(args, category)
		where += " AND a.category=$1"
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM activities a JOIN content_entities e ON e.id=a.entity_id WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(`SELECT e.id,e.type,e.owner_id,e.status,e.allow_comments,e.search_visible,e.moderation_reason,e.revision,e.deleted_at,e.created_at,e.updated_at,a.entity_id,a.category,a.title,a.body,a.location,a.starts_at,a.ends_at,a.capacity,a.status FROM content_entities e JOIN activities a ON a.entity_id=e.id WHERE %s ORDER BY a.starts_at LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		e, a, err := scanEntityActivity(rows)
		if err != nil {
			return err
		}
		p, err := s.activityPayload(r.Context(), s.DB, e, a, userOrNil(viewer))
		if err != nil {
			return err
		}
		items = append(items, p)
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) createActivity(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Category, Title, Body, Location string
		Starts                          time.Time  `json:"starts_at"`
		Ends                            *time.Time `json:"ends_at"`
		Capacity                        *int       `json:"capacity"`
		AttachmentIDs                   []int64    `json:"attachment_ids"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if !body.Starts.After(time.Now().UTC()) {
		return apiError(400, "INVALID_START_TIME", "活动开始时间必须晚于当前时间")
	}
	if body.Ends != nil && !body.Ends.After(body.Starts) {
		return apiError(400, "INVALID_END_TIME", "活动结束时间必须晚于开始时间")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, _, err := s.createEntity(r.Context(), tx, user.ID, "activity", body.Title+"\n"+body.Body, true, true, false)
	if err != nil {
		return err
	}
	a := Activity{ID: e.ID, Category: strings.TrimSpace(body.Category), Title: strings.TrimSpace(body.Title), Body: strings.TrimSpace(body.Body), Location: strings.TrimSpace(body.Location), Starts: body.Starts, Ends: body.Ends, Capacity: body.Capacity, Status: "open"}
	if _, err := tx.Exec(r.Context(), "INSERT INTO activities(entity_id,category,title,body,location,starts_at,ends_at,capacity,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'open')", a.ID, a.Category, a.Title, a.Body, a.Location, a.Starts, a.Ends, a.Capacity); err != nil {
		return err
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, e.ID, body.AttachmentIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "INSERT INTO activity_members(activity_id,user_id,status,joined_at) VALUES($1,$2,'joined',now())", e.ID, user.ID); err != nil {
		return err
	}
	p, err := s.activityPayload(r.Context(), tx, e, a, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, p)
	return nil
}
func (s *Server) updateActivity(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "activityID")
	user, _, err := s.participatingUser(w, r)
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
	e, err := getEntityForUpdate(r.Context(), tx, id)
	if err != nil {
		return apiError(404, "ACTIVITY_NOT_FOUND", "活动不存在")
	}
	a, err := scanActivity(tx.QueryRow(r.Context(), activitySelect+" WHERE entity_id=$1", id))
	if err != nil {
		return apiError(404, "ACTIVITY_NOT_FOUND", "活动不存在")
	}
	if e.OwnerID != user.ID {
		return apiError(403, "NOT_OWNER", "只有发起人可以编辑活动")
	}
	if a.Status != "open" || !a.Starts.After(time.Now().UTC()) {
		return apiError(409, "ACTIVITY_NOT_EDITABLE", "活动已开始或结束，不能再编辑")
	}
	changed := false
	for key, dest := range map[string]*string{"category": &a.Category, "title": &a.Title, "body": &a.Body, "location": &a.Location} {
		if v, ok := raw[key]; ok {
			var x string
			if json.Unmarshal(v, &x) != nil {
				return validation(key, "Input should be a valid string")
			}
			*dest = strings.TrimSpace(x)
			changed = true
		}
	}
	if v, ok := raw["starts_at"]; ok {
		if json.Unmarshal(v, &a.Starts) != nil {
			return validation("starts_at", "Input should be a valid datetime")
		}
		changed = true
	}
	if v, ok := raw["ends_at"]; ok {
		if string(v) == "null" {
			a.Ends = nil
		} else {
			var x time.Time
			if json.Unmarshal(v, &x) != nil {
				return validation("ends_at", "Input should be a valid datetime")
			}
			a.Ends = &x
		}
		changed = true
	}
	if v, ok := raw["capacity"]; ok {
		if string(v) == "null" {
			a.Capacity = nil
		} else {
			var x int
			if json.Unmarshal(v, &x) != nil || x < 2 || x > 10000 {
				return validation("capacity", "Input should be between 2 and 10000")
			}
			a.Capacity = &x
		}
		changed = true
	}
	if !a.Starts.After(time.Now().UTC()) || a.Ends != nil && !a.Ends.After(a.Starts) {
		return apiError(400, "INVALID_ACTIVITY_TIME", "活动时间无效")
	}
	if a.Capacity != nil {
		var count int
		_ = tx.QueryRow(r.Context(), "SELECT count(*) FROM activity_members WHERE activity_id=$1 AND status='joined'", id).Scan(&count)
		if *a.Capacity < count {
			return apiError(400, "CAPACITY_TOO_SMALL", "容量不能小于当前报名人数")
		}
	}
	var attachments []int64
	if v, ok := raw["attachment_ids"]; ok {
		_ = json.Unmarshal(v, &attachments)
	}
	if changed {
		if err := recordRevision(r.Context(), tx, e, user.ID, a.Title, a.Body); err != nil {
			return err
		}
		_, err = tx.Exec(r.Context(), "UPDATE activities SET category=$1,title=$2,body=$3,location=$4,starts_at=$5,ends_at=$6,capacity=$7 WHERE entity_id=$8", a.Category, a.Title, a.Body, a.Location, a.Starts, a.Ends, a.Capacity, id)
		if err != nil {
			return err
		}
		if err := s.remoderate(r.Context(), tx, &e, a.Title+"\n"+a.Body); err != nil {
			return err
		}
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, id, attachments); err != nil {
		return err
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", id)
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "activity.update", "activity", id, "", nil, nil, requestID(r.Context()))
	p, err := s.activityPayload(r.Context(), tx, e, a, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) joinActivity(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "activityID")
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, err := getEntity(r.Context(), tx, id)
	if err != nil || e.Status != "published" {
		return apiError(404, "ACTIVITY_NOT_FOUND", "活动不存在或已关闭")
	}
	a, err := scanActivity(tx.QueryRow(r.Context(), activitySelect+" WHERE entity_id=$1 FOR UPDATE", id))
	if err != nil || a.Status != "open" {
		return apiError(404, "ACTIVITY_NOT_FOUND", "活动不存在或已关闭")
	}
	var count int
	_ = tx.QueryRow(r.Context(), "SELECT count(*) FROM activity_members WHERE activity_id=$1 AND status='joined'", id).Scan(&count)
	if a.Capacity != nil && count >= *a.Capacity {
		return apiError(409, "ACTIVITY_FULL", "活动名额已满")
	}
	_, err = tx.Exec(r.Context(), `INSERT INTO activity_members(activity_id,user_id,status,joined_at) VALUES($1,$2,'joined',now()) ON CONFLICT(activity_id,user_id) DO UPDATE SET status='joined',joined_at=now()`, id, user.ID)
	if err != nil {
		return err
	}
	if e.OwnerID != user.ID {
		_ = notifySQL(r.Context(), tx, e.OwnerID, "有人加入活动", user.Nickname+" 加入了《"+a.Title+"》", fmt.Sprintf("/activities/%d", id), "system")
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", id)
	p, err := s.activityPayload(r.Context(), tx, e, a, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) leaveActivity(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "activityID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var owner int64
	_ = s.DB.QueryRow(r.Context(), "SELECT owner_id FROM content_entities WHERE id=$1", id).Scan(&owner)
	if owner == user.ID {
		return apiError(400, "OWNER_CANNOT_LEAVE", "发起人需取消活动")
	}
	_, err = s.DB.Exec(r.Context(), "UPDATE activity_members SET status='left' WHERE activity_id=$1 AND user_id=$2", id, user.ID)
	if err != nil {
		return err
	}
	if _, err = s.DB.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", id); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}
func (s *Server) cancelActivity(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "activityID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, err := getEntityForUpdate(r.Context(), tx, id)
	if err != nil {
		return apiError(404, "ACTIVITY_NOT_FOUND", "活动不存在")
	}
	a, err := scanActivity(tx.QueryRow(r.Context(), activitySelect+" WHERE entity_id=$1", id))
	if err != nil {
		return apiError(404, "ACTIVITY_NOT_FOUND", "活动不存在")
	}
	if e.OwnerID != user.ID && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "NOT_OWNER", "无权取消活动")
	}
	members, err := int64Rows(r.Context(), tx, "SELECT user_id FROM activity_members WHERE activity_id=$1 AND status='joined'", id)
	if err != nil {
		return err
	}
	_, _ = tx.Exec(r.Context(), "UPDATE activities SET status='cancelled' WHERE entity_id=$1", id)
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET status='hidden',updated_at=now() WHERE id=$1", id)
	_, _ = tx.Exec(r.Context(), "UPDATE activity_members SET status='cancelled' WHERE activity_id=$1 AND status='joined'", id)
	for _, member := range members {
		_ = notifySQL(r.Context(), tx, member, "活动已取消", "《"+a.Title+"》已取消", "/activities", "system")
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true})
	return nil
}
func scanEntityActivity(row pgx.Row) (Entity, Activity, error) {
	var e Entity
	var a Activity
	args := append(entityScan(&e), &a.ID, &a.Category, &a.Title, &a.Body, &a.Location, &a.Starts, &a.Ends, &a.Capacity, &a.Status)
	err := row.Scan(args...)
	return e, a, err
}
func (s *Server) activityPayload(ctx context.Context, q queryer, e Entity, a Activity, viewer *User) (map[string]any, error) {
	var count int
	_ = q.QueryRow(ctx, "SELECT count(*) FROM activity_members WHERE activity_id=$1 AND status='joined'", e.ID).Scan(&count)
	joined := false
	if viewer != nil {
		_ = q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM activity_members WHERE activity_id=$1 AND user_id=$2 AND status='joined')", e.ID, viewer.ID).Scan(&joined)
	}
	author, err := s.authorName(ctx, q, e, "nickname", e.ID)
	if err != nil {
		return nil, err
	}
	files, err := attachmentsPayload(ctx, q, e.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": e.ID, "category": a.Category, "title": a.Title, "body": a.Body, "location": a.Location, "starts_at": a.Starts, "ends_at": a.Ends, "capacity": a.Capacity, "status": a.Status, "member_count": count, "joined": joined, "mine": viewer != nil && viewer.ID == e.OwnerID, "author": author, "attachments": files, "created_at": e.CreatedAt, "updated_at": e.UpdatedAt}, nil
}

// Lost and found.
type LostItem struct {
	ID                                int64
	Kind, Name, Description, Location string
	Happened                          *time.Time
	Status                            string
}

const lostSelect = `SELECT entity_id,kind,item_name,description,location,happened_at,status FROM lost_items`

func scanLost(row pgx.Row) (LostItem, error) {
	var x LostItem
	err := row.Scan(&x.ID, &x.Kind, &x.Name, &x.Description, &x.Location, &x.Happened, &x.Status)
	return x, err
}
func (s *Server) listLostItems(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	where := "e.status='published'"
	args := []any{}
	if kind := r.URL.Query().Get("kind"); kind != "" {
		args = append(args, kind)
		where += " AND l.kind=$1"
	}
	var total int
	_ = s.DB.QueryRow(r.Context(), "SELECT count(*) FROM lost_items l JOIN content_entities e ON e.id=l.entity_id WHERE "+where, args...).Scan(&total)
	args = append(args, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(`SELECT e.id,e.type,e.owner_id,e.status,e.allow_comments,e.search_visible,e.moderation_reason,e.revision,e.deleted_at,e.created_at,e.updated_at,l.entity_id,l.kind,l.item_name,l.description,l.location,l.happened_at,l.status FROM content_entities e JOIN lost_items l ON l.entity_id=e.id WHERE %s ORDER BY e.created_at DESC LIMIT $%d OFFSET $%d`, where, len(args)-1, len(args)), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		e, l, err := scanEntityLost(rows)
		if err != nil {
			return err
		}
		p, err := s.lostPayload(r.Context(), s.DB, e, l, userOrNil(viewer))
		if err != nil {
			return err
		}
		items = append(items, p)
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return rows.Err()
}
func (s *Server) createLostItem(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Kind                  string `json:"kind"`
		Name                  string `json:"item_name"`
		Description, Location string
		Happened              *time.Time `json:"happened_at"`
		AttachmentIDs         []int64    `json:"attachment_ids"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Kind != "lost" && body.Kind != "found" {
		return validation("kind", "Value error, 类型必须为 lost 或 found")
	}
	if runeLen(strings.TrimSpace(body.Name)) < 2 || runeLen(strings.TrimSpace(body.Location)) < 2 {
		return validation("request", "失物信息无效")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, _, err := s.createEntity(r.Context(), tx, user.ID, "lost_item", body.Name+"\n"+body.Description, true, true, false)
	if err != nil {
		return err
	}
	item := LostItem{ID: e.ID, Kind: body.Kind, Name: strings.TrimSpace(body.Name), Description: strings.TrimSpace(body.Description), Location: strings.TrimSpace(body.Location), Happened: body.Happened, Status: "open"}
	if _, err := tx.Exec(r.Context(), "INSERT INTO lost_items(entity_id,kind,item_name,description,location,happened_at,status) VALUES($1,$2,$3,$4,$5,$6,'open')", item.ID, item.Kind, item.Name, item.Description, item.Location, item.Happened); err != nil {
		return err
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, e.ID, body.AttachmentIDs); err != nil {
		return err
	}
	p, err := s.lostPayload(r.Context(), tx, e, item, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, p)
	return nil
}
func (s *Server) updateLostItem(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "itemID")
	user, _, err := s.participatingUser(w, r)
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
	e, err := getEntityForUpdate(r.Context(), tx, id)
	if err != nil {
		return apiError(404, "LOST_ITEM_NOT_FOUND", "失物信息不存在")
	}
	item, err := scanLost(tx.QueryRow(r.Context(), lostSelect+" WHERE entity_id=$1", id))
	if err != nil {
		return apiError(404, "LOST_ITEM_NOT_FOUND", "失物信息不存在")
	}
	if e.OwnerID != user.ID {
		return apiError(403, "NOT_OWNER", "只有发布者可以编辑失物信息")
	}
	if item.Status != "open" {
		return apiError(409, "LOST_ITEM_NOT_EDITABLE", "认领流程已结束，不能再编辑")
	}
	changed := false
	for key, dest := range map[string]*string{"item_name": &item.Name, "description": &item.Description, "location": &item.Location} {
		if v, ok := raw[key]; ok {
			var x string
			if json.Unmarshal(v, &x) != nil {
				return validation(key, "Input should be a valid string")
			}
			*dest = strings.TrimSpace(x)
			changed = true
		}
	}
	if v, ok := raw["happened_at"]; ok {
		if string(v) == "null" {
			item.Happened = nil
		} else {
			var x time.Time
			if json.Unmarshal(v, &x) != nil {
				return validation("happened_at", "Input should be a valid datetime")
			}
			item.Happened = &x
		}
		changed = true
	}
	var attachments []int64
	if v, ok := raw["attachment_ids"]; ok {
		_ = json.Unmarshal(v, &attachments)
	}
	if changed {
		if err := recordRevision(r.Context(), tx, e, user.ID, item.Name, item.Description); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), "UPDATE lost_items SET item_name=$1,description=$2,location=$3,happened_at=$4 WHERE entity_id=$5", item.Name, item.Description, item.Location, item.Happened, id); err != nil {
			return err
		}
		if err := s.remoderate(r.Context(), tx, &e, item.Name+"\n"+item.Description); err != nil {
			return err
		}
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, id, attachments); err != nil {
		return err
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", id)
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "lost_item.update", "lost_item", id, "", nil, nil, requestID(r.Context()))
	p, err := s.lostPayload(r.Context(), tx, e, item, &user)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}
func (s *Server) createLostClaim(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "itemID")
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Message string `json:"message"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if runeLen(strings.TrimSpace(body.Message)) < 5 || len(body.Message) > 2000 {
		return validation("message", "String should have at least 5 characters")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, err := getEntity(r.Context(), tx, id)
	if err != nil || e.Status != "published" {
		return apiError(404, "LOST_ITEM_NOT_FOUND", "失物信息不存在或已结束")
	}
	item, err := scanLost(tx.QueryRow(r.Context(), lostSelect+" WHERE entity_id=$1 FOR UPDATE", id))
	if err != nil || item.Status != "open" {
		return apiError(404, "LOST_ITEM_NOT_FOUND", "失物信息不存在或已结束")
	}
	if e.OwnerID == user.ID {
		return apiError(400, "SELF_CLAIM", "不能认领自己发布的条目")
	}
	var claimID int64
	var status string
	err = tx.QueryRow(r.Context(), "SELECT id,status FROM lost_claims WHERE item_id=$1 AND claimant_id=$2", id, user.ID).Scan(&claimID, &status)
	if err == pgx.ErrNoRows {
		err = tx.QueryRow(r.Context(), "INSERT INTO lost_claims(item_id,claimant_id,message,status,created_at) VALUES($1,$2,$3,'pending',now()) RETURNING id,status", id, user.ID, strings.TrimSpace(body.Message)).Scan(&claimID, &status)
		if err != nil {
			return err
		}
		_ = notifySQL(r.Context(), tx, e.OwnerID, "收到认领申请", user.Nickname+" 提交了《"+item.Name+"》的认领申请", fmt.Sprintf("/lost-items/%d", id), "system")
	} else if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"id": claimID, "status": status})
	return nil
}
func (s *Server) listLostClaims(w http.ResponseWriter, r *http.Request) error {
	id, _ := pathID(r, "itemID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	e, err := getEntity(r.Context(), s.DB, id)
	if err != nil {
		return apiError(404, "LOST_ITEM_NOT_FOUND", "失物信息不存在")
	}
	if _, err := scanLost(s.DB.QueryRow(r.Context(), lostSelect+" WHERE entity_id=$1", id)); err != nil {
		return apiError(404, "LOST_ITEM_NOT_FOUND", "失物信息不存在")
	}
	if e.OwnerID != user.ID && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "OWNER_REQUIRED", "只有发布者可以查看认领申请")
	}
	rows, err := s.DB.Query(r.Context(), `SELECT c.id,c.claimant_id,u.nickname,c.message,c.status,c.created_at FROM lost_claims c JOIN users u ON u.id=c.claimant_id WHERE c.item_id=$1 ORDER BY c.created_at`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var cid, claimant int64
		var name, message, status string
		var created time.Time
		if err := rows.Scan(&cid, &claimant, &name, &message, &status, &created); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": cid, "claimant_id": claimant, "claimant": name, "message": message, "status": status, "created_at": created})
	}
	writeJSON(w, 200, pagePayload(items, 1, 100, len(items)))
	return rows.Err()
}
func (s *Server) decideLostClaim(w http.ResponseWriter, r *http.Request) error {
	itemID, _ := pathID(r, "itemID")
	claimID, _ := pathID(r, "claimID")
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		Approve bool `json:"approve"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	e, err := getEntity(r.Context(), tx, itemID)
	if err != nil {
		return apiError(404, "CLAIM_NOT_FOUND", "认领申请不存在")
	}
	item, err := scanLost(tx.QueryRow(r.Context(), lostSelect+" WHERE entity_id=$1 FOR UPDATE", itemID))
	if err != nil {
		return apiError(404, "CLAIM_NOT_FOUND", "认领申请不存在")
	}
	var claimItem, claimant int64
	var status string
	if err := tx.QueryRow(r.Context(), "SELECT item_id,claimant_id,status FROM lost_claims WHERE id=$1", claimID).Scan(&claimItem, &claimant, &status); err != nil || claimItem != itemID {
		return apiError(404, "CLAIM_NOT_FOUND", "认领申请不存在")
	}
	if e.OwnerID != user.ID && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "OWNER_REQUIRED", "只有发布者可以确认认领")
	}
	if status == "pending" {
		if body.Approve {
			status = "approved"
			_, _ = tx.Exec(r.Context(), "UPDATE lost_items SET status='completed' WHERE entity_id=$1", itemID)
			_, _ = tx.Exec(r.Context(), "UPDATE lost_claims SET status='rejected' WHERE item_id=$1 AND id<>$2", itemID, claimID)
			if item.Kind == "found" {
				_, err = s.applyCredit(r.Context(), tx, e.OwnerID, "reward.lost_claim", "lost_item", itemID)
				if err != nil {
					return err
				}
			}
			_ = notifySQL(r.Context(), tx, claimant, "认领已确认", "《"+item.Name+"》认领流程已完成", fmt.Sprintf("/lost-items/%d", itemID), "system")
		} else {
			status = "rejected"
			_ = notifySQL(r.Context(), tx, claimant, "认领申请未通过", "《"+item.Name+"》的发布者未确认该申请", fmt.Sprintf("/lost-items/%d", itemID), "system")
		}
		_, err = tx.Exec(r.Context(), "UPDATE lost_claims SET status=$1,decided_at=now() WHERE id=$2", status, claimID)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"id": claimID, "status": status})
	return nil
}
func scanEntityLost(row pgx.Row) (Entity, LostItem, error) {
	var e Entity
	var x LostItem
	args := append(entityScan(&e), &x.ID, &x.Kind, &x.Name, &x.Description, &x.Location, &x.Happened, &x.Status)
	err := row.Scan(args...)
	return e, x, err
}
func (s *Server) lostPayload(ctx context.Context, q queryer, e Entity, item LostItem, viewer *User) (map[string]any, error) {
	var claims int
	_ = q.QueryRow(ctx, "SELECT count(*) FROM lost_claims WHERE item_id=$1", e.ID).Scan(&claims)
	author, err := s.authorName(ctx, q, e, "nickname", e.ID)
	if err != nil {
		return nil, err
	}
	files, err := attachmentsPayload(ctx, q, e.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": e.ID, "kind": item.Kind, "item_name": item.Name, "description": item.Description, "location": item.Location, "happened_at": item.Happened, "status": item.Status, "claim_count": claims, "mine": viewer != nil && viewer.ID == e.OwnerID, "author": author, "attachments": files}, nil
}

func getEntity(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id int64) (Entity, error) {
	var e Entity
	err := q.QueryRow(ctx, entitySelect+" WHERE id=$1", id).Scan(entityScan(&e)...)
	return e, err
}
func getEntityForUpdate(ctx context.Context, tx pgx.Tx, id int64) (Entity, error) {
	var e Entity
	err := tx.QueryRow(ctx, entitySelect+" WHERE id=$1 FOR UPDATE", id).Scan(entityScan(&e)...)
	return e, err
}
func recordRevision(ctx context.Context, tx pgx.Tx, e Entity, editor int64, title, body string) error {
	if _, err := tx.Exec(ctx, "INSERT INTO content_revisions(entity_id,editor_id,revision,title,body,created_at) VALUES($1,$2,$3,$4,$5,now())", e.ID, editor, e.Revision, title, body); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "UPDATE content_entities SET revision=revision+1,updated_at=now() WHERE id=$1", e.ID)
	return err
}

var _ = strconv.Itoa
