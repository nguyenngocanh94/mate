# Crew observability để tối ưu harness của repo

Ngày: 2026-09-28. Trạng thái: collector, projector và Crew dashboard đã triển
khai, kiểm chứng trên transcript thật. Captain chọn giữ console hiện tại và
xem trước bản mới ở cổng 7778; chưa chuyển database chính.
Bằng chứng triển khai:
`docs/evidence/crew-observability-2026-09-28.md`.

## 1. Yêu cầu sản phẩm

Observer phải thu thập đủ bằng chứng để dashboard trả lời ngay:

1. Crew đang làm việc gì, ở lượt nhận yêu cầu nào?
2. Đoạn nào đang tiêu token, bao nhiêu đầu vào mới / cache / đầu ra?
3. Thời gian đang mất vào lệnh nào, chờ tiến trình nào, hay chờ Mate?
4. Crew đang lặp thao tác gì; kết quả, lỗi hoặc file có thay đổi giữa các lần không?
5. Điều gì trong harness của **repo này** đáng sửa, và bằng chứng ở đâu?

Đơn vị đọc chính là **đoạn công việc bên trong một lượt nhận yêu cầu**.
Tổng token theo Crew và bảng tool chỉ là dữ liệu hỗ trợ. Người dùng không phải
đọc JSON hay lần qua hàng trăm lời gọi model để phát hiện vấn đề.

Một lượt = một prompt/brief từ Captain hoặc Mate và phần việc nó gây ra.
Một model call = một response có usage riêng. Hai đơn vị có ID riêng.

## 2. Những gì đã kiểm tra trong repo và dữ liệu thật

- Observer hiện gọi `timeline.Ingest` cuối mỗi vòng quan sát; khoảng nghỉ mặc
  định giữa hai vòng là 5 giây. Đây là nơi ghi dữ liệu, dashboard dùng DB chỉ đọc.
- DB đã giữ `turn.harness_turn_ref`, các bucket token, tool call/result, timestamp,
  status, câu hỏi và thời gian chờ trả lời. `turn` hiện là model call.
- Codex đang được đọc qua `token_count` và các tool wrapper như `exec`/`wait`.
  Một wrapper trả về thành công không có nghĩa lệnh bên trong chạy thành công.
- Kiểm tra lịch sử Crew `ios7`: 363 model calls thuộc 3 prompt turns; 153 action
  có `write_stdin`. Đây là số lần kiểm tra tiến trình, chưa phải kết luận lãng phí.
- Rollout của Crew đó còn có 363 `token_usage_record`, 162 item
  `CommandExecution` (152 completed, 10 failed), 42 `FileChange`, 3 `UserMessage`,
  các item `AgentMessage`, và mốc `started_at_ms` / `completed_at_ms`.
  Trong khi đó các action `exec` hiện có `ok=true`; lỗi của lệnh con bị che bởi
  kết quả wrapper. Đây là lỗ hổng chẩn đoán phải sửa từ ingest.
- `CommandExecution` chứa command, cwd, process_id, exit_code, duration và output.
  `token_usage_record` chứa response_id, turn_id, root_turn_id và usage của response.
  `turn_context` chứa model/effort; `world_state` có một phần chỉ dẫn đầu phiên.
  Những record này được xác nhận trên rollout hiện có, chưa được coi là hợp đồng
  của mọi phiên bản Codex hoặc Claude.
- File đã kiểm tra khoảng 9,5 MB. Đọc lại cả file mỗi vòng như hiện tại sẽ không
  phù hợp khi thêm phân tích cho nhiều Crew; cần ingest tăng dần và cache theo cursor.

Nguồn code: `cmd/mate/console_watch.go`, `internal/watch/watch.go`,
`internal/timeline/ingest.go`, `internal/timeline/transcript.go`,
`internal/harness/transcript.go`, `internal/harness/codex_transcript.go`.

## 3. Luồng ghi nhận

```text
Harness transcript + snapshot cấu hình lúc launch + status/sent/health
  → adapter chuẩn hóa prompt, response usage, execution và context events
  → observer ghi các fact có ID, thời gian và source reference
  → projector tạo đoạn công việc, chuỗi lặp và phát hiện có bằng chứng
  → dashboard hiển thị vị trí đang chạy, nơi tốn nhất và nguyên nhân cần xem
```

