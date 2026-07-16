package config

import (
	"net/netip"
	"strings"
	"testing"
)

func TestRejectsInvalidIntegerInsteadOfUsingFallback(t *testing.T) {
	t.Setenv("DB_POOL_SIZE", "abc")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "DB_POOL_SIZE") {
		t.Fatalf("invalid integer accepted: %v", err)
	}
}

func TestDevelopmentDefaultsAreValidated(t *testing.T) {
	t.Setenv("ENVIRONMENT", "development")
	t.Setenv("PUBLIC_ORIGIN", "http://localhost:5173")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIPrefix != "/api/v1" || cfg.DBPoolSize != 10 || cfg.WorkerPoll.Seconds() != 10 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
	if cfg.AppTimezone != "Asia/Shanghai" {
		t.Fatalf("unexpected application timezone: %s", cfg.AppTimezone)
	}
	if len(cfg.TrustedProxyCIDRs) != 2 || !cfg.TrustedProxyCIDRs[0].Contains(netip.MustParseAddr("127.0.0.1")) {
		t.Fatalf("unexpected trusted proxies: %#v", cfg.TrustedProxyCIDRs)
	}
}

func TestRejectsInvalidCIDRAndCookieConfiguration(t *testing.T) {
	t.Run("cidr", func(t *testing.T) {
		t.Setenv("TRUSTED_PROXY_CIDRS", "not-a-cidr")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXY_CIDRS") {
			t.Fatalf("invalid CIDR accepted: %v", err)
		}
	})
	t.Run("same cookie", func(t *testing.T) {
		t.Setenv("SESSION_COOKIE_NAME", "same")
		t.Setenv("CSRF_COOKIE_NAME", "same")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "Cookie") {
			t.Fatalf("duplicate cookies accepted: %v", err)
		}
	})
	t.Run("bad prefix", func(t *testing.T) {
		t.Setenv("API_PREFIX", "api/v1/")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "API_PREFIX") {
			t.Fatalf("bad prefix accepted: %v", err)
		}
	})
	t.Run("https cookie", func(t *testing.T) {
		t.Setenv("PUBLIC_ORIGIN", "https://wall.test.edu.cn")
		t.Setenv("COOKIE_SECURE", "false")
		_, err := Load()
		if err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
			t.Fatalf("insecure cookie accepted: %v", err)
		}
	})
}

func TestRejectsInvalidBooleanInsteadOfUsingFallback(t *testing.T) {
	t.Setenv("COOKIE_SECURE", "treu")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "COOKIE_SECURE") {
		t.Fatalf("invalid boolean accepted: %v", err)
	}
}

func TestRejectsInconsistentSessionDurations(t *testing.T) {
	t.Setenv("SESSION_SLIDING_DAYS", "30")
	t.Setenv("SESSION_ABSOLUTE_DAYS", "7")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "SESSION_ABSOLUTE_DAYS") {
		t.Fatalf("inconsistent sessions accepted: %v", err)
	}
}

func TestProductionRejectsPlaceholders(t *testing.T) {
	t.Setenv("ENVIRONMENT", "production")
	t.Setenv("DATABASE_URL", "postgresql+psycopg://wutong:replace-with-password@db/wutong")
	t.Setenv("PUBLIC_ORIGIN", "https://wall.example.edu.cn")
	t.Setenv("SECRET_KEY", "replace-with-at-least-32-random-characters")
	t.Setenv("TRUSTED_HOSTS", "wall.example.edu.cn")
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
	t.Setenv("TRUSTED_HOSTS", "wall.real-campus.edu.cn")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "ALLOWED_CAMPUS_EMAIL_DOMAINS") {
		t.Fatalf("placeholder campus domains accepted: %v", err)
	}
}

