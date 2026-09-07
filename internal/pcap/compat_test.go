package pcap

import (
	"encoding/binary"
	"os/exec"
	"strings"
	"testing"
)

// dnsFrame dựng một khung Ethernet/IPv4/UDP mang truy vấn DNS thật, để công cụ ngoài
// có thể phân tích tới tận tầng ứng dụng chứ không chỉ đọc được header.
func dnsFrame() []byte {
	// Câu hỏi DNS cho ads.example.com kiểu A.
	dns := []byte{
		0x12, 0x34, // ID
		0x01, 0x00, // cờ: truy vấn đệ quy
		0x00, 0x01, // QDCOUNT
		0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
		3, 'a', 'd', 's', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 3, 'c', 'o', 'm', 0,
		0x00, 0x01, // QTYPE A
		0x00, 0x01, // QCLASS IN
	}

	udp := make([]byte, 8+len(dns))
	binary.BigEndian.PutUint16(udp[0:2], 40000)
	binary.BigEndian.PutUint16(udp[2:4], 53)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp)))
	copy(udp[8:], dns)

	ip := make([]byte, 20+len(udp))
	ip[0] = 0x45
	binary.BigEndian.PutUint16(ip[2:4], uint16(len(ip)))
	ip[8] = 64
	ip[9] = 17 // UDP
	copy(ip[12:16], []byte{192, 168, 88, 20})
	copy(ip[16:20], []byte{192, 168, 88, 1})
	binary.BigEndian.PutUint16(ip[10:12], checksum(ip[:20]))
	copy(ip[20:], udp)

	eth := []byte{
		0x00, 0x11, 0x22, 0x33, 0x44, 0x55,
		0x66, 0x77, 0x88, 0x99, 0xAA, 0xBB,
		0x08, 0x00,
	}
	return append(eth, ip...)
}

func checksum(b []byte) uint16 {
	var sum uint32
	for i := 0; i+1 < len(b); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = (sum & 0xFFFF) + (sum >> 16)
	}
	return ^uint16(sum)
}

// File sinh ra phải mở được bằng công cụ phân tích gói thật, không chỉ bằng bộ kiểm
// tra byte của chính package này.
//
// Bỏ qua khi máy không có tcpdump: đây là kiểm chứng tương thích, không phải điều kiện
// để mã nguồn đúng, và CI không nên hỏng vì thiếu một công cụ hệ thống.
func TestFileReadableByTcpdump(t *testing.T) {
	bin, err := exec.LookPath("tcpdump")
	if err != nil {
		t.Skip("không có tcpdump trên máy này")
	}

	r := newRecorder(t, Options{Dir: t.TempDir()})
	r.Write(dnsFrame())
	runUntilWritten(t, r, 1)

	files, err := r.Files()
	if err != nil || len(files) == 0 {
		t.Fatalf("không có file pcap: %v", err)
	}

	out, err := exec.Command(bin, "-r", files[0], "-nn").CombinedOutput()
	if err != nil {
		t.Fatalf("tcpdump không đọc được file: %v\n%s", err, out)
	}
	text := string(out)
	// tcpdump phải bóc được tới tầng DNS, tức là link type và header gói đều đúng.
	if !strings.Contains(text, "192.168.88.20") || !strings.Contains(text, "ads.example.com") {
		t.Errorf("tcpdump không bóc được nội dung mong đợi:\n%s", text)
	}
}
