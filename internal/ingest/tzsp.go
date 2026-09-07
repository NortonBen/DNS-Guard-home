// Package ingest nhận truy vấn DNS và biến thành sự kiện có cấu trúc.
//
// Nguồn dữ liệu chính là luồng TZSP do MikroTik mirror sang. Thiết bị chặn nằm
// *sau* điểm mirror, nên gói bắt được là gói client gửi lên router, trước khi router
// quyết định làm gì với nó. Ba hệ quả quan trọng:
//
//   - IP client là IP thật, không phải IP của resolver. Không có nó thì các tín hiệu
//     third_party, fan_out và beacon đều vô nghĩa.
//   - Truy vấn bị adlist chặn vẫn nhìn thấy được, nên domain đã chặn không bị hiểu
//     nhầm là đã chết.
//   - Truy vấn trúng cache của router vẫn nhìn thấy được.
package ingest

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
)

// Định dạng TZSP: 4 byte header, một danh sách thẻ kết thúc bằng TAG_END, rồi gói
// đã đóng gói. Xem https://en.wikipedia.org/wiki/TZSP
const (
	tzspVersion     = 0x01
	tzspTagPadding  = 0x00
	tzspTagEnd      = 0x01
	tzspEncapEther  = 0x0001
	tzspHeaderLen   = 4
	tzspMaxTagBytes = 512 // chặn trên hợp lý; thẻ thật chỉ vài chục byte
)

// Ethertype và số hiệu giao thức.
const (
	ethTypeIPv4  = 0x0800
	ethTypeIPv6  = 0x86DD
	ethTypeVLAN  = 0x8100
	ethTypeQinQ  = 0x88A8
	ethHeaderLen = 14

	protoUDP = 17

	dnsPort = 53
)

var (
	errShortPacket   = errors.New("gói quá ngắn")
	errNotEthernet   = errors.New("TZSP không đóng gói Ethernet")
	errNotDNSQuery   = errors.New("không phải truy vấn DNS")
	errUnsupportedL3 = errors.New("giao thức tầng mạng không hỗ trợ")
)

// packet là kết quả bóc tách một datagram TZSP: IP nguồn của client và phần payload
// DNS còn nguyên để tầng trên giải mã.
type packet struct {
	client  netip.Addr
	payload []byte
}

// decodeTZSP bóc TZSP → Ethernet → IP → UDP và trả về payload DNS kèm IP client.
//
// Chỉ nhận gói có cổng đích 53: đó là truy vấn client gửi lên router. Gói có cổng
// *nguồn* 53 là câu trả lời của router, mang IP nguồn của router chứ không phải của
// client, nên bỏ qua.
func decodeTZSP(buf []byte) (packet, error) {
	frame, err := tzspPayload(buf)
	if err != nil {
		return packet{}, err
	}
	return decodeEthernet(frame)
}

// tzspPayload kiểm tra header TZSP, bỏ qua danh sách thẻ và trả về khung Ethernet.
func tzspPayload(buf []byte) ([]byte, error) {
	if len(buf) < tzspHeaderLen {
		return nil, fmt.Errorf("TZSP: %w (%d byte)", errShortPacket, len(buf))
	}
	if buf[0] != tzspVersion {
		return nil, fmt.Errorf("TZSP: phiên bản %d không hỗ trợ", buf[0])
	}
	if encap := binary.BigEndian.Uint16(buf[2:4]); encap != tzspEncapEther {
		return nil, fmt.Errorf("%w (encap %#04x)", errNotEthernet, encap)
	}

	i := tzspHeaderLen
	for consumed := 0; i < len(buf); consumed++ {
		if consumed > tzspMaxTagBytes {
			return nil, errors.New("TZSP: danh sách thẻ không kết thúc")
		}
		switch tag := buf[i]; tag {
		case tzspTagEnd:
			return buf[i+1:], nil
		case tzspTagPadding:
			i++ // thẻ đệm không có trường độ dài
		default:
			if i+1 >= len(buf) {
				return nil, fmt.Errorf("TZSP: %w ở thẻ %#02x", errShortPacket, tag)
			}
			i += 2 + int(buf[i+1])
		}
	}
	return nil, errors.New("TZSP: thiếu thẻ kết thúc")
}

