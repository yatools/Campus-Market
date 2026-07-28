package market

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UnitOfWork interface {
	WithinTransaction(context.Context, func(TransactionRepository) error) error
}

type TransactionRepository interface {
	LockListing(context.Context, int64) (Listing, error)
	TransactionListingID(context.Context, int64) (int64, error)
	LockTransaction(context.Context, int64) (Transaction, error)
	LockDispute(context.Context, int64) (Dispute, error)
	CancelListing(context.Context, int64) error
	CreateTransaction(context.Context, int64, int64, int64, string) (int64, error)
	RejectOtherRequests(context.Context, int64, int64) error
	SetTransactionReserved(context.Context, int64, time.Time) error
	EndRequestedTransaction(context.Context, int64, int64, string) error
	CancelTransaction(context.Context, int64, int64, string) error
	ExpireTransaction(context.Context, int64) error
	ConfirmBuyer(context.Context, int64, time.Time) error
	ConfirmSeller(context.Context, int64, time.Time) error
	CompleteTransaction(context.Context, int64) error
	SetListingTradeStatus(context.Context, int64, string) error
	CreateDispute(context.Context, int64, int64, string) (int64, error)
	AttachEvidence(context.Context, int64, int64, []int64) error
	SetTransactionDisputed(context.Context, int64) error
	CreateReview(context.Context, int64, int64, int64, int, string, time.Time) (int64, error)
	ReviewCount(context.Context, int64) (int, error)
	RevealReviews(context.Context, int64) error
	ResolveDispute(context.Context, int64, int64, string, string) error
	CancelTransactionByDecision(context.Context, int64, string) error
	Notify(context.Context, Notification) error
	Audit(context.Context, AuditEntry) error
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) WithinTransaction(ctx context.Context, fn func(TransactionRepository) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(&postgresTx{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type postgresTx struct {
	tx pgx.Tx
}

func (r *postgresTx) LockListing(ctx context.Context, id int64) (Listing, error) {
	var listing Listing
	err := r.tx.QueryRow(ctx, `SELECT e.id,e.owner_id,l.title,l.trade_status,e.publication_status,e.moderation_status
		FROM listings l JOIN content_entities e ON e.id=l.entity_id
		WHERE e.id=$1 FOR UPDATE OF l,e`, id).Scan(
		&listing.ID,
		&listing.OwnerID,
		&listing.Title,
		&listing.TradeStatus,
		&listing.PublicationStatus,
		&listing.ModerationStatus,
	)
	return listing, normalizeNotFound(err)
}

func (r *postgresTx) TransactionListingID(ctx context.Context, id int64) (int64, error) {
	var listingID int64
	err := r.tx.QueryRow(ctx, "SELECT listing_id FROM market_transactions WHERE id=$1", id).Scan(&listingID)
	return listingID, normalizeNotFound(err)
}

func (r *postgresTx) LockTransaction(ctx context.Context, id int64) (Transaction, error) {
	var transaction Transaction
	err := r.tx.QueryRow(ctx, `SELECT id,listing_id,seller_id,buyer_id,status,reserved_until,buyer_confirmed_at,seller_confirmed_at
		FROM market_transactions WHERE id=$1 FOR UPDATE`, id).Scan(
		&transaction.ID,
		&transaction.ListingID,
		&transaction.SellerID,
		&transaction.BuyerID,
		&transaction.Status,
		&transaction.ReservedUntil,
		&transaction.BuyerConfirmedAt,
		&transaction.SellerConfirmedAt,
	)
	return transaction, normalizeNotFound(err)
}

func (r *postgresTx) LockDispute(ctx context.Context, id int64) (Dispute, error) {
	var dispute Dispute
	err := r.tx.QueryRow(ctx, "SELECT id,transaction_id,status FROM market_disputes WHERE id=$1 FOR UPDATE", id).
		Scan(&dispute.ID, &dispute.TransactionID, &dispute.Status)
	return dispute, normalizeNotFound(err)
}

func (r *postgresTx) CancelListing(ctx context.Context, id int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE listings SET trade_status='cancelled' WHERE entity_id=$1", id)
	return err
}

func (r *postgresTx) CreateTransaction(ctx context.Context, listingID, sellerID, buyerID int64, message string) (int64, error) {
	var id int64
	err := r.tx.QueryRow(ctx, `INSERT INTO market_transactions(listing_id,seller_id,buyer_id,status,message,cancel_reason,created_at,updated_at)
		VALUES($1,$2,$3,'requested',$4,'',now(),now()) RETURNING id`, listingID, sellerID, buyerID, message).Scan(&id)
	return id, normalizeConflict(err)
}

func (r *postgresTx) RejectOtherRequests(ctx context.Context, listingID, acceptedID int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE market_transactions SET status='rejected',updated_at=now() WHERE listing_id=$1 AND status='requested' AND id<>$2", listingID, acceptedID)
	return err
}

func (r *postgresTx) SetTransactionReserved(ctx context.Context, id int64, until time.Time) error {
	_, err := r.tx.Exec(ctx, "UPDATE market_transactions SET status='reserved',reserved_until=$1,updated_at=now() WHERE id=$2", until, id)
	return err
}

func (r *postgresTx) EndRequestedTransaction(ctx context.Context, id, actorID int64, status string) error {
	_, err := r.tx.Exec(ctx, `UPDATE market_transactions
		SET status=$1,
			cancelled_at=CASE WHEN $1='cancelled' THEN now() ELSE NULL END,
			cancelled_by=CASE WHEN $1='cancelled' THEN $2 ELSE NULL END,
			updated_at=now()
		WHERE id=$3`, status, actorID, id)
	return err
}

func (r *postgresTx) CancelTransaction(ctx context.Context, id, actorID int64, reason string) error {
	_, err := r.tx.Exec(ctx, `UPDATE market_transactions
		SET status='cancelled',cancelled_at=now(),cancelled_by=$1,cancel_reason=$2,updated_at=now()
		WHERE id=$3`, actorID, reason, id)
	return err
}

func (r *postgresTx) ExpireTransaction(ctx context.Context, id int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE market_transactions SET status='expired',updated_at=now() WHERE id=$1", id)
	return err
}

func (r *postgresTx) ConfirmBuyer(ctx context.Context, id int64, at time.Time) error {
	_, err := r.tx.Exec(ctx, "UPDATE market_transactions SET buyer_confirmed_at=$1,updated_at=now() WHERE id=$2", at, id)
	return err
}

func (r *postgresTx) ConfirmSeller(ctx context.Context, id int64, at time.Time) error {
	_, err := r.tx.Exec(ctx, "UPDATE market_transactions SET seller_confirmed_at=$1,updated_at=now() WHERE id=$2", at, id)
	return err
}

func (r *postgresTx) CompleteTransaction(ctx context.Context, id int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE market_transactions SET status='completed',completed_at=now(),updated_at=now() WHERE id=$1", id)
	return err
}

func (r *postgresTx) SetListingTradeStatus(ctx context.Context, id int64, status string) error {
	_, err := r.tx.Exec(ctx, "UPDATE listings SET trade_status=$1 WHERE entity_id=$2", status, id)
	return err
}

func (r *postgresTx) CreateDispute(ctx context.Context, transactionID, actorID int64, reason string) (int64, error) {
	var id int64
	err := r.tx.QueryRow(ctx, `INSERT INTO market_disputes(transaction_id,opened_by,reason,status,decision,admin_note,created_at)
		VALUES($1,$2,$3,'pending','','',now()) RETURNING id`, transactionID, actorID, reason).Scan(&id)
	return id, normalizeConflict(err)
}

func (r *postgresTx) AttachEvidence(ctx context.Context, userID, disputeID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	unique := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		unique[id] = struct{}{}
	}
	rows, err := r.tx.Query(ctx, `SELECT id FROM attachments
		WHERE id=ANY($1) AND owner_id=$2 AND status='pending' AND access_scope='market_dispute'
		FOR UPDATE`, ids, userID)
	if err != nil {
		return err
	}
	found := make(map[int64]struct{}, len(unique))
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		found[id] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if len(found) != len(unique) {
		return rule("INVALID_EVIDENCE")
	}
	for id := range unique {
		if _, err := r.tx.Exec(ctx, "UPDATE attachments SET status='attached' WHERE id=$1", id); err != nil {
			return err
		}
		if _, err := r.tx.Exec(ctx, "INSERT INTO market_dispute_evidence(dispute_id,attachment_id) VALUES($1,$2)", disputeID, id); err != nil {
			return err
		}
	}
	return nil
}

func (r *postgresTx) SetTransactionDisputed(ctx context.Context, id int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE market_transactions SET status='disputed',updated_at=now() WHERE id=$1", id)
	return err
}

func (r *postgresTx) CreateReview(ctx context.Context, transactionID, reviewerID, revieweeID int64, rating int, body string, visibleAt time.Time) (int64, error) {
	var id int64
	err := r.tx.QueryRow(ctx, `INSERT INTO market_reviews(transaction_id,reviewer_id,reviewee_id,rating,body,visible_at,created_at)
		VALUES($1,$2,$3,$4,$5,$6,now()) RETURNING id`, transactionID, reviewerID, revieweeID, rating, body, visibleAt).Scan(&id)
	return id, normalizeConflict(err)
}

func (r *postgresTx) ReviewCount(ctx context.Context, transactionID int64) (int, error) {
	var count int
	err := r.tx.QueryRow(ctx, "SELECT count(*) FROM market_reviews WHERE transaction_id=$1", transactionID).Scan(&count)
	return count, err
}

func (r *postgresTx) RevealReviews(ctx context.Context, transactionID int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE market_reviews SET visible_at=now() WHERE transaction_id=$1", transactionID)
	return err
}

func (r *postgresTx) ResolveDispute(ctx context.Context, id, moderatorID int64, decision, note string) error {
	_, err := r.tx.Exec(ctx, `UPDATE market_disputes
		SET status='resolved',decision=$1,admin_note=$2,decided_by=$3,decided_at=now()
		WHERE id=$4`, decision, note, moderatorID, id)
	return err
}

func (r *postgresTx) CancelTransactionByDecision(ctx context.Context, id int64, reason string) error {
	_, err := r.tx.Exec(ctx, `UPDATE market_transactions
		SET status='cancelled',cancelled_at=now(),cancel_reason=$1,updated_at=now()
		WHERE id=$2`, reason, id)
	return err
}

func (r *postgresTx) Notify(ctx context.Context, notification Notification) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO notifications(user_id,type,title,body,link,created_at)
		VALUES($1,'market',$2,$3,$4,now())`, notification.UserID, notification.Title, notification.Body, notification.Link)
	return err
}

func (r *postgresTx) Audit(ctx context.Context, entry AuditEntry) error {
	encode := func(value map[string]any) []byte {
		if value == nil {
			return []byte("{}")
		}
		data, _ := json.Marshal(value)
		return data
	}
	_, err := r.tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,action,target_type,target_id,reason,before_json,after_json,request_id,created_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,now())`,
		entry.ActorID,
		entry.Action,
		entry.Target,
		strconv.FormatInt(entry.TargetID, 10),
		entry.Reason,
		encode(entry.Before),
		encode(entry.After),
		entry.RequestID,
	)
	return err
}

func normalizeNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func normalizeConflict(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return ErrConflict
	}
	return err
}
