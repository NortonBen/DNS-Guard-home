package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

// Khóa cấu hình runtime. Chỉ những thứ sửa được qua giao diện nằm ở đây; đường dẫn
// và chuỗi kết nối thuộc về biến môi trường và không sửa được lúc chạy.
const (
	SettingWeights     = "scoring.weights"
	SettingSoftAllow   = "protect.soft_allow"
	SettingStagingDays = "lifecycle.staging_days"
	SettingConfirmTTL  = "lifecycle.confirm_ttl_days"
)

// GetSetting đọc một giá trị cấu hình vào dest. Trả về false nếu chưa được đặt.
func (s *Store) GetSetting(ctx context.Context, key string, dest any) (bool, error) {
	var raw string
	err := s.r.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read setting %q: %w", key, err)
	}
	if err := json.Unmarshal([]byte(raw), dest); err != nil {
		return false, fmt.Errorf("decode setting %q: %w", key, err)
	}
	return true, nil
}

// SetSetting ghi một giá trị cấu hình.
func (s *Store) SetSetting(ctx context.Context, key string, value any, actor string) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode setting %q: %w", key, err)
	}
	_, err = s.w.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at, updated_by) VALUES (?, ?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value,
		                                updated_at = excluded.updated_at,
		                                updated_by = excluded.updated_by`,
		key, string(raw), Now(), actor)
	if err != nil {
		return fmt.Errorf("write setting %q: %w", key, err)
	}
	return nil
}

// SoftAllowList trả về danh sách bảo vệ mềm do người dùng quản lý. Khác với danh
// sách cứng trong code, danh sách này sửa được qua giao diện.
func (s *Store) SoftAllowList(ctx context.Context) ([]string, error) {
	var list []string
	if _, err := s.GetSetting(ctx, SettingSoftAllow, &list); err != nil {
		return nil, err
	}
	return list, nil
}
