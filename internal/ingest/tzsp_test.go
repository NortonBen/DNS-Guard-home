package ingest

import (
	"encoding/binary"
	"net/netip"
	"strings"
	"testing"
)

// buildTZSP dựng một datagram TZSP hoàn chỉnh bọc quanh một truy vấn DNS, giống
// những gì MikroTik gửi sang. Bóc gói là chỗ dễ sai và khó gỡ lỗi trên dữ liệu thật,
// nên dựng gói tổng hợp trong test là cách rẻ nhất để bắt lỗi sớm.
func buildTZSP(t *testing.T, src netip.Addr, dstPort uint16, dns []byte, vlans int) []byte {
	t.Helper()

	udp := make([]byte, 8+len(dns))
	binary.BigEndian.PutUint16(udp[0:2], 40000) // cổng nguồn
	binary.BigEndian.PutUint16(udp[2:4], dstPort)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp)))
	copy(udp[8:], dns)

	var l3 []byte
	var etherType uint16
	if src.Is4() {
		ip := make([]byte, 20+len(udp))
		ip[0] = 0x45 // version 4, IHL 5
		binary.BigEndian.PutUint16(ip[2:4], uint16(len(ip)))
		ip[8] = 64 // TTL
		ip[9] = protoUDP
		copy(ip[12:16], src.AsSlice())
		copy(ip[16:20], []byte{192, 168, 88, 1}) // router
		copy(ip[20:], udp)
		l3, etherType = ip, ethTypeIPv4
	} else {
		ip := make([]byte, 40+len(udp))
		ip[0] = 0x60 // version 6
		binary.BigEndian.PutUint16(ip[4:6], uint16(len(udp)))
		ip[6] = protoUDP
		ip[7] = 64
		copy(ip[8:24], src.AsSlice())
		copy(ip[40:], udp)
		l3, etherType = ip, ethTypeIPv6
	}

	eth := make([]byte, 0, ethHeaderLen+4*vlans+len(l3))
	eth = append(eth, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55) // MAC đích
	eth = append(eth, 0x66, 0x77, 0x88, 0x99, 0xAA, 0xBB) // MAC nguồn
	for range vlans {
		eth = append(eth, 0x81, 0x00, 0x00, 0x64) // thẻ VLAN, id 100
	}
	eth = binary.BigEndian.AppendUint16(eth, etherType)
	eth = append(eth, l3...)

	tzsp := []byte{tzspVersion, 0x00, 0x00, tzspEncapEther}
	tzsp = append(tzsp, 0x02, 0x02, 0xAA, 0xBB) // thẻ RAWRSSI, dài 2 byte
	tzsp = append(tzsp, tzspTagEnd)
	return append(tzsp, eth...)
}