Observer dùng parser và quy tắc xác định được, không gọi thêm model để giải thích
mỗi poll. Việc tự ghi `working:` của Crew bổ sung tên đoạn, không phải điều kiện
để quan sát được công việc. Tất cả detector có version và bằng chứng nguồn.

### 3.1. Facts phải lưu

| Fact | Nội dung cần ghi | Mục đích |
| --- | --- | --- |
| Run profile | repo, commit/dirty state khi bắt đầu, Crew, harness/version, model, effort, các file chỉ dẫn/config/brief có hash và kích thước | Biết lần chạy dùng harness nào; so sánh sau khi đổi harness |
| Prompt turn | ID từ harness, session, sender, prompt thực, thời điểm nhận/bắt đầu/kết thúc, kết quả cuối | Gom đúng lượt và tách thời gian giữa các yêu cầu |
| Model response | response ID, prompt ID, usage riêng từng bucket, context size, model thực tế, timestamp/độ chính xác của mốc | Định vị token theo call rồi theo đoạn, không nhân đôi usage |
| Execution | ID, parent wrapper ID nếu liên kết được, process ID, command/tool, cwd, target file/range, start/end, exit code/status | Nhìn được lệnh thật, lỗi thật và tiến trình mà Crew đang chờ |
| Tool output | bytes, hash nội dung, số byte output mới khi poll, truncation, ref tới nguồn | Phân biệt lặp không đổi với tiến trình có thêm kết quả |
| Context input/event | chỉ dẫn hoặc kết quả tool được quan sát, path/hash/bytes, source, compaction/reset khi có marker | Theo dõi context lớn lên và nội dung được nạp lại |
| Progress evidence | FileChange, commit, kết quả test, câu hỏi/handback, output mới | Đánh giá một chuỗi lặp có tạo ra thay đổi hay chưa |
| Recording health | last observed/ingested/usage time, thiếu transcript, cursor/parser lỗi, khả năng adapter | Dashboard không hiển thị dữ liệu cũ như thể vẫn đang live |

Mỗi fact có `occurred_at`, `observed_at`, `source_ref`, `measurement_kind` và
liên kết actor/session/turn khi nguồn cung cấp. ID không đọc được phải để unknown;
không gán sang lượt trước chỉ vì timestamp gần nhau.

Chỉ thu metadata cần cho chẩn đoán; không cần lưu nội dung reasoning riêng tư.
Config snapshot chỉ lấy các trường và tài liệu chỉ dẫn được phép, không lấy
giá trị credential hay toàn bộ environment. Snapshot trước launch do đường spawn
ghi thành file trong thư mục Crew để observer ingest; nếu chỉ quan sát được sau
launch thì đánh dấu thời điểm đó, không gọi là cấu hình ban đầu.

### 3.2. Dedupe và tương quan

- Bản triển khai đầu giữ `turn` từ `token_count` làm ledger duy nhất để khớp
  với `mate usage`. `token_usage_record` cung cấp response ID và bằng chứng
  native; chỉ nối vào ledger khi cumulative usage khớp chính xác. Response chưa
  nối được hiện thiếu coverage, không bị cộng thêm vào tổng.
  Usage cộng dồn của turn/thread không được cộng như usage của response.
- UserMessage/task_started, tool call/item_completed và wrapper/result có thể
  là nhiều biểu diễn của cùng một fact. Ghi aliases theo ID/correlation xác nhận
  được; không dedupe chỉ bằng command giống nhau.
- Tool wrapper và lệnh con là hai cấp trong một cây. Token chỉ tính một lần ở
  model response. Số lần thực thi/lỗi lấy từ lệnh con khi đã liên kết chắc chắn.
  Wrapper chưa giải mã được phải hiện rõ mức quan sát còn thiếu.
- `process_id` nối lần khởi chạy với các lần poll và kết thúc. Nếu transcript
  không đủ liên kết, chỉ báo "poll chưa nối được tiến trình", không đoán tên job.
- Tool đang chạy có start và end chưa biết. Kết quả đến sau cập nhật cùng fact,
  không sinh một execution mới và không đánh dấu fail khi chưa có kết quả.
- Parser giữ cursor + state cùng một transaction. Restart, đọc lại tail,
  compaction và cumulative counter reset đều phải bảo toàn tổng usage.

