package api

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"time"

	"github.com/benji/dnsguard/internal/store"
)

// exportMaxDays chặn trên khoảng thời gian một hồ sơ được phép bao trùm.
//
// Không phải để tiết kiệm tài nguyên mà để người dùng không vô tình yêu cầu cả lịch
// sử: hồ sơ được sinh ra theo luồng, nên một khoảng quá rộng chỉ hiện ra dưới dạng
// một lượt tải kéo dài hàng chục phút rồi hỏng giữa chừng.
const exportMaxDays = 400

// manifestFile là phần mô tả một file trong hồ sơ.
type manifestFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
	Rows   int64  `json:"rows"`
}

// manifest là giấy tờ đi kèm hồ sơ điều tra.
type manifest struct {
	Tool        string         `json:"tool"`
	Version     string         `json:"version"`
	GeneratedAt string         `json:"generated_at"`
	GeneratedBy string         `json:"generated_by"`
	From        string         `json:"from"`
	To          string         `json:"to"`
	Files       []manifestFile `json:"files"`
	// Limitations đi cùng dữ liệu chứ không nằm trong tài liệu riêng. Người mở hồ sơ
	// này sáu tháng sau sẽ không có tài liệu nào trong tay, và một hồ sơ không nói rõ
	// nó *không* chứng minh điều gì là một hồ sơ dễ bị đọc sai.
	Limitations []string `json:"limitations"`
}

// countingHasher đếm số byte và tính sha256 trên đường ghi.
type countingHasher struct {
	w     io.Writer
	h     hash.Hash
	bytes int64
}

func newCountingHasher(w io.Writer) *countingHasher {
	return &countingHasher{w: w, h: sha256.New()}
}

func (c *countingHasher) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.bytes += int64(n)
	c.h.Write(p[:n])
	return n, err
}

func (c *countingHasher) sum() string { return hex.EncodeToString(c.h.Sum(nil)) }

// handleForensicsExport đóng gói log điều tra của một khoảng thời gian thành file zip.
//
// Sinh theo luồng, không dựng cả hồ sơ trong bộ nhớ: một khoảng vài tháng có thể là
// hàng chục triệu dòng.
//
// Băm được tính **trong lúc ghi**, nên manifest phải là file cuối cùng trong zip. Đó
// cũng là lý do hồ sơ hỏng giữa chừng vẫn an toàn: không có manifest và không có mục
// lục trung tâm hợp lệ, mọi công cụ giải nén đều báo hỏng thay vì đưa ra một hồ sơ
// thiếu dữ liệu mà trông như đầy đủ.
func (s *Server) handleForensicsExport(w http.ResponseWriter, r *http.Request) {
	from, to, ok := parseExportRange(w, r)
	if !ok {
		return
	}

	actor := "unknown"
	if sess, ok := sessionFrom(r.Context()); ok {
		actor = sess.Username
	}

	name := fmt.Sprintf("dnsguard-forensics-%s.zip", time.Now().UTC().Format("20060102-150405"))
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)

	zw := zip.NewWriter(w)
	m := manifest{
		Tool:        "DNSGuard",
		Version:     s.version,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		GeneratedBy: actor,
		From:        from,
		To:          to,
		Limitations: exportLimitations,
	}

	ctx := r.Context()
	queries, err := writeNDJSON(zw, "queries.ndjson", func(enc *json.Encoder) (int64, error) {
		var n int64
		err := s.store.ExportQueries(ctx, from, to, func(q store.ExportQuery) error {
			n++
			return enc.Encode(q)
		})
		return n, err
	})
	if err != nil {
		s.abortExport(zw, err)
		return
	}
	m.Files = append(m.Files, queries)

	resolutions, err := writeNDJSON(zw, "resolutions.ndjson", func(enc *json.Encoder) (int64, error) {
		var n int64
		err := s.store.ExportResolutions(ctx, from, to, func(res store.ExportResolution) error {
			n++
			return enc.Encode(res)
		})
		return n, err
	})
	if err != nil {
		s.abortExport(zw, err)
		return
	}
	m.Files = append(m.Files, resolutions)

	mw, err := zw.Create("manifest.json")
	if err != nil {
		s.abortExport(zw, err)
		return
	}
	enc := json.NewEncoder(mw)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		s.abortExport(zw, err)
		return
	}

	if err := zw.Close(); err != nil {
		s.log.Error("đóng hồ sơ điều tra thất bại", "err", err)
	}
}

// writeNDJSON ghi một file NDJSON vào zip và trả về mô tả của nó.
func writeNDJSON(zw *zip.Writer, name string, emit func(*json.Encoder) (int64, error)) (manifestFile, error) {
	f, err := zw.Create(name)
	if err != nil {
		return manifestFile{}, fmt.Errorf("tạo %s trong hồ sơ: %w", name, err)
	}
	ch := newCountingHasher(f)
	rows, err := emit(json.NewEncoder(ch))
	if err != nil {
		return manifestFile{}, err
	}
	return manifestFile{Name: name, SHA256: ch.sum(), Bytes: ch.bytes, Rows: rows}, nil
}

// abortExport bỏ dở hồ sơ đang gửi.
//
// Không gọi zw.Close(): thiếu mục lục trung tâm khiến mọi công cụ giải nén báo file
// hỏng. Đó chính là điều cần — trạng thái xấu nhất là một hồ sơ thiếu dữ liệu mà mở ra
// vẫn thấy bình thường. Không sửa được mã trạng thái nữa vì header đã gửi đi rồi.
func (s *Server) abortExport(zw *zip.Writer, err error) {
	s.log.Error("sinh hồ sơ điều tra thất bại, gửi đi file hỏng có chủ ý", "err", err)
}

// exportLimitations là những gì hồ sơ này *không* chứng minh.
var exportLimitations = []string{
	"Dữ liệu bắt nguồn từ luồng DNS mà thiết bị mạng mirror sang. Nó cho biết thiết bị nào đã phân giải tên nào, lúc nào.",
	"Một lượt phân giải KHÔNG chứng minh có kết nối tới địa chỉ đó. Luồng mirror không mang lưu lượng kết nối.",
	"Thiết bị dùng DNS mã hoá (DoH/DoT) không xuất hiện trong hồ sơ này.",
	"Ánh xạ domain → địa chỉ chỉ đúng trong khoảng first_seen..last_seen của chính nó; ngoài khoảng đó cùng tên có thể trỏ nơi khác.",
	"Trường threat là kết quả đối chiếu với danh sách bên thứ ba tại thời điểm quét gần nhất, không phải kết luận điều tra.",
}

// parseExportRange đọc và kiểm tra khoảng thời gian yêu cầu.
func parseExportRange(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	q := r.URL.Query()
	from, err := time.Parse(time.RFC3339, q.Get("from"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput,
			"Tham số from phải là thời điểm RFC3339", nil)
		return "", "", false
	}
	to, err := time.Parse(time.RFC3339, q.Get("to"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeInvalidInput,
			"Tham số to phải là thời điểm RFC3339", nil)
		return "", "", false
	}
	if !to.After(from) {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			"Mốc to phải sau mốc from", nil)
		return "", "", false
	}
	if to.Sub(from) > exportMaxDays*24*time.Hour {
		writeError(w, http.StatusUnprocessableEntity, CodeValidation,
			fmt.Sprintf("Khoảng thời gian tối đa là %d ngày", exportMaxDays), nil)
		return "", "", false
	}
	return store.TimeAt(from), store.TimeAt(to), true
}
