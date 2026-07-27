package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/yatools/wutong-campus-wall/backend/internal/marketpolicy"
)

var marketConditions = map[string]string{
	"new": "全新未拆", "like_new": "九五新", "excellent": "九成新", "good": "八成新", "fair": "有明显使用痕迹",
}

type marketListing struct {
	ID, OwnerID, CategoryID, LocationID int64
	Title, Description, Condition       string
	PriceCents                          int64
	Negotiable                          bool
	PurchasedAt                         *time.Time
	TradeStatus                         string
	PublicationStatus                   string
	ModerationStatus                    string
	CreatedAt, UpdatedAt                time.Time
}

type marketListingInput struct {
	CategoryID    int64     `json:"category_id"`
	LocationID    int64     `json:"location_id"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	PriceCents    int64     `json:"price_cents"`
	Condition     string    `json:"condition"`
	Negotiable    *bool     `json:"negotiable"`
	PurchasedAt   *JSONDate `json:"purchased_at"`
	AttachmentIDs []int64   `json:"attachment_ids"`
}

type marketListingPatch struct {
	CategoryID    *int64          `json:"category_id"`
	LocationID    *int64          `json:"location_id"`
	Title         *string         `json:"title"`
	Description   *string         `json:"description"`
	PriceCents    *int64          `json:"price_cents"`
	Condition     *string         `json:"condition"`
	Negotiable    *bool           `json:"negotiable"`
	PurchasedAt   json.RawMessage `json:"purchased_at"`
	AttachmentIDs *[]int64        `json:"attachment_ids"`
}

func decodeStrictBody(r *http.Request, value any) *APIError {
	return decodeBody(r, value)
}

func (s *Server) marketOptions(w http.ResponseWriter, r *http.Request) error {
	load := func(table string) ([]any, error) {
		rows, err := s.DB.Query(r.Context(), "SELECT id,name,slug FROM "+table+" WHERE active=true ORDER BY sort_order,id")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		items := []any{}
		for rows.Next() {
			var id int64
			var name, slug string
			if err := rows.Scan(&id, &name, &slug); err != nil {
				return nil, err
			}
			items = append(items, map[string]any{"id": id, "name": name, "slug": slug})
		}
		return items, rows.Err()
	}
	categories, err := load("market_categories")
	if err != nil {
		return err
	}
	locations, err := load("market_locations")
	if err != nil {
		return err
	}
	conditions := []any{}
	for _, code := range []string{"new", "like_new", "excellent", "good", "fair"} {
		conditions = append(conditions, map[string]any{"code": code, "name": marketConditions[code]})
	}
	writeJSON(w, 200, map[string]any{"categories": categories, "locations": locations, "conditions": conditions})
	return nil
}

func (s *Server) validateMarketListing(ctx context.Context, q queryer, listing marketListing, attachmentIDs []int64) *APIError {
	fields := map[string]string{}
	if n := runeLen(strings.TrimSpace(listing.Title)); n < 3 || n > 160 {
		fields["title"] = "标题应为 3 到 160 个字符"
	}
	if n := runeLen(strings.TrimSpace(listing.Description)); n < 5 || n > 10000 {
		fields["description"] = "描述应为 5 到 10000 个字符"
	}
	if listing.PriceCents < 0 || listing.PriceCents > 100_000_000 {
		fields["price_cents"] = "价格应为 0 到 100000000 分"
	}
	if _, ok := marketConditions[listing.Condition]; !ok {
		fields["condition"] = "商品成色无效"
	}
	location, err := time.LoadLocation(s.Config.AppTimezone)
	if err != nil {
		location, _ = time.LoadLocation("Asia/Shanghai")
	}
	if listing.PurchasedAt != nil && listing.PurchasedAt.Format("2006-01-02") > time.Now().In(location).Format("2006-01-02") {
		fields["purchased_at"] = "Purchase date cannot be in the future"
	}
	if len(attachmentIDs) > 9 {
		fields["attachment_ids"] = "最多上传 9 张图片"
	}
	var categoryOK, locationOK bool
	if err := q.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM market_categories WHERE id=$1 AND active=true),EXISTS(SELECT 1 FROM market_locations WHERE id=$2 AND active=true)", listing.CategoryID, listing.LocationID).Scan(&categoryOK, &locationOK); err != nil {
		fields["request"] = "无法验证市场字典"
	}
	if !categoryOK {
		fields["category_id"] = "商品分类不存在或已停用"
	}
	if !locationOK {
		fields["location_id"] = "交易地点不存在或已停用"
	}
	if len(fields) > 0 {
		return validationFields(fields)
	}
	return nil
}

func (s *Server) listMarketListings(w http.ResponseWriter, r *http.Request) error {
	page, size, err := pagination(r, 20, 50)
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	where := []string{"e.publication_status='published'", "e.moderation_status='approved'"}
	args := []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	statuses := []string{"available", "reserved"}
	if raw := strings.TrimSpace(r.URL.Query().Get("trade_status")); raw != "" {
		if raw != "available" && raw != "reserved" && raw != "completed" {
			return validation("trade_status", "商品交易状态无效")
		}
		statuses = []string{raw}
	}
	add("l.trade_status=ANY($%d)", statuses)
	if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
		if runeLen(q) < 2 || runeLen(q) > 80 {
			return validation("q", "关键词应为 2 到 80 个字符")
		}
		if err := s.checkSearchRateLimit(r.Context(), clientIP(r)); err != nil {
			return err
		}
		add("(l.title ILIKE $%[1]d OR l.description ILIKE $%[1]d)", "%"+q+"%")
	}
	for key, column := range map[string]string{"category_id": "l.category_id", "location_id": "l.location_id", "min_price_cents": "l.price_cents >=", "max_price_cents": "l.price_cents <="} {
		if raw := r.URL.Query().Get(key); raw != "" {
			value, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil || value < 0 {
				return validation(key, "Input should be a non-negative integer")
			}
			if strings.HasSuffix(column, ">=") || strings.HasSuffix(column, "<=") {
				add(column+" $%d", value)
			} else {
				add(column+"=$%d", value)
			}
		}
	}
	if condition := r.URL.Query().Get("condition"); condition != "" {
		if _, ok := marketConditions[condition]; !ok {
			return validation("condition", "商品成色无效")
		}
		add("l.condition=$%d", condition)
	}
	if raw := r.URL.Query().Get("negotiable"); raw != "" {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return validation("negotiable", "Input should be a boolean")
		}
		add("l.negotiable=$%d", value)
	}
	if raw := r.URL.Query().Get("has_images"); raw != "" {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			return validation("has_images", "Input should be a boolean")
		}
		clause := "EXISTS(SELECT 1 FROM attachments a WHERE a.entity_id=e.id AND a.status='attached' AND a.access_scope='public')"
		if !value {
			clause = "NOT " + clause
		}
		where = append(where, clause)
	}
	if raw := r.URL.Query().Get("created_after"); raw != "" {
		value, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			return validation("created_after", "Input should be an RFC3339 datetime")
		}
		add("e.created_at >= $%d", value)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM listings l JOIN content_entities e ON e.id=l.entity_id WHERE "+whereSQL, args...).Scan(&total); err != nil {
		return err
	}
	order := "e.created_at DESC"
	switch r.URL.Query().Get("sort") {
	case "", "newest":
	case "price_asc":
		order = "l.price_cents ASC,e.created_at DESC"
	case "price_desc":
		order = "l.price_cents DESC,e.created_at DESC"
	case "popular":
		order = "favorite_count DESC,e.created_at DESC"
	default:
		return validation("sort", "排序方式无效")
	}
	args = append(args, size, (page-1)*size)
	query := fmt.Sprintf(`SELECT e.id,e.owner_id,e.publication_status,e.moderation_status,e.created_at,e.updated_at,
		l.category_id,c.name,c.slug,l.location_id,loc.name,loc.slug,l.title,l.description,l.price_cents,l.condition,l.negotiable,l.purchased_at,l.trade_status,
		u.nickname,u.credit,(u.verified_at IS NOT NULL),
		(SELECT count(*) FROM market_transactions mt WHERE mt.seller_id=e.owner_id AND mt.status='completed') completed_sales,
		COALESCE((SELECT avg(mr.rating)::float8 FROM market_reviews mr WHERE mr.reviewee_id=e.owner_id AND mr.visible_at<=now()),0) rating_average,
		(SELECT count(*) FROM market_reviews mr WHERE mr.reviewee_id=e.owner_id AND mr.visible_at<=now()) rating_count,
		(SELECT count(*) FROM favorites f WHERE f.entity_id=e.id) favorite_count,
		COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id,'path',a.path,'thumbnail_path',a.thumbnail_path,'width',a.width,'height',a.height) ORDER BY a.id) FROM attachments a WHERE a.entity_id=e.id AND a.status='attached' AND a.access_scope='public'),'[]'::jsonb) attachments
		FROM listings l JOIN content_entities e ON e.id=l.entity_id JOIN market_categories c ON c.id=l.category_id JOIN market_locations loc ON loc.id=l.location_id JOIN users u ON u.id=e.owner_id
		WHERE %s ORDER BY %s LIMIT $%d OFFSET $%d`, whereSQL, order, len(args)-1, len(args))
	rows, err := s.DB.Query(r.Context(), query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		payload, err := s.scanMarketListingPayload(rows, viewer.ID)
		if err != nil {
			return err
		}
		items = append(items, payload)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return nil
}

func (s *Server) scanMarketListingPayload(row pgx.Row, viewerID int64) (map[string]any, error) {
	var l marketListing
	var categoryName, categorySlug, locationName, locationSlug, nickname string
	var credit, completedSales, ratingCount, favorites int
	var verified bool
	var ratingAverage float64
	var raw json.RawMessage
	err := row.Scan(&l.ID, &l.OwnerID, &l.PublicationStatus, &l.ModerationStatus, &l.CreatedAt, &l.UpdatedAt, &l.CategoryID, &categoryName, &categorySlug, &l.LocationID, &locationName, &locationSlug, &l.Title, &l.Description, &l.PriceCents, &l.Condition, &l.Negotiable, &l.PurchasedAt, &l.TradeStatus, &nickname, &credit, &verified, &completedSales, &ratingAverage, &ratingCount, &favorites, &raw)
	if err != nil {
		return nil, err
	}
	attachments := []map[string]any{}
	_ = json.Unmarshal(raw, &attachments)
	for _, attachment := range attachments {
		attachment["url"] = "/uploads/" + fmt.Sprint(attachment["path"])
		attachment["thumbnail_url"] = "/uploads/" + fmt.Sprint(attachment["thumbnail_path"])
		delete(attachment, "path")
		delete(attachment, "thumbnail_path")
	}
	var purchased any
	if l.PurchasedAt != nil {
		purchased = l.PurchasedAt.Format("2006-01-02")
	}
	return map[string]any{"id": l.ID, "category": map[string]any{"id": l.CategoryID, "name": categoryName, "slug": categorySlug}, "location": map[string]any{"id": l.LocationID, "name": locationName, "slug": locationSlug}, "title": l.Title, "description": l.Description, "price_cents": l.PriceCents, "condition": l.Condition, "condition_label": marketConditions[l.Condition], "negotiable": l.Negotiable, "purchased_at": purchased, "trade_status": l.TradeStatus, "publication_status": l.PublicationStatus, "moderation_status": l.ModerationStatus, "seller": map[string]any{"id": l.OwnerID, "nickname": nickname, "credit": credit, "verified": verified, "completed_sales": completedSales, "rating_average": ratingAverage, "rating_count": ratingCount}, "mine": viewerID != 0 && viewerID == l.OwnerID, "favorite_count": favorites, "attachments": attachments, "created_at": l.CreatedAt, "updated_at": l.UpdatedAt}, nil
}

const marketListingPayloadSelect = `SELECT e.id,e.owner_id,e.publication_status,e.moderation_status,e.created_at,e.updated_at,
	l.category_id,c.name,c.slug,l.location_id,loc.name,loc.slug,l.title,l.description,l.price_cents,l.condition,l.negotiable,l.purchased_at,l.trade_status,
	u.nickname,u.credit,(u.verified_at IS NOT NULL),
	(SELECT count(*) FROM market_transactions mt WHERE mt.seller_id=e.owner_id AND mt.status='completed'),
	COALESCE((SELECT avg(mr.rating)::float8 FROM market_reviews mr WHERE mr.reviewee_id=e.owner_id AND mr.visible_at<=now()),0),
	(SELECT count(*) FROM market_reviews mr WHERE mr.reviewee_id=e.owner_id AND mr.visible_at<=now()),
	(SELECT count(*) FROM favorites f WHERE f.entity_id=e.id),
	COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id,'path',a.path,'thumbnail_path',a.thumbnail_path,'width',a.width,'height',a.height) ORDER BY a.id) FROM attachments a WHERE a.entity_id=e.id AND a.status='attached' AND a.access_scope='public'),'[]'::jsonb)
	FROM listings l JOIN content_entities e ON e.id=l.entity_id JOIN market_categories c ON c.id=l.category_id JOIN market_locations loc ON loc.id=l.location_id JOIN users u ON u.id=e.owner_id`

func (s *Server) getMarketListing(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "listingID")
	if err != nil {
		return err
	}
	viewer, _, err := s.currentUser(w, r, false)
	if err != nil {
		return err
	}
	row := s.DB.QueryRow(r.Context(), marketListingPayloadSelect+` WHERE e.id=$1 AND ((e.publication_status='published' AND e.moderation_status='approved' AND l.trade_status<>'cancelled') OR e.owner_id=$2 OR $3)`, id, viewer.ID, viewer.Role == "moderator" || viewer.Role == "admin")
	payload, err := s.scanMarketListingPayload(row, viewer.ID)
	if err == pgx.ErrNoRows {
		return apiError(404, "LISTING_NOT_FOUND", "商品不存在")
	}
	if err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (s *Server) createMarketListing(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body marketListingInput
	if apiErr := decodeStrictBody(r, &body); apiErr != nil {
		return apiErr
	}
	negotiable := true
	if body.Negotiable != nil {
		negotiable = *body.Negotiable
	}
	listing := marketListing{CategoryID: body.CategoryID, LocationID: body.LocationID, Title: strings.TrimSpace(body.Title), Description: strings.TrimSpace(body.Description), PriceCents: body.PriceCents, Condition: body.Condition, Negotiable: negotiable, TradeStatus: "available"}
	if body.PurchasedAt != nil {
		value := body.PurchasedAt.Time
		listing.PurchasedAt = &value
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	if apiErr := s.validateMarketListing(r.Context(), tx, listing, body.AttachmentIDs); apiErr != nil {
		return apiErr
	}
	if err := s.requireCredit(r.Context(), tx, user, "threshold.listing_publish", "发布商品"); err != nil {
		return err
	}
	e, _, err := s.createEntity(r.Context(), tx, user.ID, "listing", listing.Title+"\n"+listing.Description, true, true, false)
	if err != nil {
		return err
	}
	listing.ID = e.ID
	listing.OwnerID = user.ID
	listing.PublicationStatus = e.Status
	listing.ModerationStatus = "approved"
	if e.Status != "published" {
		listing.ModerationStatus = "pending"
	}
	listing.CreatedAt = e.CreatedAt
	listing.UpdatedAt = e.UpdatedAt
	_, err = tx.Exec(r.Context(), `INSERT INTO listings(entity_id,category_id,title,description,price_cents,condition,negotiable,purchased_at,location_id,trade_status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'available')`, listing.ID, listing.CategoryID, listing.Title, listing.Description, listing.PriceCents, listing.Condition, listing.Negotiable, listing.PurchasedAt, listing.LocationID)
	if err != nil {
		return err
	}
	if err := s.attachUploads(r.Context(), tx, user.ID, e.ID, body.AttachmentIDs); err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	payload, err := s.marketListingByID(r.Context(), e.ID, user.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 201, payload)
	return nil
}

func (s *Server) marketListingByID(ctx context.Context, id, viewerID int64) (map[string]any, error) {
	return s.scanMarketListingPayload(s.DB.QueryRow(ctx, marketListingPayloadSelect+" WHERE e.id=$1", id), viewerID)
}

func (s *Server) loadMarketListingForUpdate(ctx context.Context, tx pgx.Tx, id int64) (marketListing, error) {
	var l marketListing
	err := tx.QueryRow(ctx, `SELECT e.id,e.owner_id,e.publication_status,e.moderation_status,e.created_at,e.updated_at,l.category_id,l.location_id,l.title,l.description,l.price_cents,l.condition,l.negotiable,l.purchased_at,l.trade_status FROM listings l JOIN content_entities e ON e.id=l.entity_id WHERE e.id=$1 FOR UPDATE OF l,e`, id).Scan(&l.ID, &l.OwnerID, &l.PublicationStatus, &l.ModerationStatus, &l.CreatedAt, &l.UpdatedAt, &l.CategoryID, &l.LocationID, &l.Title, &l.Description, &l.PriceCents, &l.Condition, &l.Negotiable, &l.PurchasedAt, &l.TradeStatus)
	return l, err
}

func (s *Server) updateMarketListing(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "listingID")
	if err != nil {
		return err
	}
	user, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body marketListingPatch
	if apiErr := decodeStrictBody(r, &body); apiErr != nil {
		return apiErr
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	l, err := s.loadMarketListingForUpdate(r.Context(), tx, id)
	if err == pgx.ErrNoRows {
		return apiError(404, "LISTING_NOT_FOUND", "商品不存在")
	}
	if err != nil {
		return err
	}
	if l.OwnerID != user.ID {
		return apiError(403, "NOT_SELLER", "只有卖家可以修改商品")
	}
	if !marketpolicy.ListingEditable(l.TradeStatus) {
		return apiError(409, "LISTING_NOT_EDITABLE", "只有在售商品可以编辑")
	}
	// A listing keeps trade_status='available' while buyers' requests are outstanding, so
	// the seller could raise the price after someone applied. Nothing recorded the agreed
	// price — content_revisions only tracks title and description — leaving the buyer with
	// no way to show what they signed up for. Block price changes while a request is live.
	if body.PriceCents != nil && *body.PriceCents != l.PriceCents {
		var pending bool
		if err := tx.QueryRow(r.Context(), "SELECT EXISTS(SELECT 1 FROM market_transactions WHERE listing_id=$1 AND status IN ('requested','reserved','disputed'))", l.ID).Scan(&pending); err != nil {
			return err
		}
		if pending {
			return apiError(409, "LISTING_HAS_PENDING_REQUEST", "已有买家申请时不能修改价格")
		}
	}
	if body.CategoryID != nil {
		l.CategoryID = *body.CategoryID
	}
	if body.LocationID != nil {
		l.LocationID = *body.LocationID
	}
	if body.Title != nil {
		l.Title = strings.TrimSpace(*body.Title)
	}
	if body.Description != nil {
		l.Description = strings.TrimSpace(*body.Description)
	}
	if body.PriceCents != nil {
		l.PriceCents = *body.PriceCents
	}
	if body.Condition != nil {
		l.Condition = *body.Condition
	}
	if body.Negotiable != nil {
		l.Negotiable = *body.Negotiable
	}
	if len(body.PurchasedAt) > 0 {
		if string(body.PurchasedAt) == "null" {
			l.PurchasedAt = nil
		} else {
			var date JSONDate
			if json.Unmarshal(body.PurchasedAt, &date) != nil {
				return validation("purchased_at", "购买日期无效")
			}
			l.PurchasedAt = &date.Time
		}
	}
	attachments := []int64{}
	if body.AttachmentIDs != nil {
		attachments = *body.AttachmentIDs
	}
	if apiErr := s.validateMarketListing(r.Context(), tx, l, attachments); apiErr != nil {
		return apiErr
	}
	e, err := getEntityForUpdate(r.Context(), tx, id)
	if err != nil {
		return err
	}
	if err := recordRevision(r.Context(), tx, e, user.ID, l.Title, l.Description); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), `UPDATE listings SET category_id=$1,location_id=$2,title=$3,description=$4,price_cents=$5,condition=$6,negotiable=$7,purchased_at=$8 WHERE entity_id=$9`, l.CategoryID, l.LocationID, l.Title, l.Description, l.PriceCents, l.Condition, l.Negotiable, l.PurchasedAt, id); err != nil {
		return err
	}
	if err := s.remoderate(r.Context(), tx, &e, l.Title+"\n"+l.Description); err != nil {
		return err
	}
	if body.AttachmentIDs != nil {
		if err := s.attachUploads(r.Context(), tx, user.ID, id, attachments); err != nil {
			return err
		}
	}
	_, _ = tx.Exec(r.Context(), "UPDATE content_entities SET updated_at=now() WHERE id=$1", id)
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "listing.update", "listing", id, "", nil, nil, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	payload, err := s.marketListingByID(r.Context(), id, user.ID)
	if err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (s *Server) cancelMarketListing(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "listingID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if apiErr := decodeStrictBody(r, &body); apiErr != nil {
		return apiErr
	}
	if runeLen(strings.TrimSpace(body.Reason)) > 1000 {
		return validation("reason", "下架原因不能超过 1000 字符")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	l, err := s.loadMarketListingForUpdate(r.Context(), tx, id)
	if err == pgx.ErrNoRows {
		return apiError(404, "LISTING_NOT_FOUND", "商品不存在")
	}
	if err != nil {
		return err
	}
	if l.OwnerID != user.ID {
		return apiError(403, "NOT_SELLER", "只有卖家可以取消商品")
	}
	// 更具体的 ACTIVE_TRANSACTION 必须先判断：ListingCancellable 现在只允许 available，
	// 若先跑它，reserved 会被归为笼统的 INVALID_LISTING_TRANSITION，用户拿不到「先处理预留交易」的指引。
	if l.TradeStatus == "reserved" {
		return apiError(409, "ACTIVE_TRANSACTION", "请先取消预留交易或发起纠纷")
	}
	if !marketpolicy.ListingCancellable(l.TradeStatus) {
		return apiError(409, "INVALID_LISTING_TRANSITION", "当前商品状态不能取消")
	}
	if _, err := tx.Exec(r.Context(), "UPDATE listings SET trade_status='cancelled' WHERE entity_id=$1", id); err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "listing.cancel", "listing", id, strings.TrimSpace(body.Reason), map[string]any{"trade_status": l.TradeStatus}, map[string]any{"trade_status": "cancelled"}, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"ok": true, "trade_status": "cancelled"})
	return nil
}

type marketTransaction struct {
	ID, ListingID, SellerID, BuyerID int64
	Status, Message, CancelReason    string
	ReservedUntil                    *time.Time
	BuyerConfirmedAt                 *time.Time
	SellerConfirmedAt                *time.Time
	CompletedAt, CancelledAt         *time.Time
	CancelledBy                      *int64
	CreatedAt, UpdatedAt             time.Time
}

const marketTransactionSelect = `SELECT id,listing_id,seller_id,buyer_id,status,message,reserved_until,buyer_confirmed_at,seller_confirmed_at,completed_at,cancelled_at,cancelled_by,cancel_reason,created_at,updated_at FROM market_transactions`

func scanMarketTransaction(row pgx.Row) (marketTransaction, error) {
	var t marketTransaction
	err := row.Scan(&t.ID, &t.ListingID, &t.SellerID, &t.BuyerID, &t.Status, &t.Message, &t.ReservedUntil, &t.BuyerConfirmedAt, &t.SellerConfirmedAt, &t.CompletedAt, &t.CancelledAt, &t.CancelledBy, &t.CancelReason, &t.CreatedAt, &t.UpdatedAt)
	return t, err
}

func loadMarketTransactionForUpdate(ctx context.Context, tx pgx.Tx, id int64) (marketTransaction, error) {
	return scanMarketTransaction(tx.QueryRow(ctx, marketTransactionSelect+" WHERE id=$1 FOR UPDATE", id))
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

const marketTransactionPayloadSelect = `SELECT mt.id,mt.listing_id,mt.seller_id,mt.buyer_id,mt.status,mt.message,mt.reserved_until,mt.buyer_confirmed_at,mt.seller_confirmed_at,mt.completed_at,mt.cancelled_at,mt.cancelled_by,mt.cancel_reason,mt.created_at,mt.updated_at,l.title,l.price_cents,s.nickname,b.nickname,d.id,d.status FROM market_transactions mt JOIN listings l ON l.entity_id=mt.listing_id JOIN users s ON s.id=mt.seller_id JOIN users b ON b.id=mt.buyer_id LEFT JOIN market_disputes d ON d.transaction_id=mt.id`

func scanMarketTransactionPayload(row pgx.Row) (map[string]any, error) {
	var t marketTransaction
	var title, sellerName, buyerName string
	var price int64
	var disputeID *int64
	var disputeStatus *string
	err := row.Scan(&t.ID, &t.ListingID, &t.SellerID, &t.BuyerID, &t.Status, &t.Message, &t.ReservedUntil, &t.BuyerConfirmedAt, &t.SellerConfirmedAt, &t.CompletedAt, &t.CancelledAt, &t.CancelledBy, &t.CancelReason, &t.CreatedAt, &t.UpdatedAt, &title, &price, &sellerName, &buyerName, &disputeID, &disputeStatus)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": t.ID, "listing": map[string]any{"id": t.ListingID, "title": title, "price_cents": price}, "seller": map[string]any{"id": t.SellerID, "nickname": sellerName}, "buyer": map[string]any{"id": t.BuyerID, "nickname": buyerName}, "status": t.Status, "message": t.Message, "reserved_until": t.ReservedUntil, "buyer_confirmed_at": t.BuyerConfirmedAt, "seller_confirmed_at": t.SellerConfirmedAt, "completed_at": t.CompletedAt, "cancelled_at": t.CancelledAt, "cancelled_by": t.CancelledBy, "cancel_reason": t.CancelReason, "dispute": func() any {
		if disputeID == nil {
			return nil
		}
		return map[string]any{"id": *disputeID, "status": *disputeStatus}
	}(), "created_at": t.CreatedAt, "updated_at": t.UpdatedAt}, nil
}

func (s *Server) marketTransactionPayload(ctx context.Context, q queryer, id int64) (map[string]any, error) {
	return scanMarketTransactionPayload(q.QueryRow(ctx, marketTransactionPayloadSelect+" WHERE mt.id=$1", id))
}

func (s *Server) requestMarketTransaction(w http.ResponseWriter, r *http.Request) error {
	listingID, err := pathID(r, "listingID")
	if err != nil {
		return err
	}
	buyer, _, err := s.participatingUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Message string `json:"message"`
	}
	if apiErr := decodeStrictBody(r, &body); apiErr != nil {
		return apiErr
	}
	body.Message = strings.TrimSpace(body.Message)
	if runeLen(body.Message) > 1000 {
		return validation("message", "留言不能超过 1000 字符")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	listing, err := s.loadMarketListingForUpdate(r.Context(), tx, listingID)
	if err == pgx.ErrNoRows {
		return apiError(404, "LISTING_NOT_FOUND", "商品不存在")
	}
	if err != nil {
		return err
	}
	if listing.OwnerID == buyer.ID {
		return apiError(400, "SELF_PURCHASE_NOT_ALLOWED", "不能预订自己的商品")
	}
	if !marketpolicy.ListingRequestable(listing.TradeStatus, listing.PublicationStatus, listing.ModerationStatus) {
		return apiError(409, "LISTING_NOT_AVAILABLE", "商品当前不可预订")
	}
	var id int64
	err = tx.QueryRow(r.Context(), `INSERT INTO market_transactions(listing_id,seller_id,buyer_id,status,message,cancel_reason,created_at,updated_at) VALUES($1,$2,$3,'requested',$4,'',now(),now()) RETURNING id`, listingID, listing.OwnerID, buyer.ID, body.Message).Scan(&id)
	if isUniqueViolation(err) {
		return apiError(409, "ACTIVE_REQUEST_EXISTS", "你已经有一个进行中的申请")
	}
	if err != nil {
		return err
	}
	_ = notifySQL(r.Context(), tx, listing.OwnerID, "收到商品预订申请", listing.Title, fmt.Sprintf("/listings/%d", listingID), "market")
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	payload, err := s.marketTransactionPayload(r.Context(), s.DB, id)
	if err != nil {
		return err
	}
	writeJSON(w, 201, payload)
	return nil
}

func (s *Server) listListingTransactions(w http.ResponseWriter, r *http.Request) error {
	listingID, err := pathID(r, "listingID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var owner int64
	if err := s.DB.QueryRow(r.Context(), "SELECT e.owner_id FROM listings l JOIN content_entities e ON e.id=l.entity_id WHERE l.entity_id=$1", listingID).Scan(&owner); err == pgx.ErrNoRows {
		return apiError(404, "LISTING_NOT_FOUND", "商品不存在")
	} else if err != nil {
		return err
	}
	if user.ID != owner && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "NOT_SELLER", "只有卖家可以查看申请")
	}
	return s.writeMarketTransactionList(w, r, "WHERE mt.listing_id=$1", listingID)
}

func (s *Server) listMyMarketTransactions(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	return s.writeMarketTransactionList(w, r, "WHERE mt.buyer_id=$1 OR mt.seller_id=$1", user.ID)
}

func (s *Server) writeMarketTransactionList(w http.ResponseWriter, r *http.Request, where string, args ...any) error {
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	var total int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM market_transactions mt "+where, args...).Scan(&total); err != nil {
		return err
	}
	all := append([]any{}, args...)
	all = append(all, size, (page-1)*size)
	rows, err := s.DB.Query(r.Context(), fmt.Sprintf(marketTransactionPayloadSelect+" %s ORDER BY mt.created_at DESC LIMIT $%d OFFSET $%d", where, len(all)-1, len(all)), all...)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		p, err := scanMarketTransactionPayload(rows)
		if err != nil {
			return err
		}
		items = append(items, p)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return nil
}

func (s *Server) getMarketTransaction(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "transactionID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	t, err := scanMarketTransaction(s.DB.QueryRow(r.Context(), marketTransactionSelect+" WHERE id=$1", id))
	if err == pgx.ErrNoRows {
		return apiError(404, "TRANSACTION_NOT_FOUND", "交易不存在")
	}
	if err != nil {
		return err
	}
	if user.ID != t.BuyerID && user.ID != t.SellerID && user.Role != "moderator" && user.Role != "admin" {
		return apiError(403, "TRANSACTION_PARTICIPANT_REQUIRED", "无权查看该交易")
	}
	payload, err := s.marketTransactionPayload(r.Context(), s.DB, id)
	if err != nil {
		return err
	}
	writeJSON(w, 200, payload)
	return nil
}

func (s *Server) acceptMarketTransaction(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "transactionID")
	if err != nil {
		return err
	}
	seller, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	// Lock order: listings (and its content_entities row) first, then market_transactions.
	// requestMarketTransaction takes the listing lock first, so taking the transaction lock
	// first here closed a cycle: accepting two competing requests on the same listing
	// concurrently deadlocked (40P01) and surfaced to the seller as a 500.
	var listingID int64
	switch err := tx.QueryRow(r.Context(), "SELECT listing_id FROM market_transactions WHERE id=$1", id).Scan(&listingID); {
	case err == pgx.ErrNoRows:
		return apiError(404, "TRANSACTION_NOT_FOUND", "交易不存在")
	case err != nil:
		return err
	}
	listing, err := s.loadMarketListingForUpdate(r.Context(), tx, listingID)
	if err == pgx.ErrNoRows {
		return apiError(404, "LISTING_NOT_FOUND", "商品不存在")
	}
	if err != nil {
		return err
	}
	t, err := loadMarketTransactionForUpdate(r.Context(), tx, id)
	if err == pgx.ErrNoRows {
		return apiError(404, "TRANSACTION_NOT_FOUND", "交易不存在")
	}
	if err != nil {
		return err
	}
	if t.SellerID != seller.ID {
		return apiError(403, "NOT_SELLER", "只有卖家可以接受申请")
	}
	if !marketpolicy.RequestEndable(t.Status) {
		return apiError(409, "INVALID_TRANSACTION_TRANSITION", "申请当前不能接受")
	}
	// Re-check publication and moderation state, not just trade_status. Checking only
	// trade_status let a listing that had since been hidden or rejected by a moderator go
	// on to complete, counting towards the seller's public completed_sales and rating.
	if !marketpolicy.ListingRequestable(listing.TradeStatus, listing.PublicationStatus, listing.ModerationStatus) {
		return apiError(409, "LISTING_NOT_AVAILABLE", "商品已不可预订")
	}
	reservedUntil := time.Now().UTC().Add(s.Config.MarketReservationTTL)
	if s.Config.MarketReservationTTL <= 0 {
		reservedUntil = time.Now().UTC().Add(24 * time.Hour)
	}
	if _, err := tx.Exec(r.Context(), "UPDATE market_transactions SET status='rejected',updated_at=now() WHERE listing_id=$1 AND status='requested' AND id<>$2", t.ListingID, id); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "UPDATE market_transactions SET status='reserved',reserved_until=$1,updated_at=now() WHERE id=$2", reservedUntil, id); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "UPDATE listings SET trade_status='reserved' WHERE entity_id=$1", t.ListingID); err != nil {
		return err
	}
	_ = notifySQL(r.Context(), tx, t.BuyerID, "商品预订已接受", listing.Title, fmt.Sprintf("/market-transactions/%d", id), "market")
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	p, err := s.marketTransactionPayload(r.Context(), s.DB, id)
	if err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}

func (s *Server) rejectMarketTransaction(w http.ResponseWriter, r *http.Request) error {
	return s.endRequestedMarketTransaction(w, r, "rejected")
}

func (s *Server) endRequestedMarketTransaction(w http.ResponseWriter, r *http.Request, status string) error {
	id, err := pathID(r, "transactionID")
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
	t, err := loadMarketTransactionForUpdate(r.Context(), tx, id)
	if err == pgx.ErrNoRows {
		return apiError(404, "TRANSACTION_NOT_FOUND", "交易不存在")
	}
	if err != nil {
		return err
	}
	if !marketpolicy.RequestEndable(t.Status) {
		return apiError(409, "INVALID_TRANSACTION_TRANSITION", "申请当前不能结束")
	}
	if status == "rejected" && user.ID != t.SellerID {
		return apiError(403, "NOT_SELLER", "只有卖家可以拒绝申请")
	}
	if status == "cancelled" && user.ID != t.BuyerID {
		return apiError(403, "NOT_BUYER", "只有买家可以撤回申请")
	}
	_, err = tx.Exec(r.Context(), "UPDATE market_transactions SET status=$1,cancelled_at=CASE WHEN $1='cancelled' THEN now() ELSE NULL END,cancelled_by=CASE WHEN $1='cancelled' THEN $2 ELSE NULL END,updated_at=now() WHERE id=$3", status, user.ID, id)
	if err != nil {
		return err
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	p, err := s.marketTransactionPayload(r.Context(), s.DB, id)
	if err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}

func (s *Server) cancelMarketTransaction(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "transactionID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if apiErr := decodeStrictBody(r, &body); apiErr != nil {
		return apiErr
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if runeLen(body.Reason) > 1000 {
		return validation("reason", "取消原因不能超过 1000 字符")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	t, err := loadMarketTransactionForUpdate(r.Context(), tx, id)
	if err == pgx.ErrNoRows {
		return apiError(404, "TRANSACTION_NOT_FOUND", "交易不存在")
	}
	if err != nil {
		return err
	}
	if user.ID != t.BuyerID && user.ID != t.SellerID {
		return apiError(403, "TRANSACTION_PARTICIPANT_REQUIRED", "只有交易双方可以取消")
	}
	switch marketpolicy.Cancellation(t.Status, t.BuyerConfirmedAt != nil, t.SellerConfirmedAt != nil) {
	case marketpolicy.CancelAllowed:
		if t.Status == "requested" && user.ID != t.BuyerID {
			return apiError(403, "NOT_BUYER", "只有买家可以撤回申请")
		}
	case marketpolicy.CancelNeedsDispute:
		return apiError(409, "DISPUTE_REQUIRED", "已有一方确认，必须发起纠纷")
	default:
		return apiError(409, "INVALID_TRANSACTION_TRANSITION", "交易当前不能取消")
	}
	if _, err := tx.Exec(r.Context(), "UPDATE market_transactions SET status='cancelled',cancelled_at=now(),cancelled_by=$1,cancel_reason=$2,updated_at=now() WHERE id=$3", user.ID, body.Reason, id); err != nil {
		return err
	}
	if t.Status == "reserved" {
		if _, err := tx.Exec(r.Context(), "UPDATE listings SET trade_status='available' WHERE entity_id=$1", t.ListingID); err != nil {
			return err
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	p, err := s.marketTransactionPayload(r.Context(), s.DB, id)
	if err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}

func (s *Server) confirmMarketTransaction(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "transactionID")
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
	t, err := loadMarketTransactionForUpdate(r.Context(), tx, id)
	if err == pgx.ErrNoRows {
		return apiError(404, "TRANSACTION_NOT_FOUND", "交易不存在")
	}
	if err != nil {
		return err
	}
	if user.ID != t.BuyerID && user.ID != t.SellerID {
		return apiError(403, "TRANSACTION_PARTICIPANT_REQUIRED", "只有交易双方可以确认")
	}
	if t.Status == "completed" {
		p, err := s.marketTransactionPayload(r.Context(), tx, id)
		if err != nil {
			return err
		}
		writeJSON(w, 200, p)
		return nil
	}
	if !marketpolicy.Confirmable(t.Status) {
		return apiError(409, "INVALID_TRANSACTION_TRANSITION", "交易当前不能确认")
	}
	// Only expire a reservation that neither side has confirmed. If one party already
	// confirmed the hand-over, "expired" is a terminal state with no dispute, no review and
	// no admin ruling available — the counterparty who already paid would have had no
	// recourse at all, while the listing went straight back on sale.
	if t.ReservedUntil != nil && t.ReservedUntil.Before(time.Now()) && t.BuyerConfirmedAt == nil && t.SellerConfirmedAt == nil {
		_, _ = tx.Exec(r.Context(), "UPDATE market_transactions SET status='expired',updated_at=now() WHERE id=$1", id)
		_, _ = tx.Exec(r.Context(), "UPDATE listings SET trade_status='available' WHERE entity_id=$1", t.ListingID)
		if err := tx.Commit(r.Context()); err != nil {
			return err
		}
		return apiError(409, "RESERVATION_EXPIRED", "预留已超时释放")
	}
	now := time.Now().UTC()
	if user.ID == t.BuyerID && t.BuyerConfirmedAt == nil {
		t.BuyerConfirmedAt = &now
		_, err = tx.Exec(r.Context(), "UPDATE market_transactions SET buyer_confirmed_at=$1,updated_at=now() WHERE id=$2", now, id)
	} else if user.ID == t.SellerID && t.SellerConfirmedAt == nil {
		t.SellerConfirmedAt = &now
		_, err = tx.Exec(r.Context(), "UPDATE market_transactions SET seller_confirmed_at=$1,updated_at=now() WHERE id=$2", now, id)
	}
	if err != nil {
		return err
	}
	if t.BuyerConfirmedAt != nil && t.SellerConfirmedAt != nil {
		if _, err := tx.Exec(r.Context(), "UPDATE market_transactions SET status='completed',completed_at=now(),updated_at=now() WHERE id=$1", id); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), "UPDATE listings SET trade_status='completed' WHERE entity_id=$1", t.ListingID); err != nil {
			return err
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	p, err := s.marketTransactionPayload(r.Context(), s.DB, id)
	if err != nil {
		return err
	}
	writeJSON(w, 200, p)
	return nil
}

func (s *Server) openMarketDispute(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "transactionID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		Reason        string  `json:"reason"`
		AttachmentIDs []int64 `json:"attachment_ids"`
	}
	if apiErr := decodeStrictBody(r, &body); apiErr != nil {
		return apiErr
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if n := runeLen(body.Reason); n < 5 || n > 5000 {
		return validation("reason", "纠纷说明应为 5 到 5000 字符")
	}
	if len(body.AttachmentIDs) > 9 {
		return validation("attachment_ids", "最多提交 9 张证据图片")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	t, err := loadMarketTransactionForUpdate(r.Context(), tx, id)
	if err == pgx.ErrNoRows {
		return apiError(404, "TRANSACTION_NOT_FOUND", "交易不存在")
	}
	if err != nil {
		return err
	}
	if user.ID != t.BuyerID && user.ID != t.SellerID {
		return apiError(403, "TRANSACTION_PARTICIPANT_REQUIRED", "只有交易双方可以发起纠纷")
	}
	if !marketpolicy.Disputable(t.Status) {
		return apiError(409, "INVALID_TRANSACTION_TRANSITION", "只有预留中的交易可以发起纠纷")
	}
	var disputeID int64
	err = tx.QueryRow(r.Context(), `INSERT INTO market_disputes(transaction_id,opened_by,reason,status,decision,admin_note,created_at) VALUES($1,$2,$3,'pending','','',now()) RETURNING id`, id, user.ID, body.Reason).Scan(&disputeID)
	if isUniqueViolation(err) {
		return apiError(409, "DISPUTE_EXISTS", "该交易已有纠纷")
	}
	if err != nil {
		return err
	}
	if err := s.attachMarketEvidence(r.Context(), tx, user.ID, disputeID, body.AttachmentIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(r.Context(), "UPDATE market_transactions SET status='disputed',updated_at=now() WHERE id=$1", id); err != nil {
		return err
	}
	actor := user.ID
	_ = auditSQL(r.Context(), tx, &actor, "market.dispute.open", "market_transaction", id, body.Reason, nil, map[string]any{"status": "disputed", "dispute_id": disputeID}, requestID(r.Context()))
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"id": disputeID, "transaction_id": id, "status": "pending"})
	return nil
}

func (s *Server) attachMarketEvidence(ctx context.Context, tx pgx.Tx, userID, disputeID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	unique := map[int64]bool{}
	for _, id := range ids {
		unique[id] = true
	}
	rows, err := tx.Query(ctx, "SELECT id FROM attachments WHERE id=ANY($1) AND owner_id=$2 AND status='pending' AND access_scope='market_dispute' FOR UPDATE", ids, userID)
	if err != nil {
		return err
	}
	defer rows.Close()
	found := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
		found[id] = true
	}
	// Check rows.Err() before concluding anything from the row count: a mid-stream failure
	// would otherwise be reported to the user as "evidence does not belong to you", hiding
	// a database fault behind a validation error at the worst possible moment.
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	// Compare against the de-duplicated set so submitting the same id twice is not
	// mistaken for a missing attachment.
	if len(found) != len(unique) {
		return apiError(400, "INVALID_EVIDENCE", "证据不存在、已使用或不属于当前用户")
	}
	for id := range unique {
		if _, err := tx.Exec(ctx, "UPDATE attachments SET status='attached' WHERE id=$1", id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "INSERT INTO market_dispute_evidence(dispute_id,attachment_id) VALUES($1,$2)", disputeID, id); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) createMarketReview(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "transactionID")
	if err != nil {
		return err
	}
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		Rating int    `json:"rating"`
		Body   string `json:"body"`
	}
	if apiErr := decodeStrictBody(r, &body); apiErr != nil {
		return apiErr
	}
	body.Body = strings.TrimSpace(body.Body)
	if body.Rating < 1 || body.Rating > 5 {
		return validation("rating", "评分应为 1 到 5")
	}
	if runeLen(body.Body) > 2000 {
		return validation("body", "评价不能超过 2000 字符")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	t, err := loadMarketTransactionForUpdate(r.Context(), tx, id)
	if err == pgx.ErrNoRows {
		return apiError(404, "TRANSACTION_NOT_FOUND", "交易不存在")
	}
	if err != nil {
		return err
	}
	if !marketpolicy.Reviewable(t.Status) {
		return apiError(409, "TRANSACTION_NOT_COMPLETED", "只有已完成交易可以评价")
	}
	var reviewee int64
	if user.ID == t.BuyerID {
		reviewee = t.SellerID
	} else if user.ID == t.SellerID {
		reviewee = t.BuyerID
	} else {
		return apiError(403, "TRANSACTION_PARTICIPANT_REQUIRED", "只有交易双方可以评价")
	}
	visibleAt := time.Now().UTC().Add(s.Config.MarketReviewBlindTTL)
	if s.Config.MarketReviewBlindTTL <= 0 {
		visibleAt = time.Now().UTC().Add(14 * 24 * time.Hour)
	}
	var reviewID int64
	err = tx.QueryRow(r.Context(), `INSERT INTO market_reviews(transaction_id,reviewer_id,reviewee_id,rating,body,visible_at,created_at) VALUES($1,$2,$3,$4,$5,$6,now()) RETURNING id`, id, user.ID, reviewee, body.Rating, body.Body, visibleAt).Scan(&reviewID)
	if isUniqueViolation(err) {
		return apiError(409, "REVIEW_EXISTS", "你已经评价过该交易")
	}
	if err != nil {
		return err
	}
	var count int
	if err := tx.QueryRow(r.Context(), "SELECT count(*) FROM market_reviews WHERE transaction_id=$1", id).Scan(&count); err != nil {
		return err
	}
	if count == 2 {
		if _, err := tx.Exec(r.Context(), "UPDATE market_reviews SET visible_at=now() WHERE transaction_id=$1", id); err != nil {
			return err
		}
		visibleAt = time.Now().UTC()
	}
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 201, map[string]any{"id": reviewID, "transaction_id": id, "visible_at": visibleAt})
	return nil
}

func (s *Server) listMarketDisputes(w http.ResponseWriter, r *http.Request) error {
	if _, err := s.moderatorUser(w, r); err != nil {
		return err
	}
	page, size, err := pagination(r, 20, 100)
	if err != nil {
		return err
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	if status != "pending" && status != "resolved" {
		return validation("status", "纠纷状态无效")
	}
	var total int
	if err := s.DB.QueryRow(r.Context(), "SELECT count(*) FROM market_disputes WHERE status=$1", status).Scan(&total); err != nil {
		return err
	}
	rows, err := s.DB.Query(r.Context(), `SELECT d.id,d.transaction_id,d.opened_by,u.nickname,d.reason,d.status,d.decision,d.admin_note,d.created_at,d.decided_at,COALESCE((SELECT jsonb_agg(jsonb_build_object('id',a.id,'width',a.width,'height',a.height) ORDER BY a.id) FROM market_dispute_evidence de JOIN attachments a ON a.id=de.attachment_id WHERE de.dispute_id=d.id),'[]'::jsonb) FROM market_disputes d JOIN users u ON u.id=d.opened_by WHERE d.status=$1 ORDER BY d.created_at DESC LIMIT $2 OFFSET $3`, status, size, (page-1)*size)
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id, transactionID, openedBy int64
		var nickname, reason, current, decision, note string
		var created time.Time
		var decided *time.Time
		var raw json.RawMessage
		if err := rows.Scan(&id, &transactionID, &openedBy, &nickname, &reason, &current, &decision, &note, &created, &decided, &raw); err != nil {
			return err
		}
		var evidence []map[string]any
		_ = json.Unmarshal(raw, &evidence)
		for _, item := range evidence {
			item["content_url"] = fmt.Sprintf("%s/attachments/%v/content", s.Config.APIPrefix, item["id"])
		}
		items = append(items, map[string]any{"id": id, "transaction_id": transactionID, "opened_by": map[string]any{"id": openedBy, "nickname": nickname}, "reason": reason, "status": current, "decision": decision, "admin_note": note, "evidence": evidence, "created_at": created, "decided_at": decided})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, pagePayload(items, page, size, total))
	return nil
}

func (s *Server) decideMarketDispute(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "disputeID")
	if err != nil {
		return err
	}
	moderator, err := s.moderatorUser(w, r)
	if err != nil {
		return err
	}
	var body struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if apiErr := decodeStrictBody(r, &body); apiErr != nil {
		return apiErr
	}
	body.Note = strings.TrimSpace(body.Note)
	if body.Decision != "completed" && body.Decision != "cancelled" {
		return validation("decision", "裁决只能是 completed 或 cancelled")
	}
	if n := runeLen(body.Note); n < 2 || n > 5000 {
		return validation("note", "裁决说明应为 2 到 5000 字符")
	}
	tx, err := s.DB.Begin(r.Context())
	if err != nil {
		return err
	}
	defer tx.Rollback(r.Context())
	var transactionID int64
	var status string
	if err := tx.QueryRow(r.Context(), "SELECT transaction_id,status FROM market_disputes WHERE id=$1 FOR UPDATE", id).Scan(&transactionID, &status); err == pgx.ErrNoRows {
		return apiError(404, "DISPUTE_NOT_FOUND", "纠纷不存在")
	} else if err != nil {
		return err
	}
	if status == "resolved" {
		return apiError(409, "DISPUTE_RESOLVED", "纠纷已经裁决")
	}
	t, err := loadMarketTransactionForUpdate(r.Context(), tx, transactionID)
	if err != nil {
		return err
	}
	// A moderator must recuse themselves from their own trades. The ruling is final —
	// DisputeDecidable requires status 'disputed', and deciding moves the transaction out
	// of it — so a moderator who is also the buyer or seller could settle their own dispute
	// in their favour with no appeal path for the other party.
	if moderator.ID == t.BuyerID || moderator.ID == t.SellerID {
		return apiError(403, "DISPUTE_SELF_DEALING", "不能裁决自己参与的交易纠纷")
	}
	if !marketpolicy.DisputeDecidable(status, t.Status) {
		return apiError(409, "INVALID_TRANSACTION_TRANSITION", "交易不在纠纷状态")
	}
	if _, err := tx.Exec(r.Context(), "UPDATE market_disputes SET status='resolved',decision=$1,admin_note=$2,decided_by=$3,decided_at=now() WHERE id=$4", body.Decision, body.Note, moderator.ID, id); err != nil {
		return err
	}
	if body.Decision == "completed" {
		if _, err := tx.Exec(r.Context(), "UPDATE market_transactions SET status='completed',completed_at=now(),updated_at=now() WHERE id=$1", transactionID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), "UPDATE listings SET trade_status='completed' WHERE entity_id=$1", t.ListingID); err != nil {
			return err
		}
	} else {
		if _, err := tx.Exec(r.Context(), "UPDATE market_transactions SET status='cancelled',cancelled_at=now(),cancel_reason=$1,updated_at=now() WHERE id=$2", body.Note, transactionID); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), "UPDATE listings SET trade_status='available' WHERE entity_id=$1", t.ListingID); err != nil {
			return err
		}
	}
	actor := moderator.ID
	_ = auditSQL(r.Context(), tx, &actor, "market.dispute.decide", "market_dispute", id, body.Note, map[string]any{"status": "pending"}, map[string]any{"status": "resolved", "decision": body.Decision}, requestID(r.Context()))
	_ = notifySQL(r.Context(), tx, t.BuyerID, "交易纠纷已裁决", body.Note, fmt.Sprintf("/market-transactions/%d", transactionID), "market")
	_ = notifySQL(r.Context(), tx, t.SellerID, "交易纠纷已裁决", body.Note, fmt.Sprintf("/market-transactions/%d", transactionID), "market")
	if err := tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"id": id, "transaction_id": transactionID, "status": "resolved", "decision": body.Decision})
	return nil
}

type marketOptionInput struct {
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Active    *bool  `json:"active"`
	SortOrder int    `json:"sort_order"`
}

func (s *Server) adminMarketCategories(w http.ResponseWriter, r *http.Request) error {
	return s.listAdminMarketOptions(w, r, "market_categories")
}
func (s *Server) adminMarketLocations(w http.ResponseWriter, r *http.Request) error {
	return s.listAdminMarketOptions(w, r, "market_locations")
}
func (s *Server) createMarketCategory(w http.ResponseWriter, r *http.Request) error {
	return s.createMarketOption(w, r, "market_categories", 60)
}
func (s *Server) createMarketLocation(w http.ResponseWriter, r *http.Request) error {
	return s.createMarketOption(w, r, "market_locations", 120)
}
func (s *Server) updateMarketCategory(w http.ResponseWriter, r *http.Request) error {
	return s.updateMarketOption(w, r, "market_categories", 60)
}
func (s *Server) updateMarketLocation(w http.ResponseWriter, r *http.Request) error {
	return s.updateMarketOption(w, r, "market_locations", 120)
}
func (s *Server) deleteMarketCategory(w http.ResponseWriter, r *http.Request) error {
	return s.disableMarketOption(w, r, "market_categories")
}
func (s *Server) deleteMarketLocation(w http.ResponseWriter, r *http.Request) error {
	return s.disableMarketOption(w, r, "market_locations")
}

func (s *Server) listAdminMarketOptions(w http.ResponseWriter, r *http.Request, table string) error {
	if _, err := s.adminUser(w, r); err != nil {
		return err
	}
	rows, err := s.DB.Query(r.Context(), "SELECT id,name,slug,active,sort_order,created_at,updated_at FROM "+table+" ORDER BY sort_order,id")
	if err != nil {
		return err
	}
	defer rows.Close()
	items := []any{}
	for rows.Next() {
		var id int64
		var name, slug string
		var active bool
		var order int
		var created, updated time.Time
		if err := rows.Scan(&id, &name, &slug, &active, &order, &created, &updated); err != nil {
			return err
		}
		items = append(items, map[string]any{"id": id, "name": name, "slug": slug, "active": active, "sort_order": order, "created_at": created, "updated_at": updated})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"items": items})
	return nil
}

func validateMarketOption(body marketOptionInput, maxName int) *APIError {
	body.Name = strings.TrimSpace(body.Name)
	body.Slug = strings.TrimSpace(strings.ToLower(body.Slug))
	fields := map[string]string{}
	if n := runeLen(body.Name); n < 1 || n > maxName {
		fields["name"] = fmt.Sprintf("名称应为 1 到 %d 个字符", maxName)
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}$`).MatchString(body.Slug) {
		fields["slug"] = "slug 只能包含小写字母、数字和连字符"
	}
	if body.SortOrder < -10000 || body.SortOrder > 10000 {
		fields["sort_order"] = "排序值超出范围"
	}
	if len(fields) > 0 {
		return validationFields(fields)
	}
	return nil
}

