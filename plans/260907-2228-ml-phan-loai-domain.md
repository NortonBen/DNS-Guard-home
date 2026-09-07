# Học máy cho phân loại domain — nghiên cứu khả thi

Trạng thái: nghiên cứu · Ngày 2026-09-07

Đánh giá hai đề xuất: (A) XGBoost/LightGBM trên đặc trưng từ vựng của tên miền,
(B) TF-IDF + Logistic Regression/LinearSVC trên nội dung HTML.

---

## 0. Kết luận trước

**Không nên thêm bộ phân loại ML tổng quát cho `ads`/`tracking` ở thời điểm này.**
Không phải vì ML sai về nguyên tắc, mà vì ba lý do đo được:

1. **Chưa có dữ liệu huấn luyện.** CSDL hiện tại: 39 domain, **0 dòng `decisions`**,
   0 bản ghi HTTP fact. Không có nhãn người thật nào để học.
2. **Đặc trưng từ vựng xếp hạng ngược trên chính bài toán này.** Đo bằng `Score()` với
   `Facts{}` rỗng: `criteo.com`, `outbrain.com`, `quantserve.com`, `scorecardresearch.com`
   đều **0,00**, trong khi CDN ảnh của Medium đạt **2,00**. Không phải mô hình yếu —
   thông tin không có trong đầu vào. Xem §2.2.
3. **Ngưỡng precision 0,99 loại bỏ các con số 95–98% trong tài liệu.** Xem §5.2 —
   phép tính cho thấy 97% accuracy tương ứng precision ≈ 0,78 trên luồng thật.

Nhưng có **hai chỗ hẹp mà ML thật sự đúng việc** (§8), và ba việc nên làm ngay bất kể
có ML hay không (§9) — trong đó một dương tính giả có thật của bộ luật hiện tại, tìm
ra trong lúc nghiên cứu này (§9.3).

---

## 1. Ràng buộc từ hiện trạng

Số liệu lấy từ repo, không phải giả định.

| Ràng buộc | Bằng chứng | Hệ quả cho ML |
|---|---|---|
| Chạy trên Pi 4, 4 GB RAM, arm64 | [01-requirements.md:182](docs/01-requirements.md) | Suy luận phải nhẹ; huấn luyện phải ngoại tuyến |
| Binary tĩnh, `CGO_ENABLED=0` | [deploy/Dockerfile](deploy/Dockerfile) | Không dùng được thư viện C; loại ONNX Runtime, loại `golightly` |
| `classify` thuần túy, không I/O | [types.go:1](internal/classify/types.go) | Suy luận **không được** nằm trong `Score()` |
| Giải thích được là yêu cầu cứng | FR-3.2, ADR-6 | Đầu ra mô hình phải quy về một `Signal` đọc được |
| Không tín hiệu HTTP nào tự vượt 5,5 | [score_test.go:405](internal/classify/score_test.go) | Tín hiệu ML phải bị chặn trần y hệt |
| Thân HTML bị vứt sau khi đọc | [http.go:166](internal/enrich/http.go) | TF-IDF cần thay đổi tầng lưu trữ |
| `decisions` = 0 dòng | `sqlite3 data/dnsguard.db` | Không có tập nhãn người |
| Toàn bộ phụ thuộc Go: 15 dòng | [go.mod](go.mod) | Thêm một thư viện ML là quyết định lớn |

Điểm cuối đáng nhấn: dự án này cố tình gọn. Kéo vào một thư viện suy luận cây quyết
định pre-1.0 để phục vụ một tín hiệu bị chặn trần ở +2,5 là tỉ lệ chi phí/lợi ích xấu.

---

## 2. Phương án A — XGBoost/LightGBM trên đặc trưng từ vựng

### 2.1 Đúng ở đâu

Đề xuất đúng về **chi phí**: một GBDT sâu 4–6 tầng, 100–300 cây, nặng vài trăm KB,
suy luận dưới 10 µs mỗi domain. Trên Pi 4 điều đó là không đáng kể. Không có tranh
cãi gì về mặt tài nguyên.

Và đúng về **một loại domain**: tên miền sinh bởi thuật toán (DGA) *thật sự* khác
biệt về từ vựng. Đó là lý do văn liệu báo 95–98% — phần lớn đo trên URL độc hại/DGA,
nơi tính ngẫu nhiên của chuỗi chính là nhãn.

