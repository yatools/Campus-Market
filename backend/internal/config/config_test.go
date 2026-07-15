package config

import (
	"strings"
	"testing"
)

func TestNormalizesSQLAlchemyPostgresURL(t *testing.T) {
	got := normalizeDatabaseURL("postgresql+psycopg://u:p@db:5432/wutong")
	if got != "postgresql://u:p@db:5432/wutong" {
		t.Fatalf("unexpected URL: %s", got)
	}
}

func TestProductionRejectsPlaceholders(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("DATABASE_URL", "postgresql+psycopg://wutong:replace-with-password@db/wutong")
	t.Setenv("PUBLIC_ORIGIN", "https://wall.example.edu.cn")
	t.Setenv("SECRET_KEY", "replace-with-at-least-32-random-characters")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "生产配置缺失") {
		t.Fatalf("placeholder production config accepted: %v", err)
	}
}

func TestProductionRejectsDefaultCampusDomainsEvenWithOtherRealValues(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("DATABASE_URL", "postgresql://wutong:real-password@db/wutong")
	t.Setenv("PUBLIC_ORIGIN", "https://wall.real-campus.edu.cn")
	t.Setenv("SECRET_KEY", "a-real-secret-key-with-more-than-32-characters")
	t.Setenv("ALLOWED_CAMPUS_EMAIL_DOMAINS", "stu.example.edu.cn,example.edu.cn")
	t.Setenv("SMTP_HOST", "smtp.real-campus.edu.cn")
	t.Setenv("SMTP_USERNAME", "mailer@real-campus.edu.cn")
	t.Setenv("SMTP_PASSWORD", "a-real-smtp-password")
	t.Setenv("SMTP_FROM", "mailer@real-campus.edu.cn")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "ALLOWED_CAMPUS_EMAIL_DOMAINS") {
		t.Fatalf("placeholder campus domains accepted: %v", err)
	}
}