func (s *Server) createMarketOption(w http.ResponseWriter, r *http.Request, table string, maxName int) error {
	admin, err := s.adminUser(w, r)
	if err != nil {
		return err
	}
	var body marketOptionInput
	if apiErr := decodeStrictBody(r, &body); apiErr != nil {
		return apiErr
	}
	body.Name = strings.TrimSpace(body.Name)
	body.Slug = strings.TrimSpace(strings.ToLower(body.Slug))
	if apiErr := validateMarketOption(body, maxName); apiErr != nil {
		return apiErr
	}
	active := true
	if body.Active != nil {
		active = *body.Active
	}
	var id int64
	var createdAt, updatedAt time.Time
	err = s.DB.QueryRow(r.Context(), "INSERT INTO "+table+"(name,slug,active,sort_order,created_at,updated_at) VALUES($1,$2,$3,$4,now(),now()) RETURNING id,created_at,updated_at", body.Name, body.Slug, active, body.SortOrder).Scan(&id, &createdAt, &updatedAt)
	if isUniqueViolation(err) {
		return apiError(409, "MARKET_OPTION_EXISTS", "名称或 slug 已存在")
	}
	if err != nil {
		return err
	}
	_ = admin
	writeJSON(w, 201, map[string]any{"id": id, "name": body.Name, "slug": body.Slug, "active": active, "sort_order": body.SortOrder, "created_at": createdAt, "updated_at": updatedAt})
	return nil
}

