package api

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/benji/dnsguard/internal/ingest"
)

// seedForensics nạp một truy vấn và một lượt phân giải vào CSDL thử.
func seedForensics(t *testing.T, h *harness, at time.Time) {
	t.Helper()
	ctx := context.Background()

	if err := h.store.WriteEvents(ctx, []ingest.Event{{
		Domain: "ads.example.com", ETLD1: "example.com",
		Client: "192.168.88.20", QType: ingest.TypeA, At: at,
	}}); err != nil {
		t.Fatalf("WriteEvents: %v", err)
	}
	if err := h.store.WriteResolutions(ctx, []ingest.Resolution{{
		Domain: "ads.example.com", ETLD1: "example.com",
		IPs: []netip.Addr{netip.MustParseAddr("104.21.5.6")}, TTL: 300, At: at,
	}}); err != nil {
		t.Fatalf("WriteResolutions: %v", err)
	}
}

// readZip đọc toàn bộ hồ sơ trả về thành map tên file → nội dung.
func readZip(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("hồ sơ không phải file zip hợp lệ: %v", err)
	}
	out := make(map[string][]byte, len(zr.File))
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("mở %s trong hồ sơ: %v", f.Name, err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("đọc %s trong hồ sơ: %v", f.Name, err)
		}
		out[f.Name] = data
	}
	return out
}

func TestForensicsExport(t *testing.T) {
	h := newHarness(t)
	h.login()

	at := time.Now().UTC().Add(-time.Hour)
	seedForensics(t, h, at)

	from := at.Add(-24 * time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)

	resp, body := h.do(http.MethodGet, "/api/v1/forensics/export?from="+from+"&to="+to, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trạng thái = %d, muốn 200. Thân: %s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q, muốn application/zip", ct)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, ".zip") {
		t.Errorf("Content-Disposition = %q, thiếu tên file", cd)
	}

	files := readZip(t, body)
	for _, want := range []string{"queries.ndjson", "resolutions.ndjson", "manifest.json"} {
		if _, ok := files[want]; !ok {
			t.Fatalf("hồ sơ thiếu %s", want)
		}
	}

	var m manifest
	if err := json.Unmarshal(files["manifest.json"], &m); err != nil {
		t.Fatalf("manifest không đọc được: %v", err)
	}
	if m.Tool != "DNSGuard" || m.GeneratedBy != "admin" {
		t.Errorf("manifest = tool %q, by %q; muốn DNSGuard, admin", m.Tool, m.GeneratedBy)
	}
	if len(m.Limitations) == 0 {
		t.Error("manifest không ghi giới hạn của dữ liệu")
	}
	if len(m.Files) != 2 {
		t.Fatalf("manifest mô tả %d file, muốn 2", len(m.Files))
	}

	// Băm trong manifest phải khớp nội dung thật. Đây là toàn bộ giá trị của manifest:
	// nếu nó sai thì hồ sơ không chứng minh được gì về tính toàn vẹn của chính nó.
	for _, mf := range m.Files {
		content, ok := files[mf.Name]
		if !ok {
			t.Errorf("manifest nhắc tới %s nhưng hồ sơ không có", mf.Name)
			continue
		}
		sum := sha256.Sum256(content)
		if got := hex.EncodeToString(sum[:]); got != mf.SHA256 {
			t.Errorf("%s: sha256 trong manifest %s, thật %s", mf.Name, mf.SHA256, got)
		}
		if int64(len(content)) != mf.Bytes {
			t.Errorf("%s: manifest ghi %d byte, thật %d", mf.Name, mf.Bytes, len(content))
		}
		if mf.Rows != 1 {
			t.Errorf("%s: manifest ghi %d dòng, muốn 1", mf.Name, mf.Rows)
		}
	}

	// Nội dung phải là NDJSON đọc được, không phải mảng JSON.
	var q map[string]any
	firstLine, _, _ := bytes.Cut(files["queries.ndjson"], []byte("\n"))
	if err := json.Unmarshal(firstLine, &q); err != nil {
		t.Fatalf("dòng truy vấn không phải JSON: %v", err)
	}
	if q["client"] != "192.168.88.20" || q["domain"] != "ads.example.com" {
		t.Errorf("dòng truy vấn = %v", q)
	}
}

// Dữ liệu ngoài khoảng yêu cầu không được lọt vào hồ sơ.
func TestForensicsExportRespectsRange(t *testing.T) {
	h := newHarness(t)
	h.login()
	seedForensics(t, h, time.Now().UTC().Add(-48*time.Hour))

	from := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	to := time.Now().UTC().Format(time.RFC3339)

	resp, body := h.do(http.MethodGet, "/api/v1/forensics/export?from="+from+"&to="+to, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("trạng thái = %d, muốn 200", resp.StatusCode)
	}

	files := readZip(t, body)
	if n := len(bytes.TrimSpace(files["queries.ndjson"])); n != 0 {
		t.Errorf("truy vấn ngoài khoảng lại lọt vào hồ sơ: %s", files["queries.ndjson"])
	}
}

func TestForensicsExportRejectsBadRange(t *testing.T) {
	h := newHarness(t)
	h.login()

	now := time.Now().UTC()
	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"thiếu tham số", "", http.StatusBadRequest},
		{"from không phải RFC3339", "?from=hôm-qua&to=" + now.Format(time.RFC3339), http.StatusBadRequest},
		{"to trước from", "?from=" + now.Format(time.RFC3339) +
			"&to=" + now.Add(-time.Hour).Format(time.RFC3339), http.StatusUnprocessableEntity},
		{"khoảng quá rộng", "?from=" + now.Add(-500*24*time.Hour).Format(time.RFC3339) +
			"&to=" + now.Format(time.RFC3339), http.StatusUnprocessableEntity},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, _ := h.do(http.MethodGet, "/api/v1/forensics/export"+tc.query, nil)
			if resp.StatusCode != tc.want {
				t.Errorf("trạng thái = %d, muốn %d", resp.StatusCode, tc.want)
			}
		})
	}
}
