package team

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type UnitOfWork interface {
	WithinTransaction(context.Context, func(Repository) error) error
}

type Repository interface {
	LockTeam(context.Context, int64) (Team, error)
	LockRun(context.Context, int64) (Run, error)
	CurrentRun(context.Context, int64) (Run, error)
	ActiveMembership(context.Context, int64, int64) (Membership, error)
	UpdateMembershipChannels(context.Context, int64, string) error
	ActiveMemberCount(context.Context, int64) (int, error)
	JoinTeam(context.Context, int64, int64, string) error
	JoinRun(context.Context, int64, int64) error
	LeaveMembership(context.Context, int64) error
	RunMembership(context.Context, int64, int64) (RunMember, error)
	LeaveRun(context.Context, int64) error
	ExcuseRun(context.Context, int64, int64, time.Time) error
	CheckInRun(context.Context, int64, time.Time) error
	MarkCreditAwarded(context.Context, int64) error
	RunAttendeeCount(context.Context, int64) (int, error)
	ApplyCredit(context.Context, int64, string, int64) (int, error)
	CheckRateLimit(context.Context, string, string, int, int) error
	TransferOwnership(context.Context, int64, int64, int64) error
	RemoveMember(context.Context, int64, int64) (bool, error)
	RemoveRunMember(context.Context, int64, int64) error
	ActiveMemberIDs(context.Context, int64) ([]int64, error)
	CancelTeam(context.Context, int64) error
	CountRunParticipants(context.Context, int64, []int64) (int, error)
	InsertRatings(context.Context, int64, int64, int64, []string) error
	Notify(context.Context, Notification) error
	Audit(context.Context, AuditEntry) error
	TouchTeam(context.Context, int64) error
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

func (r *postgresRepository) LockTeam(ctx context.Context, id int64) (Team, error) {
	var team Team
	err := r.tx.QueryRow(ctx, `SELECT t.entity_id,t.owner_id,t.game,t.mode,t.capacity,t.status,e.publication_status
		FROM teams t JOIN content_entities e ON e.id=t.entity_id
		WHERE t.entity_id=$1 FOR UPDATE OF t,e`, id).Scan(
		&team.ID, &team.OwnerID, &team.Game, &team.Mode, &team.Capacity, &team.Status, &team.PublicationStatus,
	)
	return team, normalizeNotFound(err)
}

func (r *postgresRepository) LockRun(ctx context.Context, id int64) (Run, error) {
	var run Run
	err := r.tx.QueryRow(ctx, "SELECT id,team_id,starts_at,status FROM team_runs WHERE id=$1 FOR UPDATE", id).
		Scan(&run.ID, &run.TeamID, &run.Starts, &run.Status)
	return run, normalizeNotFound(err)
}

func (r *postgresRepository) CurrentRun(ctx context.Context, teamID int64) (Run, error) {
	var run Run
	err := r.tx.QueryRow(ctx, `SELECT id,team_id,starts_at,status FROM team_runs
		WHERE team_id=$1 AND status='scheduled' AND (expires_at IS NULL OR expires_at>now())
		ORDER BY starts_at LIMIT 1 FOR UPDATE`, teamID).Scan(&run.ID, &run.TeamID, &run.Starts, &run.Status)
	return run, normalizeNotFound(err)
}

func (r *postgresRepository) ActiveMembership(ctx context.Context, teamID, userID int64) (Membership, error) {
	var membership Membership
	err := r.tx.QueryRow(ctx, `SELECT id,user_id,role,status,reminder_channels FROM team_memberships
		WHERE team_id=$1 AND user_id=$2 AND status='active' FOR UPDATE`, teamID, userID).
		Scan(&membership.ID, &membership.UserID, &membership.Role, &membership.Status, &membership.Channels)
	return membership, normalizeNotFound(err)
}

func (r *postgresRepository) UpdateMembershipChannels(ctx context.Context, membershipID int64, channels string) error {
	_, err := r.tx.Exec(ctx, "UPDATE team_memberships SET reminder_channels=$1 WHERE id=$2", channels, membershipID)
	return err
}

func (r *postgresRepository) ActiveMemberCount(ctx context.Context, teamID int64) (int, error) {
	var count int
	err := r.tx.QueryRow(ctx, "SELECT count(*) FROM team_memberships WHERE team_id=$1 AND status='active'", teamID).Scan(&count)
	return count, err
}

func (r *postgresRepository) JoinTeam(ctx context.Context, teamID, userID int64, channels string) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO team_memberships(team_id,user_id,role,status,reminder_channels,joined_at)
		VALUES($1,$2,'member','active',$3,now())`, teamID, userID, channels)
	return err
}

func (r *postgresRepository) JoinRun(ctx context.Context, runID, userID int64) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO team_run_members(run_id,user_id,status,credit_awarded)
		VALUES($1,$2,'joined',false)
		ON CONFLICT(run_id,user_id) DO UPDATE SET status='joined',excused_at=NULL`, runID, userID)
	return err
}

