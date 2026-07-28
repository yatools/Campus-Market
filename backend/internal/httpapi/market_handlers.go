package httpapi

import (
	"errors"
	"net/http"
	"strings"

	marketapp "github.com/yatools/wutong-campus-wall/backend/internal/market"
)

// The market handlers are adapters: authentication, request parsing, validation and
// domain-error translation stay here; transaction boundaries and state changes live in
// market.Service.

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
	body.Reason = strings.TrimSpace(body.Reason)
	if runeLen(body.Reason) > 1000 {
		return validation("reason", "下架原因不能超过 1000 个字符")
	}
	result, err := s.Market.CancelListing(
		r.Context(),
		marketapp.Actor{ID: user.ID, Role: user.Role},
		id,
		body.Reason,
		requestID(r.Context()),
	)
	if err != nil {
		return marketServiceError(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "trade_status": result.Status})
	return nil
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
		return validation("message", "留言不能超过 1000 个字符")
	}
	result, err := s.Market.RequestTransaction(
		r.Context(),
		marketapp.Actor{ID: buyer.ID, Role: buyer.Role},
		listingID,
		body.Message,
	)
	if err != nil {
		return marketServiceError(err)
	}
	payload, err := s.marketTransactionPayload(r.Context(), s.DB, result.TransactionID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusCreated, payload)
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
	result, err := s.Market.AcceptTransaction(
		r.Context(),
		marketapp.Actor{ID: seller.ID, Role: seller.Role},
		id,
	)
	if err != nil {
		return marketServiceError(err)
	}
	return s.writeMarketTransaction(w, r, result.TransactionID)
}

func (s *Server) rejectMarketTransaction(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "transactionID")
	if err != nil {
		return err
	}
	seller, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	result, err := s.Market.EndRequest(
		r.Context(),
		marketapp.Actor{ID: seller.ID, Role: seller.Role},
		id,
		"rejected",
	)
	if err != nil {
		return marketServiceError(err)
	}
	return s.writeMarketTransaction(w, r, result.TransactionID)
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
		return validation("reason", "取消原因不能超过 1000 个字符")
	}
	result, err := s.Market.CancelTransaction(
		r.Context(),
		marketapp.Actor{ID: user.ID, Role: user.Role},
		id,
		body.Reason,
	)
	if err != nil {
		return marketServiceError(err)
	}
	return s.writeMarketTransaction(w, r, result.TransactionID)
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
	result, err := s.Market.ConfirmTransaction(
		r.Context(),
		marketapp.Actor{ID: user.ID, Role: user.Role},
		id,
	)
	if err != nil {
		return marketServiceError(err)
	}
	return s.writeMarketTransaction(w, r, result.TransactionID)
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
		return validation("reason", "纠纷说明应为 5 到 5000 个字符")
	}
	if len(body.AttachmentIDs) > 9 {
		return validation("attachment_ids", "最多提交 9 张证据图片")
	}
	result, err := s.Market.OpenDispute(
		r.Context(),
		marketapp.Actor{ID: user.ID, Role: user.Role},
		id,
		body.Reason,
		body.AttachmentIDs,
		requestID(r.Context()),
	)
	if err != nil {
		return marketServiceError(err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": result.DisputeID, "transaction_id": result.TransactionID, "status": result.Status,
	})
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
		return validation("body", "评价不能超过 2000 个字符")
	}
	result, err := s.Market.CreateReview(
		r.Context(),
		marketapp.Actor{ID: user.ID, Role: user.Role},
		id,
		body.Rating,
		body.Body,
	)
	if err != nil {
		return marketServiceError(err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": result.ReviewID, "transaction_id": result.TransactionID, "visible_at": result.VisibleAt,
	})
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
		return validation("note", "裁决说明应为 2 到 5000 个字符")
	}
	result, err := s.Market.DecideDispute(
		r.Context(),
		marketapp.Actor{ID: moderator.ID, Role: moderator.Role},
		id,
		body.Decision,
		body.Note,
		requestID(r.Context()),
	)
	if err != nil {
		return marketServiceError(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": result.DisputeID, "transaction_id": result.TransactionID,
		"status": result.Status, "decision": result.Decision,
	})
	return nil
}

func marketServiceError(err error) error {
	var domainErr *marketapp.RuleError
	if !errors.As(err, &domainErr) {
		return err
	}
	type errorDefinition struct {
		status  int
		message string
	}
	definitions := map[string]errorDefinition{
		"LISTING_NOT_FOUND":                {http.StatusNotFound, "商品不存在"},
		"NOT_SELLER":                       {http.StatusForbidden, "只有卖家可以执行此操作"},
		"ACTIVE_TRANSACTION":               {http.StatusConflict, "请先处理预留交易或发起纠纷"},
		"INVALID_LISTING_TRANSITION":       {http.StatusConflict, "当前商品状态不允许此操作"},
		"SELF_PURCHASE_NOT_ALLOWED":        {http.StatusBadRequest, "不能预约自己的商品"},
		"LISTING_NOT_AVAILABLE":            {http.StatusConflict, "商品当前不可预约"},
		"ACTIVE_REQUEST_EXISTS":            {http.StatusConflict, "已有进行中的申请"},
		"TRANSACTION_NOT_FOUND":            {http.StatusNotFound, "交易不存在"},
		"INVALID_TRANSACTION_TRANSITION":   {http.StatusConflict, "当前交易状态不允许此操作"},
		"NOT_BUYER":                        {http.StatusForbidden, "只有买家可以执行此操作"},
		"TRANSACTION_PARTICIPANT_REQUIRED": {http.StatusForbidden, "只有交易双方可以执行此操作"},
		"DISPUTE_REQUIRED":                 {http.StatusConflict, "已有一方确认，必须发起纠纷"},
		"RESERVATION_EXPIRED":              {http.StatusConflict, "预留已超时释放"},
		"DISPUTE_EXISTS":                   {http.StatusConflict, "该交易已有纠纷"},
		"INVALID_EVIDENCE":                 {http.StatusBadRequest, "证据不存在、已使用或不属于当前用户"},
		"TRANSACTION_NOT_COMPLETED":        {http.StatusConflict, "只有已完成交易可以评价"},
		"REVIEW_EXISTS":                    {http.StatusConflict, "已经评价过该交易"},
		"DISPUTE_NOT_FOUND":                {http.StatusNotFound, "纠纷不存在"},
		"DISPUTE_RESOLVED":                 {http.StatusConflict, "纠纷已经裁决"},
		"DISPUTE_SELF_DEALING":             {http.StatusForbidden, "不能裁决自己参与的交易纠纷"},
		"INVALID_DISPUTE_DECISION":         {http.StatusBadRequest, "裁决结果无效"},
	}
	definition, ok := definitions[domainErr.Code]
	if !ok {
		return err
	}
	return apiError(definition.status, domainErr.Code, definition.message)
}
