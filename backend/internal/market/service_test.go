package market

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

func (w *fakeWork) WithinTransaction(ctx context.Context, fn func(TransactionRepository) error) error {
	candidate := w.repo
	err := fn(&candidate)
	if err != nil {
		w.rollbacks++
		return err
	}
	w.repo = candidate
	w.commits++
	return nil
}

type fakeRepository struct {
	listing              Listing
	transaction          Transaction
	dispute              Dispute
	nextTransactionID    int64
	nextDisputeID        int64
	nextReviewID         int64
	reviewCount          int
	createTransactionErr error
	failListingStatus    error
	notifications        []Notification
	audits               []AuditEntry
	rejectedOthers       bool
}

func (r *fakeRepository) LockListing(context.Context, int64) (Listing, error) {
	if r.listing.ID == 0 {
		return Listing{}, ErrNotFound
	}
	return r.listing, nil
}

func (r *fakeRepository) TransactionListingID(context.Context, int64) (int64, error) {
	if r.transaction.ID == 0 {
		return 0, ErrNotFound
	}
	return r.transaction.ListingID, nil
}

func (r *fakeRepository) LockTransaction(context.Context, int64) (Transaction, error) {
	if r.transaction.ID == 0 {
		return Transaction{}, ErrNotFound
	}
	return r.transaction, nil
}

func (r *fakeRepository) LockDispute(context.Context, int64) (Dispute, error) {
	if r.dispute.ID == 0 {
		return Dispute{}, ErrNotFound
	}
	return r.dispute, nil
}

func (r *fakeRepository) CancelListing(context.Context, int64) error {
	r.listing.TradeStatus = "cancelled"
	return nil
}

func (r *fakeRepository) CreateTransaction(_ context.Context, listingID, sellerID, buyerID int64, _ string) (int64, error) {
	if r.createTransactionErr != nil {
		return 0, r.createTransactionErr
	}
	r.transaction = Transaction{
		ID: r.nextTransactionID, ListingID: listingID, SellerID: sellerID, BuyerID: buyerID, Status: "requested",
	}
	return r.transaction.ID, nil
}

func (r *fakeRepository) RejectOtherRequests(context.Context, int64, int64) error {
	r.rejectedOthers = true
	return nil
}

func (r *fakeRepository) SetTransactionReserved(_ context.Context, _ int64, until time.Time) error {
	r.transaction.Status = "reserved"
	r.transaction.ReservedUntil = &until
	return nil
}

func (r *fakeRepository) EndRequestedTransaction(_ context.Context, _ int64, _ int64, status string) error {
	r.transaction.Status = status
	return nil
}

func (r *fakeRepository) CancelTransaction(context.Context, int64, int64, string) error {
	r.transaction.Status = "cancelled"
	return nil
}

func (r *fakeRepository) ExpireTransaction(context.Context, int64) error {
	r.transaction.Status = "expired"
	return nil
}

func (r *fakeRepository) ConfirmBuyer(_ context.Context, _ int64, at time.Time) error {
	r.transaction.BuyerConfirmedAt = &at
	return nil
}

func (r *fakeRepository) ConfirmSeller(_ context.Context, _ int64, at time.Time) error {
	r.transaction.SellerConfirmedAt = &at
	return nil
}

func (r *fakeRepository) CompleteTransaction(context.Context, int64) error {
	r.transaction.Status = "completed"
	return nil
}

func (r *fakeRepository) SetListingTradeStatus(_ context.Context, _ int64, status string) error {
	if r.failListingStatus != nil {
		return r.failListingStatus
	}
	r.listing.TradeStatus = status
	return nil
}

func (r *fakeRepository) CreateDispute(_ context.Context, transactionID, actorID int64, _ string) (int64, error) {
	r.dispute = Dispute{ID: r.nextDisputeID, TransactionID: transactionID, Status: "pending"}
	_ = actorID
	return r.dispute.ID, nil
}

func (*fakeRepository) AttachEvidence(context.Context, int64, int64, []int64) error { return nil }

func (r *fakeRepository) SetTransactionDisputed(context.Context, int64) error {
	r.transaction.Status = "disputed"
	return nil
}

