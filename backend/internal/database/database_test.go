package database

import (
	"strings"
	"testing"
)

var requiredBaselineTables = []string{
	"users", "credit_rules", "sessions", "verification_codes", "rate_limit_counters", "email_outbox",
	"content_entities", "content_revisions", "attachments", "posts", "comments", "thread_anonymous_identities",
	"reactions", "favorites", "reports", "moderation_cases", "notifications", "audit_logs", "team_games",
	"team_game_aliases", "game_submissions", "teams", "team_runs", "team_memberships", "team_run_members",
	"team_ratings", "questions", "answers", "handbook_articles", "courses", "course_offerings", "course_reviews",
	"campus_services", "campus_service_ratings", "market_categories", "market_locations", "listings", "market_transactions",
	"market_disputes", "market_reviews", "activities", "activity_members", "lost_items", "lost_claims", "observe_posts",
	"penalties", "appeals", "conversations", "conversation_members", "messages", "blocks", "announcements",
	"announcement_reads", "feedback", "settings", "backup_jobs", "worker_heartbeats",
}

func TestBaselineContainsEveryRequiredTable(t *testing.T) {
	data, err := migrationFS.ReadFile("migrations/00001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ToLower(string(data))
	for _, table := range requiredBaselineTables {
		if !strings.Contains(sql, "create table "+table+" ") && !strings.Contains(sql, "create table "+table+"(") {
			t.Errorf("baseline is missing table %s", table)
		}
	}
	for _, index := range []string{"ix_posts_title_trgm", "ix_questions_title_trgm", "ix_listings_title_trgm"} {
		if !strings.Contains(sql, index) {
			t.Errorf("baseline is missing %s", index)
		}
	}
}