func (s *Server) updateMarketOption(w http.ResponseWriter, r *http.Request, table string, maxName int) error {
	if _, err := s.adminUser(w, r); err != nil {
		return err
	}
	id, err := pathID(r, "optionID")
	if err != nil {
		return err
	}
	var body marketOptionInput
	if apiErr := decodeStrictBody(r, &body); apiErr != nil {
		return apiErr
	}
	body.Name = strings.TrimSpace(body.Name)
	body.Slug = strings.TrimSpace(strings.ToLower(body.Slug))
	if apiErr := validateMarketOption(body, maxName); apiErr != nil {
		return apiErr
	}
	active := true
	if body.Active != nil {
		active = *body.Active
	}
	tag, err := s.DB.Exec(r.Context(), "UPDATE "+table+" SET name=$1,slug=$2,active=$3,sort_order=$4,updated_at=now() WHERE id=$5", body.Name, body.Slug, active, body.SortOrder, id)
	if isUniqueViolation(err) {
		return apiError(409, "MARKET_OPTION_EXISTS", "名称或 slug 已存在")
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apiError(404, "MARKET_OPTION_NOT_FOUND", "市场字典项不存在")
	}
	var createdAt, updatedAt time.Time
	if err := s.DB.QueryRow(r.Context(), "SELECT created_at,updated_at FROM "+table+" WHERE id=$1", id).Scan(&createdAt, &updatedAt); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]any{"id": id, "name": body.Name, "slug": body.Slug, "active": active, "sort_order": body.SortOrder, "created_at": createdAt, "updated_at": updatedAt})
	return nil
}

func (s *Server) disableMarketOption(w http.ResponseWriter, r *http.Request, table string) error {
	if _, err := s.adminUser(w, r); err != nil {
		return err
	}
	id, err := pathID(r, "optionID")
	if err != nil {
		return err
	}
	tag, err := s.DB.Exec(r.Context(), "UPDATE "+table+" SET active=false,updated_at=now() WHERE id=$1", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return apiError(404, "MARKET_OPTION_NOT_FOUND", "市场字典项不存在")
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
