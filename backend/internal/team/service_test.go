package team

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeWork struct {
	repo      fakeRepository
	commits   int
	rollbacks int
}

func (w *fakeWork) WithinTransaction(ctx context.Context, fn func(Repository) error) error {
	candidate := w.repo
	candidate.memberships = cloneMap(w.repo.memberships)
	candidate.runMembers = cloneMap(w.repo.runMembers)
	candidate.notifications = append([]Notification(nil), w.repo.notifications...)
	candidate.audits = append([]AuditEntry(nil), w.repo.audits...)
	candidate.ratings = append([]string(nil), w.repo.ratings...)
	err := fn(&candidate)
	if err != nil {
		w.rollbacks++
		return err
	}
	w.repo = candidate
	w.commits++
	return nil
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	target := make(map[K]V, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

type fakeRepository struct {
	team             Team
	run              Run
	memberships      map[int64]Membership
	runMembers       map[int64]RunMember
	creditDelta      int
	attendees        int
	participantCount int
	activeMemberIDs  []int64
	notifications    []Notification
	audits           []AuditEntry
	ratings          []string
	failJoinRun      error
	cancelled        bool
}

func (r *fakeRepository) LockTeam(context.Context, int64) (Team, error) {
	if r.team.ID == 0 {
		return Team{}, ErrNotFound
	}
	return r.team, nil
}

func (r *fakeRepository) LockRun(context.Context, int64) (Run, error) {
	if r.run.ID == 0 {
		return Run{}, ErrNotFound
	}
	return r.run, nil
}

func (r *fakeRepository) CurrentRun(context.Context, int64) (Run, error) {
	if r.run.ID == 0 {
		return Run{}, ErrNotFound
	}
	return r.run, nil
}

func (r *fakeRepository) ActiveMembership(_ context.Context, _ int64, userID int64) (Membership, error) {
	membership, ok := r.memberships[userID]
	if !ok || membership.Status != "active" {
		return Membership{}, ErrNotFound
	}
	return membership, nil
}

func (r *fakeRepository) UpdateMembershipChannels(_ context.Context, membershipID int64, channels string) error {
	for userID, membership := range r.memberships {
		if membership.ID == membershipID {
			membership.Channels = channels
			r.memberships[userID] = membership
		}
	}
	return nil
}

func (r *fakeRepository) ActiveMemberCount(context.Context, int64) (int, error) {
	count := 0
	for _, membership := range r.memberships {
		if membership.Status == "active" {
			count++
		}
	}
	return count, nil
}

func (r *fakeRepository) JoinTeam(_ context.Context, _ int64, userID int64, channels string) error {
	r.memberships[userID] = Membership{ID: userID, UserID: userID, Status: "active", Role: "member", Channels: channels}
	return nil
}

func (r *fakeRepository) JoinRun(_ context.Context, _ int64, userID int64) error {
	if r.failJoinRun != nil {
		return r.failJoinRun
	}
	r.runMembers[userID] = RunMember{ID: userID, Status: "joined"}
	return nil
}

func (r *fakeRepository) LeaveMembership(_ context.Context, membershipID int64) error {
	for userID, membership := range r.memberships {
		if membership.ID == membershipID {
			membership.Status = "left"
			r.memberships[userID] = membership
		}
	}
	return nil
}

func (r *fakeRepository) RunMembership(_ context.Context, _ int64, userID int64) (RunMember, error) {
	member, ok := r.runMembers[userID]
	if !ok {
		return RunMember{}, ErrNotFound
	}
	return member, nil
}

func (r *fakeRepository) LeaveRun(_ context.Context, memberID int64) error {
	member := r.runMembers[memberID]
	member.Status = "left"
	r.runMembers[memberID] = member
	return nil
}

func (r *fakeRepository) ExcuseRun(_ context.Context, _ int64, userID int64, at time.Time) error {
	member := r.runMembers[userID]
	member.Status = "excused"
	member.ExcusedAt = &at
	r.runMembers[userID] = member
	return nil
}

func (r *fakeRepository) CheckInRun(_ context.Context, memberID int64, at time.Time) error {
	member := r.runMembers[memberID]
	member.Status = "checked_in"
	member.CheckedAt = &at
	r.runMembers[memberID] = member
	return nil
}

func (r *fakeRepository) MarkCreditAwarded(_ context.Context, memberID int64) error {
	member := r.runMembers[memberID]
	member.Awarded = true
	r.runMembers[memberID] = member
	return nil
}

func (r *fakeRepository) RunAttendeeCount(context.Context, int64) (int, error) {
	return r.attendees, nil
}

func (r *fakeRepository) ApplyCredit(context.Context, int64, string, int64) (int, error) {
	return r.creditDelta, nil
}

func (*fakeRepository) CheckRateLimit(context.Context, string, string, int, int) error { return nil }

func (r *fakeRepository) TransferOwnership(_ context.Context, _ int64, oldOwnerID, newOwnerID int64) error {
	r.team.OwnerID = newOwnerID
	old := r.memberships[oldOwnerID]
	old.Role = "member"
	r.memberships[oldOwnerID] = old
	next := r.memberships[newOwnerID]
	next.Role = "owner"
	r.memberships[newOwnerID] = next
	return nil
}

func (r *fakeRepository) RemoveMember(_ context.Context, _ int64, userID int64) (bool, error) {
	membership, ok := r.memberships[userID]
	if !ok || membership.Status != "active" {
		return false, nil
	}
	membership.Status = "removed"
	r.memberships[userID] = membership
	return true, nil
}

func (r *fakeRepository) RemoveRunMember(_ context.Context, _ int64, userID int64) error {
	member := r.runMembers[userID]
	member.Status = "removed"
	r.runMembers[userID] = member
	return nil
}

func (r *fakeRepository) ActiveMemberIDs(context.Context, int64) ([]int64, error) {
	return append([]int64(nil), r.activeMemberIDs...), nil
}

func (r *fakeRepository) CancelTeam(context.Context, int64) error {
	r.cancelled = true
	r.team.Status = "cancelled"
	return nil
}

func (r *fakeRepository) CountRunParticipants(context.Context, int64, []int64) (int, error) {
	return r.participantCount, nil
}

func (r *fakeRepository) InsertRatings(_ context.Context, _ int64, _ int64, _ int64, tags []string) error {
	r.ratings = append(r.ratings, tags...)
	return nil
}

func (r *fakeRepository) Notify(_ context.Context, notification Notification) error {
	r.notifications = append(r.notifications, notification)
	return nil
}

func (r *fakeRepository) Audit(_ context.Context, entry AuditEntry) error {
	r.audits = append(r.audits, entry)
	return nil
}

func (*fakeRepository) TouchTeam(context.Context, int64) error { return nil }

func baseWork(now time.Time) *fakeWork {
	return &fakeWork{repo: fakeRepository{
		team: Team{
			ID: 1, OwnerID: 10, Game: "game", Mode: "ranked", Capacity: 3,
			Status: "active", PublicationStatus: "published",
		},
		run: Run{ID: 2, TeamID: 1, Starts: now.Add(time.Hour), Status: "scheduled"},
		memberships: map[int64]Membership{
			10: {ID: 10, UserID: 10, Role: "owner", Status: "active"},
		},
		runMembers:       map[int64]RunMember{10: {ID: 10, Status: "joined"}},
		creditDelta:      2,
		attendees:        2,
		participantCount: 2,
		activeMemberIDs:  []int64{10, 20},
	}}
}

func assertRule(t *testing.T, err error, code string) {
	t.Helper()
	var domainErr *RuleError
	if !errors.As(err, &domainErr) || domainErr.Code != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
}

func TestJoinCoordinatesMembershipAndRun(t *testing.T) {
	now := time.Date(2026, 7, 28, 4, 0, 0, 0, time.UTC)
	work := baseWork(now)
	service := NewService(work)
	service.now = func() time.Time { return now }

	result, err := service.Join(context.Background(), Actor{ID: 20, Nickname: "member"}, 1, []string{"email", "in_app"})
	if err != nil {
		t.Fatal(err)
	}
	if result.RunID != 2 || work.commits != 1 || work.repo.memberships[20].Status != "active" || work.repo.runMembers[20].Status != "joined" {
		t.Fatalf("result=%+v repo=%+v", result, work.repo)
	}
	if len(work.repo.notifications) != 2 {
		t.Fatalf("notifications=%+v", work.repo.notifications)
	}
}

func TestJoinRollsBackWhenRunMembershipFails(t *testing.T) {
	now := time.Now().UTC()
	work := baseWork(now)
	work.repo.failJoinRun = errors.New("run write failed")
	service := NewService(work)
	service.now = func() time.Time { return now }

	_, err := service.Join(context.Background(), Actor{ID: 20}, 1, []string{"email"})
	if err == nil || work.commits != 0 || work.rollbacks != 1 {
		t.Fatalf("error=%v commits=%d rollbacks=%d", err, work.commits, work.rollbacks)
	}
	if _, exists := work.repo.memberships[20]; exists {
		t.Fatal("membership escaped transaction rollback")
	}
}

func TestJoinRejectsFullTeam(t *testing.T) {
	now := time.Now().UTC()
	work := baseWork(now)
	work.repo.team.Capacity = 1
	service := NewService(work)
	service.now = func() time.Time { return now }

	_, err := service.Join(context.Background(), Actor{ID: 20}, 1, []string{"email"})
	assertRule(t, err, "TEAM_FULL")
}

func TestLateLeaveAppliesPenalty(t *testing.T) {
	now := time.Date(2026, 7, 28, 4, 0, 0, 0, time.UTC)
	work := baseWork(now)
	work.repo.run.Starts = now.Add(15 * time.Minute)
	work.repo.memberships[20] = Membership{ID: 20, UserID: 20, Status: "active"}
	work.repo.runMembers[20] = RunMember{ID: 20, Status: "joined"}
	work.repo.creditDelta = -20
	service := NewService(work)
	service.now = func() time.Time { return now }

	result, err := service.Leave(context.Background(), Actor{ID: 20, Nickname: "member"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.CreditDelta != -20 || work.repo.memberships[20].Status != "left" || work.repo.runMembers[20].Status != "left" {
		t.Fatalf("result=%+v membership=%+v run=%+v", result, work.repo.memberships[20], work.repo.runMembers[20])
	}
}

func TestOwnerCannotLeave(t *testing.T) {
	work := baseWork(time.Now().UTC())
	service := NewService(work)
	_, err := service.Leave(context.Background(), Actor{ID: 10}, 1)
	assertRule(t, err, "OWNER_CANNOT_LEAVE")
}

func TestExcuseIsIdempotentAndMustPrecedeDeparture(t *testing.T) {
	now := time.Now().UTC()
	work := baseWork(now)
	work.repo.memberships[20] = Membership{ID: 20, UserID: 20, Status: "active"}
	work.repo.runMembers[20] = RunMember{ID: 20, Status: "joined"}
	service := NewService(work)
	service.now = func() time.Time { return now }

	first, err := service.Excuse(context.Background(), Actor{ID: 20, Nickname: "member"}, 1, 2)
	if err != nil || first.ExcusedAt == nil {
		t.Fatalf("result=%+v error=%v", first, err)
	}
	second, err := service.Excuse(context.Background(), Actor{ID: 20}, 1, 2)
	if err != nil || second.ExcusedAt == nil || work.commits != 2 {
		t.Fatalf("result=%+v error=%v commits=%d", second, err, work.commits)
	}
}

func TestCheckInAwardsOnceWithinWindow(t *testing.T) {
	now := time.Now().UTC()
	work := baseWork(now)
	work.repo.run.Starts = now.Add(-10 * time.Minute)
	work.repo.runMembers[20] = RunMember{ID: 20, Status: "joined"}
	service := NewService(work)
	service.now = func() time.Time { return now }

	result, err := service.CheckIn(context.Background(), Actor{ID: 20}, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.CreditDelta != 2 || result.CheckedAt == nil || !work.repo.runMembers[20].Awarded {
		t.Fatalf("result=%+v member=%+v", result, work.repo.runMembers[20])
	}
}

func TestTransferRequiresActiveTarget(t *testing.T) {
	work := baseWork(time.Now().UTC())
	service := NewService(work)
	_, err := service.Transfer(context.Background(), Actor{ID: 10}, 1, 20, "request")
	assertRule(t, err, "TARGET_NOT_MEMBER")

	work.repo.memberships[20] = Membership{ID: 20, UserID: 20, Status: "active"}
	result, err := service.Transfer(context.Background(), Actor{ID: 10}, 1, 20, "request")
	if err != nil || result.TeamID != 1 || work.repo.team.OwnerID != 20 || len(work.repo.audits) != 1 {
		t.Fatalf("result=%+v error=%v repo=%+v", result, err, work.repo)
	}
}

func TestCancelRequiresOwnerOrModeratorAndNotifiesMembers(t *testing.T) {
	work := baseWork(time.Now().UTC())
	service := NewService(work)
	_, err := service.Cancel(context.Background(), Actor{ID: 20}, 1, "request")
	assertRule(t, err, "OWNER_REQUIRED")

	_, err = service.Cancel(context.Background(), Actor{ID: 30, Role: "moderator"}, 1, "request")
	if err != nil || !work.repo.cancelled || len(work.repo.notifications) != 2 {
		t.Fatalf("error=%v repo=%+v", err, work.repo)
	}
}

func TestRatingRequiresSameRun(t *testing.T) {
	now := time.Now().UTC()
	work := baseWork(now)
	work.repo.run.Starts = now.Add(-time.Hour)
	work.repo.participantCount = 1
	service := NewService(work)
	service.now = func() time.Time { return now }
	_, err := service.Rate(context.Background(), Actor{ID: 10}, 1, 2, 20, []string{"friendly"})
	assertRule(t, err, "SAME_RUN_REQUIRED")

	work.repo.participantCount = 2
	_, err = service.Rate(context.Background(), Actor{ID: 10}, 1, 2, 20, []string{"friendly"})
	if err != nil || len(work.repo.ratings) != 1 {
		t.Fatalf("error=%v ratings=%v", err, work.repo.ratings)
	}
}