### 2.2 Sai ở đâu — hạ tầng quảng cáo không có đặc trưng từ vựng

Đây là phản biện chính, và nó **đo được**. Chạy `Score()` với `Facts{}` rỗng — tức là
chỉ còn tín hiệu từ vựng, đúng thứ mà phương án A muốn thay thế:

| Domain | Thực chất | Nhãn trái nhất | Len | Entropy | **Điểm từ vựng** |
|---|---|---|---|---|---|
| `scorecardresearch.com` | tracking | `scorecardresearch` | 17 | 2,82 | **0,00** |
| `outbrain.com` | quảng cáo | `outbrain` | 8 | 3,00 | **0,00** |
| `criteo.com` | quảng cáo | `criteo` | 6 | 2,58 | **0,00** |
| `quantserve.com` | tracking | `quantserve` | 10 | 3,12 | **0,00** |
| `doubleclick.net` | quảng cáo | `doubleclick` | 11 | 3,10 | 3,00 *(từ khóa vá tay)* |
| `booking.com` | nội dung | `booking` | 7 | 2,52 | 0,00 |
| **`cdn-images-1.medium.com`** | **nội dung** | `cdn-images-1` | 12 | **3,42** | **2,00** |

Đọc bảng này theo chiều dọc: **CDN nội dung của Medium đạt 2,00, còn bốn công ty theo
dõi lớn đạt 0,00.** Tín hiệu từ vựng không chỉ yếu — nó **xếp hạng ngược**. Domain duy
nhất mà nó bắt đúng, `doubleclick.net`, bắt được vì có người đã viết tay chuỗi
`doubleclick` vào danh sách từ khóa, không phải vì cấu trúc chuỗi nói lên điều gì.

`criteo` (entropy 2,58) còn "ít ngẫu nhiên" hơn `booking` (2,52) chỉ 0,06 — hai chuỗi
này không phân biệt được bằng bất kỳ đặc trưng từ vựng nào, dù một cái là mạng quảng
cáo tỉ đô và cái kia là trang đặt phòng.

Đây không phải vấn đề huấn luyện kém hay thiếu đặc trưng. Đây là **thông tin không có
trong đầu vào.** Không mô hình nào — XGBoost hay gì khác — trích ra được thứ không tồn tại.

**Phát hiện phụ, cần xử lý riêng:** `cdn-images-1` có đúng 12 ký tự và entropy 3,42,
vượt sát cả hai ngưỡng `randomLabelMinLen = 12` / `randomLabelMinEntropy = 3,4`
([weights.go:48](internal/classify/weights.go)). Nên `random_sub` **đang kích hoạt trên
CDN ảnh của Medium**. Chưa đủ để chặn (2,00 < 5,5) nên không phải lỗi cấp cứu, nhưng
nó là dương tính giả thật, và mẫu tên `cdn-images-N` / `img-static-2` rất phổ biến.
Xem §9.3.

Còn một điều đặc trưng từ vựng mù theo đúng nghĩa kiến trúc: **CNAME cloaking tồn tại
chính vì tên miền trông vô hại.** `metrics.trangweb.vn` trỏ về `eulerian.net` hoàn hảo
về từ vựng. Đó là kỹ thuật né mà hệ thống được xây để bắt ([06 §2.1](docs/06-classification.md)),
và mô hình chỉ đọc tên không bao giờ thấy được.

Nói gọn: với `ads`/`tracking`, đặc trưng từ vựng phần lớn chỉ mã hóa lại **danh sách 30
từ khóa đã nằm sẵn trong code**. Mô hình sẽ học lại chính cái ta đã viết tay, rồi cộng
thêm một lớp không giải thích được.

Tệ hơn: **CNAME cloaking là kỹ thuật né chính mà hệ thống được xây để bắt**
([06 §2.1](docs/06-classification.md)). `metrics.trangweb.vn` trỏ về `eulerian.net`
trông hoàn hảo về mặt từ vựng. Mô hình từ vựng mù hoàn toàn với nó, theo đúng nghĩa
kiến trúc chứ không phải do huấn luyện kém.

### 2.3 Chỗ nó thật sự có giá trị

