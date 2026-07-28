package team

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

type Service struct {
	work UnitOfWork
	now  func() time.Time
}

func NewService(work UnitOfWork) *Service {
	return &Service{work: work, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) Join(ctx context.Context, actor Actor, teamID int64, channels []string) (Result, error) {
	result := Result{TeamID: teamID}
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		currentTeam, err := repo.LockTeam(ctx, teamID)
		if err != nil || currentTeam.PublicationStatus != "published" || currentTeam.Status != "active" {
			return rule("TEAM_NOT_FOUND")
		}
		membership, err := repo.ActiveMembership(ctx, teamID, actor.ID)
		if err == nil {
			return repo.UpdateMembershipChannels(ctx, membership.ID, JoinChannels(channels))
		}
		if !errors.Is(err, ErrNotFound) {
			return err
		}
		count, err := repo.ActiveMemberCount(ctx, teamID)
		if err != nil {
			return err
		}
		if count >= currentTeam.Capacity {
			return rule("TEAM_FULL")
		}
		run, err := repo.CurrentRun(ctx, teamID)
		if err != nil || !run.Starts.After(s.now()) {
			return rule("TEAM_ALREADY_DEPARTED")
		}
		if err := repo.JoinTeam(ctx, teamID, actor.ID, JoinChannels(channels)); err != nil {
			return err
		}
		if err := repo.JoinRun(ctx, run.ID, actor.ID); err != nil {
			return err
		}
		result.RunID = run.ID
		if err := repo.Notify(ctx, Notification{
			UserID: currentTeam.OwnerID,
			Title:  "有新成员上车",
			Body:   actor.Nickname + " 加入了你的 " + currentTeam.Game + " 车队",
			Link:   fmt.Sprintf("/teams/%d", teamID),
		}); err != nil {
			return err
		}
		if err := repo.Notify(ctx, Notification{
			UserID: actor.ID,
			Title:  "上车成功",
			Body:   "你已加入 " + currentTeam.Game + " · " + currentTeam.Mode,
			Link:   fmt.Sprintf("/teams/%d", teamID),
		}); err != nil {
			return err
		}
		return repo.TouchTeam(ctx, teamID)
	})
	return result, err
}

func (s *Service) Leave(ctx context.Context, actor Actor, teamID int64) (Result, error) {
	result := Result{TeamID: teamID}
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		currentTeam, err := repo.LockTeam(ctx, teamID)
		if err != nil {
			return rule("TEAM_NOT_FOUND")
		}
		if currentTeam.OwnerID == actor.ID {
			return rule("OWNER_CANNOT_LEAVE")
		}
		membership, err := repo.ActiveMembership(ctx, teamID, actor.ID)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		run, runErr := repo.CurrentRun(ctx, teamID)
		if runErr == nil {
			member, memberErr := repo.RunMembership(ctx, run.ID, actor.ID)
			if memberErr == nil {
				now := s.now()
				if !now.Before(run.Starts.Add(-30*time.Minute)) && now.Before(run.Starts) && member.ExcusedAt == nil {
					delta, err := repo.ApplyCredit(ctx, actor.ID, "penalty.team_late_leave", run.ID)
					if err != nil {
						return err
					}
					result.CreditDelta = delta
				}
				if err := repo.LeaveRun(ctx, member.ID); err != nil {
					return err
				}
			} else if !errors.Is(memberErr, ErrNotFound) {
				return memberErr
			}
		} else if !errors.Is(runErr, ErrNotFound) {
			return runErr
		}
		if err := repo.LeaveMembership(ctx, membership.ID); err != nil {
			return err
		}
		if err := repo.Notify(ctx, Notification{
			UserID: currentTeam.OwnerID,
			Title:  "成员已下车",
			Body:   actor.Nickname + " 退出了 " + currentTeam.Game + " 车队",
			Link:   fmt.Sprintf("/teams/%d", teamID),
		}); err != nil {
			return err
		}
		return repo.TouchTeam(ctx, teamID)
	})
	return result, err
}

