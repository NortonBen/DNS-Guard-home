package store

import (
	"context"
	"fmt"

	"github.com/benji/dnsguard/internal/ingest"
)

// DomainIP là một địa chỉ mà domain đã trỏ tới, kèm vị trí mạng của nó.
type DomainIP struct {
	IP        string `json:"ip"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
	Hits      int64  `json:"hits"`
	TTL       int64  `json:"ttl"`
	ASN       int    `json:"asn,omitempty"`
	Country   string `json:"country,omitempty"`
	Org       string `json:"org,omitempty"`
	// Threat là dải trong danh sách hạ tầng độc hại đã khớp; rỗng nghĩa là sạch.
	Threat string `json:"threat,omitempty"`
}

// WriteResolutions ghi ánh xạ domain → IP lấy từ bản ghi trả lời DNS. Cài ingest.Sink.
//
// Hai điều hàm này cố ý *không* làm:
//
//   - Không tạo domain mới. Mọi câu trả lời đều đi sau một truy vấn, mà đường ghi
//     sự kiện đã tạo domain từ truy vấn đó. Tạo thêm ở đây chỉ sinh ra domain có
//     query_count bằng 0 trong đúng trường hợp truy vấn tương ứng bị bỏ vì hàng đợi
//     đầy — tức là thêm rác vào màn Triage đúng lúc hệ thống đang quá tải.
//   - Không đụng last_seen hay query_count của domains. Một câu trả lời không phải
//     một lượt truy cập; cộng thêm ở đây sẽ đếm đôi mọi truy vấn, mà điểm quyết định
//     chặn hay không lại tính từ chính con số đó.
func (s *Store) WriteResolutions(ctx context.Context, resolutions []ingest.Resolution) error {
	if len(resolutions) == 0 {
		return nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin write resolutions: %w", err)
	}
	defer tx.Rollback()

	names := make([]string, 0, len(resolutions))
	seen := make(map[string]struct{}, len(resolutions))
	for _, r := range resolutions {
		if _, dup := seen[r.Domain]; dup {
			continue
		}
		seen[r.Domain] = struct{}{}
		names = append(names, r.Domain)
	}

	ids, err := lookupDomainIDs(ctx, tx, names)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return nil
	}

	// ttl giữ giá trị nhỏ nhất từng thấy: nó trả lời "ánh xạ này còn đúng bao lâu",
	// và giá trị thấp bất thường trên một domain lạ là dấu hiệu fast-flux.
	upsert, err := tx.PrepareContext(ctx, `
		INSERT INTO domain_ips (domain_id, ip, first_seen, last_seen, hits, ttl)
		VALUES (?, ?, ?, ?, 1, ?)
		ON CONFLICT (domain_id, ip) DO UPDATE SET
		  last_seen = excluded.last_seen,
		  hits      = hits + 1,
		  ttl       = min(ttl, excluded.ttl)`)
	if err != nil {
		return fmt.Errorf("prepare upsert domain_ip: %w", err)
	}
	defer upsert.Close()

	for _, r := range resolutions {
		did, ok := ids[r.Domain]
		if !ok {
			continue
		}
		at := TimeAt(r.At)
		for _, ip := range r.IPs {
			if _, err := upsert.ExecContext(ctx, did, ip.String(), at, at, int64(r.TTL)); err != nil {
				return fmt.Errorf("upsert domain_ip %q → %s: %w", r.Domain, ip, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit write resolutions: %w", err)
	}
	return nil
}

// DomainIPs trả về các địa chỉ một domain đã trỏ tới, mới thấy gần đây trước.
func (s *Store) DomainIPs(ctx context.Context, domainID int64, limit int) ([]DomainIP, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT ip, first_seen, last_seen, hits, ttl,
		       coalesce(asn, 0), coalesce(country, ''), coalesce(org, ''),
		       coalesce(threat, '')
		FROM domain_ips
		WHERE domain_id = ?
		ORDER BY last_seen DESC
		LIMIT ?`, domainID, limit)
	if err != nil {
		return nil, fmt.Errorf("list domain ips: %w", err)
	}
	defer rows.Close()

	var out []DomainIP
	for rows.Next() {
		var d DomainIP
		if err := rows.Scan(&d.IP, &d.FirstSeen, &d.LastSeen, &d.Hits, &d.TTL,
			&d.ASN, &d.Country, &d.Org, &d.Threat); err != nil {
			return nil, fmt.Errorf("scan domain ip: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// IPGeo là vị trí mạng của một địa chỉ.
type IPGeo struct {
	IP      string
	ASN     int
	Country string
	Org     string
}

// PendingGeoIPs trả về các địa chỉ chưa tra vị trí, mới thấy gần đây trước.
func (s *Store) PendingGeoIPs(ctx context.Context, limit int) ([]string, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT ip, max(last_seen) AS seen
		FROM domain_ips
		WHERE country IS NULL
		GROUP BY ip
		ORDER BY seen DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending geo ips: %w", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var ip, seen string
		if err := rows.Scan(&ip, &seen); err != nil {
			return nil, fmt.Errorf("scan pending geo ip: %w", err)
		}
		out = append(out, ip)
	}
	return out, rows.Err()
}

// SetIPGeo điền vị trí cho mọi dòng mang các địa chỉ này.
//
// Địa chỉ không có trong bảng tra vẫn phải được ghi với country rỗng, không để NULL.
// NULL là "chưa tra", nên bỏ trống sẽ khiến địa chỉ đó quay lại hàng đợi ở mọi lượt
// chạy sau — một dải IP không nằm trong iptoasn sẽ bị tra lại mãi mãi.
func (s *Store) SetIPGeo(ctx context.Context, geos []IPGeo) (int64, error) {
	if len(geos) == 0 {
		return 0, nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin set ip geo: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE domain_ips SET asn = ?, country = ?, org = ? WHERE ip = ?`)
	if err != nil {
		return 0, fmt.Errorf("prepare set ip geo: %w", err)
	}
	defer stmt.Close()

	var updated int64
	for _, g := range geos {
		res, err := stmt.ExecContext(ctx, g.ASN, g.Country, g.Org, g.IP)
		if err != nil {
			return 0, fmt.Errorf("set geo cho %s: %w", g.IP, err)
		}
		n, _ := res.RowsAffected()
		updated += n
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit set ip geo: %w", err)
	}
	return updated, nil
}

// IPDomain là một domain đã trỏ tới một địa chỉ.
type IPDomain struct {
	DomainID  int64  `json:"domain_id"`
	Name      string `json:"name"`
	Status    string `json:"status"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
	Hits      int64  `json:"hits"`
}

// IPAccess là một lượt truy vấn rơi vào khoảng thời gian mà domain được hỏi đang
// trỏ tới địa chỉ đang điều tra.
type IPAccess struct {
	Client     string `json:"client"`
	Domain     string `json:"domain"`
	OccurredAt string `json:"occurred_at"`
}

// DomainsByIP trả về các domain đã trỏ tới một địa chỉ.
//
// Đây là thao tác mở đầu khi điều tra bắt đầu từ một địa chỉ trong cảnh báo: nó
// biến một con số vô nghĩa thành danh sách tên mà mạng đã hỏi.
func (s *Store) DomainsByIP(ctx context.Context, ip string, limit int) ([]IPDomain, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT d.id, d.name, d.status, i.first_seen, i.last_seen, i.hits
		FROM domain_ips i
		JOIN domains d ON d.id = i.domain_id
		WHERE i.ip = ?
		ORDER BY i.last_seen DESC
		LIMIT ?`, ip, limit)
	if err != nil {
		return nil, fmt.Errorf("list domains by ip: %w", err)
	}
	defer rows.Close()

	var out []IPDomain
	for rows.Next() {
		var d IPDomain
		if err := rows.Scan(&d.DomainID, &d.Name, &d.Status,
			&d.FirstSeen, &d.LastSeen, &d.Hits); err != nil {
			return nil, fmt.Errorf("scan domain by ip: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// AccessesByIP trả về các lượt truy vấn liên quan tới một địa chỉ: thiết bị nào đã
// hỏi tên nào, lúc nào.
//
// Ràng buộc thời gian là phần quan trọng nhất và cũng là phần dễ đọc sai nhất. Một
// truy vấn chỉ được tính khi nó rơi vào khoảng mà ánh xạ domain → địa chỉ này đang
// được quan sát; ngoài khoảng đó, cùng tên ấy có thể đã trỏ đi nơi khác.
//
// Kết quả trả lời đúng câu "thiết bị nào đã phân giải một tên trỏ tới địa chỉ này,
// lúc mấy giờ". Nó **không** chứng minh có kết nối thật tới địa chỉ đó: luồng mirror
// chỉ mang DNS, nên phần kết nối nằm ngoài tầm quan sát của hệ thống này.
func (s *Store) AccessesByIP(ctx context.Context, ip string, limit int) ([]IPAccess, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT c.ip, d.name, e.occurred_at
		FROM domain_ips i
		JOIN query_events e ON e.domain_id = i.domain_id
		JOIN domains d      ON d.id = e.domain_id
		JOIN clients c      ON c.id = e.client_id
		WHERE i.ip = ?
		  AND e.occurred_at >= i.first_seen
		  AND e.occurred_at <= i.last_seen
		ORDER BY e.occurred_at DESC
		LIMIT ?`, ip, limit)
	if err != nil {
		return nil, fmt.Errorf("list accesses by ip: %w", err)
	}
	defer rows.Close()

	var out []IPAccess
	for rows.Next() {
		var a IPAccess
		if err := rows.Scan(&a.Client, &a.Domain, &a.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan access by ip: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// IPThreat là trạng thái đe dọa của một địa chỉ.
type IPThreat struct {
	IP string
	// Threat là dải đã khớp trong danh sách; rỗng nghĩa là sạch.
	Threat string
}

// ThreatMatch là một dòng trong danh sách cảnh báo.
type ThreatMatch struct {
	DomainID int64  `json:"domain_id"`
	Name     string `json:"name"`
	Status   string `json:"status"`
	IP       string `json:"ip"`
	Threat   string `json:"threat"`
	LastSeen string `json:"last_seen"`
	Hits     int64  `json:"hits"`
	Country  string `json:"country,omitempty"`
	Org      string `json:"org,omitempty"`
	// Beacon bằng true khi domain được hỏi theo nhịp đều bất thường. Kết hợp với một
	// địa chỉ trong danh sách, đây là dấu hiệu mạnh của kênh điều khiển; riêng lẻ thì
	// một trong hai đều có thể chỉ là phần mềm cập nhật theo lịch.
	Beacon bool `json:"beacon"`
}

// IPThreatState trả về trạng thái đe dọa hiện tại của mọi địa chỉ phân biệt.
//
// Đọc hết vào bộ nhớ để job đối chiếu tính phần chênh lệch rồi chỉ ghi những dòng
// thật sự đổi. Số địa chỉ phân biệt của một mạng nhỏ hơn số dòng log nhiều bậc, nên
// bản đồ này nhỏ.
func (s *Store) IPThreatState(ctx context.Context) (map[string]string, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT ip, coalesce(max(threat), '') FROM domain_ips GROUP BY ip`)
	if err != nil {
		return nil, fmt.Errorf("read ip threat state: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string, 1024)
	for rows.Next() {
		var ip, threat string
		if err := rows.Scan(&ip, &threat); err != nil {
			return nil, fmt.Errorf("scan ip threat state: %w", err)
		}
		out[ip] = threat
	}
	return out, rows.Err()
}

// SetIPThreats ghi trạng thái đe dọa cho các địa chỉ. Chuỗi rỗng xoá cảnh báo.
func (s *Store) SetIPThreats(ctx context.Context, threats []IPThreat) (int64, error) {
	if len(threats) == 0 {
		return 0, nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin set ip threats: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `UPDATE domain_ips SET threat = ? WHERE ip = ?`)
	if err != nil {
		return 0, fmt.Errorf("prepare set ip threat: %w", err)
	}
	defer stmt.Close()

	var updated int64
	for _, t := range threats {
		// Ghi NULL thay vì chuỗi rỗng khi sạch, để index bộ phận chỉ chứa dòng có
		// cảnh báo thật.
		var value any
		if t.Threat != "" {
			value = t.Threat
		}
		res, err := stmt.ExecContext(ctx, value, t.IP)
		if err != nil {
			return 0, fmt.Errorf("set threat cho %s: %w", t.IP, err)
		}
		n, _ := res.RowsAffected()
		updated += n
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit set ip threats: %w", err)
	}
	return updated, nil
}

// ThreatMatches trả về danh sách cảnh báo: domain đã phân giải tới một địa chỉ nằm
// trong danh sách hạ tầng độc hại.
//
// beaconMaxCV là ngưỡng hệ số biến thiên khoảng cách truy vấn; dưới ngưỡng nghĩa là
// nhịp đều bất thường. Truyền vào từ tầng trên chứ không đặt cứng ở đây, vì ngưỡng đó
// thuộc về bộ luật phân loại và người vận hành sửa được.
func (s *Store) ThreatMatches(ctx context.Context, beaconMaxCV float64, limit int) ([]ThreatMatch, error) {
	rows, err := s.r.QueryContext(ctx, `
		SELECT d.id, d.name, d.status, i.ip, i.threat, i.last_seen, i.hits,
		       coalesce(i.country, ''), coalesce(i.org, ''),
		       coalesce(b.interval_cv, 0) > 0 AND coalesce(b.interval_cv, 0) < ?
		FROM domain_ips i
		JOIN domains d          ON d.id = i.domain_id
		LEFT JOIN domain_behavior b ON b.domain_id = i.domain_id
		WHERE i.threat IS NOT NULL
		ORDER BY i.last_seen DESC
		LIMIT ?`, beaconMaxCV, limit)
	if err != nil {
		return nil, fmt.Errorf("list threat matches: %w", err)
	}
	defer rows.Close()

	var out []ThreatMatch
	for rows.Next() {
		var m ThreatMatch
		if err := rows.Scan(&m.DomainID, &m.Name, &m.Status, &m.IP, &m.Threat,
			&m.LastSeen, &m.Hits, &m.Country, &m.Org, &m.Beacon); err != nil {
			return nil, fmt.Errorf("scan threat match: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ExportQuery là một dòng truy vấn trong hồ sơ điều tra.
type ExportQuery struct {
	Client string `json:"client"`
	Domain string `json:"domain"`
	QType  int64  `json:"qtype"`
	At     string `json:"at"`
}

// ExportResolution là một ánh xạ domain → địa chỉ trong hồ sơ điều tra.
type ExportResolution struct {
	Domain    string `json:"domain"`
	IP        string `json:"ip"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
	Hits      int64  `json:"hits"`
	TTL       int64  `json:"ttl"`
	ASN       int    `json:"asn,omitempty"`
	Country   string `json:"country,omitempty"`
	Org       string `json:"org,omitempty"`
	Threat    string `json:"threat,omitempty"`
}

// ExportQueries duyệt các truy vấn trong khoảng thời gian, cũ nhất trước.
//
// Gọi lại theo từng dòng thay vì trả về một lát cắt: một khoảng vài tháng có thể là
// hàng chục triệu dòng, và nạp hết vào bộ nhớ trên một máy Pi sẽ giết tiến trình.
// Người gọi ghi thẳng từng dòng ra luồng đầu ra.
func (s *Store) ExportQueries(ctx context.Context, from, to string, fn func(ExportQuery) error) error {
	rows, err := s.r.QueryContext(ctx, `
		SELECT c.ip, d.name, e.qtype, e.occurred_at
		FROM query_events e
		JOIN domains d ON d.id = e.domain_id
		JOIN clients c ON c.id = e.client_id
		WHERE e.occurred_at >= ? AND e.occurred_at <= ?
		ORDER BY e.occurred_at`, from, to)
	if err != nil {
		return fmt.Errorf("export queries: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var q ExportQuery
		if err := rows.Scan(&q.Client, &q.Domain, &q.QType, &q.At); err != nil {
			return fmt.Errorf("scan export query: %w", err)
		}
		if err := fn(q); err != nil {
			return err
		}
	}
	return rows.Err()
}

// ExportResolutions duyệt các ánh xạ domain → địa chỉ **còn hiệu lực trong khoảng**.
//
// Điều kiện là hai khoảng giao nhau, không phải ánh xạ nằm gọn bên trong: một địa chỉ
// quan sát lần đầu từ tháng trước và vẫn còn dùng trong khoảng điều tra là bằng chứng
// liên quan, và lọc theo first_seen sẽ đánh rơi nó.
func (s *Store) ExportResolutions(ctx context.Context, from, to string, fn func(ExportResolution) error) error {
	rows, err := s.r.QueryContext(ctx, `
		SELECT d.name, i.ip, i.first_seen, i.last_seen, i.hits, i.ttl,
		       coalesce(i.asn, 0), coalesce(i.country, ''), coalesce(i.org, ''),
		       coalesce(i.threat, '')
		FROM domain_ips i
		JOIN domains d ON d.id = i.domain_id
		WHERE i.last_seen >= ? AND i.first_seen <= ?
		ORDER BY i.last_seen`, from, to)
	if err != nil {
		return fmt.Errorf("export resolutions: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var r ExportResolution
		if err := rows.Scan(&r.Domain, &r.IP, &r.FirstSeen, &r.LastSeen, &r.Hits, &r.TTL,
			&r.ASN, &r.Country, &r.Org, &r.Threat); err != nil {
			return fmt.Errorf("scan export resolution: %w", err)
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}