`random_sub` hiện là luật hai ngưỡng cứng: `len ≥ 12 && entropy ≥ 3,4`, +2,0.
Đó chính xác là bài toán mà GBDT làm tốt hơn luật ngưỡng — biên quyết định phi tuyến
trên n-gram, tỉ lệ nguyên âm, chuyển tiếp ký tự, độ dài nhãn.

Nếu làm A, **phạm vi đúng là thay thế một tín hiệu `random_sub`, cho nhóm
DGA/malware**, không phải thay bộ phân loại.

---

## 3. Phương án B — TF-IDF + Logistic Regression / LinearSVC trên HTML

### 3.1 Đúng ở đâu — và đúng hơn A về mặt kiến trúc

Điểm mà đề xuất chưa nói tới nhưng là lợi thế lớn nhất của B: **mô hình tuyến tính
trên TF-IDF tự nó đã giải thích được.** Điểm số là tổng đóng góp từng từ:

```
adult 0,91 = "webcam" +0,34 · "18+" +0,28 · "escort" +0,21 · … 
```

Đó đúng bằng hình dạng của bảng `signals` hiện có. Không cần SHAP, không cần lớp giải
thích riêng. Trong khi GBDT cho ra `0,87` và không nói được vì sao — chính xác là thứ
ADR-6 từ chối.

Và về triển khai: **không cần thư viện nào cả.** Suy luận là
`sigmoid(Σ wᵢ·xᵢ + b)` trên một vector thưa. Khoảng 120 dòng Go thuần, phụ thuộc bằng 0,
mô hình là một bảng `term → weight` vài trăm KB. Xem §6.

### 3.2 Sai ở đâu — nội dung không tồn tại đúng chỗ cần nhất

Hai vấn đề, cái thứ hai nghiêm trọng hơn.

**Thứ nhất: TF-IDF học "trang này kiếm tiền thế nào", không học "domain này LÀ gì".**
[06 §2.4](docs/06-classification.md) đã dựng cả một nhóm tín hiệu quanh phân biệt này
và **cố ý không** đếm script bên thứ ba, không dò `gtag(`/`fbq(`/`adsbygoogle`. Lý do:
chúng dính vào gần như mọi trang có quảng cáo. Một mô hình TF-IDF trên HTML thô sẽ học
lại đúng những token đó — chúng là token phân biệt nhất trong tập dữ liệu — và gắn cờ
mọi tờ báo. Đây không phải rủi ro lý thuyết; nó là kết quả mặc định.

**Thứ hai: nơi cần TF-IDF nhất lại không có HTML.** Endpoint adtech thuần trả 204,
trả một pixel, hoặc thân rỗng — đó chính là tín hiệu `http_beacon` (+4,0). Không có
văn bản thì TF-IDF không có đầu vào. Mô hình im lặng đúng ở vùng quan trọng nhất.

### 3.3 Chỗ nó thật sự có giá trị

Ba phân loại mà **nội dung chính là danh tính**, nên phản biện §3.2 không áp dụng:

| Phân loại | Vì sao TF-IDF hợp | Hiện trạng |
|---|---|---|
| `adult` | Từ ngữ trên trang *là* phân loại. Không có cách nào khác ngoài đọc chữ | Chỉ dựa vào list ngoài |
| trang đỗ tên miền | `matchParking()` là danh sách vân tay cứng; TF-IDF tổng quát hóa sang nhà cung cấp chưa biết | +3,0, khớp chuỗi cố định |
| `malware`/phishing | Trang phishing có văn bản đặc trưng mạnh (form đăng nhập nhái thương hiệu) | Chỉ dựa vào VT + list |

`adult` còn có lợi thế vận hành: **mặc định tắt**, nên chi phí một lần sai thấp hơn
hẳn so với chặn nhầm `ads`.

---

## 4. Vấn đề xuyên suốt — nhãn từ đâu ra

Đây là chỗ cả hai phương án cùng gãy, và không phương án nào giải quyết được bằng
cách chọn thuật toán khác.

### 4.1 Vòng lặp nhãn

Huấn luyện trên nhãn blocklist → mô hình học "domain trong blocklist trông thế nào" →
đo trên tập giữ lại của blocklist → 97% → triển khai.