func (s *Service) Excuse(ctx context.Context, actor Actor, teamID, runID int64) (Result, error) {
	result := Result{TeamID: teamID, RunID: runID}
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		run, err := repo.LockRun(ctx, runID)
		if err != nil || run.TeamID != teamID {
			return rule("RUN_MEMBER_REQUIRED")
		}
		member, err := repo.RunMembership(ctx, runID, actor.ID)
		if err != nil {
			return rule("RUN_MEMBER_REQUIRED")
		}
		if member.Status == "excused" && member.ExcusedAt != nil {
			result.ExcusedAt = member.ExcusedAt
			return nil
		}
		now := s.now()
		if !now.Before(run.Starts) {
			return rule("RUN_STARTED")
		}
		if member.Status != "joined" {
			return rule("RUN_MEMBER_REQUIRED")
		}
		if err := repo.ExcuseRun(ctx, runID, actor.ID, now); err != nil {
			return err
		}
		result.ExcusedAt = &now
		currentTeam, err := repo.LockTeam(ctx, teamID)
		if err != nil {
			return err
		}
		if err := repo.Notify(ctx, Notification{
			UserID: currentTeam.OwnerID,
			Title:  "成员请假",
			Body:   actor.Nickname + " 已为本次发车请假",
			Link:   fmt.Sprintf("/teams/%d", teamID),
		}); err != nil {
			return err
		}
		return repo.TouchTeam(ctx, teamID)
	})
	return result, err
}

func (s *Service) CheckIn(ctx context.Context, actor Actor, teamID, runID int64) (Result, error) {
	result := Result{TeamID: teamID, RunID: runID}
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		run, err := repo.LockRun(ctx, runID)
		if err != nil || run.TeamID != teamID {
			return rule("RUN_NOT_FOUND")
		}
		if run.Status != "scheduled" {
			return rule("RUN_NOT_ACTIVE")
		}
		currentTeam, err := repo.LockTeam(ctx, teamID)
		if err != nil || currentTeam.Status != "active" {
			return rule("TEAM_NOT_ACTIVE")
		}
		now := s.now()
		if now.Before(run.Starts) || now.Sub(run.Starts) > 30*time.Minute {
			return rule("OUTSIDE_CHECKIN_WINDOW")
		}
		member, err := repo.RunMembership(ctx, runID, actor.ID)
		if err != nil || member.Status != "joined" && member.Status != "checked_in" {
			return rule("RUN_MEMBER_REQUIRED")
		}
		if member.CheckedAt == nil {
			if err := repo.CheckInRun(ctx, member.ID, now); err != nil {
				return err
			}
			member.CheckedAt = &now
		}
		result.CheckedAt = member.CheckedAt
		if !member.Awarded {
			attendees, err := repo.RunAttendeeCount(ctx, runID)
			if err != nil {
				return err
			}
			if attendees >= 2 {
				if err := repo.CheckRateLimit(ctx, "team_check_in_day", strconv.FormatInt(actor.ID, 10), 4, 24*60); err != nil {
					return err
				}
				delta, err := repo.ApplyCredit(ctx, actor.ID, "reward.team_check_in", runID)
				if err != nil {
					return err
				}
				result.CreditDelta = delta
				if err := repo.MarkCreditAwarded(ctx, member.ID); err != nil {
					return err
				}
			}
		}
		return repo.TouchTeam(ctx, teamID)
	})
	return result, err
}

