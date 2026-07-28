package market

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yatools/wutong-campus-wall/backend/internal/marketpolicy"
)

type Service struct {
	work           UnitOfWork
	reservationTTL time.Duration
	reviewBlindTTL time.Duration
	now            func() time.Time
}

func NewService(work UnitOfWork, reservationTTL, reviewBlindTTL time.Duration) *Service {
	if reservationTTL <= 0 {
		reservationTTL = 24 * time.Hour
	}
	if reviewBlindTTL <= 0 {
		reviewBlindTTL = 14 * 24 * time.Hour
	}
	return &Service{
		work:           work,
		reservationTTL: reservationTTL,
		reviewBlindTTL: reviewBlindTTL,
		now:            func() time.Time { return time.Now().UTC() },
	}
}

func (s *Service) CancelListing(ctx context.Context, actor Actor, listingID int64, reason, requestID string) (Result, error) {
	result := Result{ListingID: listingID, Status: "cancelled"}
	err := s.work.WithinTransaction(ctx, func(repo TransactionRepository) error {
		listing, err := repo.LockListing(ctx, listingID)
		if err != nil {
			return mapNotFound(err, "LISTING_NOT_FOUND")
		}
		if listing.OwnerID != actor.ID {
			return rule("NOT_SELLER")
		}
		if listing.TradeStatus == "reserved" {
			return rule("ACTIVE_TRANSACTION")
		}
		if !marketpolicy.ListingCancellable(listing.TradeStatus) {
			return rule("INVALID_LISTING_TRANSITION")
		}
		if err := repo.CancelListing(ctx, listingID); err != nil {
			return err
		}
		return repo.Audit(ctx, AuditEntry{
			ActorID: actor.ID, Action: "listing.cancel", Target: "listing", TargetID: listingID,
			Reason: reason, Before: map[string]any{"trade_status": listing.TradeStatus},
			After: map[string]any{"trade_status": "cancelled"}, RequestID: requestID,
		})
	})
	return result, err
}

func (s *Service) RequestTransaction(ctx context.Context, buyer Actor, listingID int64, message string) (Result, error) {
	result := Result{ListingID: listingID, Status: "requested"}
	err := s.work.WithinTransaction(ctx, func(repo TransactionRepository) error {
		listing, err := repo.LockListing(ctx, listingID)
		if err != nil {
			return mapNotFound(err, "LISTING_NOT_FOUND")
		}
		if listing.OwnerID == buyer.ID {
			return rule("SELF_PURCHASE_NOT_ALLOWED")
		}
		if !marketpolicy.ListingRequestable(listing.TradeStatus, listing.PublicationStatus, listing.ModerationStatus) {
			return rule("LISTING_NOT_AVAILABLE")
		}
		transactionID, err := repo.CreateTransaction(ctx, listingID, listing.OwnerID, buyer.ID, message)
		if errors.Is(err, ErrConflict) {
			return rule("ACTIVE_REQUEST_EXISTS")
		}
		if err != nil {
			return err
		}
		result.TransactionID = transactionID
		return repo.Notify(ctx, Notification{
			UserID: listing.OwnerID,
			Title:  "收到商品预约申请",
			Body:   listing.Title,
			Link:   fmt.Sprintf("/listings/%d", listingID),
		})
	})
	return result, err
}

