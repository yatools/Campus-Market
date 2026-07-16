package config

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"regexp"
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
	TrustedProxyCIDRs         []netip.Prefix
	HealthCheckToken          string
	S3Endpoint                string
	S3Region                  string
	S3AccessKeyID             string
	S3SecretAccessKey         string
	S3PublicBucket            string
	S3PrivateBucket           string
	S3BackupBucket            string
	S3PublicBaseURL           string
	S3UsePathStyle            bool
	CSPMediaOrigins           []string
	AppTimezone               string
	MarketReservationTTL      time.Duration
	MarketReviewBlindTTL      time.Duration
}

func Load() (Config, error) {
	dbPool, err := envInt("DB_POOL_SIZE", 10, 1, 100)
	if err != nil {
		return Config{}, err
	}
	dbOverflow, err := envInt("DB_MAX_OVERFLOW", 10, 0, 100)
	if err != nil {
		return Config{}, err
	}
	slidingDays, err := envInt("SESSION_SLIDING_DAYS", 7, 1, 30)
	if err != nil {
		return Config{}, err
	}
	absoluteDays, err := envInt("SESSION_ABSOLUTE_DAYS", 30, 1, 365)
	if err != nil {
		return Config{}, err
	}
	rotationHours, err := envInt("SESSION_ROTATION_HOURS", 24, 1, 720)
	if err != nil {
		return Config{}, err
	}
	maxUploadMB, err := envInt("MAX_UPLOAD_MB", 8, 1, 50)
	if err != nil {
		return Config{}, err
	}
	smtpPort, err := envInt("SMTP_PORT", 465, 1, 65535)
	if err != nil {
		return Config{}, err
	}
	workerPoll, err := envInt("WORKER_POLL_SECONDS", 10, 1, 300)
	if err != nil {
		return Config{}, err
	}
	reservationHours, err := envInt("MARKET_RESERVATION_HOURS", 24, 1, 168)
	if err != nil {
		return Config{}, err
	}
	reviewBlindDays, err := envInt("MARKET_REVIEW_BLIND_DAYS", 14, 1, 90)
	if err != nil {
		return Config{}, err
	}
	cookieSecure, err := envBool("COOKIE_SECURE", false)
	if err != nil {
		return Config{}, err
	}
	smtpSSL, err := envBool("SMTP_USE_SSL", true)
	if err != nil {
		return Config{}, err
	}
	docsEnabled, err := envBool("DOCS_ENABLED", true)
	if err != nil {
		return Config{}, err
	}
	s3PathStyle, err := envBool("S3_USE_PATH_STYLE", true)
	if err != nil {
		return Config{}, err
	}
	trustedProxies, err := parseCIDRs("TRUSTED_PROXY_CIDRS", envString("TRUSTED_PROXY_CIDRS", "127.0.0.1/32,::1/128"))
	if err != nil {
		return Config{}, err
	}
	mediaOrigins, err := parseOrigins("CSP_MEDIA_ORIGINS", envString("CSP_MEDIA_ORIGINS", ""))
	if err != nil {
		return Config{}, err
	}
	appTimezone := envString("APP_TIMEZONE", "Asia/Shanghai")
	if _, err := time.LoadLocation(appTimezone); err != nil {
		return Config{}, fmt.Errorf("APP_TIMEZONE is invalid: %w", err)
	}
	c := Config{
		Environment:               envString("ENVIRONMENT", "development"),
		AppName:                   envString("APP_NAME", "梧桐墙"),
		APIPrefix:                 envString("API_PREFIX", "/api/v1"),
		PublicOrigin:              strings.TrimRight(envString("PUBLIC_ORIGIN", "http://localhost:5173"), "/"),
		SecretKey:                 envString("SECRET_KEY", "development-only-change-me"),
		DatabaseURL:               strings.TrimSpace(envString("DATABASE_URL", "postgresql://wutong:wutong@127.0.0.1:5432/wutong")),
		DBPoolSize:                int32(dbPool),
		DBMaxOverflow:             int32(dbOverflow),
		AllowedCampusEmailDomains: csvSet(envString("ALLOWED_CAMPUS_EMAIL_DOMAINS", "stu.example.edu.cn,example.edu.cn")),
		SessionCookieName:         envString("SESSION_COOKIE_NAME", "wutong_session"),
		CSRFCookieName:            envString("CSRF_COOKIE_NAME", "wutong_csrf"),
		CookieSecure:              cookieSecure,
		SessionSliding:            time.Duration(slidingDays) * 24 * time.Hour,
		SessionAbsolute:           time.Duration(absoluteDays) * 24 * time.Hour,
		SessionRotation:           time.Duration(rotationHours) * time.Hour,
		MaxUploadBytes:            int64(maxUploadMB) * 1024 * 1024,
		SMTPHost:                  envString("SMTP_HOST", ""),
		SMTPPort:                  smtpPort,
		SMTPUsername:              envString("SMTP_USERNAME", ""),
		SMTPPassword:              envString("SMTP_PASSWORD", ""),
		SMTPFrom:                  envString("SMTP_FROM", ""),
		SMTPUseSSL:                smtpSSL,
		WorkerPoll:                time.Duration(workerPoll) * time.Second,
		LogLevel:                  envString("LOG_LEVEL", "INFO"),
		DocsEnabled:               docsEnabled,
		TrustedHosts:              csvSet(envString("TRUSTED_HOSTS", "localhost,127.0.0.1,testserver")),
		TrustedProxyCIDRs:         trustedProxies,
		HealthCheckToken:          envString("HEALTH_CHECK_TOKEN", ""),
		S3Endpoint:                strings.TrimRight(envString("S3_ENDPOINT", "http://127.0.0.1:9000"), "/"),
		S3Region:                  envString("S3_REGION", "us-east-1"),
		S3AccessKeyID:             envString("S3_ACCESS_KEY_ID", "minioadmin"),
		S3SecretAccessKey:         envString("S3_SECRET_ACCESS_KEY", "minioadmin"),
		S3PublicBucket:            envString("S3_PUBLIC_BUCKET", "wutong-public"),
		S3PrivateBucket:           envString("S3_PRIVATE_BUCKET", "wutong-private"),
		S3BackupBucket:            envString("S3_BACKUP_BUCKET", "wutong-backups"),
		S3PublicBaseURL:           strings.TrimRight(envString("S3_PUBLIC_BASE_URL", "http://127.0.0.1:9000/wutong-public"), "/"),
		S3UsePathStyle:            s3PathStyle,
		CSPMediaOrigins:           mediaOrigins,
		AppTimezone:               appTimezone,
		MarketReservationTTL:      time.Duration(reservationHours) * time.Hour,
		MarketReviewBlindTTL:      time.Duration(reviewBlindDays) * 24 * time.Hour,
	}
	if c.SessionAbsolute < c.SessionSliding || c.SessionRotation > c.SessionSliding {
		return Config{}, errors.New("SESSION_ABSOLUTE_DAYS 必须不少于 SESSION_SLIDING_DAYS，且 SESSION_ROTATION_HOURS 不能超过滑动会话时长")
	}
	if !regexp.MustCompile(`^/[A-Za-z0-9/_-]*[A-Za-z0-9_-]$`).MatchString(c.APIPrefix) || strings.HasSuffix(c.APIPrefix, "/") {
		return Config{}, errors.New("API_PREFIX 必须是无尾斜杠的绝对路径")
	}
	if !validCookieName(c.SessionCookieName) || !validCookieName(c.CSRFCookieName) || c.SessionCookieName == c.CSRFCookieName {
		return Config{}, errors.New("SESSION_COOKIE_NAME 与 CSRF_COOKIE_NAME 必须是不同的合法 Cookie 名")
	}
	origin, err := url.Parse(c.PublicOrigin)
	if err != nil || origin.Scheme == "" || origin.Hostname() == "" || origin.Path != "" {
		return Config{}, errors.New("PUBLIC_ORIGIN 必须仅包含协议和主机")
	}
	if _, ok := c.TrustedHosts[strings.ToLower(origin.Hostname())]; !ok && c.Environment == "production" {
		return Config{}, errors.New("TRUSTED_HOSTS 必须包含 PUBLIC_ORIGIN 主机")
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
		if len(c.HealthCheckToken) < 24 || strings.Contains(strings.ToLower(c.HealthCheckToken), "replace-with") {
			missing = append(missing, "HEALTH_CHECK_TOKEN（至少 24 字符）")
		}
		for name, value := range map[string]string{"S3_ENDPOINT": c.S3Endpoint, "S3_ACCESS_KEY_ID": c.S3AccessKeyID, "S3_SECRET_ACCESS_KEY": c.S3SecretAccessKey, "S3_PUBLIC_BASE_URL": c.S3PublicBaseURL} {
			lower := strings.ToLower(value)
			if value == "" || strings.Contains(lower, "replace-with") || strings.Contains(lower, "example.edu.cn") {
				missing = append(missing, name)
			}
		}
		if !strings.HasPrefix(c.S3Endpoint, "https://") || !strings.HasPrefix(c.S3PublicBaseURL, "https://") {
			missing = append(missing, "S3_ENDPOINT/S3_PUBLIC_BASE_URL（HTTPS）")
		}
		for _, mediaOrigin := range c.CSPMediaOrigins {
			if !strings.HasPrefix(mediaOrigin, "https://") {
				missing = append(missing, "CSP_MEDIA_ORIGINS")
				break
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
	if parsed, err := url.Parse(c.DatabaseURL); err != nil || parsed.Host == "" || strings.Trim(parsed.Path, "/") == "" {
		return Config{}, errors.New("DATABASE_URL 无效")
	}
	if strings.HasPrefix(c.PublicOrigin, "https://") && !c.CookieSecure {
		return Config{}, errors.New("HTTPS PUBLIC_ORIGIN 必须启用 COOKIE_SECURE")
	}
	for key, value := range map[string]string{"S3_ENDPOINT": c.S3Endpoint, "S3_REGION": c.S3Region, "S3_ACCESS_KEY_ID": c.S3AccessKeyID, "S3_SECRET_ACCESS_KEY": c.S3SecretAccessKey, "S3_PUBLIC_BUCKET": c.S3PublicBucket, "S3_PRIVATE_BUCKET": c.S3PrivateBucket, "S3_BACKUP_BUCKET": c.S3BackupBucket} {
		if strings.TrimSpace(value) == "" {
			return Config{}, fmt.Errorf("%s 不能为空", key)
		}
	}
	if _, err := url.ParseRequestURI(c.S3Endpoint); err != nil {
		return Config{}, errors.New("S3_ENDPOINT 无效")
	}
	endpointURL, _ := url.Parse(c.S3Endpoint)
	if endpointURL.Scheme != "http" && endpointURL.Scheme != "https" {
		return Config{}, errors.New("S3_ENDPOINT 仅支持 http 或 https")
	}
	if c.S3PublicBucket == c.S3PrivateBucket || c.S3PublicBucket == c.S3BackupBucket || c.S3PrivateBucket == c.S3BackupBucket {
		return Config{}, errors.New("S3 public/private/backup bucket 必须互不相同")
	}
	for key, value := range map[string]string{"S3_PUBLIC_BUCKET": c.S3PublicBucket, "S3_PRIVATE_BUCKET": c.S3PrivateBucket, "S3_BACKUP_BUCKET": c.S3BackupBucket} {
		if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`).MatchString(value) {
			return Config{}, fmt.Errorf("%s 不是合法 bucket 名", key)
		}
	}
	if level := strings.ToUpper(c.LogLevel); level != "DEBUG" && level != "INFO" && level != "WARN" && level != "ERROR" {
		return Config{}, errors.New("LOG_LEVEL 必须是 DEBUG、INFO、WARN 或 ERROR")
	}
	smtpValues := []string{c.SMTPHost, c.SMTPUsername, c.SMTPPassword, c.SMTPFrom}
	configured := 0
	for _, value := range smtpValues {
		if strings.TrimSpace(value) != "" {
			configured++
		}
	}
	if configured != 0 && configured != len(smtpValues) {
		return Config{}, errors.New("SMTP_HOST、SMTP_USERNAME、SMTP_PASSWORD、SMTP_FROM 必须同时配置")
	}
	return c, nil
}

func envString(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func envInt(key string, fallback, min, max int) (int, error) {
	raw := envString(key, strconv.Itoa(fallback))
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s 必须是整数", key)
	}
	if value < min || value > max {
		return 0, fmt.Errorf("%s 必须在 %d 到 %d 之间", key, min, max)
	}
	return value, nil
}

func envBool(key string, fallback bool) (bool, error) {
	value, err := strconv.ParseBool(envString(key, strconv.FormatBool(fallback)))
	if err != nil {
		return false, fmt.Errorf("%s 必须是 true 或 false", key)
	}
	return value, nil
}

func parseCIDRs(key, value string) ([]netip.Prefix, error) {
	var result []netip.Prefix
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		prefix, err := netip.ParsePrefix(raw)
		if err != nil {
			return nil, fmt.Errorf("%s 包含无效 CIDR", key)
		}
		result = append(result, prefix.Masked())
	}
	return result, nil
}

func validCookieName(value string) bool {
	return regexp.MustCompile(`^[!#$%&'*+.^_` + "`" + `|~0-9A-Za-z-]+$`).MatchString(value)
}

func parseOrigins(key, value string) ([]string, error) {
	var origins []string
	seen := map[string]struct{}{}
	for _, raw := range strings.Fields(value) {
		parsed, err := url.ParseRequestURI(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("%s contains an invalid origin", key)
		}
		origin := parsed.Scheme + "://" + parsed.Host
		if _, ok := seen[origin]; !ok {
			seen[origin] = struct{}{}
			origins = append(origins, origin)
		}
	}
	return origins, nil
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