func TestStrictScalarConfiguration(t *testing.T) {
	cases := []struct{ key, value string }{
		{"DB_MAX_OVERFLOW", "invalid"}, {"SESSION_SLIDING_DAYS", "0"}, {"SESSION_ABSOLUTE_DAYS", "0"},
		{"SESSION_ROTATION_HOURS", "0"}, {"MAX_UPLOAD_MB", "51"}, {"SMTP_PORT", "0"},
		{"WORKER_POLL_SECONDS", "301"}, {"MARKET_RESERVATION_HOURS", "0"}, {"MARKET_REVIEW_BLIND_DAYS", "91"},
		{"SMTP_USE_SSL", "perhaps"}, {"DOCS_ENABLED", "perhaps"}, {"S3_USE_PATH_STYLE", "perhaps"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("invalid %s accepted: %v", tc.key, err)
			}
		})
	}
}

func TestRejectsInvalidDependencyConfiguration(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"rotation", map[string]string{"SESSION_SLIDING_DAYS": "1", "SESSION_ROTATION_HOURS": "25"}, "SESSION_ROTATION_HOURS"},
		{"origin path", map[string]string{"PUBLIC_ORIGIN": "http://localhost:5173/path"}, "PUBLIC_ORIGIN"},
		{"database kind", map[string]string{"DATABASE_URL": "sqlite:///tmp/app.db"}, "PostgreSQL"},
		{"database shape", map[string]string{"DATABASE_URL": "postgresql:///wutong"}, "DATABASE_URL"},
		{"empty s3", map[string]string{"S3_REGION": ""}, "S3_REGION"},
		{"s3 scheme", map[string]string{"S3_ENDPOINT": "ftp://storage.test"}, "S3_ENDPOINT"},
		{"same buckets", map[string]string{"S3_PUBLIC_BUCKET": "same-bucket", "S3_PRIVATE_BUCKET": "same-bucket"}, "互不相同"},
		{"bad bucket", map[string]string{"S3_PUBLIC_BUCKET": "Bad_Bucket"}, "S3_PUBLIC_BUCKET"},
		{"log level", map[string]string{"LOG_LEVEL": "TRACE"}, "LOG_LEVEL"},
		{"partial smtp", map[string]string{"SMTP_HOST": "smtp.test"}, "必须同时配置"},
		{"bad timezone", map[string]string{"APP_TIMEZONE": "Mars/Olympus"}, "APP_TIMEZONE"},
		{"bad media origin", map[string]string{"CSP_MEDIA_ORIGINS": "https://media.test/path"}, "CSP_MEDIA_ORIGINS"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for key, value := range tc.env {
				t.Setenv(key, value)
			}
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid dependency config accepted: %v", err)
			}
		})
	}
}

func TestCompleteProductionConfiguration(t *testing.T) {
	values := map[string]string{
		"ENVIRONMENT": "production", "DATABASE_URL": "postgresql://wutong:secret@db:5432/wutong",
		"PUBLIC_ORIGIN": "https://wall.real-campus.edu.cn", "SECRET_KEY": "a-real-secret-key-with-more-than-32-characters",
		"ALLOWED_CAMPUS_EMAIL_DOMAINS": "stu.real-campus.edu.cn", "SMTP_HOST": "smtp.real-campus.edu.cn",
		"SMTP_USERNAME": "mailer@real-campus.edu.cn", "SMTP_PASSWORD": "real-smtp-password", "SMTP_FROM": "mailer@real-campus.edu.cn",
		"TRUSTED_HOSTS": "wall.real-campus.edu.cn", "HEALTH_CHECK_TOKEN": "a-real-health-token-longer-than-24",
		"S3_ENDPOINT": "https://objects.real-campus.edu.cn", "S3_ACCESS_KEY_ID": "real-access-key", "S3_SECRET_ACCESS_KEY": "real-secret-key",
		"S3_PUBLIC_BASE_URL": "https://media.real-campus.edu.cn", "COOKIE_SECURE": "true", "DOCS_ENABLED": "true",
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.CookieSecure || cfg.DocsEnabled {
		t.Fatalf("production safety overrides not applied: %#v", cfg)
	}
}
