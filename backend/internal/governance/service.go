package governance

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/yatools/wutong-campus-wall/backend/internal/security"
)

type Service struct {
	work      UnitOfWork
	secretKey string
}

func NewService(work UnitOfWork, secretKey string) *Service {
	return &Service{work: work, secretKey: secretKey}
}

func (s *Service) DeactivateAccount(ctx context.Context, actor Actor, requestID string) error {
	return s.work.WithinTransaction(ctx, func(repo Repository) error {
		if _, err := repo.LockAccount(ctx, actor.ID); err != nil {
			return mapNotFound(err, "USER_NOT_FOUND")
		}
		if err := repo.DeactivateAccount(ctx, actor.ID); err != nil {
			return err
		}
		if err := repo.RevokeSessions(ctx, actor.ID); err != nil {
			return err
		}
		return repo.Audit(ctx, AuditEntry{
			ActorID: actor.ID, Action: "account.deactivate", Target: "user",
			TargetID: actor.ID, RequestID: requestID,
		})
	})
}

func (s *Service) UpdateAccount(ctx context.Context, admin Actor, targetID int64, patch AccountPatch, requestID string) (Account, error) {
	var result Account
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		if !admin.IsAdmin() {
			return rule("ADMIN_REQUIRED")
		}
		target, err := repo.LockAccount(ctx, targetID)
		if err != nil {
			return mapNotFound(err, "USER_NOT_FOUND")
		}
		before := target
		if patch.Role != nil {
			if !oneOf(*patch.Role, "user", "moderator", "admin") {
				return rule("INVALID_ROLE")
			}
			target.Role = *patch.Role
		}
		if patch.CampusIdentity != nil {
			if !oneOf(*patch.CampusIdentity, "student", "alumni", "staff") {
				return rule("INVALID_CAMPUS_IDENTITY")
			}
			target.CampusIdentity = *patch.CampusIdentity
		}
		if patch.Status != nil {
			if !oneOf(*patch.Status, "active", "restricted", "disabled") {
				return rule("INVALID_ACCOUNT_STATUS")
			}
			if target.ID == admin.ID && (*patch.Status == "disabled" || *patch.Status == "restricted") {
				return rule("SELF_LOCKOUT")
			}
			target.Status = *patch.Status
		}
		if patch.Credit != nil {
			if *patch.Credit < 0 || *patch.Credit > 1000 {
				return rule("INVALID_CREDIT")
			}
			target.Credit = *patch.Credit
		}
		if err := repo.SaveAccount(ctx, target); err != nil {
			return err
		}
		if target.Status == "disabled" || target.Role != before.Role {
			if err := repo.RevokeSessions(ctx, target.ID); err != nil {
				return err
			}
		}
		if err := repo.Audit(ctx, AuditEntry{
			ActorID: admin.ID, Action: "admin.user_update", Target: "user",
			TargetID: target.ID, Reason: patch.Reason, Before: before, After: target,
			RequestID: requestID,
		}); err != nil {
			return err
		}
		if err := repo.Notify(ctx, Notification{
			UserID: target.ID, Title: "账号状态已更新", Body: patch.Reason, Link: "/me",
		}); err != nil {
			return err
		}
		result = target
		return nil
	})
	return result, err
}

func (s *Service) CreatePenalty(ctx context.Context, moderator Actor, command PenaltyCommand) (PenaltyResult, error) {
	var result PenaltyResult
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		if !moderator.CanModerate() {
			return rule("MODERATOR_REQUIRED")
		}
		if command.Delta > 0 || command.Delta < -1000 {
			return rule("INVALID_CREDIT_DELTA")
		}
		before, after, err := repo.AdjustCredit(ctx, command.UserID, command.Delta)
		if err != nil {
			return mapNotFound(err, "USER_NOT_FOUND")
		}
		mask := "用户 " + strings.ToUpper(security.TokenHash(
			s.secretKey,
			"penalty-mask:"+strconv.FormatInt(command.UserID, 10),
		)[:10])
		id, err := repo.CreatePenalty(
			ctx, command.UserID, mask, command.Violation, command.Result, command.Rule,
		)
		if err != nil {
			return err
		}
		if err := repo.Audit(ctx, AuditEntry{
			ActorID: moderator.ID, Action: "penalty.create", Target: "penalty",
			TargetID: id, Reason: command.Rule,
			Before:    map[string]any{"user_id": command.UserID, "credit": before},
			After:     map[string]any{"user_id": command.UserID, "credit": after, "result": command.Result},
			RequestID: command.RequestID,
		}); err != nil {
			return err
		}
		if err := repo.Notify(ctx, Notification{
			UserID: command.UserID, Title: "收到治理处理", Body: command.Result, Link: "/governance",
		}); err != nil {
			return err
		}
		result = PenaltyResult{ID: id, Credit: after}
		return nil
	})
	return result, err
}