DB vẫn là dữ liệu dẫn xuất theo `docs/mvp.md`: transcript/log/snapshot là nguồn,
reindex tái tạo được facts và phát hiện. Bắt đầu bằng bổ sung các projection cho
prompt turn, execution, run profile, segment và finding; tận dụng `turn`,
`action`, `event` hiện có, không tạo một kho token tổng thứ hai.

## 4. Phân đoạn và quy tắc tính

### 4.1. Đoạn công việc

Phân đoạn theo bằng chứng execution và target: nạp chỉ dẫn, khảo sát repo,
đọc/tìm kiếm, sửa file, build/test, debug, browser, git/bàn giao, trao đổi,
chờ tiến trình, chưa phân loại. Tên cụ thể phải ưu tiên target:
"Chạy TripUITests", "Đọc cấu hình Xcode", "Chờ tiến trình build #…".

Một lượt có thể đi qua đọc → sửa → test → sửa → test. Giữ nguyên thứ tự và
các lần quay lại, không gom mọi thao tác `test` thành một cục mất lịch sử.
Status của Crew là một annotation, có ghi nguồn. Đoạn tự nhận diện ghi quy tắc
phân loại và có trạng thái mixed/unknown khi call chứa nhiều loại việc.

Execution chạy nền còn sống trong lúc Crew sửa file được hiển thị ở lane riêng.
Các call chỉ poll nó thuộc đoạn "kiểm tra tiến trình", vẫn nối về job gốc để
người đọc phân biệt thời gian job chạy với token dùng để hỏi tiến độ.

### 4.2. Token

- Mỗi model call thuộc tối đa một đoạn để tổng token đoạn cộng đúng tổng lượt.
  Call hỗn hợp nằm ở đoạn mixed và vẫn mở được danh sách thao tác của nó.
- Luôn hiện input mới, cache read, cache write, output riêng. Reasoning tokens
  là phần đã nằm trong output khi harness định nghĩa như vậy, không cộng thêm.
- Nhãn đúng là "token của các call trong đoạn này". Không chia đều token cho
  từng tool, không gọi tổng đó là số token do một command/file cụ thể gây ra.
- Một output lớn có thể làm context các call sau lớn lên: nối output → response
  tiếp theo khi đủ ID/thứ tự. Ghi bytes và context delta; mối liên hệ quan sát
  được không phải phép đo chính xác số token mà file ấy gây ra.
- Nội dung chỉ dẫn quan sát được có bytes và có thể có **ước tính** token với
  tokenizer/version đã ghi. File tồn tại trong repo không chứng minh đã vào
  prompt; cấu hình dự định nạp và nội dung đã quan sát phải khác nhãn.
- Cache lớn là một thành phần chi phí, không tự động là lỗi. Chi phí tiền chỉ
  xuất khi có giá cho model/bucket; lưu version giá dùng cho phép so sánh.

### 4.3. Thời gian

- Prompt elapsed đo từ mốc prompt thực, không dùng `crew age` làm thời gian làm.
- Tool elapsed tính union các khoảng chạy; hai tool song song 10 giây cho
  elapsed 10 giây. Tổng invocation time có thể là 20 giây và phải ghi đúng tên.
- Chờ job, chờ Mate/captain, khoảng rảnh giữa lượt và model/harness overhead
  được hiển thị riêng; các lane có thể trùng nhau, không cộng thành tổng giả.
- Dùng mốc native của harness khi có. Khoảng giữa hai `token_count` hiện tại
  không phải latency API. Phần thời gian chưa được giải thích ghi "chưa phân bổ",
  không đổi tên thành thinking/model latency.
- Native Reasoning/AgentMessage item timing, nếu dùng, chỉ mang nghĩa hoạt động
  mà harness đã đặt tên; không chứng minh đó là toàn bộ thời gian suy luận/API.

## 5. Các vấn đề observer phải nhận diện

Mỗi finding gồm: câu mô tả cụ thể, đang xảy ra/đã kết thúc, khoảng thời gian,
turn/segment liên quan, số lần, usage của các call liên quan, mức độ chắc chắn,
bằng chứng, và vị trí cấu hình/quy trình nên xem. Không tự sửa harness.