func (r *fakeRepository) CreateReview(context.Context, int64, int64, int64, int, string, time.Time) (int64, error) {
	r.reviewCount++
	return r.nextReviewID, nil
}

func (r *fakeRepository) ReviewCount(context.Context, int64) (int, error) {
	return r.reviewCount, nil
}

func (*fakeRepository) RevealReviews(context.Context, int64) error { return nil }

func (r *fakeRepository) ResolveDispute(_ context.Context, _ int64, _ int64, decision, _ string) error {
	r.dispute.Status = "resolved"
	_ = decision
	return nil
}

func (r *fakeRepository) CancelTransactionByDecision(context.Context, int64, string) error {
	r.transaction.Status = "cancelled"
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

func baseFakeWork() *fakeWork {
	return &fakeWork{repo: fakeRepository{
		listing: Listing{
			ID: 1, OwnerID: 10, Title: "bike", TradeStatus: "available",
			PublicationStatus: "published", ModerationStatus: "approved",
		},
		transaction:       Transaction{ID: 2, ListingID: 1, SellerID: 10, BuyerID: 20, Status: "requested"},
		dispute:           Dispute{ID: 3, TransactionID: 2, Status: "pending"},
		nextTransactionID: 2,
		nextDisputeID:     3,
		nextReviewID:      4,
	}}
}

func assertRule(t *testing.T, err error, code string) {
	t.Helper()
	var ruleErr *RuleError
	if !errors.As(err, &ruleErr) || ruleErr.Code != code {
		t.Fatalf("error=%v, want rule %s", err, code)
	}
}

func TestAcceptTransactionCommitsCoordinatedState(t *testing.T) {
	work := baseFakeWork()
	service := NewService(work, time.Hour, time.Hour)
	fixed := time.Date(2026, 7, 28, 1, 2, 3, 0, time.UTC)
	service.now = func() time.Time { return fixed }

	result, err := service.AcceptTransaction(context.Background(), Actor{ID: 10}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "reserved" || work.commits != 1 || work.rollbacks != 0 {
		t.Fatalf("result=%+v commits=%d rollbacks=%d", result, work.commits, work.rollbacks)
	}
	if work.repo.transaction.Status != "reserved" || work.repo.listing.TradeStatus != "reserved" || !work.repo.rejectedOthers {
		t.Fatalf("transactional state not coordinated: %+v %+v", work.repo.transaction, work.repo.listing)
	}
	if work.repo.transaction.ReservedUntil == nil || !work.repo.transaction.ReservedUntil.Equal(fixed.Add(time.Hour)) {
		t.Fatalf("reservation deadline=%v", work.repo.transaction.ReservedUntil)
	}
	if len(work.repo.notifications) != 1 || work.repo.notifications[0].UserID != 20 {
		t.Fatalf("notification=%+v", work.repo.notifications)
	}
}

func TestAcceptTransactionRollsBackWhenListingUpdateFails(t *testing.T) {
	work := baseFakeWork()
	work.repo.failListingStatus = errors.New("database write failed")
	service := NewService(work, time.Hour, time.Hour)

	_, err := service.AcceptTransaction(context.Background(), Actor{ID: 10}, 2)
	if err == nil {
		t.Fatal("expected write failure")
	}
	if work.commits != 0 || work.rollbacks != 1 {
		t.Fatalf("commits=%d rollbacks=%d", work.commits, work.rollbacks)
	}
	if work.repo.transaction.Status != "requested" || work.repo.listing.TradeStatus != "available" {
		t.Fatalf("partial state escaped rollback: %+v %+v", work.repo.transaction, work.repo.listing)
	}
}

func TestRequestTransactionMapsConcurrentUniqueConflict(t *testing.T) {
	work := baseFakeWork()
	work.repo.createTransactionErr = ErrConflict
	service := NewService(work, time.Hour, time.Hour)

	_, err := service.RequestTransaction(context.Background(), Actor{ID: 20}, 1, "please reserve")
	assertRule(t, err, "ACTIVE_REQUEST_EXISTS")
	if work.commits != 0 || work.rollbacks != 1 {
		t.Fatalf("commits=%d rollbacks=%d", work.commits, work.rollbacks)
	}
}

func TestRequestedTransactionCanOnlyBeCancelledByBuyer(t *testing.T) {
	work := baseFakeWork()
	service := NewService(work, time.Hour, time.Hour)

	_, err := service.CancelTransaction(context.Background(), Actor{ID: 10}, 2, "changed mind")
	assertRule(t, err, "NOT_BUYER")
	if work.repo.transaction.Status != "requested" || work.commits != 0 {
		t.Fatalf("unauthorized cancellation changed state: %+v", work.repo.transaction)
	}
}

func TestTwoPartyConfirmationCompletesTransaction(t *testing.T) {
	work := baseFakeWork()
	work.repo.transaction.Status = "reserved"
	work.repo.listing.TradeStatus = "reserved"
	service := NewService(work, time.Hour, time.Hour)
	fixed := time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return fixed }

	first, err := service.ConfirmTransaction(context.Background(), Actor{ID: 20}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != "reserved" || work.repo.transaction.BuyerConfirmedAt == nil {
		t.Fatalf("first confirmation=%+v transaction=%+v", first, work.repo.transaction)
	}
	second, err := service.ConfirmTransaction(context.Background(), Actor{ID: 10}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != "completed" || work.repo.transaction.Status != "completed" || work.repo.listing.TradeStatus != "completed" {
		t.Fatalf("second confirmation=%+v transaction=%+v listing=%+v", second, work.repo.transaction, work.repo.listing)
	}
}

func TestExpiredUnconfirmedReservationCommitsReleaseThenReturnsConflict(t *testing.T) {
	work := baseFakeWork()
	work.repo.transaction.Status = "reserved"
	work.repo.listing.TradeStatus = "reserved"
	expiredAt := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	work.repo.transaction.ReservedUntil = &expiredAt
	service := NewService(work, time.Hour, time.Hour)
	service.now = func() time.Time { return expiredAt.Add(time.Hour) }

	_, err := service.ConfirmTransaction(context.Background(), Actor{ID: 20}, 2)
	assertRule(t, err, "RESERVATION_EXPIRED")
	if work.commits != 1 || work.repo.transaction.Status != "expired" || work.repo.listing.TradeStatus != "available" {
		t.Fatalf("expiration was not committed: commits=%d transaction=%+v listing=%+v", work.commits, work.repo.transaction, work.repo.listing)
	}
}

func TestModeratorCannotDecideOwnDispute(t *testing.T) {
	work := baseFakeWork()
	work.repo.transaction.Status = "disputed"
	service := NewService(work, time.Hour, time.Hour)

	_, err := service.DecideDispute(context.Background(), Actor{ID: 10, Role: "moderator"}, 3, "completed", "note", "request")
	assertRule(t, err, "DISPUTE_SELF_DEALING")
	if work.commits != 0 || work.repo.dispute.Status != "pending" {
		t.Fatalf("self-dealing decision changed state: %+v", work.repo.dispute)
	}
}

func TestCancelListingEnforcesOwnershipAndState(t *testing.T) {
	t.Run("owner can cancel available listing", func(t *testing.T) {
		work := baseFakeWork()
		service := NewService(work, time.Hour, time.Hour)
		result, err := service.CancelListing(context.Background(), Actor{ID: 10}, 1, "sold elsewhere", "request")
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != "cancelled" || work.repo.listing.TradeStatus != "cancelled" || len(work.repo.audits) != 1 {
			t.Fatalf("result=%+v listing=%+v audits=%+v", result, work.repo.listing, work.repo.audits)
		}
	})
	t.Run("non-owner is rejected", func(t *testing.T) {
		work := baseFakeWork()
		service := NewService(work, time.Hour, time.Hour)
		_, err := service.CancelListing(context.Background(), Actor{ID: 20}, 1, "", "request")
		assertRule(t, err, "NOT_SELLER")
		if work.commits != 0 {
			t.Fatal("unauthorized listing cancellation committed")
		}
	})
	t.Run("reserved listing requires transaction resolution", func(t *testing.T) {
		work := baseFakeWork()
		work.repo.listing.TradeStatus = "reserved"
		service := NewService(work, time.Hour, time.Hour)
		_, err := service.CancelListing(context.Background(), Actor{ID: 10}, 1, "", "request")
		assertRule(t, err, "ACTIVE_TRANSACTION")
	})
}

func TestEndRequestEnforcesActorRole(t *testing.T) {
	work := baseFakeWork()
	service := NewService(work, time.Hour, time.Hour)
	result, err := service.EndRequest(context.Background(), Actor{ID: 10}, 2, "rejected")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "rejected" || work.repo.transaction.Status != "rejected" {
		t.Fatalf("result=%+v transaction=%+v", result, work.repo.transaction)
	}

	work = baseFakeWork()
	service = NewService(work, time.Hour, time.Hour)
	_, err = service.EndRequest(context.Background(), Actor{ID: 20}, 2, "rejected")
	assertRule(t, err, "NOT_SELLER")
}

func TestConfirmedReservationRequiresDisputeBeforeCancellation(t *testing.T) {
	work := baseFakeWork()
	work.repo.transaction.Status = "reserved"
	confirmedAt := time.Now().UTC()
	work.repo.transaction.BuyerConfirmedAt = &confirmedAt
	service := NewService(work, time.Hour, time.Hour)

	_, err := service.CancelTransaction(context.Background(), Actor{ID: 20}, 2, "problem")
	assertRule(t, err, "DISPUTE_REQUIRED")
	if work.repo.transaction.Status != "reserved" || work.commits != 0 {
		t.Fatalf("confirmed reservation was cancelled: %+v", work.repo.transaction)
	}
}

func TestOpenDisputeMovesTransactionAndAudits(t *testing.T) {
	work := baseFakeWork()
	work.repo.transaction.Status = "reserved"
	service := NewService(work, time.Hour, time.Hour)

	result, err := service.OpenDispute(context.Background(), Actor{ID: 20}, 2, "item differs", []int64{7}, "request")
	if err != nil {
		t.Fatal(err)
	}
	if result.DisputeID != 3 || work.repo.transaction.Status != "disputed" || len(work.repo.audits) != 1 {
		t.Fatalf("result=%+v transaction=%+v audits=%+v", result, work.repo.transaction, work.repo.audits)
	}
}

func TestReviewRequiresCompletedParticipant(t *testing.T) {
	work := baseFakeWork()
	service := NewService(work, time.Hour, time.Hour)
	_, err := service.CreateReview(context.Background(), Actor{ID: 20}, 2, 5, "great")
	assertRule(t, err, "TRANSACTION_NOT_COMPLETED")

	work = baseFakeWork()
	work.repo.transaction.Status = "completed"
	service = NewService(work, time.Hour, time.Hour)
	result, err := service.CreateReview(context.Background(), Actor{ID: 20}, 2, 5, "great")
	if err != nil {
		t.Fatal(err)
	}
	if result.ReviewID != 4 || result.VisibleAt.IsZero() || work.repo.reviewCount != 1 {
		t.Fatalf("result=%+v review count=%d", result, work.repo.reviewCount)
	}
}

func TestDecideDisputeCoordinatesFinalStateAndNotifications(t *testing.T) {
	work := baseFakeWork()
	work.repo.transaction.Status = "disputed"
	service := NewService(work, time.Hour, time.Hour)

	result, err := service.DecideDispute(context.Background(), Actor{ID: 30, Role: "moderator"}, 3, "completed", "evidence reviewed", "request")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "resolved" || work.repo.dispute.Status != "resolved" ||
		work.repo.transaction.Status != "completed" || work.repo.listing.TradeStatus != "completed" {
		t.Fatalf("result=%+v dispute=%+v transaction=%+v listing=%+v", result, work.repo.dispute, work.repo.transaction, work.repo.listing)
	}
	if len(work.repo.notifications) != 2 || len(work.repo.audits) != 1 {
		t.Fatalf("notifications=%+v audits=%+v", work.repo.notifications, work.repo.audits)
	}
}