func (s *Service) AppealPenalty(ctx context.Context, actor Actor, penaltyID int64, reason, requestID string) (AppealResult, error) {
	var result AppealResult
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		ownerID, err := repo.PenaltyOwner(ctx, penaltyID)
		if err != nil {
			return mapNotFound(err, "PENALTY_NOT_FOUND")
		}
		if ownerID != actor.ID {
			return rule("PENALTY_OWNER_REQUIRED")
		}
		result, err = repo.UpsertAppeal(ctx, penaltyID, actor.ID, reason)
		if err != nil {
			return err
		}
		return repo.Audit(ctx, AuditEntry{
			ActorID: actor.ID, Action: "appeal.create", Target: "appeal",
			TargetID: result.ID, RequestID: requestID,
		})
	})
	return result, err
}

func (s *Service) DecideAppeal(ctx context.Context, moderator Actor, appealID int64, status, note, requestID string) (AppealResult, error) {
	result := AppealResult{ID: appealID}
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		if !moderator.CanModerate() {
			return rule("MODERATOR_REQUIRED")
		}
		if !oneOf(status, "approved", "rejected") {
			return rule("INVALID_APPEAL_STATUS")
		}
		userID, current, err := repo.LockAppeal(ctx, appealID)
		if err != nil {
			return mapNotFound(err, "APPEAL_NOT_FOUND")
		}
		result.Status = current
		if current != "pending" {
			return nil
		}
		if err := repo.ResolveAppeal(ctx, appealID, status, note); err != nil {
			return err
		}
		if err := repo.Audit(ctx, AuditEntry{
			ActorID: moderator.ID, Action: "appeal.decide", Target: "appeal",
			TargetID: appealID, Reason: note,
			Before: map[string]any{"status": current}, After: map[string]any{"status": status},
			RequestID: requestID,
		}); err != nil {
			return err
		}
		if err := repo.Notify(ctx, Notification{
			UserID: userID, Title: "申诉处理结果", Body: status + "：" + note, Link: "/me",
		}); err != nil {
			return err
		}
		result.Status = status
		return nil
	})
	return result, err
}

func (s *Service) DecideModeration(ctx context.Context, moderator Actor, caseID int64, command ModerationCommand) (ModerationResult, error) {
	result := ModerationResult{ID: caseID}
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		if !moderator.CanModerate() {
			return rule("MODERATOR_REQUIRED")
		}
		if !oneOf(command.Decision, "approve", "reject", "hide") {
			return rule("INVALID_MODERATION_DECISION")
		}
		item, err := repo.LockModerationCase(ctx, caseID)
		if err != nil {
			return mapNotFound(err, "CASE_NOT_FOUND")
		}
		if item.Status != "pending" {
			result.Status = item.Status
			result.Decision = item.Decision
			return nil
		}
		entity, err := repo.LockEntity(ctx, item.EntityID)
		if err != nil {
			return mapNotFound(err, "CONTENT_NOT_FOUND")
		}
		publication, moderation := "hidden", "rejected"
		if command.Decision == "approve" {
			publication, moderation = "published", "approved"
		}
		updatePublication := entity.PublicationStatus == "published" || entity.PublicationStatus == "hidden"
		if !updatePublication {
			publication = entity.PublicationStatus
		}
		if err := repo.ResolveModerationCase(ctx, caseID, moderator.ID, command.Decision, command.Note); err != nil {
			return err
		}
		if err := repo.SetEntityModeration(ctx, entity.ID, publication, moderation, updatePublication); err != nil {
			return err
		}
		if command.Decision != "approve" {
			if err := repo.RefundQuestionBounty(ctx, entity.ID, entity.OwnerID); err != nil {
				return err
			}
		}
		observeTitle, isObserve, err := repo.ObserveTitle(ctx, entity.ID)
		if err != nil {
			return err
		}
		if isObserve {
			if command.Respondent != nil {
				active, err := repo.ActiveUser(ctx, *command.Respondent)
				if err != nil {
					return err
				}
				if !active {
					return rule("RESPONDENT_NOT_FOUND")
				}
				if err := repo.Notify(ctx, Notification{
					UserID: *command.Respondent, Title: "你被指定为观察帖回应方",
					Body: observeTitle, Link: fmt.Sprintf("/observe/%d", entity.ID),
				}); err != nil {
					return err
				}
			}
			if err := repo.SetObserveDecision(ctx, entity.ID, command.Respondent, command.Note); err != nil {
				return err
			}
		}
		if err := repo.ResolveReports(ctx, entity.ID); err != nil {
			return err
		}
		if err := repo.Audit(ctx, AuditEntry{
			ActorID: moderator.ID, Action: "moderation.decide", Target: entity.Type,
			TargetID: entity.ID, Reason: command.Note,
			Before:    map[string]any{"case_status": item.Status, "entity_status": entity.PublicationStatus},
			After:     map[string]any{"case_status": "resolved", "entity_status": publication, "decision": command.Decision},
			RequestID: command.RequestID,
		}); err != nil {
			return err
		}
		if err := repo.Notify(ctx, Notification{
			UserID: entity.OwnerID, Title: "内容审核结果",
			Body: "审核结果：" + command.Decision + "。" + command.Note,
			Link: fmt.Sprintf("/content/%d", entity.ID),
		}); err != nil {
			return err
		}
		result.Status = "resolved"
		result.Decision = command.Decision
		return nil
	})
	return result, err
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func mapNotFound(err error, code string) error {
	if errors.Is(err, ErrNotFound) {
		return rule(code)
	}
	return err
}
