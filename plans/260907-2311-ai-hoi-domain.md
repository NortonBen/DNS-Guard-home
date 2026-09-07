# Hỏi AI về domain — gom lô, lưu lịch sử riêng, Ask AI, skill và MCP

Trạng thái: đã xong · Ngày 2026-09-07

Thêm một nguồn phán đoán mới: gọi model ngôn ngữ (DeepSeek và mọi endpoint nói
được giao thức Chat Completions) để hỏi về domain, kèm giao diện hỏi đáp, bộ công
cụ dựng sẵn, skill và MCP.

---

## 1. Kết quả mong muốn

| # | Yêu cầu | Cách đáp ứng |
|---|---|---|
| 1 | Gọi model AI để hỏi về domain | `internal/llm` — client Chat Completions, đổi nhà cung cấp bằng cấu hình |
| 2 | Gom domain, hỏi một lô trong một request | `ai.Classifier` cài `enrich.BatchEnricher`, mặc định 40 domain/lượt |
| 3 | Domain đã hỏi thì không hỏi lại | `domain_facts(source='ai')` + TTL — đúng cơ chế `http`/`vt` đang dùng |
| 4 | Phản hồi dạng CSV để tiết kiệm token | Prompt ép CSV; `parseCSV` chịu được rào ```, thiếu cột, dòng rác |
| 5 | Ask AI | Vòng lặp gọi công cụ, màn `/ai` kèm dấu vết từng lượt tra cứu |
| 6 | Tận dụng mã nguồn k8s-terraform-manager | Chuyển `llm`, `mcp`, vòng lặp agent, cơ chế skill |
| 7 | Lịch sử request trong file SQLite độc lập | `internal/ai/store.go` mở `dnsguard-ai.db` riêng |
| 8 | Người dùng yêu cầu AI kiểm tra lại domain | `POST /domains/{id}/ai-recheck` → job `ai_classify` kèm danh sách domain, bỏ qua TTL |
| 9 | Hỗ trợ skill và MCP, kèm công cụ dựng sẵn | 9 công cụ đọc + 1 công cụ hỏi lại; skill và máy chủ MCP sửa được trên giao diện |

**Không làm:** để AI tự quyết định chặn. Tín hiệu AI bị chặn trần dưới ngưỡng
chặn, đúng lập luận ở [plans/260907-2228-ml-phan-loai-domain.md](260907-2228-ml-phan-loai-domain.md) §6.2.

---

## 2. Ranh giới kiến trúc

```
internal/llm     giao thức Chat Completions, không biết gì về domain
internal/mcp     client MCP (JSON-RPC 2.0 trên HTTP)
internal/ai      tầng AI của DNSGuard
  store.go       CSDL RIÊNG: lịch sử request, verdict, chat, skill, MCP server
  classify.go    BatchEnricher — gom lô, hỏi, phân tích CSV
  agent.go       vòng lặp gọi công cụ
  tools.go       công cụ dựng sẵn đọc dữ liệu DNSGuard
  skills.go      chọn và chèn quy ước vận hành