func (s *Service) AcceptTransaction(ctx context.Context, seller Actor, transactionID int64) (Result, error) {
	result := Result{TransactionID: transactionID, Status: "reserved"}
	err := s.work.WithinTransaction(ctx, func(repo TransactionRepository) error {
		listingID, err := repo.TransactionListingID(ctx, transactionID)
		if err != nil {
			return mapNotFound(err, "TRANSACTION_NOT_FOUND")
		}
		listing, err := repo.LockListing(ctx, listingID)
		if err != nil {
			return mapNotFound(err, "LISTING_NOT_FOUND")
		}
		transaction, err := repo.LockTransaction(ctx, transactionID)
		if err != nil {
			return mapNotFound(err, "TRANSACTION_NOT_FOUND")
		}
		if transaction.SellerID != seller.ID {
			return rule("NOT_SELLER")
		}
		if !marketpolicy.RequestEndable(transaction.Status) {
			return rule("INVALID_TRANSACTION_TRANSITION")
		}
		if !marketpolicy.ListingRequestable(listing.TradeStatus, listing.PublicationStatus, listing.ModerationStatus) {
			return rule("LISTING_NOT_AVAILABLE")
		}
		if err := repo.RejectOtherRequests(ctx, transaction.ListingID, transactionID); err != nil {
			return err
		}
		if err := repo.SetTransactionReserved(ctx, transactionID, s.now().Add(s.reservationTTL)); err != nil {
			return err
		}
		if err := repo.SetListingTradeStatus(ctx, transaction.ListingID, "reserved"); err != nil {
			return err
		}
		result.ListingID = transaction.ListingID
		return repo.Notify(ctx, Notification{
			UserID: transaction.BuyerID,
			Title:  "商品预约已接受",
			Body:   listing.Title,
			Link:   fmt.Sprintf("/market-transactions/%d", transactionID),
		})
	})
	return result, err
}

func (s *Service) EndRequest(ctx context.Context, actor Actor, transactionID int64, status string) (Result, error) {
	result := Result{TransactionID: transactionID, Status: status}
	err := s.work.WithinTransaction(ctx, func(repo TransactionRepository) error {
		transaction, err := repo.LockTransaction(ctx, transactionID)
		if err != nil {
			return mapNotFound(err, "TRANSACTION_NOT_FOUND")
		}
		if !marketpolicy.RequestEndable(transaction.Status) {
			return rule("INVALID_TRANSACTION_TRANSITION")
		}
		switch status {
		case "rejected":
			if actor.ID != transaction.SellerID {
				return rule("NOT_SELLER")
			}
		case "cancelled":
			if actor.ID != transaction.BuyerID {
				return rule("NOT_BUYER")
			}
		default:
			return rule("INVALID_TRANSACTION_TRANSITION")
		}
		result.ListingID = transaction.ListingID
		return repo.EndRequestedTransaction(ctx, transactionID, actor.ID, status)
	})
	return result, err
}

func (s *Service) CancelTransaction(ctx context.Context, actor Actor, transactionID int64, reason string) (Result, error) {
	result := Result{TransactionID: transactionID, Status: "cancelled"}
	err := s.work.WithinTransaction(ctx, func(repo TransactionRepository) error {
		transaction, err := repo.LockTransaction(ctx, transactionID)
		if err != nil {
			return mapNotFound(err, "TRANSACTION_NOT_FOUND")
		}
		if !transaction.Includes(actor.ID) {
			return rule("TRANSACTION_PARTICIPANT_REQUIRED")
		}
		switch marketpolicy.Cancellation(transaction.Status, transaction.BuyerConfirmedAt != nil, transaction.SellerConfirmedAt != nil) {
		case marketpolicy.CancelAllowed:
			if transaction.Status == "requested" && actor.ID != transaction.BuyerID {
				return rule("NOT_BUYER")
			}
		case marketpolicy.CancelNeedsDispute:
			return rule("DISPUTE_REQUIRED")
		default:
			return rule("INVALID_TRANSACTION_TRANSITION")
		}
		if err := repo.CancelTransaction(ctx, transactionID, actor.ID, reason); err != nil {
			return err
		}
		if transaction.Status == "reserved" {
			if err := repo.SetListingTradeStatus(ctx, transaction.ListingID, "available"); err != nil {
				return err
			}
		}
		result.ListingID = transaction.ListingID
		return nil
	})
	return result, err
}