func (r *postgresRepository) LeaveMembership(ctx context.Context, membershipID int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE team_memberships SET status='left',left_at=now() WHERE id=$1", membershipID)
	return err
}

func (r *postgresRepository) RunMembership(ctx context.Context, runID, userID int64) (RunMember, error) {
	var member RunMember
	err := r.tx.QueryRow(ctx, `SELECT id,status,checked_in_at,excused_at,credit_awarded
		FROM team_run_members WHERE run_id=$1 AND user_id=$2 FOR UPDATE`, runID, userID).
		Scan(&member.ID, &member.Status, &member.CheckedAt, &member.ExcusedAt, &member.Awarded)
	return member, normalizeNotFound(err)
}

func (r *postgresRepository) LeaveRun(ctx context.Context, memberID int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE team_run_members SET status='left' WHERE id=$1", memberID)
	return err
}

func (r *postgresRepository) ExcuseRun(ctx context.Context, runID, userID int64, at time.Time) error {
	_, err := r.tx.Exec(ctx, `UPDATE team_run_members SET excused_at=$1,status='excused'
		WHERE run_id=$2 AND user_id=$3`, at, runID, userID)
	return err
}

func (r *postgresRepository) CheckInRun(ctx context.Context, memberID int64, at time.Time) error {
	_, err := r.tx.Exec(ctx, "UPDATE team_run_members SET checked_in_at=$1,status='checked_in' WHERE id=$2", at, memberID)
	return err
}

func (r *postgresRepository) MarkCreditAwarded(ctx context.Context, memberID int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE team_run_members SET credit_awarded=true WHERE id=$1", memberID)
	return err
}

func (r *postgresRepository) RunAttendeeCount(ctx context.Context, runID int64) (int, error) {
	var count int
	err := r.tx.QueryRow(ctx, `SELECT count(*) FROM team_run_members
		WHERE run_id=$1 AND status IN ('joined','checked_in')`, runID).Scan(&count)
	return count, err
}

func (r *postgresRepository) ApplyCredit(ctx context.Context, userID int64, key string, targetID int64) (int, error) {
	fallback := map[string]int{"penalty.team_late_leave": -20, "reward.team_check_in": 2}[key]
	delta := fallback
	_ = r.tx.QueryRow(ctx, "SELECT value FROM credit_rules WHERE key=$1", key).Scan(&delta)
	var before, after int
	if err := r.tx.QueryRow(ctx, "SELECT credit FROM users WHERE id=$1 FOR UPDATE", userID).Scan(&before); err != nil {
		return 0, err
	}
	if err := r.tx.QueryRow(ctx, `UPDATE users SET credit=GREATEST(0,LEAST(1000,credit+$1)),updated_at=now()
		WHERE id=$2 RETURNING credit`, delta, userID).Scan(&after); err != nil {
		return 0, err
	}
	applied := after - before
	if applied != 0 {
		entry := AuditEntry{
			ActorID: userID, Action: "credit." + key, Target: "team_run", TargetID: targetID,
			After: map[string]any{"credit": after, "delta": applied, "rule": key},
		}
		if err := r.Audit(ctx, entry); err != nil {
			return 0, err
		}
	}
	return applied, nil
}

func (r *postgresRepository) CheckRateLimit(ctx context.Context, action, subject string, limit, minutes int) error {
	var count int
	err := r.tx.QueryRow(ctx, `INSERT INTO rate_limit_counters(action,subject,window_start,count,expires_at)
		VALUES($1,$2,to_timestamp(floor(extract(epoch FROM now())/($3*60))*($3*60)),1,now()+($3*interval '1 minute')*2)
		ON CONFLICT(action,subject,window_start) DO UPDATE
		SET count=rate_limit_counters.count+1,expires_at=EXCLUDED.expires_at RETURNING count`,
		action, subject, minutes).Scan(&count)
	if err != nil {
		return err
	}
	if count > limit {
		return rule("RATE_LIMITED")
	}
	return nil
}

