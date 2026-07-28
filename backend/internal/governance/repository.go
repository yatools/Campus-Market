package governance

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UnitOfWork interface {
	WithinTransaction(context.Context, func(Repository) error) error
}

type Repository interface {
	LockAccount(context.Context, int64) (Account, error)
	SaveAccount(context.Context, Account) error
	DeactivateAccount(context.Context, int64) error
	RevokeSessions(context.Context, int64) error
	AdjustCredit(context.Context, int64, int) (int, int, error)
	CreatePenalty(context.Context, int64, string, string, string, string) (int64, error)
	PenaltyOwner(context.Context, int64) (int64, error)
	UpsertAppeal(context.Context, int64, int64, string) (AppealResult, error)
	LockAppeal(context.Context, int64) (int64, string, error)
	ResolveAppeal(context.Context, int64, string, string) error
	LockModerationCase(context.Context, int64) (ModerationCase, error)
	LockEntity(context.Context, int64) (Entity, error)
	ResolveModerationCase(context.Context, int64, int64, string, string) error
	SetEntityModeration(context.Context, int64, string, string, bool) error
	RefundQuestionBounty(context.Context, int64, int64) error
	ObserveTitle(context.Context, int64) (string, bool, error)
	ActiveUser(context.Context, int64) (bool, error)
	SetObserveDecision(context.Context, int64, *int64, string) error
	ResolveReports(context.Context, int64) error
	Notify(context.Context, Notification) error
	Audit(context.Context, AuditEntry) error
}

type PostgresRepository struct {
	pool *pgxpool.Pool
}

func NewPostgresRepository(pool *pgxpool.Pool) *PostgresRepository {
	return &PostgresRepository{pool: pool}
}

