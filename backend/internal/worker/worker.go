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
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	domain "github.com/yatools/wutong-campus-wall/backend/internal/app"
	"github.com/yatools/wutong-campus-wall/backend/internal/config"
	"github.com/yatools/wutong-campus-wall/backend/internal/security"
)

const advisoryLockID int64 = 846208411

func Run(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) error {
	ticker := time.NewTicker(cfg.WorkerPoll)
	defer ticker.Stop()
	for {
		if err := Cycle(ctx, cfg, pool); err != nil && ctx.Err() == nil {
			slog.Error("worker_cycle_failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func Cycle(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", advisoryLockID).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return nil
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", advisoryLockID)
	tx, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := processEmail(ctx, cfg, tx); err != nil {
		return err
	}
	if err := processTeamRuns(ctx, tx); err != nil {
		return err
	}
	if err := cleanup(ctx, cfg, tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return processBackup(ctx, cfg, conn)
}

type outbox struct {
	ID                int64
	To, Subject, Body string
	Attempts          int
}

func processEmail(ctx context.Context, cfg config.Config, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, `SELECT id,to_email,subject,body,attempts FROM email_outbox WHERE status='pending' AND next_attempt_at<=now() ORDER BY id LIMIT 10 FOR UPDATE SKIP LOCKED`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var messages []outbox
	for rows.Next() {
		var row outbox
		if err := rows.Scan(&row.ID, &row.To, &row.Subject, &row.Body, &row.Attempts); err != nil {
			return err
		}
		messages = append(messages, row)
	}
	rows.Close()
	for _, row := range messages {
		if err := sendEmail(cfg, row); err != nil {
			attempts := row.Attempts + 1
			status := "pending"
			if attempts >= 5 {
				status = "failed"
			}
			delay := time.Duration(1<<attempts) * time.Minute
			_, dbErr := tx.Exec(ctx, "UPDATE email_outbox SET attempts=$1,status=$2,last_error=$3,next_attempt_at=now()+$4::interval WHERE id=$5", attempts, status, truncate(err.Error(), 2000), fmt.Sprintf("%d seconds", int(delay.Seconds())), row.ID)
			if dbErr != nil {
				return dbErr
			}
		} else {
			if _, err := tx.Exec(ctx, "UPDATE email_outbox SET status='sent',sent_at=now(),body='[邮件正文发送后已清除]',last_error='' WHERE id=$1", row.ID); err != nil {
				return err
			}
		}
	}
	return nil
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
	headers := map[string]string{"From": cfg.SMTPFrom, "To": row.To, "Subject": row.Subject, "MIME-Version": "1.0", "Content-Type": "text/plain; charset=UTF-8"}
	var builder strings.Builder
	for _, key := range []string{"From", "To", "Subject", "MIME-Version", "Content-Type"} {
		builder.WriteString(key + ": " + headers[key] + "\r\n")
	}
	builder.WriteString("\r\n" + row.Body)
	auth := smtp.PlainAuth("", cfg.SMTPUsername, cfg.SMTPPassword, cfg.SMTPHost)
	if cfg.SMTPUseSSL {
		conn, err := tls.Dial("tcp", hostPort, &tls.Config{ServerName: cfg.SMTPHost, MinVersion: tls.VersionTLS12})
		if err != nil {
			return err
		}
		defer conn.Close()
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
			return err
		}
		runs = append(runs, r)
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
				if _, err := tx.Exec(ctx, "UPDATE content_entities SET status='expired',search_visible=false WHERE id=$1 AND status='published'", r.teamID); err != nil {
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

func cleanup(ctx context.Context, cfg config.Config, tx pgx.Tx) error {
	if _, err := tx.Exec(ctx, `UPDATE content_entities e SET status='expired',search_visible=false FROM posts p WHERE p.entity_id=e.id AND p.expires_at IS NOT NULL AND p.expires_at<=now() AND e.status='published'`); err != nil {
		return err
	}
	rows, err := tx.Query(ctx, "SELECT id,path,thumbnail_path FROM attachments WHERE status='pending' AND created_at<=now()-interval '24 hours'")
	if err != nil {
		return err
	}
	type fileRow struct {
		id          int64
		path, thumb string
	}
	var files []fileRow
	for rows.Next() {
		var f fileRow
		if err := rows.Scan(&f.id, &f.path, &f.thumb); err != nil {
			return err
		}
		files = append(files, f)
	}
	rows.Close()
	for _, f := range files {
		for _, relative := range []string{f.path, f.thumb} {
			if relative != "" {
				_ = safeRemove(cfg.UploadDir, relative)
			}
		}
		if _, err := tx.Exec(ctx, "DELETE FROM attachments WHERE id=$1", f.id); err != nil {
			return err
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
			return err
		}
		ids = append(ids, id)
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
		if _, err := tx.Exec(ctx, `UPDATE content_entities SET status='deleted' WHERE owner_id=$1 AND type='message'`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM notifications WHERE user_id=$1`, id); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `DELETE FROM verification_codes WHERE expires_at<=now()-interval '1 day'`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM rate_limit_events WHERE created_at<=now()-interval '2 days'`); err != nil {
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
	var id int64
	err = tx.QueryRow(ctx, "SELECT id FROM backup_jobs WHERE status='pending' ORDER BY created_at LIMIT 1 FOR UPDATE SKIP LOCKED").Scan(&id)
	if err == pgx.ErrNoRows {
		if err := expireOldBackups(ctx, cfg, tx); err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "UPDATE backup_jobs SET status='running' WHERE id=$1", id); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	archive, backupErr := createBackup(ctx, cfg, id)
	finalize, err := conn.Begin(ctx)
	if err != nil {
		return err
	}
	defer finalize.Rollback(ctx)
	if backupErr != nil {
		_, dbErr := finalize.Exec(ctx, "UPDATE backup_jobs SET status='failed',error=$1,finished_at=now() WHERE id=$2", truncate(backupErr.Error(), 4000), id)
		if dbErr != nil {
			return dbErr
		}
	} else {
		if _, err := finalize.Exec(ctx, "UPDATE backup_jobs SET status='ready',file_path=$1,error='',finished_at=now() WHERE id=$2", archive, id); err != nil {
			return err
		}
	}
	if err := expireOldBackups(ctx, cfg, finalize); err != nil {
		return err
	}
	return finalize.Commit(ctx)
}

func createBackup(ctx context.Context, cfg config.Config, jobID int64) (string, error) {
	if err := os.MkdirAll(cfg.BackupDir, 0o750); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	work, err := os.MkdirTemp(cfg.BackupDir, fmt.Sprintf("job-%d-%s-", jobID, stamp))
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(work)
	dump := filepath.Join(work, "database.dump")
	parsed, err := url.Parse(cfg.DatabaseURL)
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, "pg_dump", "--format=custom", "--file", dump, "--host", parsed.Hostname(), "--port", portOr(parsed, "5432"), "--username", parsed.User.Username(), "--dbname", strings.TrimPrefix(parsed.Path, "/"))
	if password, ok := parsed.User.Password(); ok {
		command.Env = append(os.Environ(), "PGPASSWORD="+password, "PGCONNECT_TIMEOUT=10")
	}
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("pg_dump: %w: %s", err, truncate(string(output), 1000))
	}
	archive := filepath.Join(cfg.BackupDir, "wutong-backup-"+stamp+".zip")
	if err := writeBackupZip(archive, dump, cfg.UploadDir); err != nil {
		return "", err
	}
	return archive, nil
}
func writeBackupZip(destination, dump, uploads string) error {
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
func expireOldBackups(ctx context.Context, cfg config.Config, tx pgx.Tx) error {
	rows, err := tx.Query(ctx, "SELECT id,file_path FROM backup_jobs WHERE status='ready' ORDER BY finished_at DESC OFFSET 7")
	if err != nil {
		return err
	}
	type backup struct {
		id   int64
		path string
	}
	backups := []backup{}
	for rows.Next() {
		var item backup
		if err := rows.Scan(&item.id, &item.path); err != nil {
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
	for _, item := range backups {
		_ = safeRemoveAbsolute(cfg.BackupDir, item.path)
		if _, err := tx.Exec(ctx, "UPDATE backup_jobs SET status='expired',file_path='' WHERE id=$1", item.id); err != nil {
			return err
		}
	}
	return nil
}

func safeRemove(root, relative string) error {
	return safeRemoveAbsolute(root, filepath.Join(root, filepath.FromSlash(relative)))
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
func portOr(value *url.URL, fallback string) string {
	if value.Port() != "" {
		return value.Port()
	}
	return fallback
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
	return value[:n]
}