Nhưng **domain đã có trong blocklist thì không cần mô hình**: hệ thống đã có
`etld1_blocked` (+4,0) và `InPublicList`. Giá trị duy nhất của mô hình nằm ở domain
**không** có trong list nào — đúng cái phân phối mà nó chưa từng thấy nhãn thật.

Các con số 95–98% trong văn liệu đo trên phân phối blocklist, không phải phân phối
triển khai. Chúng không chuyển giao được.

### 4.2 Positive-Unlabeled, và nguồn "âm đáng tin" đã có sẵn

ADR-6 đã chỉ đúng hướng: đây là bài toán **positive-unlabeled**. Tập dương biết chắc
(blocklist), tập còn lại là *chưa gán nhãn*, không phải âm. Coi nó là phân lớp nhị
phân thường sẽ ra mô hình lệch.

Điểm hữu ích: **codebase đã có sẵn nguồn âm đáng tin.** `high_rank` (−8,0) mã hóa
"Tranco top 50k ⇒ nhiều khả năng hợp lệ". Vậy:

```
Dương đáng tin  = domain trong ≥2 blocklist độc lập
Âm  đáng tin  = Tranco top 50k \ (mọi blocklist) \ danh sách adtech
Chưa gán nhãn = phần còn lại  ← nơi mô hình phải làm việc
```

Kỹ thuật phù hợp: **spy technique** (trộn một phần dương đã biết vào tập chưa gán
nhãn để tìm âm đáng tin), hoặc **hiệu chỉnh Elkan-Noto**. Cả hai chạy được với
sklearn, ngoại tuyến, không cần thư viện đặc biệt.

### 4.3 Nguồn nhãn thật sự tốt lại đang bị vứt

Bảng `decisions` là nhãn người — chất lượng cao nhất, có **cả hai lớp**, và
append-only nên không mất. Nhưng `snapshot` hiện chỉ lưu
`status_before / score / confidence / category / signals`
([decisions.go:142](internal/store/decisions.go)).

**Không lưu vector đặc trưng thô**: chuỗi CNAME, ASN, tuổi domain, thứ hạng Tranco,
kết quả HTTP. Nghĩa là một năm nữa, khi đã có 500 quyết định người, ta vẫn không
huấn luyện được — vì facts lúc đó đã hết hạn và bị ghi đè.

Đây là việc **nên sửa ngay**, chi phí gần bằng 0, và là điều kiện cần cho mọi phương
án ML sau này. Xem §9.1.

---

## 5. Ngưỡng chấp nhận — phép tính quyết định

### 5.1 Đo sai chỉ số là hỏng cả nghiên cứu

`accuracy` vô nghĩa ở đây vì tập thật lệch mạnh về `content`. Chỉ số đúng là
**precision tại một recall cố định**, đúng như [06 §6](docs/06-classification.md)
đã đặt: mục tiêu **precision ≥ 0,99**.

### 5.2 97% accuracy tương ứng precision bao nhiêu

Giả sử tỉ lệ nền 10% adtech / 90% content, mô hình đối xứng TPR = TNR = 0,97:

```
TP = 0,10 × 0,97 = 0,0970
FP = 0,90 × 0,03 = 0,0270
precision = 0,0970 / (0,0970 + 0,0270) = 0,78
```

**0,78 so với mục tiêu 0,99.** Cứ 100 domain bị chặn thì 22 cái chặn nhầm.

Ngược lại, để đạt precision 0,99 ở tỉ lệ nền đó, với recall khiêm tốn 0,50:

```
FPR ≤ TPR × 0,10 / (0,90 × 99) = 0,50 × 0,001122 ≈ 0,00056
```

Tức **dưới 6 lần dương tính giả trên 10.000 domain nội dung.** Đó là ngưỡng thật.
Không bài báo nào trong §7 báo cáo ở mức đó, vì họ đo trên tập cân bằng.

### 5.3 Giao thức đánh giá bắt buộc

Nếu vẫn thử, bốn điều này quyết định kết quả có thật hay không:

1. **Chia theo eTLD+1, không theo domain.** `a.doubleclick.net` ở train và
   `b.doubleclick.net` ở test là rò rỉ nhãn — và là nguồn gốc phổ biến nhất của các
   con số 97% giả.