func (s *Service) ConfirmTransaction(ctx context.Context, actor Actor, transactionID int64) (Result, error) {
	result := Result{TransactionID: transactionID}
	expired := false
	err := s.work.WithinTransaction(ctx, func(repo TransactionRepository) error {
		transaction, err := repo.LockTransaction(ctx, transactionID)
		if err != nil {
			return mapNotFound(err, "TRANSACTION_NOT_FOUND")
		}
		if !transaction.Includes(actor.ID) {
			return rule("TRANSACTION_PARTICIPANT_REQUIRED")
		}
		result.ListingID = transaction.ListingID
		result.Status = transaction.Status
		if transaction.Status == "completed" {
			return nil
		}
		if !marketpolicy.Confirmable(transaction.Status) {
			return rule("INVALID_TRANSACTION_TRANSITION")
		}
		now := s.now()
		if transaction.ReservedUntil != nil &&
			transaction.ReservedUntil.Before(now) &&
			transaction.BuyerConfirmedAt == nil &&
			transaction.SellerConfirmedAt == nil {
			if err := repo.ExpireTransaction(ctx, transactionID); err != nil {
				return err
			}
			if err := repo.SetListingTradeStatus(ctx, transaction.ListingID, "available"); err != nil {
				return err
			}
			result.Status = "expired"
			expired = true
			return nil
		}
		if actor.ID == transaction.BuyerID && transaction.BuyerConfirmedAt == nil {
			transaction.BuyerConfirmedAt = &now
			if err := repo.ConfirmBuyer(ctx, transactionID, now); err != nil {
				return err
			}
		}
		if actor.ID == transaction.SellerID && transaction.SellerConfirmedAt == nil {
			transaction.SellerConfirmedAt = &now
			if err := repo.ConfirmSeller(ctx, transactionID, now); err != nil {
				return err
			}
		}
		if transaction.BuyerConfirmedAt != nil && transaction.SellerConfirmedAt != nil {
			if err := repo.CompleteTransaction(ctx, transactionID); err != nil {
				return err
			}
			if err := repo.SetListingTradeStatus(ctx, transaction.ListingID, "completed"); err != nil {
				return err
			}
			result.Status = "completed"
		}
		return nil
	})
	if err == nil && expired {
		err = rule("RESERVATION_EXPIRED")
	}
	return result, err
}

func (s *Service) OpenDispute(ctx context.Context, actor Actor, transactionID int64, reason string, attachmentIDs []int64, requestID string) (Result, error) {
	result := Result{TransactionID: transactionID, Status: "pending"}
	err := s.work.WithinTransaction(ctx, func(repo TransactionRepository) error {
		transaction, err := repo.LockTransaction(ctx, transactionID)
		if err != nil {
			return mapNotFound(err, "TRANSACTION_NOT_FOUND")
		}
		if !transaction.Includes(actor.ID) {
			return rule("TRANSACTION_PARTICIPANT_REQUIRED")
		}
		if !marketpolicy.Disputable(transaction.Status) {
			return rule("INVALID_TRANSACTION_TRANSITION")
		}
		disputeID, err := repo.CreateDispute(ctx, transactionID, actor.ID, reason)
		if errors.Is(err, ErrConflict) {
			return rule("DISPUTE_EXISTS")
		}
		if err != nil {
			return err
		}
		if err := repo.AttachEvidence(ctx, actor.ID, disputeID, attachmentIDs); err != nil {
			return err
		}
		if err := repo.SetTransactionDisputed(ctx, transactionID); err != nil {
			return err
		}
		result.ListingID = transaction.ListingID
		result.DisputeID = disputeID
		return repo.Audit(ctx, AuditEntry{
			ActorID: actor.ID, Action: "market.dispute.open", Target: "market_transaction",
			TargetID: transactionID, Reason: reason,
			After: map[string]any{"status": "disputed", "dispute_id": disputeID}, RequestID: requestID,
		})
	})
	return result, err
}

