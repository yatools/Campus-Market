package worker

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/yatools/wutong-campus-wall/backend/internal/config"
	"github.com/yatools/wutong-campus-wall/backend/internal/database"
)

func TestWriteBackupZipIncludesDatabaseUploadsAndChecksums(t *testing.T) {
	root := t.TempDir()
	dump := filepath.Join(root, "database.dump")
	uploads := filepath.Join(root, "uploads")
	if err := os.MkdirAll(filepath.Join(uploads, "2026", "07", "15"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dump, []byte("postgres-dump"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uploads, "2026", "07", "15", "image.webp"), []byte("image"), 0o600); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(root, "backup.zip")
	if err := writeBackupZip(archive, dump, uploads); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.OpenReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entries := map[string]string{}
	for _, entry := range reader.File {
		handle, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(handle)
		if err != nil {
			handle.Close()
			t.Fatal(err)
		}
		handle.Close()
		entries[filepath.ToSlash(entry.Name)] = string(data)
	}
	if entries["database.dump"] != "postgres-dump" {
		t.Fatalf("database dump missing from archive: %#v", entries)
	}
	if entries["uploads/2026/07/15/image.webp"] != "image" {
		t.Fatalf("upload missing from archive: %#v", entries)
	}
	checksums := entries["SHA256SUMS"]
	if !strings.Contains(checksums, "  database.dump") || !strings.Contains(checksums, "  uploads/2026/07/15/image.webp") {
		t.Fatalf("checksum manifest incomplete: %q", checksums)
	}
}

func TestCycleQueuesTeamReminderAfterClosingMemberRows(t *testing.T) {
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		t.Skip("TEST_DATABASE_URL is not configured")
	}
	parsed, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("wutong_worker_%d", time.Now().UnixNano())
	adminURL := *parsed
	adminURL.Path = "/postgres"
	admin, err := pgx.Connect(context.Background(), adminURL.String())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close(context.Background())
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(context.Background(), "CREATE DATABASE "+identifier); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+identifier+" WITH (FORCE)")
	}()
	targetURL := *parsed
	targetURL.Path = "/" + name
	cfg := config.Config{DatabaseURL: targetURL.String(), DBPoolSize: 2, UploadDir: t.TempDir(), BackupDir: t.TempDir()}
	if err := database.Migrate(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	pool, err := database.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var userID int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO users(email,password_hash,nickname,alias,campus_identity,role,status,credit,xp,dm_stranger_off,hide_online,verified_at,created_at,updated_at)
		VALUES('worker@test.edu.cn','unused','提醒成员','梧桐#worker','student','user','active',800,0,false,false,now(),now(),now()) RETURNING id`).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var teamID int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO content_entities(type,owner_id,status,allow_comments,search_visible,moderation_reason,revision,created_at,updated_at)
		VALUES('team',$1,'published',true,true,'',1,now(),now()) RETURNING id`, userID).Scan(&teamID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO teams(entity_id,owner_id,game,mode,rank_requirement,capacity,voice_name,voice_link,notes,newbie_level,vibe,reminder_channels,recurrence,reminder_minutes,post_departure_retention_minutes,status)
		VALUES($1,$2,'测试游戏','提醒场次','',5,'','','','','','in_app,email','once',30,120,'active')`, teamID, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO team_memberships(team_id,user_id,role,status,reminder_channels,joined_at)
		VALUES($1,$2,'owner','active','in_app,email',now())`, teamID, userID); err != nil {
		t.Fatal(err)
	}
	var runID int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO team_runs(team_id,starts_at,expires_at,status,created_at)
		VALUES($1,now()+interval '5 minutes',now()+interval '125 minutes','scheduled',now()) RETURNING id`, teamID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO team_run_members(run_id,user_id,status,credit_awarded)
		VALUES($1,$2,'joined',false)`, runID, userID); err != nil {
		t.Fatal(err)
	}
	if err := Cycle(context.Background(), cfg, pool); err != nil {
		t.Fatal(err)
	}
	var notifications, emails int
	var sentAt *time.Time
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM notifications WHERE user_id=$1", userID).Scan(&notifications); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM email_outbox WHERE to_email='worker@test.edu.cn'").Scan(&emails); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(context.Background(), "SELECT reminder_sent_at FROM team_runs WHERE id=$1", runID).Scan(&sentAt); err != nil {
		t.Fatal(err)
	}
	if notifications != 1 || emails != 1 || sentAt == nil {
		t.Fatalf("reminder was not queued exactly once: notifications=%d emails=%d sent_at=%v", notifications, emails, sentAt)
	}
	if _, err := exec.LookPath("pg_dump"); err == nil {
		var backupID int64
		if err := pool.QueryRow(context.Background(), `INSERT INTO backup_jobs(requested_by,status,file_path,download_token,error,created_at)
			VALUES($1,'pending','','worker-test-token','',now()) RETURNING id`, userID).Scan(&backupID); err != nil {
			t.Fatal(err)
		}
		if err := Cycle(context.Background(), cfg, pool); err != nil {
			t.Fatal(err)
		}
		var status, archivePath string
		if err := pool.QueryRow(context.Background(), "SELECT status,file_path FROM backup_jobs WHERE id=$1", backupID).Scan(&status, &archivePath); err != nil {
			t.Fatal(err)
		}
		if status != "ready" {
			t.Fatalf("backup job did not finish: status=%s path=%s", status, archivePath)
		}
		reader, err := zip.OpenReader(archivePath)
		if err != nil {
			t.Fatal(err)
		}
		entries := map[string]bool{}
		for _, entry := range reader.File {
			entries[entry.Name] = true
		}
		reader.Close()
		if !entries["database.dump"] || !entries["SHA256SUMS"] {
			t.Fatalf("worker backup archive is incomplete: %#v", entries)
		}
	}
}

func TestSafeRemoveRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.txt")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })
	if err := safeRemoveAbsolute(root, outside); err == nil {
		t.Fatal("path traversal was accepted")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file was changed: %v", err)
	}
}