2. **Chia thêm theo thời gian.** Train trên domain thấy trước ngày T, test sau T.
   Hạ tầng adtech thay đổi liên tục.
3. **Đo *lift* so với điểm quy tắc hiện có, không đo độc lập.** Câu hỏi không bao giờ
   là "mô hình tốt không", mà là "**mô hình thêm được gì mà 30 từ khóa chưa có**".
   Nếu lift < 3 điểm precision ở cùng recall → không triển khai.
4. **Kiểm soát âm bắt buộc.** Mô hình chạy trên `hardAllow` (35 mục) và Tranco top
   50k phải cho **0 dương tính**. Test khóa lại, giống test ở
   [score_test.go:405](internal/classify/score_test.go).

---

## 6. Nếu làm — làm ở đâu trong kiến trúc

### 6.1 Suy luận không được vào `classify`

`classify` thuần túy là ràng buộc cho phép chấm điểm lại toàn bộ CSDL mà không tra
lại dịch vụ ngoài, và cho bảng `dry_run` chính xác tuyệt đối. Nạp mô hình và chạy
suy luận trong `Score()` phá cả hai.

**Đúng chỗ: một `Enricher` mới**, y hệt `http` và `vt`:

```
internal/enrich/ml.go   → chạy mô hình, ghi domain_facts(source='ml')
internal/classify       → Facts.ML {Score, Version, TopTerms} sinh một Signal
```

Kết quả: mô hình chạy một lần khi làm giàu; chấm điểm lại vẫn thuần túy và tức thì;
đổi trọng số của tín hiệu ML không cần chạy lại mô hình. Đúng khuôn `vt_malicious`.

Cần thêm `'ml'` vào ràng buộc `CHECK (source IN (...))` của `domain_facts`
([0003_http_analysis.sql:11](internal/store/migrations/0003_http_analysis.sql)).

### 6.2 Chặn trần, khóa bằng test

Tín hiệu ML nhận **+2,5**, dưới ngưỡng `ads` 5,5, và thêm vào đúng test đang khóa
nhóm HTTP. Lý do không đổi: *một tín hiệu tự nó đủ để chặn nghĩa là một lần đoán sai
đủ để chặn nhầm.* Với tín hiệu không giải thích trực tiếp được, lập luận này còn mạnh hơn.

### 6.3 Phiên bản mô hình phải nằm trong `Signal.Detail`

```go
Detail: map[string]any{"model": "lex-dga-v3", "p": 0.82, "top": [...]}
```

Không có nó, một quyết định chặn từ sáu tháng trước không truy ngược được về mô hình
đã tạo ra nó — tức là mất tính giải thích được theo thời gian, dù tại thời điểm chặn
vẫn có vẻ ổn.

### 6.4 Suy luận trong Go — so sánh