func (s *Service) Transfer(ctx context.Context, actor Actor, teamID, targetID int64, requestID string) (Result, error) {
	result := Result{TeamID: teamID}
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		currentTeam, err := repo.LockTeam(ctx, teamID)
		if err != nil || currentTeam.OwnerID != actor.ID {
			return rule("OWNER_REQUIRED")
		}
		if _, err := repo.ActiveMembership(ctx, teamID, targetID); err != nil {
			return rule("TARGET_NOT_MEMBER")
		}
		if err := repo.TransferOwnership(ctx, teamID, actor.ID, targetID); err != nil {
			return err
		}
		if err := repo.Notify(ctx, Notification{
			UserID: targetID, Title: "你已成为车头",
			Body: currentTeam.Game + " · " + currentTeam.Mode + " 已转让给你",
			Link: fmt.Sprintf("/teams/%d", teamID),
		}); err != nil {
			return err
		}
		if err := repo.Audit(ctx, AuditEntry{
			ActorID: actor.ID, Action: "team.transfer", Target: "team", TargetID: teamID,
			After: map[string]any{"owner_id": targetID}, RequestID: requestID,
		}); err != nil {
			return err
		}
		return repo.TouchTeam(ctx, teamID)
	})
	return result, err
}

func (s *Service) RemoveMember(ctx context.Context, actor Actor, teamID, memberID int64) (Result, error) {
	result := Result{TeamID: teamID}
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		currentTeam, err := repo.LockTeam(ctx, teamID)
		if err != nil || currentTeam.OwnerID != actor.ID && !actor.CanModerate() {
			return rule("OWNER_REQUIRED")
		}
		if memberID == currentTeam.OwnerID {
			return rule("CANNOT_REMOVE_OWNER")
		}
		removed, err := repo.RemoveMember(ctx, teamID, memberID)
		if err != nil {
			return err
		}
		if !removed {
			return rule("MEMBER_NOT_FOUND")
		}
		if run, err := repo.CurrentRun(ctx, teamID); err == nil {
			if err := repo.RemoveRunMember(ctx, run.ID, memberID); err != nil {
				return err
			}
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if err := repo.Notify(ctx, Notification{
			UserID: memberID, Title: "你已被移出车队",
			Body: "你已被移出 " + currentTeam.Game + " · " + currentTeam.Mode,
			Link: "/teams",
		}); err != nil {
			return err
		}
		return repo.TouchTeam(ctx, teamID)
	})
	return result, err
}

func (s *Service) Cancel(ctx context.Context, actor Actor, teamID int64, requestID string) (Result, error) {
	result := Result{TeamID: teamID}
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		currentTeam, err := repo.LockTeam(ctx, teamID)
		if err != nil || currentTeam.OwnerID != actor.ID && !actor.CanModerate() {
			return rule("OWNER_REQUIRED")
		}
		members, err := repo.ActiveMemberIDs(ctx, teamID)
		if err != nil {
			return err
		}
		if err := repo.CancelTeam(ctx, teamID); err != nil {
			return err
		}
		for _, id := range members {
			if err := repo.Notify(ctx, Notification{
				UserID: id, Title: "车队已取消",
				Body: currentTeam.Game + " · " + currentTeam.Mode + " 已取消", Link: "/teams",
			}); err != nil {
				return err
			}
		}
		return repo.Audit(ctx, AuditEntry{
			ActorID: actor.ID, Action: "team.cancel", Target: "team", TargetID: teamID, RequestID: requestID,
		})
	})
	return result, err
}

func (s *Service) Rate(ctx context.Context, actor Actor, teamID, runID, targetID int64, tags []string) (Result, error) {
	result := Result{TeamID: teamID, RunID: runID}
	err := s.work.WithinTransaction(ctx, func(repo Repository) error {
		run, err := repo.LockRun(ctx, runID)
		if err != nil || run.TeamID != teamID || s.now().Before(run.Starts) {
			return rule("RATING_NOT_OPEN")
		}
		if run.Status == "cancelled" {
			return rule("RUN_NOT_ACTIVE")
		}
		count, err := repo.CountRunParticipants(ctx, runID, []int64{actor.ID, targetID})
		if err != nil {
			return err
		}
		if count != 2 {
			return rule("SAME_RUN_REQUIRED")
		}
		return repo.InsertRatings(ctx, runID, actor.ID, targetID, tags)
	})
	return result, err
}