| Finding | Bằng chứng tối thiểu | Dashboard cần nói | Hướng xem xét |
| --- | --- | --- | --- |
| Poll thường xuyên | Cùng process; số poll; output mới; usage các response liên quan | Đang hỏi tiến độ job nào, bao nhiêu lần, tốn token nào | Cách chờ, thời hạn chờ, thông báo completion |
| Đọc lại nội dung | Cùng file/range hoặc truy vấn; hash kết quả không đổi | File/truy vấn nào được đọc lại, ở các đoạn nào | Bản đồ repo, chỉ dẫn tìm file, ghi nhớ kết quả |
| Retry cùng lỗi | Cùng command/cwd/config; exit code/error signature; thay đổi code giữa các lần | Lệnh lỗi gì, bao nhiêu lần, có sửa gì giữa hai lần không | Lệnh test/build, prerequisite, setup môi trường |
| Vòng sửa–test kéo dài | Chuỗi FileChange → cùng test → lỗi; có/không có tiến bộ quan sát được | Đang quay lại test nào, bao nhiêu vòng, tốn thời gian/token nào | Test mục tiêu, brief, troubleshooting guide |
| Output/context phình | Tool result bytes/truncation; context các call sau; thành phần input quan sát được | Lệnh nào trả nhiều output, context tăng ở đâu | Giới hạn log, đọc vùng cụ thể, lọc kết quả |
| Nạp lại context | Compaction/reset marker và các lần nạp lại nội dung xác nhận được | Bao nhiêu lần, xảy ra giữa đoạn nào | Nội dung chỉ dẫn, cách chia việc, checkpoint |
| Công cụ chậm | Execution start/end hoặc still running; so với lịch sử phù hợp | Job/lệnh cụ thể đang chiếm thời gian | Build cache, simulator, timeout, phạm vi test |
| Chờ quyết định | Question → message trả lời; người đang được chờ | Crew dừng ở quyết định nào và đã chờ bao lâu | Brief, quyền quyết định, đường trả lời |

Quy tắc chống báo nhầm:

- Lặp không đồng nghĩa lãng phí. Poll chờ một test dài vẫn hợp lệ; finding chỉ
  rõ overhead của polling và bằng chứng output, không kết luận test bị treo.
- Retry sau khi sửa code là vòng thử sửa; chỉ nói "cùng lỗi chưa thấy thay đổi"
  khi bằng chứng đủ. Thiếu file state thì phải nói thiếu, không coi là không đổi.
- Chuẩn hóa path worktree về repo-relative; giữ nguyên cờ lệnh, test target,
  query và file range ảnh hưởng nghĩa. Không gộp hai lệnh khác nhau vì chung prefix.
- Ngưỡng số lần/khoảng thời gian cấu hình được và phải có fixture hiệu chỉnh.
  Bản đầu ưu tiên số liệu thực và xếp hạng ảnh hưởng; chưa dùng một điểm số "waste"
  hoặc hứa tiết kiệm X token khi chưa chạy đối chứng.
- Không cộng usage các finding chồng lấn. Một call có thể là bằng chứng của
  cả output lớn và lặp test nhưng chỉ xuất hiện một lần trong tổng đoạn/lượt.

## 6. Màn hình cần thiết kế

### 6.1. Vừa mở Crew là nhìn ra vấn đề

1. **Đang làm gì:** tên đoạn/target; từ lúc nào; token mới nhất; token tăng trong
   cửa sổ gần đây; tuổi dữ liệu. Đang chạy tool phải có elapsed đang tăng ngay
   cả khi chưa có usage mới. Usage chỉ tăng khi harness báo, không nội suy.
2. **Các vấn đề đáng xem:** tối đa ba finding có ảnh hưởng lớn, viết thành câu
   có đối tượng, số lần và chi phí. Click đưa thẳng tới khoảng đang được nói tới.
3. **Timeline của lượt:** các đoạn theo thời gian; token tăng theo model response;
   tool/process và chờ quyết định ở lane riêng; phần lặp được khoanh và nối lại.
   Cùng thang thời gian giúp nhìn đoạn chậm, đoạn token dốc, và chuỗi quay vòng.
4. **Bảng đoạn công việc:** tên/target, token theo bucket, elapsed, số model calls,
   số lần lặp, kết quả. Sort theo token mới, tổng token hoặc thời gian.
5. **Bằng chứng khi mở:** prompt, commands thực, exit/error, output mới/không đổi,
   file changes, model calls và source refs. Raw events nằm dưới cùng.

Ví dụ câu hiển thị (mẫu câu, không phải số đo của một run):

> Đang kiểm tra tiến độ `xcodebuild … TripUITests`: N lần poll, M lần không có
> output mới. Các call kiểm tra tiến độ dùng X input mới + Y cache trong Z phút.