// buildDNSQuery dựng phần câu hỏi của một thông điệp DNS.
func buildDNSQuery(t *testing.T, name string, qtype uint16, response bool) []byte {
	t.Helper()

	msg := make([]byte, dnsHeaderLen)
	binary.BigEndian.PutUint16(msg[0:2], 0x1234) // ID
	if response {
		msg[2] |= 0x80
	}
	binary.BigEndian.PutUint16(msg[4:6], 1) // QDCOUNT

	for _, label := range strings.Split(name, ".") {
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	msg = append(msg, 0x00)
	msg = binary.BigEndian.AppendUint16(msg, qtype)
	return binary.BigEndian.AppendUint16(msg, 1) // QCLASS IN
}

func TestDecodeTZSP(t *testing.T) {
	client4 := netip.MustParseAddr("192.168.88.20")
	client6 := netip.MustParseAddr("fd00::20")

	tests := []struct {
		name       string
		src        netip.Addr
		dstPort    uint16
		query      string
		qtype      uint16
		isResponse bool
		vlans      int
		wantDomain string
		wantOK     bool
	}{
		{
			name: "truy vấn IPv4 thông thường", src: client4, dstPort: dnsPort,
			query: "ads.example.com", qtype: TypeA,
			wantDomain: "ads.example.com", wantOK: true,
		},
		{
			name: "truy vấn IPv6", src: client6, dstPort: dnsPort,
			query: "metrics.trangweb.vn", qtype: TypeAAAA,
			wantDomain: "metrics.trangweb.vn", wantOK: true,
		},
		{
			name: "khung có thẻ VLAN", src: client4, dstPort: dnsPort,
			query: "cdn.example.com", qtype: TypeHTTPS, vlans: 1,
			wantDomain: "cdn.example.com", wantOK: true,
		},
		{
			name: "khung QinQ hai lớp VLAN", src: client4, dstPort: dnsPort,
			query: "a.example.com", qtype: TypeA, vlans: 2,
			wantDomain: "a.example.com", wantOK: true,
		},
		{
			// Tên miền hoa thường lẫn lộn phải gộp về cùng một domain: DNS không
			// phân biệt hoa thường, và CSDL có ràng buộc name = lower(name).
			name: "tên miền viết hoa được hạ chữ thường", src: client4, dstPort: dnsPort,
			query: "ADS.Example.COM", qtype: TypeA,
			wantDomain: "ads.example.com", wantOK: true,
		},
		{
			// Câu trả lời mang IP nguồn của router, không phải của client.
			name: "bỏ qua câu trả lời từ cổng 53", src: client4, dstPort: 40000,
			query: "ads.example.com", qtype: TypeA, isResponse: true,
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dns := buildDNSQuery(t, tc.query, tc.qtype, tc.isResponse)
			raw := buildTZSP(t, tc.src, tc.dstPort, dns, tc.vlans)

			pkt, err := decodeTZSP(raw)
			if !tc.wantOK {
				if err == nil {
					t.Fatal("muốn lỗi, nhưng bóc tách thành công")
				}
				return
			}
			if err != nil {
				t.Fatalf("decodeTZSP: %v", err)
			}
			if pkt.client != tc.src {
				t.Errorf("client = %v, muốn %v", pkt.client, tc.src)
			}

			name, qtype, err := parseQuestion(pkt.payload)
			if err != nil {
				t.Fatalf("parseQuestion: %v", err)
			}
			if name != tc.wantDomain {
				t.Errorf("domain = %q, muốn %q", name, tc.wantDomain)
			}
			if qtype != tc.qtype {
				t.Errorf("qtype = %d, muốn %d", qtype, tc.qtype)
			}
		})
	}
}

// Gói méo không được làm sập tiến trình. Cổng mirror nhận đủ thứ rác và không có
// cách nào kiểm soát đầu vào.
func TestDecodeTZSPRejectsMalformed(t *testing.T) {
	valid := buildTZSP(t, netip.MustParseAddr("192.168.88.20"), dnsPort,
		buildDNSQuery(t, "ads.example.com", TypeA, false), 0)

	tests := []struct {
		name string
		raw  []byte
	}{
		{"rỗng", nil},
		{"chỉ có một byte", []byte{0x01}},
		{"sai phiên bản", []byte{0x09, 0x00, 0x00, 0x01, tzspTagEnd}},
		{"không đóng gói Ethernet", []byte{0x01, 0x00, 0x00, 0x02, tzspTagEnd}},
		{"thiếu thẻ kết thúc", []byte{0x01, 0x00, 0x00, 0x01, 0x02, 0x01, 0xFF}},
	}
	// Mọi tiền tố cắt cụt của một gói hợp lệ cũng phải bị từ chối gọn gàng.
	for i := 1; i < len(valid); i += 7 {
		tests = append(tests, struct {
			name string
			raw  []byte
		}{"gói hợp lệ bị cắt cụt", valid[:i]})
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if pkt, err := decodeTZSP(tc.raw); err == nil {
				if _, _, err := parseQuestion(pkt.payload); err == nil {
					t.Error("gói méo được chấp nhận")
				}
			}
		})
	}
}

func TestValidDomain(t *testing.T) {
	valid := []string{"ads.example.com", "a.b.c.example.co.uk", "x-1.example.com", "_dmarc.example.com"}
	for _, name := range valid {
		if !validDomain(name) {
			t.Errorf("%q phải hợp lệ", name)
		}
	}

	invalid := []string{
		"", ".example.com", "example.com.", "wpad", "localhost",
		"ads example.com", "ads.exa$mple.com",
		strings.Repeat("a", 64) + ".example.com",
		strings.Repeat("a.", 130) + "com",
	}
	for _, name := range invalid {
		if validDomain(name) {
			t.Errorf("%q phải bị từ chối", name)
		}
	}
}

func TestETLD1(t *testing.T) {
	tests := map[string]string{
		"ads.example.com":     "example.com",
		"a.b.example.co.uk":   "example.co.uk",
		"metrics.trangweb.vn": "trangweb.vn",
		"x.eulerian.net":      "eulerian.net",
		"example.com":         "example.com",
	}
	for in, want := range tests {
		if got := ETLD1(in); got != want {
			t.Errorf("ETLD1(%q) = %q, muốn %q", in, got, want)
		}
	}
}