func (r *PostgresRepository) WithinTransaction(ctx context.Context, fn func(Repository) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := fn(&postgresRepository{tx: tx}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type postgresRepository struct {
	tx pgx.Tx
}

func (r *postgresRepository) LockAccount(ctx context.Context, id int64) (Account, error) {
	var account Account
	err := r.tx.QueryRow(ctx, `SELECT id,email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,
		avatar_path,dm_stranger_off,hide_online,verified_at,created_at
		FROM users WHERE id=$1 FOR UPDATE`, id).Scan(
		&account.ID, &account.Email, &account.PasswordHash, &account.Nickname, &account.Alias,
		&account.CampusIdentity, &account.Role, &account.Status, &account.Credit, &account.XP,
		&account.AvatarPath, &account.DMStrangerOff, &account.HideOnline,
		&account.VerifiedAt, &account.CreatedAt,
	)
	return account, normalizeNotFound(err)
}

func (r *postgresRepository) SaveAccount(ctx context.Context, account Account) error {
	_, err := r.tx.Exec(ctx, `UPDATE users
		SET role=$1,campus_identity=$2,status=$3,credit=$4,updated_at=now()
		WHERE id=$5`,
		account.Role, account.CampusIdentity, account.Status, account.Credit, account.ID,
	)
	return err
}

func (r *postgresRepository) DeactivateAccount(ctx context.Context, id int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE users SET status='disabled',deactivated_at=now(),updated_at=now() WHERE id=$1", id)
	return err
}

func (r *postgresRepository) RevokeSessions(ctx context.Context, userID int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE sessions SET revoked_at=now() WHERE user_id=$1 AND revoked_at IS NULL", userID)
	return err
}

func (r *postgresRepository) AdjustCredit(ctx context.Context, userID int64, delta int) (int, int, error) {
	var before int
	if err := r.tx.QueryRow(ctx, "SELECT credit FROM users WHERE id=$1 FOR UPDATE", userID).Scan(&before); err != nil {
		return 0, 0, normalizeNotFound(err)
	}
	var after int
	err := r.tx.QueryRow(ctx, `UPDATE users
		SET credit=GREATEST(0,LEAST(1000,credit+$1)),updated_at=now()
		WHERE id=$2 RETURNING credit`, delta, userID).Scan(&after)
	return before, after, err
}

func (r *postgresRepository) CreatePenalty(ctx context.Context, userID int64, mask, violation, result, ruleText string) (int64, error) {
	var id int64
	err := r.tx.QueryRow(ctx, `INSERT INTO penalties(
		user_id,public_mask,violation_type,result,rule,created_at
	) VALUES($1,$2,$3,$4,$5,now()) RETURNING id`,
		userID, mask, violation, result, ruleText,
	).Scan(&id)
	return id, err
}

func (r *postgresRepository) PenaltyOwner(ctx context.Context, id int64) (int64, error) {
	var ownerID int64
	err := r.tx.QueryRow(ctx, "SELECT user_id FROM penalties WHERE id=$1", id).Scan(&ownerID)
	return ownerID, normalizeNotFound(err)
}

func (r *postgresRepository) UpsertAppeal(ctx context.Context, penaltyID, userID int64, reason string) (AppealResult, error) {
	var result AppealResult
	err := r.tx.QueryRow(ctx, `INSERT INTO appeals(
		penalty_id,user_id,reason,status,admin_note,created_at
	) VALUES($1,$2,$3,'pending','',now())
	ON CONFLICT(penalty_id,user_id) DO UPDATE SET penalty_id=EXCLUDED.penalty_id
	RETURNING id,status`, penaltyID, userID, reason).Scan(&result.ID, &result.Status)
	return result, err
}

func (r *postgresRepository) LockAppeal(ctx context.Context, id int64) (int64, string, error) {
	var userID int64
	var status string
	err := r.tx.QueryRow(ctx, "SELECT user_id,status FROM appeals WHERE id=$1 FOR UPDATE", id).Scan(&userID, &status)
	return userID, status, normalizeNotFound(err)
}

func (r *postgresRepository) ResolveAppeal(ctx context.Context, id int64, status, note string) error {
	_, err := r.tx.Exec(ctx, "UPDATE appeals SET status=$1,admin_note=$2 WHERE id=$3", status, note, id)
	return err
}

func (r *postgresRepository) LockModerationCase(ctx context.Context, id int64) (ModerationCase, error) {
	var item ModerationCase
	err := r.tx.QueryRow(ctx, `SELECT id,entity_id,status,decision
		FROM moderation_cases WHERE id=$1 FOR UPDATE`, id).Scan(
		&item.ID, &item.EntityID, &item.Status, &item.Decision,
	)
	return item, normalizeNotFound(err)
}

func (r *postgresRepository) LockEntity(ctx context.Context, id int64) (Entity, error) {
	var entity Entity
	err := r.tx.QueryRow(ctx, `SELECT id,type,owner_id,publication_status
		FROM content_entities WHERE id=$1 FOR UPDATE`, id).Scan(
		&entity.ID, &entity.Type, &entity.OwnerID, &entity.PublicationStatus,
	)
	return entity, normalizeNotFound(err)
}

func (r *postgresRepository) ResolveModerationCase(ctx context.Context, id, assigneeID int64, decision, note string) error {
	_, err := r.tx.Exec(ctx, `UPDATE moderation_cases
		SET status='resolved',assignee_id=$1,decision=$2,notes=$3,decided_at=now()
		WHERE id=$4`, assigneeID, decision, note, id)
	return err
}

func (r *postgresRepository) SetEntityModeration(ctx context.Context, id int64, publication, moderation string, updatePublication bool) error {
	if updatePublication {
		_, err := r.tx.Exec(ctx, `UPDATE content_entities
			SET publication_status=$1,moderation_status=$2,updated_at=now()
			WHERE id=$3`, publication, moderation, id)
		return err
	}
	_, err := r.tx.Exec(ctx, `UPDATE content_entities
		SET moderation_status=$1,updated_at=now() WHERE id=$2`, moderation, id)
	return err
}

func (r *postgresRepository) RefundQuestionBounty(ctx context.Context, entityID, ownerID int64) error {
	var bounty int
	err := r.tx.QueryRow(ctx, `UPDATE questions
		SET bounty_settled=true
		WHERE entity_id=$1 AND bounty_settled=false AND accepted_answer_id IS NULL
		RETURNING bounty_xp`, entityID).Scan(&bounty)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = r.tx.Exec(ctx, "UPDATE users SET xp=xp+$1 WHERE id=$2", bounty, ownerID)
	return err
}

func (r *postgresRepository) ObserveTitle(ctx context.Context, entityID int64) (string, bool, error) {
	var title string
	err := r.tx.QueryRow(ctx, "SELECT title FROM observe_posts WHERE entity_id=$1", entityID).Scan(&title)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}
	return title, err == nil, err
}

func (r *postgresRepository) ActiveUser(ctx context.Context, userID int64) (bool, error) {
	var active bool
	err := r.tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND status='active')", userID).Scan(&active)
	return active, err
}

func (r *postgresRepository) SetObserveDecision(ctx context.Context, entityID int64, respondent *int64, note string) error {
	if respondent != nil {
		if _, err := r.tx.Exec(ctx, "UPDATE observe_posts SET respondent_id=$1 WHERE entity_id=$2", *respondent, entityID); err != nil {
			return err
		}
	}
	_, err := r.tx.Exec(ctx, "UPDATE observe_posts SET admin_note=$1 WHERE entity_id=$2", note, entityID)
	return err
}

func (r *postgresRepository) ResolveReports(ctx context.Context, entityID int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE reports SET status='resolved' WHERE entity_id=$1 AND status='pending'", entityID)
	return err
}

func (r *postgresRepository) Notify(ctx context.Context, item Notification) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO notifications(
		user_id,type,title,body,link,created_at
	) VALUES($1,'system',$2,$3,$4,now())`, item.UserID, item.Title, item.Body, item.Link)
	return err
}

func (r *postgresRepository) Audit(ctx context.Context, item AuditEntry) error {
	encode := func(value any) string {
		if value == nil {
			return ""
		}
		data, _ := json.Marshal(value)
		return string(data)
	}
	_, err := r.tx.Exec(ctx, `INSERT INTO audit_logs(
		actor_id,action,target_type,target_id,reason,before_json,after_json,request_id,created_at
	) VALUES($1,$2,$3,$4,$5,$6,$7,$8,now())`,
		item.ActorID, item.Action, item.Target, strconv.FormatInt(item.TargetID, 10),
		item.Reason, encode(item.Before), encode(item.After), item.RequestID,
	)
	return err
}

func normalizeNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
