package catalog

import (
	"slices"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name   string
		format string
		input  string
		want   []string
	}{
		{
			name:   "định dạng hosts",
			format: "hosts",
			input: `# Bình luận đầu file
0.0.0.0 ads.example.com
127.0.0.1 track.example.com   # chú thích cuối dòng
0.0.0.0 localhost
::1 ip6-localhost

0.0.0.0 ads.example.com`,
			want: []string{"ads.example.com", "track.example.com"},
		},
		{
			name:   "định dạng adblock",
			format: "adblock",
			input: `! Tiêu đề
||ads.example.com^
||track.example.com^$third-party
||cdn.example.com/banner.js
@@||allow.example.com^
/^ad[0-9]+\./`,
			want: []string{"ads.example.com", "track.example.com", "cdn.example.com"},
		},
		{
			name:   "danh sách thuần",
			format: "plain",
			input: `ads.example.com
track.example.com # ghi chú
không-hợp-lệ
TRACK.EXAMPLE.COM`,
			want: []string{"ads.example.com", "track.example.com"},
		},
		{
			name:   "bỏ qua dòng rác",
			format: "hosts",
			input: `<!DOCTYPE html>
<html><body>404 Not Found</body></html>`,
			want: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(strings.NewReader(tc.input), tc.format)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("Parse = %v, muốn %v", got, tc.want)
			}
		})
	}
}

// Khử trùng lặp phải giữ nguyên thứ tự xuất hiện đầu tiên, để nội dung file xuất
// bản ổn định giữa các lần đồng bộ.
func TestParseDeduplicatesStably(t *testing.T) {
	input := "0.0.0.0 b.example.com\n0.0.0.0 a.example.com\n0.0.0.0 b.example.com\n"
	got, err := Parse(strings.NewReader(input), "hosts")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"b.example.com", "a.example.com"}
	if !slices.Equal(got, want) {
		t.Errorf("Parse = %v, muốn %v", got, want)
	}
}
