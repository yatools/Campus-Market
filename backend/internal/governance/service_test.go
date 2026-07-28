package governance

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
	candidate := w.repo.clone()
	if err := fn(&candidate); err != nil {
		w.rollbacks++
		return err
	}
	w.repo = candidate
	w.commits++
	return nil
}

type appealState struct {
	ID     int64
	UserID int64
	Status string
	Note   string
}

type fakeRepository struct {
	accounts          map[int64]Account
	sessionsRevoked   map[int64]bool
	penaltyOwners     map[int64]int64
	appeals           map[int64]appealState
	appealByPenalty   map[int64]int64
	cases             map[int64]ModerationCase
	entities          map[int64]Entity
	observeTitles     map[int64]string
	activeUsers       map[int64]bool
	notifications     []Notification
	audits            []AuditEntry
	entityModeration  map[int64]string
	entityPublication map[int64]string
	reportsResolved   map[int64]bool
	bountyRefunded    map[int64]bool
	failAudit         error
	failPenalty       error
	nextPenaltyID     int64
	nextAppealID      int64
}

func (r fakeRepository) clone() fakeRepository {
	r.accounts = cloneMap(r.accounts)
	r.sessionsRevoked = cloneMap(r.sessionsRevoked)
	r.penaltyOwners = cloneMap(r.penaltyOwners)
	r.appeals = cloneMap(r.appeals)
	r.appealByPenalty = cloneMap(r.appealByPenalty)
	r.cases = cloneMap(r.cases)
	r.entities = cloneMap(r.entities)
	r.observeTitles = cloneMap(r.observeTitles)
	r.activeUsers = cloneMap(r.activeUsers)
	r.notifications = append([]Notification(nil), r.notifications...)
	r.audits = append([]AuditEntry(nil), r.audits...)
	r.entityModeration = cloneMap(r.entityModeration)
	r.entityPublication = cloneMap(r.entityPublication)
	r.reportsResolved = cloneMap(r.reportsResolved)
	r.bountyRefunded = cloneMap(r.bountyRefunded)
	return r
}

