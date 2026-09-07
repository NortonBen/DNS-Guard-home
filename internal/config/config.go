// Package config đọc cấu hình từ biến môi trường.
//
// Mọi giá trị đặt được qua biến môi trường với tiền tố DNSGUARD_. Những thứ liên
// quan tới hạ tầng — đường dẫn, địa chỉ lắng nghe — chỉ ở đây và không sửa được lúc
// chạy; những thứ thuộc về nghiệp vụ — trọng số, ngưỡng — nằm trong bảng settings và
// sửa được qua giao diện.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config là toàn bộ cấu hình khởi động.
type Config struct {
	DBPath      string
	Listen      string
	TZSPListen  string
	ListsDir    string
	IP2ASNPath  string
	TrancoPath  string
	QuerylogDir string

	LogRetentionDays    int
	HourlyRetentionDays int
	StagingDays         int
	ConfirmTTLDays      int
	KeepSnapshots       int

	EnrichConcurrency int
	ExternalEnabled   bool
	// HTTPAnalysisEnabled bật việc tải trang gốc của domain để phân tích. Tách riêng
	// khỏi ExternalEnabled vì nó lộ nhiều hơn: tra ASN chỉ đọc bảng cục bộ, còn tải
	// trang là gõ cửa trực tiếp máy chủ đích.
	HTTPAnalysisEnabled bool
	VTAPIKey            string
	PublishMinRatio     float64
	ListsAllowCIDR      []string

	SessionTTL   time.Duration
	AutoMigrate  bool
	LogLevel     string
	LogSQL       bool
	MetricsToken string
}

// Load đọc cấu hình, áp mặc định và kiểm tra tính hợp lệ.
func Load() (Config, error) {
	c := Config{
		DBPath:      env("DNSGUARD_DB_PATH", "/var/lib/dnsguard/dnsguard.db"),
		Listen:      env("DNSGUARD_LISTEN", ":8080"),
		TZSPListen:  env("DNSGUARD_TZSP_LISTEN", ":37008"),
		ListsDir:    env("DNSGUARD_LISTS_DIR", "/var/lib/dnsguard/lists"),
		IP2ASNPath:  env("DNSGUARD_IP2ASN_PATH", "/var/lib/dnsguard/ip2asn.tsv.gz"),
		TrancoPath:  env("DNSGUARD_TRANCO_PATH", "/var/lib/dnsguard/tranco.csv.zip"),
		QuerylogDir: env("DNSGUARD_QUERYLOG_DIR", ""),

		LogRetentionDays:    envInt("DNSGUARD_LOG_RETENTION_DAYS", 90),
		HourlyRetentionDays: envInt("DNSGUARD_HOURLY_RETENTION_DAYS", 400),
		StagingDays:         envInt("DNSGUARD_STAGING_DAYS", 7),
		ConfirmTTLDays:      envInt("DNSGUARD_CONFIRM_TTL_DAYS", 180),
		KeepSnapshots:       envInt("DNSGUARD_KEEP_SNAPSHOTS", 30),

		EnrichConcurrency: envInt("DNSGUARD_ENRICH_CONCURRENCY", 4),
		ExternalEnabled:   envBool("DNSGUARD_EXTERNAL_ENABLED", true),
		// Mặc định tắt: người vận hành phải chủ động đồng ý việc máy chủ tự đi gõ cửa
		// các domain thấy trong mạng.
		HTTPAnalysisEnabled: envBool("DNSGUARD_HTTP_ANALYSIS_ENABLED", false),
		VTAPIKey:            env("DNSGUARD_VT_API_KEY", ""),
		PublishMinRatio:     envFloat("DNSGUARD_PUBLISH_MIN_RATIO", 0.5),
		ListsAllowCIDR:      envList("DNSGUARD_LISTS_ALLOW_CIDR"),

		SessionTTL:   time.Duration(envInt("DNSGUARD_SESSION_TTL_HOURS", 168)) * time.Hour,
		AutoMigrate:  envBool("DNSGUARD_AUTO_MIGRATE", true),
		LogLevel:     env("DNSGUARD_LOG_LEVEL", "info"),
		LogSQL:       envBool("DNSGUARD_LOG_SQL", false),
		MetricsToken: env("DNSGUARD_METRICS_TOKEN", ""),
	}

	if c.PublishMinRatio < 0 || c.PublishMinRatio > 1 {
		return c, fmt.Errorf("DNSGUARD_PUBLISH_MIN_RATIO phải trong [0,1], nhận %.2f", c.PublishMinRatio)
	}
	if c.StagingDays < 0 {
		return c, fmt.Errorf("DNSGUARD_STAGING_DAYS không được âm")
	}
	if c.EnrichConcurrency < 1 {
		return c, fmt.Errorf("DNSGUARD_ENRICH_CONCURRENCY phải ≥ 1")
	}
	return c, nil
}

func env(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	if v, err := strconv.ParseFloat(env(key, ""), 64); err == nil {
		return v
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v, err := strconv.ParseBool(env(key, "")); err == nil {
		return v
	}
	return fallback
}

func envList(key string) []string {
	raw := env(key, "")
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
