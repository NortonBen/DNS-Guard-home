package ingest

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// answerRR là một bản ghi trong phần trả lời. name rỗng nghĩa là dùng con trỏ nén
// trỏ về tên trong phần câu hỏi — đúng như resolver thật vẫn làm.
type answerRR struct {
	name   string
	rrType uint16
	ttl    uint32
	rdata  []byte
}

// buildDNSAnswer dựng một thông điệp trả lời DNS hoàn chỉnh.
func buildDNSAnswer(t *testing.T, qname string, qtype uint16, rrs []answerRR) []byte {
	t.Helper()

	msg := buildDNSQuery(t, qname, qtype, true)
	binary.BigEndian.PutUint16(msg[6:8], uint16(len(rrs))) // ANCOUNT

	for _, rr := range rrs {
		if rr.name == "" {
			msg = append(msg, 0xC0, byte(dnsHeaderLen)) // con trỏ về tên câu hỏi
		} else {
			msg = append(msg, encodeName(rr.name)...)
		}
		msg = binary.BigEndian.AppendUint16(msg, rr.rrType)
		msg = binary.BigEndian.AppendUint16(msg, 1) // CLASS IN
		msg = binary.BigEndian.AppendUint32(msg, rr.ttl)
		msg = binary.BigEndian.AppendUint16(msg, uint16(len(rr.rdata)))
		msg = append(msg, rr.rdata...)
	}
	return msg
}

func encodeName(name string) []byte {
	var out []byte
	for _, label := range strings.Split(name, ".") {
		out = append(out, byte(len(label)))
		out = append(out, label...)
	}
	return append(out, 0x00)
}

func ipRData(t *testing.T, s string) []byte {
	t.Helper()
	return netip.MustParseAddr(s).AsSlice()
}

func TestParseAnswer(t *testing.T) {
	tests := []struct {
		name    string
		msg     []byte
		wantDom string
		wantIPs []string
		wantTTL uint32
	}{
		{
			name: "một bản ghi A",
			msg: buildDNSAnswer(t, "ads.example.com", TypeA, []answerRR{
				{rrType: TypeA, ttl: 300, rdata: ipRData(t, "104.21.5.6")},
			}),
			wantDom: "ads.example.com", wantIPs: []string{"104.21.5.6"}, wantTTL: 300,
		},
		{
			name: "nhiều bản ghi A, lấy TTL nhỏ nhất",
			msg: buildDNSAnswer(t, "cdn.example.com", TypeA, []answerRR{
				{rrType: TypeA, ttl: 300, rdata: ipRData(t, "1.1.1.1")},
				{rrType: TypeA, ttl: 60, rdata: ipRData(t, "1.0.0.1")},
			}),
			wantDom: "cdn.example.com", wantIPs: []string{"1.1.1.1", "1.0.0.1"}, wantTTL: 60,
		},
		{
			name: "bản ghi AAAA",
			msg: buildDNSAnswer(t, "v6.example.com", TypeAAAA, []answerRR{
				{rrType: TypeAAAA, ttl: 120, rdata: ipRData(t, "2606:4700::1111")},
			}),
			wantDom: "v6.example.com", wantIPs: []string{"2606:4700::1111"}, wantTTL: 120,
		},
		{
			// Bản ghi A mang tên đích của chuỗi CNAME, không phải tên được hỏi. Địa
			// chỉ vẫn phải quy về tên được hỏi, vì đó là tên nằm trong bảng domains.
			name: "chuỗi CNAME quy về tên được hỏi",
			msg: buildDNSAnswer(t, "www.example.com", TypeA, []answerRR{
				{rrType: 5, ttl: 300, rdata: encodeName("cdn.example.net")},
				{name: "cdn.example.net", rrType: TypeA, ttl: 90, rdata: ipRData(t, "203.0.113.7")},
			}),
			wantDom: "www.example.com", wantIPs: []string{"203.0.113.7"}, wantTTL: 90,
		},
		{
			name: "bỏ qua bản ghi không phải địa chỉ",
			msg: buildDNSAnswer(t, "mix.example.com", TypeA, []answerRR{
				{rrType: 16, ttl: 300, rdata: []byte{3, 'a', 'b', 'c'}}, // TXT
				{rrType: TypeA, ttl: 45, rdata: ipRData(t, "198.51.100.9")},
			}),
			wantDom: "mix.example.com", wantIPs: []string{"198.51.100.9"}, wantTTL: 45,
		},
		{
			name: "chữ hoa trong tên được hạ xuống",
			msg: buildDNSAnswer(t, "ADS.Example.COM", TypeA, []answerRR{
				{rrType: TypeA, ttl: 30, rdata: ipRData(t, "192.0.2.1")},
			}),
			wantDom: "ads.example.com", wantIPs: []string{"192.0.2.1"}, wantTTL: 30,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dom, ips, ttl, err := parseAnswer(tc.msg)
			if err != nil {
				t.Fatalf("parseAnswer: %v", err)
			}
			if dom != tc.wantDom {
				t.Errorf("domain = %q, muốn %q", dom, tc.wantDom)
			}
			if ttl != tc.wantTTL {
				t.Errorf("ttl = %d, muốn %d", ttl, tc.wantTTL)
			}
			got := make([]string, len(ips))
			for i, ip := range ips {
				got[i] = ip.String()
			}
			if strings.Join(got, ",") != strings.Join(tc.wantIPs, ",") {
				t.Errorf("ips = %v, muốn %v", got, tc.wantIPs)
			}
		})
	}
}