func (r *postgresRepository) TransferOwnership(ctx context.Context, teamID, oldOwnerID, newOwnerID int64) error {
	if _, err := r.tx.Exec(ctx, "UPDATE teams SET owner_id=$1 WHERE entity_id=$2", newOwnerID, teamID); err != nil {
		return err
	}
	_, err := r.tx.Exec(ctx, `UPDATE team_memberships
		SET role=CASE WHEN user_id=$1 THEN 'owner' WHEN user_id=$2 THEN 'member' ELSE role END
		WHERE team_id=$3`, newOwnerID, oldOwnerID, teamID)
	return err
}

func (r *postgresRepository) RemoveMember(ctx context.Context, teamID, userID int64) (bool, error) {
	tag, err := r.tx.Exec(ctx, `UPDATE team_memberships SET status='removed',left_at=now()
		WHERE team_id=$1 AND user_id=$2 AND status='active'`, teamID, userID)
	return err == nil && tag.RowsAffected() > 0, err
}

func (r *postgresRepository) RemoveRunMember(ctx context.Context, runID, userID int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE team_run_members SET status='removed' WHERE run_id=$1 AND user_id=$2", runID, userID)
	return err
}

func (r *postgresRepository) ActiveMemberIDs(ctx context.Context, teamID int64) ([]int64, error) {
	rows, err := r.tx.Query(ctx, "SELECT user_id FROM team_memberships WHERE team_id=$1 AND status='active'", teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *postgresRepository) CancelTeam(ctx context.Context, teamID int64) error {
	statements := []string{
		"UPDATE teams SET status='cancelled' WHERE entity_id=$1",
		"UPDATE content_entities SET publication_status='hidden',updated_at=now() WHERE id=$1",
		"UPDATE team_runs SET status='cancelled' WHERE team_id=$1 AND status='scheduled'",
		`UPDATE team_run_members SET status='cancelled' FROM team_runs tr
			WHERE tr.id=team_run_members.run_id AND tr.team_id=$1
			AND team_run_members.status NOT IN ('left','removed')`,
		"UPDATE team_memberships SET status='cancelled',left_at=now() WHERE team_id=$1 AND status='active'",
	}
	for _, statement := range statements {
		if _, err := r.tx.Exec(ctx, statement, teamID); err != nil {
			return err
		}
	}
	return nil
}

func (r *postgresRepository) CountRunParticipants(ctx context.Context, runID int64, users []int64) (int, error) {
	var count int
	err := r.tx.QueryRow(ctx, `SELECT count(*) FROM team_run_members
		WHERE run_id=$1 AND user_id=ANY($2) AND status=ANY($3)`,
		runID, users, []string{"joined", "checked_in", "excused"}).Scan(&count)
	return count, err
}

func (r *postgresRepository) InsertRatings(ctx context.Context, runID, raterID, targetID int64, tags []string) error {
	for _, tag := range tags {
		if _, err := r.tx.Exec(ctx, `INSERT INTO team_ratings(run_id,rater_id,target_id,tag,created_at)
			VALUES($1,$2,$3,$4,now()) ON CONFLICT(run_id,rater_id,target_id,tag) DO NOTHING`,
			runID, raterID, targetID, tag); err != nil {
			return err
		}
	}
	return nil
}

func (r *postgresRepository) Notify(ctx context.Context, notification Notification) error {
	_, err := r.tx.Exec(ctx, `INSERT INTO notifications(user_id,type,title,body,link,created_at)
		VALUES($1,'team',$2,$3,$4,now())`, notification.UserID, notification.Title, notification.Body, notification.Link)
	return err
}

func (r *postgresRepository) Audit(ctx context.Context, entry AuditEntry) error {
	after, _ := json.Marshal(entry.After)
	_, err := r.tx.Exec(ctx, `INSERT INTO audit_logs(
		actor_id,action,target_type,target_id,reason,before_json,after_json,request_id,created_at
	) VALUES($1,$2,$3,$4,'','{}',$5,$6,now())`,
		entry.ActorID, entry.Action, entry.Target, strconv.FormatInt(entry.TargetID, 10), after, entry.RequestID)
	return err
}

func (r *postgresRepository) TouchTeam(ctx context.Context, teamID int64) error {
	_, err := r.tx.Exec(ctx, "UPDATE content_entities SET updated_at=now() WHERE id=$1", teamID)
	return err
}

func normalizeNotFound(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func JoinChannels(channels []string) string {
	return strings.Join(channels, ",")
}
