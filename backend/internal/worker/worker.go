package worker

import (
	"archive/zip"
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	domain "github.com/yatools/wutong-campus-wall/backend/internal/app"
	"github.com/yatools/wutong-campus-wall/backend/internal/config"
	"github.com/yatools/wutong-campus-wall/backend/internal/metrics"
	"github.com/yatools/wutong-campus-wall/backend/internal/security"
	storagepkg "github.com/yatools/wutong-campus-wall/backend/internal/storage"
)

const (
	emailLockID       int64 = 846208411
	teamLockID        int64 = 846208412
	maintenanceLockID int64 = 846208413
	backupLockID      int64 = 846208414
)

const emailLease = 5 * time.Minute

func Run(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) error {
	instanceID := workerInstanceID()
	jobs := workerJobs(cfg)
	var wg sync.WaitGroup
	for _, job := range jobs {
		job := job
		wg.Add(1)
		go func() {
			defer wg.Done()
			runLoop(ctx, cfg.WorkerPoll, pool, instanceID, job)
		}()
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}

type workerJob struct {
	name string
	lock int64
	run  func(context.Context, *pgxpool.Conn, string) error
}

func workerJobs(cfg config.Config) []workerJob {
	return []workerJob{
		{"email", emailLockID, func(ctx context.Context, conn *pgxpool.Conn, workerID string) error {
			return processEmail(ctx, cfg, conn, workerID)
		}},
		{"team", teamLockID, func(ctx context.Context, conn *pgxpool.Conn, _ string) error { return processTeamRunsTx(ctx, conn) }},
		{"maintenance", maintenanceLockID, func(ctx context.Context, conn *pgxpool.Conn, _ string) error { return cleanupTx(ctx, cfg, conn) }},
		{"backup", backupLockID, func(ctx context.Context, conn *pgxpool.Conn, _ string) error { return processBackup(ctx, cfg, conn) }},
	}
}

func Cycle(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) error {
	instanceID := workerInstanceID()
	for _, job := range workerJobs(cfg) {
		if err := runJob(ctx, pool, instanceID, job); err != nil {
			return err
		}
	}
	return nil
}

func workerInstanceID() string {
	instanceID, _ := os.Hostname()
	if instanceID == "" {
		return "worker"
	}
	return instanceID
}

func runLoop(ctx context.Context, interval time.Duration, pool *pgxpool.Pool, instanceID string, job workerJob) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := runJob(ctx, pool, instanceID, job); err != nil && ctx.Err() == nil {
			slog.Error("worker_job_failed", "job", job.name, "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runJob(ctx context.Context, pool *pgxpool.Pool, instanceID string, job workerJob) (jobErr error) {
	started := time.Now()
	defer func() {
		metrics.Default.Observe(fmt.Sprintf("worker_job_duration_seconds{job=\"%s\"}", job.name), time.Since(started))
		if jobErr != nil {
			metrics.Default.Inc(fmt.Sprintf("worker_job_failures_total{job=\"%s\"}", job.name))
		} else {
			metrics.Default.Inc(fmt.Sprintf("worker_job_success_total{job=\"%s\"}", job.name))
			metrics.Default.Set(fmt.Sprintf("worker_last_success_timestamp_seconds{job=\"%s\"}", job.name), float64(time.Now().Unix()))
		}
	}()
	_, _ = pool.Exec(ctx, `INSERT INTO worker_heartbeats(worker_name,instance_id,last_seen_at,last_error) VALUES($1,$2,now(),'') ON CONFLICT(worker_name,instance_id) DO UPDATE SET last_seen_at=now()`, job.name, instanceID)
	defer func() {
		message := ""
		if jobErr != nil {
			message = truncate(jobErr.Error(), 2000)
		}
		_, _ = pool.Exec(context.Background(), `INSERT INTO worker_heartbeats(worker_name,instance_id,last_seen_at,last_success_at,last_error) VALUES($1,$2,now(),CASE WHEN $3='' THEN now() ELSE NULL END,$3) ON CONFLICT(worker_name,instance_id) DO UPDATE SET last_seen_at=now(),last_success_at=CASE WHEN $3='' THEN now() ELSE worker_heartbeats.last_success_at END,last_error=$3`, job.name, instanceID, message)
	}()
	// Register recovery last so it records the panic before heartbeat and metric defers run.
	defer func() {
		if value := recover(); value != nil {
			stack := debug.Stack()
			jobErr = fmt.Errorf("panic: %v", value)
			slog.Error("worker_job_panic", "job", job.name, "value", value, "stack", string(stack))
		}
	}()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", job.lock).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", job.lock)
	return job.run(ctx, conn, instanceID+":"+job.name)
}

func processTeamRunsTx(ctx context.Context, conn *pgxpool.Conn) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := processTeamRuns(ctx, tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func cleanupTx(ctx context.Context, cfg config.Config, conn *pgxpool.Conn) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var orphans []storedObject
	if err := cleanup(ctx, tx, &orphans); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	// Object deletion happens only after the rows are durably gone. Failures here leave
	// orphaned objects, which the storage manifest check surfaces — far better than the
	// reverse, where a rollback resurrects a row whose object we already destroyed.
	return removeStoredObjects(ctx, cfg, orphans)
}

type outbox struct {
	ID                int64
	To, Subject, Body string
	Attempts          int
}

func processEmail(ctx context.Context, cfg config.Config, conn *pgxpool.Conn, workerID string) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT id,to_email,subject,body,attempts FROM email_outbox
		WHERE (status='pending' AND next_attempt_at<=now()) OR (status='processing' AND lease_until<=now())
		ORDER BY id LIMIT 10 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	var messages []outbox
	for rows.Next() {
		var row outbox
		if err := rows.Scan(&row.ID, &row.To, &row.Subject, &row.Body, &row.Attempts); err != nil {
			rows.Close()
			return err
		}
		messages = append(messages, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, row := range messages {
		if _, err := tx.Exec(ctx, `UPDATE email_outbox SET status='processing',worker_id=$1,processing_at=now(),lease_until=now()+$2::interval,last_error='' WHERE id=$3`, workerID, fmt.Sprintf("%d seconds", int(emailLease.Seconds())), row.ID); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	for _, row := range messages {
		if sendErr := sendEmail(cfg, row); sendErr != nil {
			attempts := row.Attempts + 1
			status := "pending"
			if attempts >= 5 {
				status = "failed"
			}
			delay := time.Duration(1<<attempts) * time.Minute
			if err := finalizeEmailFailure(ctx, conn, workerID, row.ID, attempts, status, delay, sendErr); err != nil {
				return err
			}
			continue
		}
		if err := finalizeEmailSuccess(ctx, conn, workerID, row.ID); err != nil {
			return err
		}
	}
	return nil
}

func finalizeEmailSuccess(ctx context.Context, conn *pgxpool.Conn, workerID string, id int64) error {
	_, err := conn.Exec(ctx, `UPDATE email_outbox SET status='sent',sent_at=now(),body='[sent message body removed]',last_error='',lease_until=NULL,worker_id=NULL WHERE id=$1 AND status='processing' AND worker_id=$2`, id, workerID)
	if err == nil {
		metrics.Default.Inc("email_outbox_sent_total")
	}
	return err
}

func finalizeEmailFailure(ctx context.Context, conn *pgxpool.Conn, workerID string, id int64, attempts int, status string, delay time.Duration, sendErr error) error {
	_, err := conn.Exec(ctx, `UPDATE email_outbox SET attempts=$1,status=$2,last_error=$3,next_attempt_at=now()+$4::interval,lease_until=NULL,worker_id=NULL WHERE id=$5 AND status='processing' AND worker_id=$6`, attempts, status, truncate(sendErr.Error(), 2000), fmt.Sprintf("%d seconds", int(delay.Seconds())), id, workerID)
	if err == nil {
		metrics.Default.Inc("email_outbox_retry_total")
	}
	return err
}

func sendEmail(cfg config.Config, row outbox) error {
	if cfg.SMTPHost == "" {
		return fmt.Errorf("SMTP_HOST 未配置")
	}
	from, err := mail.ParseAddress(cfg.SMTPFrom)
	if err != nil {
		return err
	}
	hostPort := net.JoinHostPort(cfg.SMTPHost, fmt.Sprint(cfg.SMTPPort))
	headers := map[string]string{"From": cfg.SMTPFrom, "To": row.To, "Subject": row.Subject, "MIME-Version": "1.0", "Content-Type": "text/plain; charset=UTF-8", "Message-ID": fmt.Sprintf("<outbox-%d@wutong.local>", row.ID)}
	// Strip CR/LF from header values so a subject like "【梧桐墙公告】" + attacker-controlled
	// announcement title cannot inject extra headers (Bcc:, a forged body, ...).
	stripHeader := strings.NewReplacer("\r", " ", "\n", " ").Replace
	var builder strings.Builder
	for _, key := range []string{"From", "To", "Subject", "MIME-Version", "Content-Type", "Message-ID"} {
		builder.WriteString(key + ": " + stripHeader(headers[key]) + "\r\n")
	}
	builder.WriteString("\r\n" + row.Body)
	auth := smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPHost)
	if cfg.SMTPUseSSL {
		dialer := &net.Dialer{Timeout: 20 * time.Second}
		conn, err := tls.DialWithDialer(dialer, "tcp", hostPort, &tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12})
		if err != nil {
			return err
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
		client, err := smtp.NewClient(conn, cfg.SMTPHost)
		if err != nil {
			return err
		}
		defer client.Close()
		return smtpSend(client, auth, from.Address, row.To, []byte(builder.String()))
	}
	conn, err := net.DialTimeout("tcp", hostPort, 20*time.Second)
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	client, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		return err
	}
	defer client.Close()
	if ok, _ := client.Extension("STARTTLS"); !ok {
		return fmt.Errorf("SMTP 服务器不支持 STARTTLS")
	}
	if err := client.StartTLS(&tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
		return err
	}
	return smtpSend(client, auth, from.Address, row.To, []byte(builder.String()))
}
func smtpSend(client *smtp.Client, auth smtp.Auth, from, to string, message []byte) error {
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

func processTeamRuns(ctx context.Context, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT r.id,r.team_id,r.starts_at,t.game,t.mode,t.reminder_minutes,t.reminder_channels,t.recurrence,t.post_departure_retention_minutes
		FROM team_runs r JOIN teams t ON t.entity_id=r.team_id WHERE r.status='scheduled' ORDER BY r.starts_at FOR UPDATE OF r`)
	if err != nil {
		return err
	}
	type run struct {
		id, teamID           int64
		starts               time.Time
		game, mode           string
		reminder             int
		channels, recurrence string
		retention            int
	}
	var runs []run
	for rows.Next() {
		var r run
		if err := rows.Scan(&r.id, &r.teamID, &r.starts, &r.game, &r.mode, &r.reminder, &r.channels, &r.recurrence, &r.retention); err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, r)
	}
	// Cursor errors invalidate the whole reminder batch.
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	now := time.Now().UTC()
	for _, r := range runs {
		var reminderSent *time.Time
		if err := tx.QueryRow(ctx, "SELECT reminder_sent_at FROM team_runs WHERE id=$1", r.id).Scan(&reminderSent); err != nil {
			return err
		}
		if reminderSent == nil && r.starts.After(now) && !r.starts.After(now.Add(time.Duration(r.reminder)*time.Minute)) {
			members, err := tx.Query(ctx, `SELECT u.id,u.email,COALESCE(NULLIF(m.reminder_channels,''),$2) FROM team_run_members rm JOIN users u ON u.id=rm.user_id LEFT JOIN team_memberships m ON m.team_id=$1 AND m.user_id=u.id AND m.status='active' WHERE rm.run_id=$3 AND rm.status IN ('joined','checked_in')`, r.teamID, r.channels, r.id)
			if err != nil {
				return err
			}
			type reminderMember struct {
				userID   int64
				email    *string
				channels string
			}
			reminderMembers := []reminderMember{}
			for members.Next() {
				var member reminderMember
				if err := members.Scan(&member.userID, &member.email, &member.channels); err != nil {
					members.Close()
					return err
				}
				reminderMembers = append(reminderMembers, member)
			}
			if err := members.Err(); err != nil {
				members.Close()
				return err
			}
			members.Close()
			for _, member := range reminderMembers {
				body := fmt.Sprintf("%s · %s 将于 %s 发车。", r.game, r.mode, r.starts.Format("01-02 15:04"))
				set := csvSet(member.channels)
				if set["in_app"] {
					if err := domain.Notify(ctx, tx, member.userID, "车队即将发车", body, fmt.Sprintf("/teams/%d", r.teamID), "team"); err != nil {
						return err
					}
				}
				if set["email"] && member.email != nil {
					if err := domain.EnqueueEmail(ctx, tx, *member.email, "【梧桐墙】车队发车提醒", body); err != nil {
						return err
					}
				}
			}
			if _, err := tx.Exec(ctx, "UPDATE team_runs SET reminder_sent_at=$1 WHERE id=$2", now, r.id); err != nil {
				return err
			}
		}
		expires := r.starts.Add(time.Duration(r.retention) * time.Minute)
		if !expires.After(now) {
			if _, err := tx.Exec(ctx, "UPDATE team_runs SET status='completed',expires_at=COALESCE(expires_at,$1) WHERE id=$2", expires, r.id); err != nil {
				return err
			}
			if r.recurrence == "once" {
				if _, err := tx.Exec(ctx, "UPDATE teams SET status='archived' WHERE entity_id=$1", r.teamID); err != nil {
					return err
				}
				if _, err := tx.Exec(ctx, "UPDATE content_entities SET publication_status='expired',search_visible=false WHERE id=$1 AND publication_status='published'", r.teamID); err != nil {
					return err
				}
			} else {
				if err := ensureNextWeeklyRun(ctx, tx, r, now); err != nil {
					return err
				}
			}
		} else {
			if _, err := tx.Exec(ctx, "UPDATE team_runs SET expires_at=COALESCE(expires_at,$1) WHERE id=$2", expires, r.id); err != nil {
				return err
			}
		}
	}
	return nil
}
func ensureNextWeeklyRun(ctx context.Context, tx pgx.Tx, r struct {
	id, teamID           int64
	starts               time.Time
	game, mode           string
	reminder             int
	channels, recurrence string
	retention            int
}, now time.Time) error {
	var exists bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM team_runs WHERE team_id=$1 AND status='scheduled' AND expires_at>now())", r.teamID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	next := r.starts.Add(7 * 24 * time.Hour)
	for !next.Add(time.Duration(r.retention) * time.Minute).After(now) {
		next = next.Add(7 * 24 * time.Hour)
	}
	var runID int64
	err := tx.QueryRow(ctx, `INSERT INTO team_runs(team_id,starts_at,expires_at,status,created_at) VALUES($1,$2,$3,'scheduled',now()) ON CONFLICT(team_id,starts_at) DO UPDATE SET starts_at=EXCLUDED.starts_at RETURNING id`, r.teamID, next, next.Add(time.Duration(r.retention)*time.Minute)).Scan(&runID)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO team_run_members(run_id,user_id,status,credit_awarded) SELECT $1,user_id,'joined',false FROM team_memberships WHERE team_id=$2 AND status='active' ON CONFLICT(run_id,user_id) DO NOTHING`, runID, r.teamID)
	return err
}

// storedObject identifies an object-storage item queued for deletion after commit.
type storedObject struct {
	scope, path string
}

func cleanup(ctx context.Context, tx pgx.Tx, orphans *[]storedObject) error {
	if _, err := tx.Exec(ctx, `UPDATE content_entities e SET publication_status='expired',search_visible=false FROM posts p WHERE p.entity_id=e.id AND p.expires_at IS NOT NULL AND p.expires_at<=now() AND e.publication_status='published'`); err != nil {
		return err
	}
	// A reservation with either confirmation enters dispute instead of expiring.
	if _, err := tx.Exec(ctx, `UPDATE market_transactions SET status='disputed',updated_at=now()
		WHERE status='reserved' AND reserved_until<=now()
		  AND (buyer_confirmed_at IS NOT NULL OR seller_confirmed_at IS NOT NULL)`); err != nil {
		return err
	}
	// decision 与 admin_note 是 NOT NULL 且没有默认值，必须显式给空串：NOT NULL 约束在
	// ON CONFLICT 仲裁之前求值，DO NOTHING 挡不住 23502。
	if _, err := tx.Exec(ctx, `INSERT INTO market_disputes(transaction_id,opened_by,reason,status,decision,admin_note,created_at)
		SELECT mt.id,CASE WHEN mt.buyer_confirmed_at IS NOT NULL THEN mt.buyer_id ELSE mt.seller_id END,
			'预留超时且存在单方确认，系统自动转入纠纷','pending','','',now()
		FROM market_transactions mt WHERE mt.status='disputed' AND mt.reserved_until<=now()
		ON CONFLICT(transaction_id) DO NOTHING`); err != nil {
		return err
	}
	expired, err := tx.Query(ctx, `UPDATE market_transactions SET status='expired',updated_at=now()
		WHERE status='reserved' AND reserved_until<=now()
		  AND buyer_confirmed_at IS NULL AND seller_confirmed_at IS NULL RETURNING listing_id`)
	if err != nil {
		return err
	}
	var expiredListings []int64
	for expired.Next() {
		var id int64
		if err := expired.Scan(&id); err != nil {
			expired.Close()
			return err
		}
		expiredListings = append(expiredListings, id)
	}
	if err := expired.Err(); err != nil {
		expired.Close()
		return err
	}
	expired.Close()
	for _, id := range expiredListings {
		if _, err := tx.Exec(ctx, `UPDATE listings SET trade_status='available' WHERE entity_id=$1 AND trade_status='reserved' AND NOT EXISTS(SELECT 1 FROM market_transactions WHERE listing_id=$1 AND status IN ('reserved','disputed'))`, id); err != nil {
			return err
		}
	}
	// Lock the stale-pending rows so a concurrent attach (dispute evidence / listing
	// image) that takes FOR UPDATE on the same row cannot interleave: SKIP LOCKED means
	// we never touch a row being attached right now, and rows we lock make attach wait.
	rows, err := tx.Query(ctx, "SELECT id,path,thumbnail_path,access_scope FROM attachments WHERE status='pending' AND created_at<=now()-interval '24 hours' FOR UPDATE SKIP LOCKED")
	if err != nil {
		return err
	}
	type fileRow struct {
		id                 int64
		path, thumb, scope string
	}
	var files []fileRow
	for rows.Next() {
		var f fileRow
		if err := rows.Scan(&f.id, &f.path, &f.thumb, &f.scope); err != nil {
			rows.Close()
			return err
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, f := range files {
		// Only a successfully deleted pending row can schedule object removal.
		tag, err := tx.Exec(ctx, "DELETE FROM attachments WHERE id=$1 AND status='pending'", f.id)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			continue
		}
		// Object deletion is irreversible and therefore runs only after database commit.
		for _, relative := range []string{f.path, f.thumb} {
			if relative != "" {
				*orphans = append(*orphans, storedObject{scope: f.scope, path: relative})
			}
		}
	}
	users, err := tx.Query(ctx, "SELECT id FROM users WHERE status='disabled' AND deactivated_at IS NOT NULL AND deactivated_at<=now()-interval '30 days' FOR UPDATE")
	if err != nil {
		return err
	}
	var ids []int64
	for users.Next() {
		var id int64
		if err := users.Scan(&id); err != nil {
			users.Close()
			return err
		}
		ids = append(ids, id)
	}
	// Without this check a mid-stream failure looks like "no accounts to anonymise" and the
	// job still reports success, so the monitoring signal is wrong too.
	if err := users.Err(); err != nil {
		users.Close()
		return err
	}
	users.Close()
	for _, id := range ids {
		hash, err := security.HashPassword(fmt.Sprintf("deleted-%d-%d", id, time.Now().UnixNano()))
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET email=NULL,nickname='已注销用户',alias=$1,avatar_path=NULL,password_hash=$2,status='deleted',updated_at=now() WHERE id=$3`, fmt.Sprintf("deleted-%d", id), hash, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE messages m SET body='' FROM content_entities e WHERE e.id=m.entity_id AND e.owner_id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE content_entities SET publication_status='deleted' WHERE owner_id=$1 AND type='message'`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM notifications WHERE user_id=$1`, id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM verification_codes WHERE expires_at<=now()-interval '1 day'`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM rate_limit_counters WHERE expires_at<=now()`); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE absolute_expires_at<=now()-interval '30 days' OR revoked_at<=now()-interval '30 days'`)
	return err
}

func processBackup(ctx context.Context, cfg config.Config, conn *pgxpool.Conn) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var orphans []storedObject
	var id int64
	// A lapsed lease makes an interrupted backup job eligible for another worker.
	err = tx.QueryRow(ctx, `SELECT id FROM backup_jobs
		WHERE status='pending' OR (status='running' AND (lease_until IS NULL OR lease_until<=now()))
		ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id)
	if err == pgx.ErrNoRows {
		if err := expireOldBackups(ctx, tx, &orphans); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return removeStoredObjects(ctx, cfg, orphans)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE backup_jobs SET status='running',lease_until=now()+interval '30 minutes' WHERE id=$1", id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	archive, backupErr := createBackup(ctx, cfg, id, conn)
	finalize, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer finalize.Rollback(ctx)
	if backupErr != nil {
		_, dbErr := finalize.Exec(ctx, "UPDATE backup_jobs SET status='failed',error=$1,finished_at=now(),lease_until=NULL WHERE id=$2", truncate(backupErr.Error(), 4000), id)
		if dbErr != nil {
			return dbErr
		}
	} else {
		if _, err := finalize.Exec(ctx, "UPDATE backup_jobs SET status='ready',file_path=$1,error='',finished_at=now(),lease_until=NULL WHERE id=$2", archive, id); err != nil {
			return err
		}
	}
	if err := expireOldBackups(ctx, finalize, &orphans); err != nil {
		return err
	}
	if err := finalize.Commit(ctx); err != nil {
		return err
	}
	return removeStoredObjects(ctx, cfg, orphans)
}

// removeStoredObjects deletes object-storage items whose database rows are already
// committed as gone. Errors are logged, not returned: a stray object is recoverable via
// the storage manifest check, whereas failing the job here would roll nothing back anyway.
func removeStoredObjects(ctx context.Context, cfg config.Config, objects []storedObject) error {
	if len(objects) == 0 {
		return nil
	}
	store, err := storagepkg.New(cfg)
	if err != nil {
		return err
	}
	for _, object := range objects {
		if err := store.Remove(ctx, object.scope, object.path); err != nil {
			slog.Warn("backup_object_delete_failed", "scope", object.scope, "path", object.path, "error", err)
			metrics.Default.Inc("storage_object_delete_failures_total")
		}
	}
	return nil
}

func createBackup(ctx context.Context, cfg config.Config, jobID int64, conn *pgxpool.Conn) (string, error) {
	stamp := time.Now().UTC().Format("20060102-150405")
	work, err := os.MkdirTemp("", fmt.Sprintf("wutong-backup-%d-%s-", jobID, stamp))
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	dump := filepath.Join(work, "database.dump")
	// Pass the complete connection string so pg_dump preserves TLS and connection options.
	if _, err := url.Parse(cfg.DatabaseURL); err != nil {
		return "", fmt.Errorf("DATABASE_URL 无法解析: %w", err)
	}
	command := exec.CommandContext(ctx, "pg_dump", "--format=custom", "--file", dump, cfg.DatabaseURL)
	command.Env = append(os.Environ(), "PGCONNECT_TIMEOUT=10")
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("pg_dump: %w: %s", err, truncate(string(output), 1000))
	}
	countsPath := filepath.Join(work, "TABLE_COUNTS.tsv")
	countsFile, err := os.Create(countsPath)
	if err != nil {
		return "", err
	}
	countsWriter := bufio.NewWriter(countsFile)
	for _, table := range []string{"users", "content_entities", "attachments", "listings", "market_transactions", "market_disputes", "market_reviews", "messages", "audit_logs"} {
		var count int64
		if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			countsFile.Close()
			return "", err
		}
		fmt.Fprintf(countsWriter, "%s\t%d\n", table, count)
	}
	if err := countsWriter.Flush(); err != nil {
		countsFile.Close()
		return "", err
	}
	if err := countsFile.Close(); err != nil {
		return "", err
	}
	objectsPath := filepath.Join(work, "OBJECTS.tsv")
	objectsFile, err := os.Create(objectsPath)
	if err != nil {
		return "", err
	}
	objectsWriter := bufio.NewWriter(objectsFile)
	rows, err := conn.Query(ctx, "SELECT storage_bucket,access_scope,path,thumbnail_path,size_bytes FROM attachments WHERE status='attached' ORDER BY id")
	if err != nil {
		objectsFile.Close()
		return "", err
	}
	for rows.Next() {
		var bucket, scope, path, thumb string
		var size int64
		if err := rows.Scan(&bucket, &scope, &path, &thumb, &size); err != nil {
			rows.Close()
			objectsFile.Close()
			return "", err
		}
		fmt.Fprintf(objectsWriter, "%s\t%s\t%s\t%s\t%d\n", bucket, scope, path, thumb, size)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		objectsFile.Close()
		return "", err
	}
	rows.Close()
	if err := objectsWriter.Flush(); err != nil {
		objectsFile.Close()
		return "", err
	}
	if err := objectsFile.Close(); err != nil {
		return "", err
	}
	archive := filepath.Join(work, "wutong-backup-"+stamp+".zip")
	if err := writeBackupBundle(archive, dump, "", []string{countsPath, objectsPath}); err != nil {
		return "", err
	}
	info, err := os.Stat(archive)
	if err != nil {
		return "", err
	}
	input, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer input.Close()
	store, err := storagepkg.New(cfg)
	if err != nil {
		return "", err
	}
	key := "daily/" + time.Now().UTC().Format("2006/01/02") + "/" + filepath.Base(archive)
	if err := store.Put(ctx, "backup", key, "application/zip", "private, no-store", input, info.Size()); err != nil {
		return "", err
	}
	return key, nil
}
func writeBackupZip(destination, dump, uploads string) error {
	return writeBackupBundle(destination, dump, uploads, nil)
}
func writeBackupBundle(destination, dump, uploads string, extras []string) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	writer := zip.NewWriter(file)
	checksums := []string{}
	add := func(source, name string) error {
		input, err := os.Open(source)
		if err != nil {
			return err
		}
		defer input.Close()
		info, err := input.Stat()
		if err != nil {
			return err
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(name)
		header.Method = zip.Deflate
		entry, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		hash := sha256.New()
		if _, err := io.Copy(io.MultiWriter(entry, hash), input); err != nil {
			return err
		}
		checksums = append(checksums, hex.EncodeToString(hash.Sum(nil))+"  "+header.Name)
		return nil
	}
	if err := add(dump, "database.dump"); err != nil {
		writer.Close()
		file.Close()
		return err
	}
	for _, extra := range extras {
		if err := add(extra, filepath.Base(extra)); err != nil {
			writer.Close()
			file.Close()
			return err
		}
	}
	if uploads != "" {
		if info, statErr := os.Stat(uploads); statErr == nil && info.IsDir() {
			walkErr := filepath.WalkDir(uploads, func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.Type().IsRegular() {
					relative, err := filepath.Rel(uploads, path)
					if err != nil {
						return err
					}
					return add(path, filepath.Join("uploads", relative))
				}
				return nil
			})
			if walkErr != nil {
				writer.Close()
				file.Close()
				_ = os.Remove(destination)
				return walkErr
			}
		} else if statErr != nil && !os.IsNotExist(statErr) {
			writer.Close()
			file.Close()
			_ = os.Remove(destination)
			return statErr
		}
	}
	sort.Strings(checksums)
	checksum, err := writer.Create("SHA256SUMS")
	if err == nil {
		_, err = io.WriteString(checksum, strings.Join(checksums, "\n")+"\n")
	}
	closeErr := writer.Close()
	fileErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return fileErr
}
func expireOldBackups(ctx context.Context, tx pgx.Tx, orphans *[]storedObject) error {
	rows, err := tx.Query(ctx, "SELECT id,file_path,finished_at FROM backup_jobs WHERE status='ready' ORDER BY finished_at DESC")
	if err != nil {
		return err
	}
	type backup struct {
		id       int64
		path     string
		finished time.Time
	}
	backups := []backup{}
	for rows.Next() {
		var item backup
		if err := rows.Scan(&item.id, &item.path, &item.finished); err != nil {
			rows.Close()
			return err
		}
		backups = append(backups, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	now := time.Now().UTC()
	seen := map[string]bool{}
	expired := []backup{}
	for _, item := range backups {
		age := now.Sub(item.finished)
		key := ""
		switch {
		case age <= 7*24*time.Hour:
			key = "day:" + item.finished.Format("2006-01-02")
		case age <= 35*24*time.Hour:
			year, week := item.finished.ISOWeek()
			key = fmt.Sprintf("week:%04d-%02d", year, week)
		case age <= 370*24*time.Hour:
			key = "month:" + item.finished.Format("2006-01")
		default:
			expired = append(expired, item)
			continue
		}
		if seen[key] {
			expired = append(expired, item)
		} else {
			seen[key] = true
		}
	}
	// Mark the rows expired inside the transaction, but hand the object keys back to the
	// caller so deletion happens after commit. Deleting first and clearing file_path
	// unconditionally meant a transient object-store outage silently lost the only record
	// of the key, leaving the object orphaned in the bucket forever; and a failed commit
	// after a successful delete left a "ready" backup whose download 404s.
	for _, item := range expired {
		if _, err := tx.Exec(ctx, "UPDATE backup_jobs SET status='expired',file_path='' WHERE id=$1", item.id); err != nil {
			return err
		}
		*orphans = append(*orphans, storedObject{scope: "backup", path: item.path})
	}
	return nil
}

func safeRemoveAbsolute(root, path string) error {
	base, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("文件路径越界")
	}
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
func csvSet(value string) map[string]bool {
	result := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(value, ",", "\n")))
	for scanner.Scan() {
		result[strings.TrimSpace(scanner.Text())] = true
	}
	return result
}
func truncate(value string, n int) string {
	if len(value) <= n {
		return value
	}
	// Persisted error text must remain valid UTF-8.
	return strings.ToValidUTF8(value[:n], "")
}