// Câu trả lời không dùng được phải bị từ chối gọn gàng, không được làm sập tiến trình.
func TestParseAnswerRejects(t *testing.T) {
	valid := buildDNSAnswer(t, "ads.example.com", TypeA, []answerRR{
		{rrType: TypeA, ttl: 300, rdata: ipRData(t, "104.21.5.6")},
	})

	// RDLENGTH khai dài hơn phần còn lại của gói.
	overrun := buildDNSAnswer(t, "bad.example.com", TypeA, []answerRR{
		{rrType: TypeA, ttl: 300, rdata: ipRData(t, "1.2.3.4")},
	})
	overrun[len(overrun)-6] = 0xFF

	tests := []struct {
		name string
		msg  []byte
	}{
		{"rỗng", nil},
		{"ngắn hơn header", []byte{0x01, 0x02, 0x03}},
		{"là truy vấn chứ không phải trả lời", buildDNSQuery(t, "ads.example.com", TypeA, false)},
		{"không có bản ghi trả lời", buildDNSAnswer(t, "nx.example.com", TypeA, nil)},
		{"chỉ có CNAME, không có địa chỉ", buildDNSAnswer(t, "c.example.com", TypeA, []answerRR{
			{rrType: 5, ttl: 300, rdata: encodeName("target.example.net")},
		})},
		{"RDLENGTH vượt quá gói", overrun},
	}
	for i := 1; i < len(valid); i += 5 {
		tests = append(tests, struct {
			name string
			msg  []byte
		}{"gói hợp lệ bị cắt cụt", valid[:i]})
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, ips, _, err := parseAnswer(tc.msg); err == nil {
				t.Errorf("gói không dùng được lại được chấp nhận, ips = %v", ips)
			}
		})
	}
}

// Gói trả lời phải đi qua được tầng TZSP/UDP và được đánh dấu đúng chiều.
func TestDecodeTZSPAcceptsAnswers(t *testing.T) {
	router := netip.MustParseAddr("192.168.88.1")
	msg := buildDNSAnswer(t, "ads.example.com", TypeA, []answerRR{
		{rrType: TypeA, ttl: 300, rdata: ipRData(t, "104.21.5.6")},
	})

	pkt, err := decodeTZSP(buildTZSPPorts(t, router, dnsPort, 40000, msg, 0))
	if err != nil {
		t.Fatalf("decodeTZSP gói trả lời: %v", err)
	}
	if !pkt.answer {
		t.Fatal("gói trả lời không được đánh dấu là answer")
	}
	// IP nguồn của câu trả lời là resolver, không phải client. Đặt nó vào client sẽ
	// quy mọi lượt phân giải upstream cho chính router.
	if pkt.client.IsValid() {
		t.Errorf("gói trả lời không được mang client, có %v", pkt.client)
	}

	dom, ips, _, err := parseAnswer(pkt.payload)
	if err != nil {
		t.Fatalf("parseAnswer sau khi bóc TZSP: %v", err)
	}
	if dom != "ads.example.com" || len(ips) != 1 || ips[0].String() != "104.21.5.6" {
		t.Errorf("bóc ra %q → %v", dom, ips)
	}
}

// Gói truy vấn phải giữ nguyên hành vi cũ: có client, không bị đánh dấu answer.
func TestDecodeTZSPQueryUnchanged(t *testing.T) {
	client := netip.MustParseAddr("192.168.88.20")
	pkt, err := decodeTZSP(buildTZSP(t, client, dnsPort,
		buildDNSQuery(t, "ads.example.com", TypeA, false), 0))
	if err != nil {
		t.Fatalf("decodeTZSP: %v", err)
	}
	if pkt.answer {
		t.Error("gói truy vấn bị đánh dấu là answer")
	}
	if pkt.client != client {
		t.Errorf("client = %v, muốn %v", pkt.client, client)
	}
}

// Gói không liên quan tới cổng 53 vẫn phải bị loại ở cả hai chiều.
func TestDecodeTZSPRejectsNonDNSPorts(t *testing.T) {
	host := netip.MustParseAddr("192.168.88.20")
	raw := buildTZSPPorts(t, host, 40000, 443, []byte{0xDE, 0xAD}, 0)
	if _, err := decodeTZSP(raw); err == nil {
		t.Error("gói không phải DNS được chấp nhận")
	}
}

// Con trỏ nén trỏ về chính nó không được làm treo tiến trình.
//
// skipName cố ý không đi theo con trỏ — nó chỉ cần biết tên kết thúc ở đâu, mà một
// con trỏ luôn là hai byte cuối của tên trên dây. Nhờ vậy không có bước nhảy nào để
// lặp. Gói vì thế vẫn đọc được bình thường, và điều đó không sao: địa chỉ được quy
// về tên trong phần câu hỏi chứ không phải tên chủ của bản ghi, nên tên dị dạng ở
// đó không ảnh hưởng tới kết quả.
//
// Test này tồn tại để nếu sau này có ai cho skipName đi theo con trỏ, việc đó phải
// kèm bộ đếm số lần nhảy — nếu không, một gói mười byte từ mạng sẽ treo bộ nhận.
func TestParseAnswerSelfReferentialPointerTerminates(t *testing.T) {
	msg := buildDNSQuery(t, "loop.example.com", TypeA, true)
	binary.BigEndian.PutUint16(msg[6:8], 1) // ANCOUNT
	msg = append(msg, 0xC0, byte(len(msg))) // con trỏ trỏ về chính nó
	msg = append(msg,
		0x00, 0x01, // TYPE A
		0x00, 0x01, // CLASS IN
		0, 0, 1, 44, // TTL 300
		0x00, 0x04, // RDLENGTH
		1, 2, 3, 4,
	)

	done := make(chan struct{})
	go func() {
		defer close(done)
		parseAnswer(msg)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("parseAnswer treo trên con trỏ nén tự trỏ")
	}
}
