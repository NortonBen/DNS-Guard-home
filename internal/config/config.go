// Package config đọc cấu hình từ biến môi trường.
//
// Mọi giá trị đặt được qua biến môi trường với tiền tố DNSGUARD_. Những thứ liên
// quan tới hạ tầng — đường dẫn, địa chỉ lắng nghe — chỉ ở đây và không sửa được lúc
// chạy; những thứ thuộc về nghiệp vụ — trọng số, ngưỡng — nằm trong bảng settings và
// sửa được qua giao diện.
package config

import (
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config là toàn bộ cấu hình khởi động.
type Config struct {
	DBPath     string
	Listen     string
	TZSPListen string
	ListsDir   string
	IP2ASNPath string
	TrancoPath string
	// DROPPath là nơi lưu danh sách dải hạ tầng rogue (Spamhaus DROP) tải về.
	DROPPath string
	// ThreatFoxPath là nơi lưu danh sách máy chủ điều khiển mã độc tải về.
	ThreatFoxPath string

	// Ghi pcap phục vụ điều tra. Mặc định tắt: nó ghi liên tục xuống đĩa, và trên
	// một máy chạy thẻ SD thì đó là quyết định người vận hành phải tự đưa ra.
	PcapEnabled    bool
	PcapDir        string
	PcapMaxFileMB  int
	PcapMaxTotalMB int
	QuerylogDir    string

	LogRetentionDays    int
	HourlyRetentionDays int
	StagingDays         int
	ConfirmTTLDays      int
	KeepSnapshots       int
	// ResourceSampleEvery là nhịp đo tài nguyên. Mười giây đủ để thấy đỉnh ngắn mà
	// không tạo ra lượng ghi đáng kể.
	ResourceSampleEvery time.Duration
	ResourceRetainDays  int

	EnrichConcurrency int
	ExternalEnabled   bool
	// HTTPAnalysisEnabled bật việc tải trang gốc của domain để phân tích. Tách riêng
	// khỏi ExternalEnabled vì nó lộ nhiều hơn: tra ASN chỉ đọc bảng cục bộ, còn tải
	// trang là gõ cửa trực tiếp máy chủ đích.
	HTTPAnalysisEnabled bool
	VTAPIKey            string
	PublishMinRatio     float64
	// PublishSink là địa chỉ mặc định trong file hosts, dùng khi giao diện chưa đặt.
	PublishSink    string
	ListsAllowCIDR []string

	// AIDBPath là file nhật ký AI — tách hẳn khỏi CSDL chính. Rỗng nghĩa là đặt
	// cạnh CSDL chính với hậu tố -ai.
	AIDBPath string
	// Cấu hình nhà cung cấp model. Mọi endpoint nói được giao thức Chat Completions
	// đều dùng được; mặc định trỏ DeepSeek vì nó rẻ nhất trong nhóm đủ tốt cho việc
	// phân loại tên miền.
	AIBaseURL string
	AIAPIKey  string
	AIModel   string
	// AIMaxTokens: model dạng reasoning tiêu token cho cả phần suy luận, nên cần
	// nới rộng hơn. 0 nghĩa là để gói llm dùng mặc định của nó.
	AIMaxTokens int
	// AIBatchSize là số domain hỏi trong một lượt gọi.
	AIBatchSize int
	// AIHistoryRetainDays là thời gian giữ nhật ký lượt gọi.
	AIHistoryRetainDays int

	SessionTTL   time.Duration
	AutoMigrate  bool
	LogLevel     string
	LogSQL       bool
	MetricsToken string
}

// Load đọc cấu hình, áp mặc định và kiểm tra tính hợp lệ.
func Load() (Config, error) {
	c := Config{
		DBPath:        env("DNSGUARD_DB_PATH", "/var/lib/dnsguard/dnsguard.db"),
		Listen:        env("DNSGUARD_LISTEN", ":8080"),
		TZSPListen:    env("DNSGUARD_TZSP_LISTEN", ":37008"),
		ListsDir:      env("DNSGUARD_LISTS_DIR", "/var/lib/dnsguard/lists"),
		IP2ASNPath:    env("DNSGUARD_IP2ASN_PATH", "/var/lib/dnsguard/ip2asn.tsv.gz"),
		TrancoPath:    env("DNSGUARD_TRANCO_PATH", "/var/lib/dnsguard/tranco.csv.zip"),
		DROPPath:      env("DNSGUARD_DROP_PATH", "/var/lib/dnsguard/spamhaus-drop.txt"),
		ThreatFoxPath: env("DNSGUARD_THREATFOX_PATH", "/var/lib/dnsguard/threatfox.csv.zip"),

		PcapEnabled:    envBool("DNSGUARD_PCAP_ENABLED", false),
		PcapDir:        env("DNSGUARD_PCAP_DIR", "/var/lib/dnsguard/pcap"),
		PcapMaxFileMB:  envInt("DNSGUARD_PCAP_MAX_FILE_MB", 64),
		PcapMaxTotalMB: envInt("DNSGUARD_PCAP_MAX_TOTAL_MB", 2048),
		QuerylogDir:    env("DNSGUARD_QUERYLOG_DIR", ""),

		LogRetentionDays:    envInt("DNSGUARD_LOG_RETENTION_DAYS", 90),
		HourlyRetentionDays: envInt("DNSGUARD_HOURLY_RETENTION_DAYS", 400),
		StagingDays:         envInt("DNSGUARD_STAGING_DAYS", 7),
		ConfirmTTLDays:      envInt("DNSGUARD_CONFIRM_TTL_DAYS", 180),
		KeepSnapshots:       envInt("DNSGUARD_KEEP_SNAPSHOTS", 30),
		ResourceSampleEvery: time.Duration(envInt("DNSGUARD_RESOURCE_SAMPLE_SECONDS", 10)) * time.Second,
		ResourceRetainDays:  envInt("DNSGUARD_RESOURCE_RETAIN_DAYS", 30),

		EnrichConcurrency: envInt("DNSGUARD_ENRICH_CONCURRENCY", 4),
		ExternalEnabled:   envBool("DNSGUARD_EXTERNAL_ENABLED", true),
		// Mặc định tắt: người vận hành phải chủ động đồng ý việc máy chủ tự đi gõ cửa
		// các domain thấy trong mạng.
		HTTPAnalysisEnabled: envBool("DNSGUARD_HTTP_ANALYSIS_ENABLED", false),
		VTAPIKey:            env("DNSGUARD_VT_API_KEY", ""),
		PublishMinRatio:     envFloat("DNSGUARD_PUBLISH_MIN_RATIO", 0.5),
		PublishSink:         env("DNSGUARD_PUBLISH_SINK", "0.0.0.0"),
		ListsAllowCIDR:      envList("DNSGUARD_LISTS_ALLOW_CIDR"),

		AIDBPath:            env("DNSGUARD_AI_DB_PATH", ""),
		AIBaseURL:           env("DNSGUARD_AI_BASE_URL", "https://api.deepseek.com/v1"),
		AIAPIKey:            env("DNSGUARD_AI_API_KEY", ""),
		AIModel:             env("DNSGUARD_AI_MODEL", "deepseek-chat"),
		AIMaxTokens:         envInt("DNSGUARD_AI_MAX_TOKENS", 0),
		AIBatchSize:         envInt("DNSGUARD_AI_BATCH_SIZE", 40),
		AIHistoryRetainDays: envInt("DNSGUARD_AI_HISTORY_RETAIN_DAYS", 90),

		SessionTTL:   time.Duration(envInt("DNSGUARD_SESSION_TTL_HOURS", 168)) * time.Hour,
		AutoMigrate:  envBool("DNSGUARD_AUTO_MIGRATE", true),
		LogLevel:     env("DNSGUARD_LOG_LEVEL", "info"),
		LogSQL:       envBool("DNSGUARD_LOG_SQL", false),
		MetricsToken: env("DNSGUARD_METRICS_TOKEN", ""),
	}

	if _, err := netip.ParseAddr(c.PublishSink); err != nil {
		return c, fmt.Errorf("DNSGUARD_PUBLISH_SINK phải là địa chỉ IP, nhận %q", c.PublishSink)
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
	if c.AIBatchSize < 1 || c.AIBatchSize > 200 {
		return c, fmt.Errorf("DNSGUARD_AI_BATCH_SIZE phải trong [1,200], nhận %d", c.AIBatchSize)
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