func (s *Service) CreateReview(ctx context.Context, actor Actor, transactionID int64, rating int, body string) (Result, error) {
	result := Result{TransactionID: transactionID}
	err := s.work.WithinTransaction(ctx, func(repo TransactionRepository) error {
		transaction, err := repo.LockTransaction(ctx, transactionID)
		if err != nil {
			return mapNotFound(err, "TRANSACTION_NOT_FOUND")
		}
		if !marketpolicy.Reviewable(transaction.Status) {
			return rule("TRANSACTION_NOT_COMPLETED")
		}
		revieweeID := transaction.BuyerID
		switch actor.ID {
		case transaction.BuyerID:
			revieweeID = transaction.SellerID
		case transaction.SellerID:
		default:
			return rule("TRANSACTION_PARTICIPANT_REQUIRED")
		}
		visibleAt := s.now().Add(s.reviewBlindTTL)
		reviewID, err := repo.CreateReview(ctx, transactionID, actor.ID, revieweeID, rating, body, visibleAt)
		if errors.Is(err, ErrConflict) {
			return rule("REVIEW_EXISTS")
		}
		if err != nil {
			return err
		}
		count, err := repo.ReviewCount(ctx, transactionID)
		if err != nil {
			return err
		}
		if count == 2 {
			if err := repo.RevealReviews(ctx, transactionID); err != nil {
				return err
			}
			visibleAt = s.now()
		}
		result.ListingID = transaction.ListingID
		result.ReviewID = reviewID
		result.VisibleAt = visibleAt
		return nil
	})
	return result, err
}

func (s *Service) DecideDispute(ctx context.Context, moderator Actor, disputeID int64, decision, note, requestID string) (Result, error) {
	result := Result{DisputeID: disputeID, Status: "resolved", Decision: decision}
	err := s.work.WithinTransaction(ctx, func(repo TransactionRepository) error {
		dispute, err := repo.LockDispute(ctx, disputeID)
		if err != nil {
			return mapNotFound(err, "DISPUTE_NOT_FOUND")
		}
		if dispute.Status == "resolved" {
			return rule("DISPUTE_RESOLVED")
		}
		transaction, err := repo.LockTransaction(ctx, dispute.TransactionID)
		if err != nil {
			return mapNotFound(err, "TRANSACTION_NOT_FOUND")
		}
		if transaction.Includes(moderator.ID) {
			return rule("DISPUTE_SELF_DEALING")
		}
		if !marketpolicy.DisputeDecidable(dispute.Status, transaction.Status) {
			return rule("INVALID_TRANSACTION_TRANSITION")
		}
		if err := repo.ResolveDispute(ctx, disputeID, moderator.ID, decision, note); err != nil {
			return err
		}
		switch decision {
		case "completed":
			if err := repo.CompleteTransaction(ctx, transaction.ID); err != nil {
				return err
			}
			if err := repo.SetListingTradeStatus(ctx, transaction.ListingID, "completed"); err != nil {
				return err
			}
		case "cancelled":
			if err := repo.CancelTransactionByDecision(ctx, transaction.ID, note); err != nil {
				return err
			}
			if err := repo.SetListingTradeStatus(ctx, transaction.ListingID, "available"); err != nil {
				return err
			}
		default:
			return rule("INVALID_DISPUTE_DECISION")
		}
		result.TransactionID = transaction.ID
		result.ListingID = transaction.ListingID
		if err := repo.Audit(ctx, AuditEntry{
			ActorID: moderator.ID, Action: "market.dispute.decide", Target: "market_dispute",
			TargetID: disputeID, Reason: note, Before: map[string]any{"status": "pending"},
			After: map[string]any{"status": "resolved", "decision": decision}, RequestID: requestID,
		}); err != nil {
			return err
		}
		notification := Notification{
			Title: "交易纠纷已裁决", Body: note,
			Link: fmt.Sprintf("/market-transactions/%d", transaction.ID),
		}
		notification.UserID = transaction.BuyerID
		if err := repo.Notify(ctx, notification); err != nil {
			return err
		}
		notification.UserID = transaction.SellerID
		return repo.Notify(ctx, notification)
	})
	return result, err
}

func mapNotFound(err error, code string) error {
	if errors.Is(err, ErrNotFound) {
		return rule(code)
	}
	return err
}