// decodeEthernet bóc khung Ethernet, bỏ qua thẻ VLAN nếu có, rồi chuyển xuống IP.
func decodeEthernet(frame []byte) (packet, error) {
	if len(frame) < ethHeaderLen {
		return packet{}, fmt.Errorf("Ethernet: %w", errShortPacket)
	}
	off := 12
	etherType := binary.BigEndian.Uint16(frame[off : off+2])
	off += 2

	// MikroTik mirror có thể giữ nguyên thẻ VLAN. Cho phép hai lớp (QinQ).
	for range 2 {
		if etherType != ethTypeVLAN && etherType != ethTypeQinQ {
			break
		}
		if len(frame) < off+4 {
			return packet{}, fmt.Errorf("VLAN: %w", errShortPacket)
		}
		etherType = binary.BigEndian.Uint16(frame[off+2 : off+4])
		off += 4
	}

	switch etherType {
	case ethTypeIPv4:
		return decodeIPv4(frame[off:])
	case ethTypeIPv6:
		return decodeIPv6(frame[off:])
	default:
		return packet{}, fmt.Errorf("%w (ethertype %#04x)", errUnsupportedL3, etherType)
	}
}

func decodeIPv4(b []byte) (packet, error) {
	if len(b) < 20 {
		return packet{}, fmt.Errorf("IPv4: %w", errShortPacket)
	}
	ihl := int(b[0]&0x0F) * 4
	if ihl < 20 || len(b) < ihl {
		return packet{}, fmt.Errorf("IPv4: độ dài header %d không hợp lệ", ihl)
	}
	// Gói phân mảnh: chỉ mảnh đầu mới có header UDP. Truy vấn DNS qua UDP hiếm khi
	// vượt MTU, nên bỏ qua các mảnh sau thay vì ghép lại.
	if flagsFrag := binary.BigEndian.Uint16(b[6:8]); flagsFrag&0x1FFF != 0 {
		return packet{}, errors.New("IPv4: bỏ qua mảnh phân mảnh")
	}
	if b[9] != protoUDP {
		return packet{}, fmt.Errorf("%w (proto %d)", errUnsupportedL3, b[9])
	}
	src, ok := netip.AddrFromSlice(b[12:16])
	if !ok {
		return packet{}, errors.New("IPv4: địa chỉ nguồn không hợp lệ")
	}
	return decodeUDP(src, b[ihl:])
}

func decodeIPv6(b []byte) (packet, error) {
	if len(b) < 40 {
		return packet{}, fmt.Errorf("IPv6: %w", errShortPacket)
	}
	// Không đi qua chuỗi extension header: truy vấn DNS trong mạng LAN không dùng
	// tới chúng, và bỏ qua gói lạ an toàn hơn là đoán sai vị trí payload.
	if b[6] != protoUDP {
		return packet{}, fmt.Errorf("%w (next header %d)", errUnsupportedL3, b[6])
	}
	src, ok := netip.AddrFromSlice(b[8:24])
	if !ok {
		return packet{}, errors.New("IPv6: địa chỉ nguồn không hợp lệ")
	}
	return decodeUDP(src, b[40:])
}

func decodeUDP(src netip.Addr, b []byte) (packet, error) {
	if len(b) < 8 {
		return packet{}, fmt.Errorf("UDP: %w", errShortPacket)
	}
	if dstPort := binary.BigEndian.Uint16(b[2:4]); dstPort != dnsPort {
		return packet{}, fmt.Errorf("%w (cổng đích %d)", errNotDNSQuery, dstPort)
	}
	return packet{client: src.Unmap(), payload: b[8:]}, nil
}
