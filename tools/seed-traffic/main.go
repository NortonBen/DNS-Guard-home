// Command seed-traffic gửi truy vấn DNS tổng hợp vào cổng TZSP của DNSGuard.
//
// Không ai muốn chờ một tuần mới có dữ liệu để làm giao diện. Dữ liệu ở đây cố ý
// **thực tế** chứ không nhạt nhẽo: có một cụm CNAME cloaking, vài domain nội dung
// bình thường, một domain telemetry truy vấn đều đặn, một subdomain entropy cao, và
// một domain nằm trong danh sách bảo vệ. Fixtures nhạt nhẽo dẫn tới giao diện chỉ đẹp
// khi dữ liệu đẹp.
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"
)

// profile là một domain giả kèm hành vi truy vấn của nó.
type profile struct {
	name string
	// weight là trọng số chọn ngẫu nhiên; domain nội dung xuất hiện nhiều hơn.
	weight int
	// clients giới hạn số client truy vấn domain này. 0 nghĩa là tất cả.
	clients int
}

var profiles = []profile{
	// Nội dung bình thường — người dùng chủ động truy cập.
	{name: "trangweb.vn", weight: 10},
	{name: "cdn.trangweb.vn", weight: 8},
	{name: "baomoi.com", weight: 7},
	{name: "shopee.vn", weight: 6},

	// Cụm CNAME cloaking: tên trông như của chính trang chủ nhà.
	{name: "metrics.trangweb.vn", weight: 9},
	{name: "analytics.congty.vn", weight: 4},

	// Hạ tầng adtech lộ mặt.
	{name: "doubleclick.net", weight: 6},
	{name: "ads.example.com", weight: 5},
	{name: "tracker.example.com", weight: 5},

	// Máy chủ quảng cáo sinh subdomain theo chiến dịch.
	{name: "c1.quangcao.vn", weight: 3},
	{name: "c2.quangcao.vn", weight: 3},
	{name: "c3.quangcao.vn", weight: 3},
	{name: "c4.quangcao.vn", weight: 2},
	{name: "a7f3k9x2m1qz.tracker.io", weight: 2},

	// Telemetry: một thiết bị báo cáo đều đặn.
	{name: "check.thietbi.vn", weight: 8, clients: 1},

	// Nằm trong danh sách bảo vệ cứng — không bao giờ được phép chặn.
	{name: "api.vnpay.vn", weight: 4},
	{name: "push.apple.com", weight: 3},

	// Thứ hạng cao, phải được tín hiệu high_rank bảo vệ.
	{name: "google.com", weight: 9},
	{name: "github.com", weight: 4},
}

func main() {
	addr := flag.String("addr", "127.0.0.1:37008", "địa chỉ cổng TZSP của DNSGuard")
	rounds := flag.Int("rounds", 60, "số vòng gửi")
	clientCount := flag.Int("clients", 5, "số client giả lập")
	flag.Parse()

	conn, err := net.Dial("udp", *addr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "không mở được socket:", err)
		os.Exit(1)
	}
	defer conn.Close()

	clients := make([][4]byte, *clientCount)
	for i := range clients {
		clients[i] = [4]byte{192, 168, 88, byte(20 + i)}
	}

	// Bảng chọn có trọng số: dựng một lần rồi lấy ngẫu nhiên trong đó.
	var pool []profile
	for _, p := range profiles {
		for range p.weight {
			pool = append(pool, p)
		}
	}

	sent := 0
	for range *rounds {
		for range len(profiles) {
			p := pool[rand.Intn(len(pool))]

			client := clients[rand.Intn(len(clients))]
			if p.clients > 0 {
				client = clients[0]
			}

			qtype := uint16(1) // A
			if rand.Intn(4) == 0 {
				qtype = 28 // AAAA
			}

			if _, err := conn.Write(encodeTZSP(client, encodeQuery(p.name, qtype))); err != nil {
				fmt.Fprintln(os.Stderr, "gửi thất bại:", err)
				os.Exit(1)
			}
			sent++
		}
		// Giãn nhẹ để bộ nhận không bị dồn cụm và để mốc thời gian trải ra.
		time.Sleep(5 * time.Millisecond)
	}

	fmt.Printf("đã gửi %d truy vấn cho %d domain từ %d client tới %s\n",
		sent, len(profiles), len(clients), *addr)
}

// encodeQuery dựng phần câu hỏi của một thông điệp DNS.
func encodeQuery(name string, qtype uint16) []byte {
	msg := make([]byte, 12)
	binary.BigEndian.PutUint16(msg[0:2], uint16(rand.Intn(65536)))
	binary.BigEndian.PutUint16(msg[4:6], 1) // QDCOUNT

	for _, label := range strings.Split(name, ".") {
		msg = append(msg, byte(len(label)))
		msg = append(msg, label...)
	}
	msg = append(msg, 0)
	msg = binary.BigEndian.AppendUint16(msg, qtype)
	return binary.BigEndian.AppendUint16(msg, 1) // QCLASS IN
}

// encodeTZSP bọc một thông điệp DNS trong UDP → IPv4 → Ethernet → TZSP, giống hệt
// những gì thiết bị mạng gửi sang khi bật mirror.
func encodeTZSP(client [4]byte, dns []byte) []byte {
	udp := make([]byte, 8+len(dns))
	binary.BigEndian.PutUint16(udp[0:2], uint16(40000+rand.Intn(20000)))
	binary.BigEndian.PutUint16(udp[2:4], 53)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp)))
	copy(udp[8:], dns)

	ip := make([]byte, 20+len(udp))
	ip[0] = 0x45 // IPv4, header 20 byte
	binary.BigEndian.PutUint16(ip[2:4], uint16(len(ip)))
	ip[8] = 64 // TTL
	ip[9] = 17 // UDP
	copy(ip[12:16], client[:])
	copy(ip[16:20], []byte{192, 168, 88, 1}) // thiết bị mạng
	copy(ip[20:], udp)

	eth := make([]byte, 0, 14+len(ip))
	eth = append(eth, 0x00, 0x11, 0x22, 0x33, 0x44, 0x55)
	eth = append(eth, 0x66, 0x77, 0x88, 0x99, 0xAA, 0xBB)
	eth = binary.BigEndian.AppendUint16(eth, 0x0800) // IPv4
	eth = append(eth, ip...)

	// Header TZSP: phiên bản 1, kiểu 0, đóng gói Ethernet, rồi thẻ kết thúc.
	tzsp := []byte{0x01, 0x00, 0x00, 0x01, 0x01}
	return append(tzsp, eth...)
}