> `TripService.swift` cùng vùng nội dung đã đọc N lần; hash kết quả không đổi.
> Xem những đoạn gọi lại và chỉ dẫn tìm file của repo.

Không thay câu này bằng "exec × 153" hay một khối JSON.

### 6.2. Từ Crew lên repo

Run profile phải được ghi ngay từ phiên bản đầu, để lần sau đổi harness không
mất baseline. View repo sau đó gom các finding tái diễn theo profile revision:
đọc lặp file nào, test lỗi gì, polling chiếm bao nhiêu call, context ban đầu.

So sánh các run cùng model/effort, loại việc và quy mô tương đương; hiển thị số
mẫu, median/p90 và phân bố. Hai task khác nhau trước/sau sửa AGENTS.md không đủ
để khẳng định sửa AGENTS.md làm giảm chi phí. Đo hiệu quả cần replay task cố định
hoặc ghi rõ đây chỉ là quan sát trên tập run khác nhau.

### 6.3. Freshness và thiếu dữ liệu

Dashboard ghi riêng lần observer cập nhật và lần harness báo usage. Mục tiêu
cập nhật trong vòng một vòng quan sát cộng thời gian ingest sau khi source ghi
record, không hứa token theo thời gian thực khi response chưa có usage.

Observer tắt/chậm, parser chưa hỗ trợ record, child process chưa nối, transcript
thiếu hoặc bị truncate đều phải hiện thành khoảng trống có lý do. Backfill từ
transcript cũ giữ capability coverage; không tạo config snapshot quá khứ từ
file cấu hình hiện tại.

## 7. Trình tự triển khai và tiêu chí xong

1. **Collector:** fixture từ record native đã quan sát; prompt/response/execution
   correlation, snapshot profile, usage dedupe, cursor tăng dần. Chưa làm UI để
   tránh vô tình cam kết những phép đo observer chưa có.
2. **Projector:** segment membership, elapsed với parallel spans, process polling
   chains, repeated reads, retry signatures, các finding kèm evidence IDs.
3. **Crew dashboard:** current segment + top findings + timeline token/time +
   drilldown. Thử trực tiếp trên run thật; raw diagnostics là tầng cuối.
4. **Repo comparison:** profile revisions và baseline sau khi collector đủ dữ liệu.

Các gate bắt buộc:

- Một response có cả token_usage_record và token_count vẫn chỉ tính một lần;
  tổng bucket theo call = theo đoạn = theo lượt = ledger, qua restart/reindex.
- Wrapper thành công, command con exit != 0: màn hình vẫn chỉ đúng command lỗi.
- Một process bị poll nhiều lần không trở thành nhiều lần build/test; output
  có tiến triển được phân biệt với output không đổi.
- Hai tool chạy song song không nhân đôi elapsed; chờ Mate không tính như CPU/model.
- Cùng file vừa thay đổi rồi đọc lại không thành "đọc lại nội dung không đổi".
- Thiếu output/timestamp/link phải cho unknown, không cho zero/success giả.
- Tool đang chạy, usage chưa về, late result, chuyển turn, multi-session và
  parent/child không cộng trùng; child usage không có nguồn thì nêu thiếu.
- Dữ liệu live đi từ harness → observer → DB → UI được kiểm chứng; suite có
  TestLive skip chưa chứng minh được vòng này.
- Đo overhead ingest trên transcript nhiều MB và nhiều Crew; observer không
  được tự làm chậm luồng công việc mà nó đang chẩn đoán.
- Chấp nhận sản phẩm: mở một Crew có vấn đề, trong màn hình đầu người dùng thấy
  được **đoạn nào**, **tốn gì**, **lặp gì**, và **click đâu để thấy bằng chứng**.

## 8. Phạm vi triển khai đầu

Triển khai collector native, profile lúc launch, projector và Crew dashboard
(bước 1–3). Repo comparison ở bước 4 cần tích lũy các run có profile, rồi mới có
baseline để so sánh. Không dựng profile quá khứ từ cấu hình hiện tại.

Native executions chưa có ID wrapper cha đáng tin cậy vẫn được hiển thị cùng
lượt nhận yêu cầu và có finding/lỗi thật. Token cho execution đó là unknown;
token của đoạn lấy từ ledger của các model call. Không dùng timestamp gần nhau
để gán chi phí cho một lệnh con.