| Cách | Phụ thuộc | CGO | Phù hợp |
|---|---|---|---|
| **Tuyến tính tự viết** | 0 | không | ★★★ Mô hình tuyến tính chỉ là tích vô hướng, ~120 dòng |
| [`dmitryikh/leaves`](https://github.com/dmitryikh/leaves) | 1, MIT, ~485★ | không | ★★ Go thuần, đọc LightGBM text/JSON + XGBoost binary. Nhưng **pre-1.0, API còn đổi**, chỉ hỗ trợ sigmoid/softmax, XGBoost có sai lệch dấu phẩy động nhỏ |
| [`Elvenson/xgboost-go`](https://github.com/Elvenson/xgboost-go) | 1 | không | ★★ Go thuần, chỉ đọc JSON dump của XGBoost. Ít dùng hơn `leaves` |
| `alden-scientific/golightly` | purego + `.so` | không CGO nhưng **cần thư viện động** | ✗ Phá binary tĩnh |
| ONNX Runtime | C++ | có | ✗ Phá `CGO_ENABLED=0` |

**Đây là lý lẽ kỹ thuật mạnh nhất nghiêng về phương án tuyến tính:** LogReg/LinearSVC
cần *không thư viện nào*, GBDT cần một thư viện pre-1.0 vào một `go.mod` 15 dòng.

### 6.5 Huấn luyện ngoại tuyến, artifact nhúng vào binary

Pi 4 không cài Python. Quy trình:

```
tools/ml/          scikit-learn, chạy trên máy dev hoặc CI
  export.py    →   model.json  (term → weight, hoặc cây LightGBM)
internal/classify/model/  go:embed
```

Mô hình tuyến tính ~200–500 KB sau cắt tỉa; GBDT 100 cây sâu 5 khoảng 300 KB. Cả hai
không đáng kể so với bundle React đang nhúng.

### 6.6 Với TF-IDF: lưu vector, đừng lưu HTML

`http.go` đọc tối đa 256 KB rồi vứt. Ba lựa chọn:

| Cách | Dung lượng ở 50k domain | Đánh giá |
|---|---|---|
| Lưu HTML thô | **~12,8 GB** | ✗ Không khả thi trên Pi; và lưu nội dung bên thứ ba lên đĩa người vận hành là vấn đề riêng tư |
| Chỉ lưu điểm số | ~0 | ✗ Huấn luyện lại **bắt buộc tải lại toàn bộ** — làm lộ mạng lần nữa |
| **Lưu vector băm thưa** (top ~300 term) | **~120 MB** | ★ Huấn luyện lại không cần tải lại; không giữ văn bản gốc |

Chọn cách ba. Đây cũng là quyết định riêng tư đúng: giữ được đặc trưng, không giữ nội dung.

---

## 7. Văn liệu — đọc gì và đọc thế nào

| Nguồn | Liên quan | Cảnh báo khi đọc |
|---|---|---|
| [Improved Detection of Trackers via One-class Learning](https://arxiv.org/abs/1603.06289) | **Sát nhất.** Dùng one-class learning đúng vì blocklist không đầy đủ; 99% vs 78% của công cụ sẵn có; phát hiện được dịch vụ theo dõi chưa ai biết | Phân loại **file JS**, không phải domain. Xác nhận lập luận PU ở §4, không phải công thức copy được |
| [An Assessment of Lexical, Network, and Content-Based Features](https://www.ncbi.nlm.nih.gov/pmc/articles/PMC9436524/) | So sánh đúng ba nhóm đặc trưng đang bàn | Mục tiêu là URL **độc hại**, không phải adtech. Kết quả không chuyển giao sang `ads` |
| [Detecting Malicious URLs Using Lexical Analysis](https://cyberlab.usask.ca/papers/Mamun2016_Chapter_DetectingMaliciousURLsUsingLex.pdf) | Danh mục đặc trưng từ vựng đầy đủ | Tập cân bằng; xem §5.2 |
| [A Lightweight Online Advertising Classification System](https://www.scitepress.org/papers/2017/64598/64598.pdf) | Đúng bài toán quảng cáo, đúng ràng buộc "nhẹ" | Kiểm tra kỹ cách chia tập — hầu hết chia theo URL, không theo eTLD+1 |

Kết luận từ văn liệu: **đặc trưng từ vựng đã được chứng minh cho URL độc hại/DGA,
chưa được chứng minh cho hạ tầng quảng cáo.** Đó chính là ranh giới ở §2.3.

---

## 8. Lộ trình đề xuất

Bốn bước, mỗi bước có điều kiện dừng rõ ràng. Không bước nào phụ thuộc vào bước sau.

### Bước 0 — Bộ đánh giá (làm trước, bắt buộc)

[06 §6](docs/06-classification.md) mô tả tập đánh giá phân tầng 300 mẫu **nhưng nó
chưa tồn tại**. Không có nó thì không thể trả lời "ML có thêm được gì" — cũng không
thể biết bộ luật hiện tại đang chạy tốt tới đâu.

Đây là việc có giá trị cao nhất trong toàn bộ nghiên cứu này, **và nó không cần ML.**

### Bước 1 — TF-IDF + LogReg cho `adult` và trang đỗ tên miền

Phạm vi hẹp nhất mà vẫn có ích thật:

- Nội dung *là* danh tính → phản biện §3.2 không áp dụng
- Tuyến tính → tự giải thích được, hợp khuôn `Signal` sẵn có
- **Không thêm phụ thuộc nào**
- `adult` mặc định tắt → chi phí sai thấp
- Dựng xong toàn bộ đường ống: `tools/ml` → `go:embed` → enricher → signal

Điều kiện đi tiếp: precision ≥ 0,99 trên tập bước 0, 0 dương tính trên `hardAllow`.

### Bước 2 — GBDT từ vựng, chỉ cho DGA/malware

Chỉ khi bước 1 xong và đường ống đã chạy. Thay/bổ sung `random_sub`, chặn trần +2,5.

Điều kiện đi tiếp: **lift ≥ 3 điểm precision** so với luật `len ≥ 12 && entropy ≥ 3,4`
hiện tại. Không đạt → giữ luật cũ, dừng. Luật hai ngưỡng đọc được; mô hình 300 cây thì không.

### Bước 3 — PU learning trên `decisions` (≥ 12 tháng nữa)

Chỉ khả thi khi có ≥ 500 quyết định người **và** §9.1 đã làm từ trước. Đây là hướng
ADR-6 đã chỉ, và là nơi ML thật sự có thể vượt luật — vì lúc đó nhãn đến từ mạng của
chính người vận hành, không phải từ blocklist quốc tế.

### Không bao giờ

- Để đầu ra mô hình một mình quyết định chặn `ads`/`tracking`
- Thay điểm quy tắc bằng điểm mô hình. Điểm quy tắc là *đặc trưng đầu vào*, đúng như ADR-6

---

## 9. Việc nên làm ngay, bất kể có ML hay không

### 9.1 Mở rộng `decisions.snapshot` thành vector đặc trưng đầy đủ

[decisions.go:142](internal/store/decisions.go) đang lưu 5 trường. Thêm facts thô:
CNAME chain, ASN, tuổi, Tranco rank, tóm tắt HTTP.

**Vì sao gấp:** `domain_facts` có `expires_at` và bị ghi đè. Mỗi quyết định người ghi
hôm nay mà không kèm facts là **một mẫu huấn luyện mất vĩnh viễn**. Chi phí sửa: một
hàm, vài chục dòng. Chi phí không sửa: sau 12 tháng vẫn không huấn luyện được gì.

Việc này có giá trị ngay cả khi không bao giờ làm ML — nó biến `decisions` thành nhật
ký kiểm toán đầy đủ thay vì chỉ ghi lại điểm số.

### 9.2 Dựng tập đánh giá phân tầng (bước 0)

Đo được bộ luật hiện tại là điều kiện cần cho mọi so sánh sau này.

### 9.3 Siết `random_sub` để không bắt nhầm mẫu `cdn-images-1`

Phát hiện ở §2.2: `cdn-images-1` (len 12, entropy 3,42) kích hoạt `random_sub` +2,0.
Nhãn kiểu `cdn-images-N`, `img-static-2`, `assets-cdn-3` rất phổ biến ở CDN nội dung.

Nguyên nhân: entropy Shannon coi `-` và chữ số như ký tự bình thường, nên gạch ngang
làm tăng entropy đúng ở những nhãn *có cấu trúc rõ ràng* — ngược hẳn ý định của tín hiệu.

Ba cách sửa, chưa chọn:

- Bỏ qua nhãn có gạch ngang phân tách thành từ có nghĩa (`cdn`, `images` là từ thật)
- Tính entropy trên nhãn đã bỏ `-` và chữ số → `cdnimages` cho entropy thấp hơn nhiều
- Nâng `randomLabelMinLen` lên 14

Cần chạy trên dữ liệu thật (bước 0) trước khi chốt — sửa mù có thể làm hụt DGA thật.
Đây là ví dụ đúng cho luận điểm §8: **bộ đánh giá có giá trị ngay cả khi không làm ML.**

---

## 10. Câu hỏi chưa chốt

1. **Mạng này thực tế thấy bao nhiêu domain phân biệt mỗi tháng?** CSDL dev có 39.
   Nếu con số thật dưới ~5.000 thì bước 3 không bao giờ đủ dữ liệu và nên bỏ khỏi lộ trình.
2. **Chấp nhận thêm phụ thuộc `leaves` (pre-1.0) cho bước 2 không?** Nếu không,
   bước 2 phải hoặc tự sinh mã Go từ cây, hoặc bỏ.
3. **`adult` có đáng đầu tư không?** Mặc định tắt. Nếu không ai bật thì bước 1 nên
   đổi mục tiêu sang trang đỗ tên miền và phishing.
4. **120 MB vector băm trên Pi 4 có chấp nhận được không**, hay cần cắt xuống top-100
   term (~40 MB)?