```

Hai file CSDL, phân vai rõ:

- `dnsguard.db` — bằng chứng và quyết định. Chỉ nhận thêm `domain_facts(source='ai')`
  và cấu hình nhà cung cấp trong `settings`.
- `dnsguard-ai.db` — nhật ký vận hành AI. Xoá cả file này không mất một quyết định
  chặn nào; sao lưu CSDL chính không kéo theo hàng nghìn dòng prompt.

Không có khoá ngoại nào bắc qua hai file. Bảng ở CSDL AI tham chiếu domain bằng
**tên**, không bằng id.

---

## 3. Vì sao gom lô là một `BatchEnricher` chứ không phải job riêng

`Enricher` hiện tại hỏi từng domain một. Thêm `BatchEnricher` mở rộng nó thay vì
đi vòng, để nguồn AI vẫn nằm trong `enrich.Registry` và thừa hưởng nguyên ba lớp
bảo vệ đã có: công tắc bật/tắt lúc chạy, giới hạn tốc độ, circuit breaker.

Cơ chế "đã hỏi thì không hỏi lại" cũng có sẵn: `GatedCandidates` chỉ chọn domain
chưa có dòng `domain_facts` hoặc đã hết TTL. Nguồn `ai` chỉ cần khai TTL của mình.

---

## 4. Định dạng CSV

Yêu cầu model trả đúng bốn cột, không header, không rào code:

```
domain,category,confidence,reason
```

`category` thuộc tám nhãn ở `classify.AllCategories`. Dòng có nhãn lạ, domain
không nằm trong lô, hoặc thiếu cột bị bỏ qua và đếm vào `skipped` — model bịa ra
một domain không được phép biến thành bằng chứng.

Chi phí token: CSV cho một lô 40 domain tốn khoảng 1/3 so với JSON tương đương,
vì không lặp lại tên khoá ở mỗi dòng.

---

## 5. Tín hiệu

| Tín hiệu | Trọng số | Lý do |
|---|---|---|
| `ai_adtech` | +2,5 | Dưới ngưỡng chặn 5,5 — một lượt đoán sai không đủ chặn nhầm |
| `ai_clean` | −2,0 | Cân xứng với `vt_clean`, để AI kéo được domain lành ra khỏi hàng chờ |

`Signal.Detail` mang `model`, `confidence`, `reason`. Không có nó thì sáu tháng
sau không truy ngược được quyết định về model đã sinh ra nó.

---

## 6. Công cụ dựng sẵn

Đọc: `domain_lookup`, `domain_search`, `domain_facts`, `domain_relations`,
`domain_timeline`, `domain_decisions`, `stats_overview`, `top_domains`, `ai_history`.
Ghi: `domain_recheck` — chỉ xếp một job hỏi lại, quản trị viên mới gọi được.

Không có công cụ nào đổi trạng thái domain. Chặn hay bỏ chặn vẫn phải bấm trên
giao diện, đúng như `destroy` trong dự án k8s không có công cụ tương ứng.

---

## 7. Các bước — đã xong

| # | Việc | Nơi |
|---|---|---|
| 1 | Client Chat Completions và client MCP | `internal/llm`, `internal/mcp` |
| 2 | CSDL nhật ký riêng + migration | `internal/ai/store.go`, `internal/ai/migrations/` |
| 3 | Gom lô, dựng prompt, đọc CSV | `internal/ai/{classify,prompt,csv}.go` |
| 4 | `BatchEnricher` + đường chạy lô | `internal/enrich/enrich.go`, `internal/worker/worker.go` |
| 5 | `Facts.AI`, hai tín hiệu, migration `'ai'` | `internal/classify/`, `0005_ai_source.sql` |
| 6 | Vòng lặp công cụ, công cụ dựng sẵn, skill | `internal/ai/{agent,tools,skills}.go` |
| 7 | API, cấu hình, nối dây | `internal/api/handlers_ai.go`, `internal/config`, `cmd/dnsguard` |
| 8 | Màn `/ai`, thẻ AI ở chi tiết domain | `web/src/routes/ai.tsx`, `web/src/components/ai/` |
| 9 | Test và tài liệu | `*_test.go`, `docs/09-ai.md` |

## 8. Đã kiểm chứng đầu-cuối

Chạy thật với một máy chủ giả nói giao thức Chat Completions, `DNSGUARD_AI_BATCH_SIZE=3`,
bốn domain trong CSDL:

| Điều cần đúng | Kết quả |
|---|---|
| Gom lô | 4 domain → **2 lượt gọi** (3 + 1) |
| Đọc CSV bọc rào code | 4/4 dòng đọc được, 0 dòng bỏ |
| Ghi vào CSDL chính | 4 dòng `domain_facts(source='ai')` |
| Nhật ký ở file riêng | `dnsguard-ai.db` tạo cạnh CSDL chính |
| Chấm điểm lại | `px.quangcao.vn` +2,5 · `baochi.vn` −2,0 |
| AI một mình không chặn được | `px.quangcao.vn` đạt 2,5 → vẫn `new`, không vào hàng chờ |
| Đã hỏi thì không hỏi lại | Chạy lại job → **0 lượt gọi thêm** |
| Nút hỏi lại thì có hỏi lại | Bỏ qua TTL → **1 lượt gọi mới** |
| Skill khớp theo từ khoá | Câu hỏi chứa "tín hiệu" kéo đúng `doc-tin-hieu` cùng hai skill nền |

---

## 9. Câu hỏi chưa chốt

1. Kích thước lô mặc định 40 có hợp với trần ngữ cảnh của model rẻ nhất không —
   cần đo trên dữ liệu thật.
2. Có nên để `ai_clean` tự động đẩy domain ra khỏi hàng chờ duyệt, hay chỉ hạ điểm?
   Hiện chọn chỉ hạ điểm.
