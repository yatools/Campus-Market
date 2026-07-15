package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Environment               string
	AppName                   string
	APIPrefix                 string
	PublicOrigin              string
	SecretKey                 string
	DatabaseURL               string
	DBPoolSize                int32
	DBMaxOverflow             int32
	AllowedCampusEmailDomains map[string]struct{}
	SessionCookieName         string
	CSRFCookieName            string
	CookieSecure              bool
	SessionSliding            time.Duration
	SessionAbsolute           time.Duration
	SessionRotation           time.Duration
	UploadDir                 string
	BackupDir                 string
	MaxUploadBytes            int64
	SMTPHost                  string
	SMTPPort                  int
	SMTPUsername              string
	SMTPPassword              string
	SMTPFrom                  string
	SMTPUseSSL                bool
	WorkerPoll                time.Duration
	LogLevel                  string
	DocsEnabled               bool
	TrustedHosts              map[string]struct{}
}

func Load() (Config, error) {
	cwd, _ := os.Getwd()
	c := Config{
		Environment:               env("ENVIRONMENT", "development"),
		AppName:                   env("APP_NAME", "梧桐墙"),
		APIPrefix:                 env("API_PREFIX", "/api/v1"),
		PublicOrigin:              strings.TrimRight(env("PUBLIC_ORIGIN", "http://localhost:5173"), "/"),
		SecretKey:                 env("SECRET_KEY", "development-only-change-me"),
		DatabaseURL:               normalizeDatabaseURL(env("DATABASE_URL", "postgresql://wutong:wutong@127.0.0.1:5432/wutong")),
		DBPoolSize:                int32(envInt("DB_POOL_SIZE", 10)),
		DBMaxOverflow:             int32(envInt("DB_MAX_OVERFLOW", 10)),
		AllowedCampusEmailDomains: csvSet(env("ALLOWED_CAMPUS_EMAIL_DOMAINS", "stu.example.edu.cn,example.edu.cn")),
		SessionCookieName:         env("SESSION_COOKIE_NAME", "wutong_session"),
		CSRFCookieName:            env("CSRF_COOKIE_NAME", "wutong_csrf"),
		CookieSecure:              envBool("COOKIE_SECURE", false),
		SessionSliding:            time.Duration(envInt("SESSION_SLIDING_DAYS", 7)) * 24 * time.Hour,
		SessionAbsolute:           time.Duration(envInt("SESSION_ABSOLUTE_DAYS", 30)) * 24 * time.Hour,
		SessionRotation:           time.Duration(envInt("SESSION_ROTATION_HOURS", 24)) * time.Hour,
		UploadDir:                 env("UPLOAD_DIR", filepath.Join(cwd, "uploads")),
		BackupDir:                 env("BACKUP_DIR", filepath.Join(cwd, "backups")),
		MaxUploadBytes:            int64(envInt("MAX_UPLOAD_MB", 8)) * 1024 * 1024,
		SMTPHost:                  env("SMTP_HOST", ""),
		SMTPPort:                  envInt("SMTP_PORT", 465),
		SMTPUsername:              env("SMTP_USERNAME", ""),
		SMTPPassword:              env("SMTP_PASSWORD", ""),
		SMTPFrom:                  env("SMTP_FROM", ""),
		SMTPUseSSL:                envBool("SMTP_USE_SSL", true),
		WorkerPoll:                time.Duration(envInt("WORKER_POLL_SECONDS", 10)) * time.Second,
		LogLevel:                  env("LOG_LEVEL", "INFO"),
		DocsEnabled:               envBool("DOCS_ENABLED", true),
		TrustedHosts:              csvSet(env("TRUSTED_HOSTS", "localhost,127.0.0.1,testserver")),
	}
	if c.Environment == "production" {
		var missing []string
		lowerSecret := strings.ToLower(c.SecretKey)
		if len(c.SecretKey) < 32 || strings.Contains(lowerSecret, "change-me") || strings.Contains(lowerSecret, "replace-with") {
			missing = append(missing, "SECRET_KEY（至少 32 字符）")
		}
		if !strings.HasPrefix(c.DatabaseURL, "postgres") || strings.Contains(c.DatabaseURL, "replace-with") {
			missing = append(missing, "DATABASE_URL（PostgreSQL）")
		}
		if !strings.HasPrefix(c.PublicOrigin, "https://") || strings.Contains(c.PublicOrigin, "example.edu.cn") {
			missing = append(missing, "PUBLIC_ORIGIN（HTTPS）")
		}
		campusDomainsArePlaceholders := len(c.AllowedCampusEmailDomains) == 0
		if !campusDomainsArePlaceholders {
			campusDomainsArePlaceholders = true
			for domain := range c.AllowedCampusEmailDomains {
				if domain != "stu.example.edu.cn" && domain != "example.edu.cn" {
					campusDomainsArePlaceholders = false
					break
				}
			}
		}
		if campusDomainsArePlaceholders {
			missing = append(missing, "ALLOWED_CAMPUS_EMAIL_DOMAINS")
		}
		for name, value := range map[string]string{"SMTP_HOST": c.SMTPHost, "SMTP_USERNAME": c.SMTPUsername, "SMTP_PASSWORD": c.SMTPPassword, "SMTP_FROM": c.SMTPFrom} {
			lower := strings.ToLower(value)
			if value == "" || strings.Contains(lower, "replace-with") || strings.Contains(lower, "example.com") {
				missing = append(missing, name)
			}
		}
		if len(missing) > 0 {
			return Config{}, fmt.Errorf("生产配置缺失：%s", strings.Join(missing, "、"))
		}
		c.CookieSecure = true
		c.DocsEnabled = false
	}
	if !strings.HasPrefix(c.DatabaseURL, "postgres://") && !strings.HasPrefix(c.DatabaseURL, "postgresql://") {
		return Config{}, errors.New("go 后端仅支持 PostgreSQL DATABASE_URL")
	}
	if _, err := url.Parse(c.DatabaseURL); err != nil {
		return Config{}, fmt.Errorf("database_url 无效: %w", err)
	}
	return c, nil
}

func normalizeDatabaseURL(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Replace(value, "postgresql+psycopg://", "postgresql://", 1)
	return value
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(env(key, strconv.Itoa(fallback)))
	if err != nil {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value, err := strconv.ParseBool(env(key, strconv.FormatBool(fallback)))
	if err != nil {
		return fallback
	}
	return value
}

func csvSet(value string) map[string]struct{} {
	result := map[string]struct{}{}
	for _, item := range strings.Split(value, ",") {
		item = strings.ToLower(strings.TrimSpace(item))
		if item != "" {
			result[item] = struct{}{}
		}
	}
	return result
}