func cloneMap[K comparable, V any](source map[K]V) map[K]V {
	target := make(map[K]V, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func (r *fakeRepository) LockAccount(_ context.Context, id int64) (Account, error) {
	item, ok := r.accounts[id]
	if !ok {
		return Account{}, ErrNotFound
	}
	return item, nil
}

func (r *fakeRepository) SaveAccount(_ context.Context, account Account) error {
	r.accounts[account.ID] = account
	return nil
}

func (r *fakeRepository) DeactivateAccount(_ context.Context, id int64) error {
	item, ok := r.accounts[id]
	if !ok {
		return ErrNotFound
	}
	item.Status = "disabled"
	r.accounts[id] = item
	return nil
}

func (r *fakeRepository) RevokeSessions(_ context.Context, id int64) error {
	r.sessionsRevoked[id] = true
	return nil
}

func (r *fakeRepository) AdjustCredit(_ context.Context, id int64, delta int) (int, int, error) {
	item, ok := r.accounts[id]
	if !ok {
		return 0, 0, ErrNotFound
	}
	before := item.Credit
	item.Credit += delta
	if item.Credit < 0 {
		item.Credit = 0
	}
	if item.Credit > 1000 {
		item.Credit = 1000
	}
	r.accounts[id] = item
	return before, item.Credit, nil
}

func (r *fakeRepository) CreatePenalty(_ context.Context, userID int64, _, _, _, _ string) (int64, error) {
	if r.failPenalty != nil {
		return 0, r.failPenalty
	}
	r.nextPenaltyID++
	r.penaltyOwners[r.nextPenaltyID] = userID
	return r.nextPenaltyID, nil
}

func (r *fakeRepository) PenaltyOwner(_ context.Context, id int64) (int64, error) {
	owner, ok := r.penaltyOwners[id]
	if !ok {
		return 0, ErrNotFound
	}
	return owner, nil
}

func (r *fakeRepository) UpsertAppeal(_ context.Context, penaltyID, userID int64, _ string) (AppealResult, error) {
	if id, ok := r.appealByPenalty[penaltyID]; ok {
		return AppealResult{ID: id, Status: r.appeals[id].Status}, nil
	}
	r.nextAppealID++
	item := appealState{ID: r.nextAppealID, UserID: userID, Status: "pending"}
	r.appeals[item.ID] = item
	r.appealByPenalty[penaltyID] = item.ID
	return AppealResult{ID: item.ID, Status: item.Status}, nil
}

func (r *fakeRepository) LockAppeal(_ context.Context, id int64) (int64, string, error) {
	item, ok := r.appeals[id]
	if !ok {
		return 0, "", ErrNotFound
	}
	return item.UserID, item.Status, nil
}

func (r *fakeRepository) ResolveAppeal(_ context.Context, id int64, status, note string) error {
	item := r.appeals[id]
	item.Status = status
	item.Note = note
	r.appeals[id] = item
	return nil
}

func (r *fakeRepository) LockModerationCase(_ context.Context, id int64) (ModerationCase, error) {
	item, ok := r.cases[id]
	if !ok {
		return ModerationCase{}, ErrNotFound
	}
	return item, nil
}

func (r *fakeRepository) LockEntity(_ context.Context, id int64) (Entity, error) {
	item, ok := r.entities[id]
	if !ok {
		return Entity{}, ErrNotFound
	}
	return item, nil
}

func (r *fakeRepository) ResolveModerationCase(_ context.Context, id, _ int64, decision, _ string) error {
	item := r.cases[id]
	item.Status = "resolved"
	item.Decision = decision
	r.cases[id] = item
	return nil
}

func (r *fakeRepository) SetEntityModeration(_ context.Context, id int64, publication, moderation string, updatePublication bool) error {
	r.entityModeration[id] = moderation
	if updatePublication {
		r.entityPublication[id] = publication
	}
	return nil
}

func (r *fakeRepository) RefundQuestionBounty(_ context.Context, entityID, _ int64) error {
	r.bountyRefunded[entityID] = true
	return nil
}

func (r *fakeRepository) ObserveTitle(_ context.Context, entityID int64) (string, bool, error) {
	title, ok := r.observeTitles[entityID]
	return title, ok, nil
}

func (r *fakeRepository) ActiveUser(_ context.Context, id int64) (bool, error) {
	return r.activeUsers[id], nil
}

func (r *fakeRepository) SetObserveDecision(context.Context, int64, *int64, string) error {
	return nil
}

func (r *fakeRepository) ResolveReports(_ context.Context, entityID int64) error {
	r.reportsResolved[entityID] = true
	return nil
}

func (r *fakeRepository) Notify(_ context.Context, item Notification) error {
	r.notifications = append(r.notifications, item)
	return nil
}

func (r *fakeRepository) Audit(_ context.Context, item AuditEntry) error {
	if r.failAudit != nil {
		return r.failAudit
	}
	r.audits = append(r.audits, item)
	return nil
}

func baseWork() *fakeWork {
	now := time.Now().UTC()
	return &fakeWork{repo: fakeRepository{
		accounts: map[int64]Account{
			1: {ID: 1, Role: "admin", Status: "active", CampusIdentity: "staff", Credit: 900, VerifiedAt: now, CreatedAt: now},
			2: {ID: 2, Role: "user", Status: "active", CampusIdentity: "student", Credit: 800, VerifiedAt: now, CreatedAt: now},
		},
		sessionsRevoked:   map[int64]bool{},
		penaltyOwners:     map[int64]int64{7: 2},
		appeals:           map[int64]appealState{},
		appealByPenalty:   map[int64]int64{},
		cases:             map[int64]ModerationCase{5: {ID: 5, EntityID: 9, Status: "pending"}},
		entities:          map[int64]Entity{9: {ID: 9, Type: "post", OwnerID: 2, PublicationStatus: "published"}},
		observeTitles:     map[int64]string{},
		activeUsers:       map[int64]bool{},
		entityModeration:  map[int64]string{},
		entityPublication: map[int64]string{},
		reportsResolved:   map[int64]bool{},
		bountyRefunded:    map[int64]bool{},
		nextPenaltyID:     10,
		nextAppealID:      20,
	}}
}

func assertRule(t *testing.T, err error, code string) {
	t.Helper()
	var item *RuleError
	if !errors.As(err, &item) || item.Code != code {
		t.Fatalf("error=%v want rule %s", err, code)
	}
}

func TestUpdateAccountMovesRulesAndSessionRevocationIntoService(t *testing.T) {
	work := baseWork()
	service := NewService(work, "secret")
	role := "moderator"
	status := "restricted"
	result, err := service.UpdateAccount(
		context.Background(), Actor{ID: 1, Role: "admin"}, 2,
		AccountPatch{Role: &role, Status: &status, Reason: "policy"}, "request",
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Role != role || result.Status != status || !work.repo.sessionsRevoked[2] || work.commits != 1 {
		t.Fatalf("result=%+v revoked=%v commits=%d", result, work.repo.sessionsRevoked, work.commits)
	}
}

func TestUpdateAccountRejectsSelfLockoutAndRollsBack(t *testing.T) {
	work := baseWork()
	service := NewService(work, "secret")
	status := "disabled"
	_, err := service.UpdateAccount(
		context.Background(), Actor{ID: 1, Role: "admin"}, 1,
		AccountPatch{Status: &status, Reason: "invalid"}, "request",
	)
	assertRule(t, err, "SELF_LOCKOUT")
	if work.repo.accounts[1].Status != "active" || work.rollbacks != 1 {
		t.Fatalf("account=%+v rollbacks=%d", work.repo.accounts[1], work.rollbacks)
	}
}

func TestDeactivateAccountRollsBackAllWritesWhenAuditFails(t *testing.T) {
	work := baseWork()
	work.repo.failAudit = errors.New("audit unavailable")
	service := NewService(work, "secret")
	err := service.DeactivateAccount(context.Background(), Actor{ID: 2}, "request")
	if err == nil || work.repo.accounts[2].Status != "active" || work.repo.sessionsRevoked[2] {
		t.Fatalf("error=%v account=%+v revoked=%v", err, work.repo.accounts[2], work.repo.sessionsRevoked)
	}
}

func TestCreatePenaltyChangesCreditAtomically(t *testing.T) {
	work := baseWork()
	service := NewService(work, "secret")
	result, err := service.CreatePenalty(context.Background(), Actor{ID: 1, Role: "moderator"}, PenaltyCommand{
		UserID: 2, Violation: "spam", Result: "warning", Rule: "rule", Delta: -50,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != 11 || result.Credit != 750 || work.repo.accounts[2].Credit != 750 {
		t.Fatalf("result=%+v account=%+v", result, work.repo.accounts[2])
	}
}

func TestCreatePenaltyRollsBackCreditOnInsertFailure(t *testing.T) {
	work := baseWork()
	work.repo.failPenalty = errors.New("insert failed")
	service := NewService(work, "secret")
	_, err := service.CreatePenalty(context.Background(), Actor{ID: 1, Role: "moderator"}, PenaltyCommand{
		UserID: 2, Violation: "spam", Result: "warning", Rule: "rule", Delta: -50,
	})
	if err == nil || work.repo.accounts[2].Credit != 800 || work.rollbacks != 1 {
		t.Fatalf("error=%v credit=%d rollbacks=%d", err, work.repo.accounts[2].Credit, work.rollbacks)
	}
}

func TestAppealPenaltyRequiresOwnershipAndIsIdempotent(t *testing.T) {
	work := baseWork()
	service := NewService(work, "secret")
	_, err := service.AppealPenalty(context.Background(), Actor{ID: 1}, 7, "reason enough", "request")
	assertRule(t, err, "PENALTY_OWNER_REQUIRED")
	first, err := service.AppealPenalty(context.Background(), Actor{ID: 2}, 7, "reason enough", "request")
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.AppealPenalty(context.Background(), Actor{ID: 2}, 7, "reason enough", "request")
	if err != nil || first != second {
		t.Fatalf("first=%+v second=%+v error=%v", first, second, err)
	}
}

func TestDecideAppealIsIdempotent(t *testing.T) {
	work := baseWork()
	work.repo.appeals[21] = appealState{ID: 21, UserID: 2, Status: "pending"}
	service := NewService(work, "secret")
	first, err := service.DecideAppeal(context.Background(), Actor{ID: 1, Role: "moderator"}, 21, "approved", "ok", "request")
	if err != nil || first.Status != "approved" {
		t.Fatalf("result=%+v error=%v", first, err)
	}
	second, err := service.DecideAppeal(context.Background(), Actor{ID: 1, Role: "moderator"}, 21, "rejected", "late", "request")
	if err != nil || second.Status != "approved" || len(work.repo.notifications) != 1 {
		t.Fatalf("result=%+v error=%v notifications=%d", second, err, len(work.repo.notifications))
	}
}

func TestModerationDecisionPreservesDeletedPublicationStatus(t *testing.T) {
	work := baseWork()
	entity := work.repo.entities[9]
	entity.PublicationStatus = "deleted"
	work.repo.entities[9] = entity
	service := NewService(work, "secret")
	result, err := service.DecideModeration(context.Background(), Actor{ID: 1, Role: "moderator"}, 5, ModerationCommand{
		Decision: "approve", Note: "reviewed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "resolved" {
		t.Fatalf("result=%+v", result)
	}
	if _, changed := work.repo.entityPublication[9]; changed {
		t.Fatal("deleted content was republished")
	}
	if work.repo.entityModeration[9] != "approved" || !work.repo.reportsResolved[9] {
		t.Fatalf("moderation=%v reports=%v", work.repo.entityModeration, work.repo.reportsResolved)
	}
}

func TestModerationRejectsInactiveRespondentAndRollsBack(t *testing.T) {
	work := baseWork()
	work.repo.observeTitles[9] = "observe"
	service := NewService(work, "secret")
	respondent := int64(99)
	_, err := service.DecideModeration(context.Background(), Actor{ID: 1, Role: "moderator"}, 5, ModerationCommand{
		Decision: "approve", Respondent: &respondent,
	})
	assertRule(t, err, "RESPONDENT_NOT_FOUND")
	if work.repo.cases[5].Status != "pending" || work.rollbacks != 1 {
		t.Fatalf("case=%+v rollbacks=%d", work.repo.cases[5], work.rollbacks)
	}
}
