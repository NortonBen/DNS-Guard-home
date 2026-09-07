package ai

import (
	"context"
	"log/slog"

	"github.com/benji/dnsguard/internal/enrich"
	"github.com/benji/dnsguard/internal/llm"
	"github.com/benji/dnsguard/internal/store"
)

// Defaults là cấu hình nhà cung cấp lấy từ biến môi trường, dùng khi giao diện
// chưa đặt gì.
//
// Nhận một struct riêng thay vì config.Config: gói ai không cần biết đường dẫn
// file hay địa chỉ lắng nghe, và một tham số hẹp hơn là một hợp đồng dễ đọc hơn.
type Defaults struct {
	BaseURL   string
	APIKey    string
	Model     string
	MaxTokens int
	BatchSize int
	// ExternalEnabled là công tắc cứng ở biến môi trường: bật trên giao diện cũng
	// không thắng được nó. Cùng quy tắc với phân tích HTTP và VirusTotal.
	ExternalEnabled bool
}

// ApplySettings đọc cấu hình đã lưu, gộp lên mặc định, rồi áp vào client đang chạy.
//
// Giá trị nhập trên giao diện thắng biến môi trường: nó mới hơn và là hành động có
// chủ ý của người quản trị, trong khi biến môi trường thường nằm trong file compose
// từ lần cài đặt đầu.
//
// Gọi được cả lúc khởi động lẫn khi người dùng bấm lưu, và luôn đọc lại toàn bộ:
// llm.Configure nhận cả bộ giá trị một lượt, nên áp từng trường sẽ sinh ra trạng
// thái nửa vời khi hai trường được sửa trong hai request liên tiếp.
func ApplySettings(ctx context.Context, db *store.Store, d Defaults, client *llm.Client,
	classifier *Classifier, registry *enrich.Registry, log *slog.Logger) error {

	baseURL, apiKey, model := d.BaseURL, d.APIKey, d.Model
	batchSize := d.BatchSize
	// Mặc định BẬT khi đã có khóa: người vừa nhập khóa xong mong nó chạy, không
	// mong phải tìm thêm một công tắc nữa.
	enabled := true

	if err := readString(ctx, db, store.SettingAIBaseURL, &baseURL); err != nil {
		return err
	}
	if err := readString(ctx, db, store.SettingAIModel, &model); err != nil {
		return err
	}
	// Khóa rỗng trong settings là lệnh gỡ khóa có chủ ý, nên nó ghi đè cả khi rỗng
	// — khác với base URL và model, nơi rỗng nghĩa là "dùng mặc định".
	var storedKey string
	if found, err := db.GetSetting(ctx, store.SettingAIAPIKey, &storedKey); err != nil {
		return err
	} else if found {
		apiKey = storedKey
	}

	var storedBatch int
	if found, err := db.GetSetting(ctx, store.SettingAIBatchSize, &storedBatch); err != nil {
		return err
	} else if found && storedBatch > 0 {
		batchSize = storedBatch
	}

	var storedEnabled bool
	if found, err := db.GetSetting(ctx, store.SettingAIEnabled, &storedEnabled); err != nil {
		return err
	} else if found {
		enabled = storedEnabled
	}

	if client != nil {
		client.Configure(baseURL, apiKey, model, d.MaxTokens)
	}
	if classifier != nil && batchSize > 0 {
		classifier.SetBatchSize(batchSize)
	}

	// Nguồn chỉ chạy khi hội đủ ba điều: công tắc cứng mở, người vận hành bật, và
	// đã có khóa. Thiếu một trong ba thì nó im lặng thay vì gọi ra ngoài và hỏng.
	effective := d.ExternalEnabled && enabled && apiKey != ""
	if registry != nil {
		registry.SetEnabled(SourceName, effective)
	}
	if log != nil {
		// Không log khóa, chỉ log việc đã nạp và trạng thái kết quả.
		log.Info("cấu hình AI", "enabled", effective, "model", model,
			"base_url", baseURL, "co_khoa", apiKey != "", "batch_size", batchSize)
	}
	return nil
}

// readString đọc một cài đặt chuỗi, giữ nguyên dest khi chưa đặt hoặc đặt rỗng.
func readString(ctx context.Context, db *store.Store, key string, dest *string) error {
	var v string
	found, err := db.GetSetting(ctx, key, &v)
	if err != nil {
		return err
	}
	if found && v != "" {
		*dest = v
	}
	return nil
}
