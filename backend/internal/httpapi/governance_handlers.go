package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	governanceapp "github.com/yatools/wutong-campus-wall/backend/internal/governance"
	"github.com/yatools/wutong-campus-wall/backend/internal/security"
)

func (s *Server) deactivateAccount(w http.ResponseWriter, r *http.Request) error {
	user, _, err := s.currentUser(w, r, true)
	if err != nil {
		return err
	}
	var body struct {
		Password     string `json:"password"`
		Confirmation string `json:"confirmation"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.Confirmation != "注销我的账号" {
		return apiError(http.StatusBadRequest, "CONFIRMATION_REQUIRED", "请输入“注销我的账号”确认")
	}
	if !security.VerifyPassword(body.Password, user.PasswordHash) {
		return apiError(http.StatusBadRequest, "PASSWORD_INVALID", "密码错误")
	}
	if err := s.Governance.DeactivateAccount(
		r.Context(),
		governanceapp.Actor{ID: user.ID, Role: user.Role},
		requestID(r.Context()),
	); err != nil {
		return governanceServiceError(err)
	}
	s.clearSessionCookies(w)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "anonymize_after_days": 30})
	return nil
}

func (s *Server) adminUpdateUser(w http.ResponseWriter, r *http.Request) error {
	admin, err := s.adminUser(w, r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "userID")
	if err != nil {
		return err
	}
	var raw map[string]json.RawMessage
	if err := decodeBody(r, &raw); err != nil {
		return err
	}
	var patch governanceapp.AccountPatch
	if value, ok := raw["reason"]; ok {
		_ = json.Unmarshal(value, &patch.Reason)
	}
	patch.Reason = strings.TrimSpace(patch.Reason)
	if runeLen(patch.Reason) < 2 || runeLen(patch.Reason) > 1000 {
		return validation("reason", "String should have at least 2 characters")
	}
	if value, ok := raw["role"]; ok {
		var item string
		_ = json.Unmarshal(value, &item)
		patch.Role = &item
	}
	if value, ok := raw["campus_identity"]; ok {
		var item string
		_ = json.Unmarshal(value, &item)
		patch.CampusIdentity = &item
	}
	if value, ok := raw["status"]; ok {
		var item string
		_ = json.Unmarshal(value, &item)
		patch.Status = &item
	}
	if value, ok := raw["credit"]; ok {
		var item int
		if json.Unmarshal(value, &item) != nil {
			return validation("credit", "Input should be between 0 and 1000")
		}
		patch.Credit = &item
	}
	account, err := s.Governance.UpdateAccount(
		r.Context(),
		governanceapp.Actor{ID: admin.ID, Role: admin.Role},
		id,
		patch,
		requestID(r.Context()),
	)
	if err != nil {
		return governanceServiceError(err)
	}
	writeJSON(w, http.StatusOK, userPayload(governanceAccountUser(account)))
	return nil
}

func (s *Server) adminDecideModeration(w http.ResponseWriter, r *http.Request) error {
	moderator, err := s.moderatorUser(w, r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "caseID")
	if err != nil {
		return err
	}
	var body struct {
		Decision   string `json:"decision"`
		Note       string `json:"note"`
		Respondent *int64 `json:"respondent_id"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	result, err := s.Governance.DecideModeration(
		r.Context(),
		governanceapp.Actor{ID: moderator.ID, Role: moderator.Role},
		id,
		governanceapp.ModerationCommand{
			Decision: body.Decision, Note: body.Note, Respondent: body.Respondent,
			RequestID: requestID(r.Context()),
		},
	)
	if err != nil {
		return governanceServiceError(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id": result.ID, "status": result.Status, "decision": result.Decision,
	})
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
	body.Violation = strings.TrimSpace(body.Violation)
	body.Result = strings.TrimSpace(body.Result)
	body.Rule = strings.TrimSpace(body.Rule)
	fields := map[string]string{}
	if runeLen(body.Violation) < 2 || runeLen(body.Violation) > 120 {
		fields["violation"] = "String should have at least 2 characters"
	}
	if runeLen(body.Result) < 2 || runeLen(body.Result) > 2000 {
		fields["result"] = "String should have at least 2 characters"
	}
	if runeLen(body.Rule) < 2 || runeLen(body.Rule) > 160 {
		fields["rule"] = "String should have at least 2 characters"
	}
	if len(fields) > 0 {
		return validationFields(fields)
	}
	result, err := s.Governance.CreatePenalty(
		r.Context(),
		governanceapp.Actor{ID: moderator.ID, Role: moderator.Role},
		governanceapp.PenaltyCommand{
			UserID: body.UserID, Violation: body.Violation, Result: body.Result,
			Rule: body.Rule, Delta: body.Delta, RequestID: requestID(r.Context()),
		},
	)
	if err != nil {
		return governanceServiceError(err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": result.ID, "credit": result.Credit})
	return nil
}

func (s *Server) appealPenalty(w http.ResponseWriter, r *http.Request) error {
	id, err := pathID(r, "penaltyID")
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
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	body.Reason = strings.TrimSpace(body.Reason)
	if runeLen(body.Reason) < 10 || runeLen(body.Reason) > 5000 {
		return validation("reason", "String should have at least 10 characters")
	}
	result, err := s.Governance.AppealPenalty(
		r.Context(),
		governanceapp.Actor{ID: user.ID, Role: user.Role},
		id,
		body.Reason,
		requestID(r.Context()),
	)
	if err != nil {
		return governanceServiceError(err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": result.ID, "status": result.Status})
	return nil
}

func (s *Server) adminDecideAppeal(w http.ResponseWriter, r *http.Request) error {
	moderator, err := s.moderatorUser(w, r)
	if err != nil {
		return err
	}
	id, err := pathID(r, "appealID")
	if err != nil {
		return err
	}
	var body struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	result, err := s.Governance.DecideAppeal(
		r.Context(),
		governanceapp.Actor{ID: moderator.ID, Role: moderator.Role},
		id,
		body.Status,
		body.Note,
		requestID(r.Context()),
	)
	if err != nil {
		return governanceServiceError(err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": result.ID, "status": result.Status})
	return nil
}

func governanceAccountUser(account governanceapp.Account) User {
	return User{
		ID: account.ID, Email: account.Email, PasswordHash: account.PasswordHash,
		Nickname: account.Nickname, Alias: account.Alias, CampusIdentity: account.CampusIdentity,
		Role: account.Role, Status: account.Status, Credit: account.Credit, XP: account.XP,
		AvatarPath: account.AvatarPath, DMStrangerOff: account.DMStrangerOff,
		HideOnline: account.HideOnline, VerifiedAt: account.VerifiedAt, CreatedAt: account.CreatedAt,
	}
}

func governanceServiceError(err error) error {
	var serviceErr *governanceapp.RuleError
	if !errors.As(err, &serviceErr) {
		return err
	}
	switch serviceErr.Code {
	case "ADMIN_REQUIRED":
		return apiError(http.StatusForbidden, serviceErr.Code, "需要管理员权限")
	case "MODERATOR_REQUIRED":
		return apiError(http.StatusForbidden, serviceErr.Code, "需要治理权限")
	case "USER_NOT_FOUND":
		return apiError(http.StatusNotFound, serviceErr.Code, "用户不存在")
	case "SELF_LOCKOUT":
		return apiError(http.StatusBadRequest, serviceErr.Code, "不能限制自己的管理员账号")
	case "INVALID_ROLE":
		return validation("role", "Value error, 角色无效")
	case "INVALID_CAMPUS_IDENTITY":
		return validation("campus_identity", "Value error, 校园身份无效")
	case "INVALID_ACCOUNT_STATUS":
		return validation("status", "Value error, 账号状态无效")
	case "INVALID_CREDIT":
		return validation("credit", "Input should be between 0 and 1000")
	case "INVALID_CREDIT_DELTA":
		return validation("credit_delta", "Input should be between -1000 and 0")
	case "PENALTY_NOT_FOUND":
		return apiError(http.StatusNotFound, serviceErr.Code, "处罚记录不存在")
	case "PENALTY_OWNER_REQUIRED":
		return apiError(http.StatusForbidden, serviceErr.Code, "只能申诉自己的处罚记录")
	case "APPEAL_NOT_FOUND":
		return apiError(http.StatusNotFound, serviceErr.Code, "申诉不存在")
	case "INVALID_APPEAL_STATUS":
		return validation("status", "Value error, 申诉决定无效")
	case "CASE_NOT_FOUND":
		return apiError(http.StatusNotFound, serviceErr.Code, "审核案件不存在")
	case "CONTENT_NOT_FOUND":
		return apiError(http.StatusNotFound, serviceErr.Code, "内容不存在")
	case "RESPONDENT_NOT_FOUND":
		return apiError(http.StatusNotFound, serviceErr.Code, "指定回应方不存在")
	case "INVALID_MODERATION_DECISION":
		return validation("decision", "Value error, 审核决定无效")
	default:
		return apiError(http.StatusConflict, serviceErr.Code, "状态冲突")
	}
}
