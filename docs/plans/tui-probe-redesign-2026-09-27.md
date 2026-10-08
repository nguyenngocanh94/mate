# Phương án sửa probe TUI và xác nhận gửi của Mate

- Ngày: 2026-09-27.
- Trạng thái: đề xuất triển khai; chưa sửa production code.
- Baseline đã review: `f769416`, Codex 0.157.1 và Claude Code 2.1.283 trên máy.
- Bằng chứng ban đầu: [review nhận diện TUI](../research/tui-state-review-2026-09-27.md).
- Spec cần cập nhật khi triển khai: [MVP](../mvp.md), đặc biệt mục 4, 4b, 5 và ghi chú về send/startup.

## 1. Quyết định kiến trúc

Giữ Herdr và TUI tương tác của Codex/Claude. Thay bộ phân loại màn hình đang kiêm nhiều nhiệm vụ bằng ba lớp:

1. **Observation:** thu thập những gì biết được về process, turn, composer và từng lần gửi; giữ nguồn và giới hạn của bằng chứng.
2. **Policy:** quyết định có được gõ, retry Enter, xác nhận gửi, mở incident hoặc tiếp tục startup hay không.
3. **Delivery:** lưu vòng đời thông điệp trước khi thao tác terminal, xác nhận bằng receipt, phục hồi được sau crash.

Hook/event là nguồn chính cho vòng đời turn khi adapter đã kiểm chứng khả năng đó. Screen parser phụ trách vùng tương tác. Receipt phụ trách kết quả gửi. Adapter theo harness dịch tín hiệu về hợp đồng chung; sender và watcher không chứa những nhánh riêng theo câu chữ của Codex/Claude.

Không có screen parser nào bảo đảm đúng với mọi bản cập nhật. Mục tiêu là **thay đổi trang trí không làm hỏng điều khiển; thay đổi chưa hỗ trợ chỉ làm giảm khả năng tự động hóa ở thao tác liên quan**, không khiến cả crew bị coi là hỏng.

Nếu về sau yêu cầu hoàn toàn độc lập với UI cho cả gửi và quan sát, chuyển transport sang giao thức native và thiết kế quyền sở hữu session/client. Đó là hướng riêng, không phải điều kiện để thực hiện phương án này.

## 2. Hành vi người dùng sẽ thấy

| Tình huống | Hành vi đích |
| --- | --- |
| Footer thêm cảnh báo sắp hết limit; composer vẫn khả dụng | Cảnh báo riêng; tiếp tục gửi khi các điều kiện gửi khác hợp lệ. |
| Harness đổi câu gợi ý, tên model, số dòng footer | Không phụ thuộc các chuỗi này để kết luận ready. |
| Agent đang làm nhưng ô nhập trống | Activity là running, input là empty; không gộp thành một state. |
| Agent đang làm và người dùng có draft | Không được SendText, kể cả có yêu cầu queue khi busy. |
| Dialog lạ che composer | Hoãn gửi, giữ payload, cho mở pane xử lý; không tự Enter/Esc. |
| Sau Enter không rõ prompt đã được nhận | Hiện “chưa xác nhận gửi”; reconcile trước khi thử tiếp; không gõ lại payload. |
| API thực sự từ chối vì rate limit | Hiện lý do thực thi cụ thể; không báo lỗi parser chung chung. |
| Không đọc được trạng thái sau update | Health nói rõ khả năng quan sát bị giảm; không tự đổi declaration của crew thành failed/blocked. |
| Startup còn sống nhưng cần thao tác chưa hỗ trợ | Giữ phiên để attach xử lý rồi tiếp tục startup; không tự hủy worktree chỉ vì layout lạ. |

## 3. Những lỗi phải giải quyết

| Mức | Vị trí hiện tại | Vấn đề |
| --- | --- | --- |
| P1 | `internal/send/send.go`, `Delivered` và vòng sau Enter | `after != pending` coi cả unknown là thành công. |
| P1 | `internal/send/classify.go`, busy trước locate composer | Mất thông tin draft khi busy; probe bổ sung đã chứng minh `QueueWhileBusy=true` gọi SendText trên composer có `human draft`. Cờ mặc định tắt. |
| P1 | `internal/send/classify.go`, `seedBusy` | Substring trong transcript làm false-busy; hint đổi chữ có thể làm false-idle. |
| P2 | `internal/harness/startup_prompt.go` và `internal/send/classify.go` | Hai định nghĩa ready/composer khác nhau, phụ thuộc placeholder/rule/scan window. |
| P2 | `internal/watch/watch.go` | Unknown có thể mở stale; thay đổi hash toàn màn hình có thể đóng stale. |
| P1 về độ bao phủ sửa | `internal/spawn/crew.go`, `deliverBrief` | Brief đi qua `PromptAgent` rồi `WaitAgent(working/done)`, chưa đi qua `send.Send`; sửa sender đơn lẻ không sửa được đường này. |

Các probe mutation hiện tại là bằng chứng tái hiện, không phải acceptance. Khi đưa vào regression suite phải đổi assertion sang hành vi mong muốn; không commit test khẳng định bug là đúng.

## 4. Hợp đồng observation và policy

### 4.1. Quan sát độc lập

Tạo package `internal/observation` chứa types và reducer thuần, không import store, runtime, send hoặc UI. Adapter và wiring cung cấp dữ liệu vào package này.

```text
Observation
  identity: project, role, agent_id, launch_id, session_id
  process: alive | exited | unknown
  activity: starting | running | idle | waiting_input | interrupted | failed | unknown
  input: empty | occupied | obstructed | unknown
  notice: []advisory | actionable | unknown
  evidence: source, observed_at, source_sequence?, coverage, reason
```

Đây là trạng thái quan sát harness. Giữ nguyên bảy state nghiệp vụ của crew và quyền ghi `.status`/`.meta`; không thêm `unknown` vào state task. `activity=failed` chỉ nói một turn thất bại, không tự đặt crew thành `failed`.

Các quy tắc bắt buộc:

- Không thấy busy không suy ra idle; không thấy draft không suy ra empty nếu chưa tìm thấy đủ vùng composer.
- Một snapshot thiếu vùng input hoặc chỉ có scrollback phải nói rõ coverage; không giả là ảnh đầy đủ.
- Nguồn không hỗ trợ một khả năng khác với nguồn có hỗ trợ nhưng hiện không đọc được.
- Bằng chứng khác session/lần launch không được áp dụng. `launch_id` đổi khi process mới được tạo, kể cả resume cùng session.
- Thời điểm collector nhận event không tự chứng minh event đó xảy ra sau event khác. Nếu nguồn thiếu thứ tự đáng tin và các event mâu thuẫn, hạ chiều liên quan xuống unknown rồi reconcile.
- Không đặt TTL chung khiến một turn/tool dài thành idle khi hết hạn. Kiểm tra continuity của nguồn và độ mới của snapshot theo từng hành động.
- UI activity lấy từ screen chỉ là bằng chứng suy diễn. Herdr agent_status không trở thành nguồn độc lập thứ hai vì cũng đọc màn hình.

### 4.2. Policy trả quyết định, không chỉ bool

Đề xuất các hàm thuần `CanType`, `CanPressEnter`, `CanConfirmDelivery`, `CanDeclareStale`, `CanContinueStartup`.

Kết quả: `allow | defer | needs_attention`, kèm reason và các evidence đã dùng. Không dùng tổng điểm confidence để vượt qua thiếu một điều kiện bắt buộc.

| Hành động | Bằng chứng bắt buộc |
| --- | --- |
| Gõ payload | Đúng process/session, tìm thấy composer hiện hành và xác nhận empty, không obstruction, quyền auto/manual còn hiệu lực, activity phù hợp policy. |
| Gửi khi đang busy | Tất cả điều kiện input trên; thêm capability queue đã được đo và yêu cầu queue rõ ràng. |
| Retry Enter | Đúng payload/envelope của attempt còn nguyên trong composer, đúng session, không dialog; người dùng chưa sửa draft; giới hạn số lần/thời gian. |
| Đánh dấu confirmed | Receipt có semantics phù hợp, khớp delivery ID, payload và session/launch; không chỉ dựa vào composer trống/busy. |
| Mở stale | Có bằng chứng activity/progress đủ để áp dụng ngưỡng; không mở vì unknown hoặc vì một tool dài không in output. |
| Đóng stale | Progress hoặc chuyển trạng thái được xác nhận, hay crew ghi status mới; thay đổi banner/hash đơn thuần không đủ. |

Recheck ngay trước gõ và trước mỗi Enter. Nhiều mẫu ổn định chỉ giảm race với redraw; không thể khóa bàn phím người dùng hoặc bảo đảm atomic giữa read và write.

## 5. Probe khả năng harness và bộ thu event

### 5.1. Tách probe khả năng khỏi quan sát phiên đang chạy

Capability cần mô tả: lifecycle, submit receipt, acceptance receipt, interrupt, permission/error, queue input, snapshot geometry/style/cursor. Trạng thái mỗi khả năng là `verified | unsupported | unknown`, có bằng chứng và phạm vi áp dụng.

Hai tầng kiểm chứng:

1. **Lab compatibility:** thử trên session riêng, với binary/version, runtime version, config hook và profile liên quan được ghi lại. Cache theo fingerprint này. Sau update thì invalidation những khả năng bị ảnh hưởng, không mặc định cả harness là hỏng.
2. **Session activation:** hook/config đúng đã được cài và hoạt động trong phiên thực tế chưa. Capability được chứng minh trong lab không chứng minh hook đã được trust trong worktree mới.

Không gửi prompt thử, Ctrl+C, Escape hoặc ký tự thăm dò vào phiên người dùng đang làm việc. Lab có thể chạy prompt ngắn để đo protocol; phiên thật chỉ quan sát sự kiện tự nhiên. Không chặn startup vào sự kiện chỉ xuất hiện sau prompt đầu tiên — ví dụ SessionStart Codex theo bằng chứng hiện có của repo.

### 5.2. Thu event qua file phẳng

Mở rộng hook cục bộ cho cả Mate và Crew; giữ hook recall/auto-mode hiện tại nhưng tách side effect của chúng khỏi reducer.

Đề xuất bố trí mới dưới project:

```text
.mate/projects/<project>/
  observation/<agent-key>/
    events.jsonl
  delivery/<agent-key>/
    attempts.jsonl
    sender.lock
```

`agent-key` lấy từ identity đã validate, phân biệt Mate và Crew; generation nằm trong record. Tất cả thao tác file đi qua workspace store và luật đường dẫn của repo.

Hook gọi một subcommand mới, ví dụ `mate hook observe`, chỉ validate/chuẩn hóa payload và append event dưới khóa ghi ngắn. Không gọi model, không chờ network và không cần console đang mở. Không ghi health hay state crew từ hook; health là projection do observer dựng. Việc bổ sung file telemetry và quyền append này phải được ghi vào spec.

Event có schema version, event ID nếu nguồn cung cấp, identity, native event/turn ID nếu có, source timestamp và received timestamp. Không giả có native turn ID hoặc total ordering ở mọi harness. Consumer có cursor theo từng stream, không đọc lại toàn bộ mỗi tick.

Record mới chưa biết: giữ raw event có giới hạn và báo unsupported; không phá toàn bộ stream. Partial trailing record: đọc tiếp ở lần sau. Record hỏng giữa stream: ghi lỗi quan sát/continuity, không silently skip rồi vẫn khẳng định trạng thái chắc chắn. Retention không được xóa receipt của delivery chưa giải quyết; checkpoint lưu offset và identity để rebuild/reconcile.

### 5.3. Chốt semantics receipt trước khi bật xác nhận tự động

Phân biệt các mốc:

- **Submitted:** prompt đã đi vào đường xử lý input của harness.
- **Accepted:** đã được harness tiếp nhận cho turn/queue theo semantics adapter đã đo; không có nghĩa turn sẽ thành công.
- **Completed/failed/interrupted:** kết quả turn, độc lập delivery.

`UserPromptSubmit` một mình chỉ chứng minh mốc trước xử lý; hook khác có thể chặn prompt. `Stop` không tự chứng minh idle nếu hook có thể yêu cầu tiếp tục. Adapter chỉ phát receipt Accepted khi có bằng chứng đã đo là phù hợp, ví dụ acknowledgement của protocol hoặc chuỗi event/transcript có liên kết đúng prompt và semantics được xác minh.

**Điểm kiểm chứng bắt buộc của PR đầu:** với từng harness, có lấy được Accepted đáng tin cho TUI hiện tại không? Nếu không, capability đó là unsupported/unknown; chỉ báo submitted/unconfirmed và không bật tự động tiêu thụ outbox dựa vào sự biến mất của composer. Không hứa hook giải quyết hết trước khi đo.

Transcript chỉ là nguồn bổ sung có adapter/version riêng. Type mới trong transcript không phải lỗi của agent; schema drift phải được cô lập. Không lấy timestamp gần nhau hoặc một tool event bất kỳ làm bằng chứng nhận đúng prompt.

## 6. Screen parser chung

Tạo package `internal/screen` cho snapshot và parser input. Chuyển phần nhận diện composer khỏi `internal/send`; startup và send dùng cùng kết quả parser. Các driver xác nhận trust/update/hook dialog vẫn có policy riêng, không được tự chấp thuận một dialog chỉ vì hình dạng giống.

Đầu vào ưu tiên viewport đang hiển thị, có rows/cols, wrapping, cell attributes và cursor nếu Herdr cung cấp. `internal/runtime` phải trả thêm nguồn/coverage của snapshot; chỉ bổ sung field đã đo được. Giữ `recent-unwrapped` cho `peek` và đọc lịch sử. Nếu chỉ có ANSI/text, parser khai báo thông tin thiếu.

Parser thực hiện:

1. Dựng text/cell cùng style từ snapshot theo đúng hợp đồng runtime; không áp dụng bộ bỏ ANSI như thể đó là terminal emulator cho một stream VT thô.
2. Tìm vùng input bằng cấu trúc và nhiều đặc trưng; không tìm một chuỗi placeholder ở bất kỳ đâu trong transcript.
3. Phân biệt input với menu/dialog; nếu không phân biệt được thì unknown/obstructed.
4. Gắn faint/ghost suggestion vào đúng cell thuộc composer. Không dùng last substring match trên toàn màn hình.
5. Nhận diện draft nhiều dòng/wrap trong toàn vùng input; thiếu phần vùng này thì không kết luận empty.
6. Không phụ thuộc chữ model, câu hint, tên spinner hoặc số dòng footer chính xác.

Có thể dùng layout profile riêng cho từng họ UI. Profile chỉ nhận diện cấu trúc; policy dùng chung và không chứa luật theo phiên bản. Bounded snapshot vẫn cần thiết; nếu notice đẩy composer ra ngoài phần đã đọc, trả incomplete chứ không mở rộng vô hạn hoặc đoán rảnh.

Dialog lạ không được tự dismiss bằng Enter/Esc. LLM nhìn screen, nếu bổ sung sau này, chỉ giúp giải thích và xây fixture; không cấp quyền tự gõ/trust.

## 7. Delivery bền vững và tích hợp mọi đường gửi

### 7.1. Vòng đời

Mỗi thông điệp có `delivery_id` ngẫu nhiên duy nhất và payload đã đóng băng. Outbox ID tăng dần hiện tại chỉ dùng cho hàng UI; không dùng nó như ID toàn cục vì retention có thể làm mất lịch sử ID.

```text
queued → prepared → typing → submitted → confirmed
                        ↘       ↘
                          unconfirmed → reconcile → confirmed / rejected
queued → cancelled
```

- `prepared`: đã lưu ý định cùng target generation, payload/hash, loại message và nguồn.
- Ghi bền vững `typing` **trước** SendText. Crash ngay sau ghi typing nhưng trước khi thật sự gõ vẫn phải coi là ambiguous; không đoán chưa gửi.
- `submitted`: bằng chứng submit; chưa đủ cho accepted.
- `confirmed`: Accepted receipt hợp lệ; chỉ mốc này cho phép settle item và tiêu thụ digest cursor.
- `unconfirmed`: có thể đã ghi một phần/toàn bộ hoặc đã submit nhưng chưa biết kết quả. Không quay về queued tự động.
- `cancelled` chỉ dùng khi chứng minh chưa gõ. Người dùng chủ động bỏ theo dõi một attempt ambiguous phải có kết quả “abandoned, delivery unknown”, không ghi là chưa gửi.

Journal là nguồn sự thật cho delivery. `sent.log`, suffix của inbox và outbox state là projection tương thích. Sync record ý định trước side effect terminal và record Accepted trước các projection phụ thuộc vào nó; test cả lỗi ghi và crash giữa các bước.

Sau restart: đọc journal, kiểm tra process/session, tìm receipt từ offset đã lưu. Nếu session khác, không đưa payload ambiguous sang session mới tự động. Deadline hết chỉ đổi trạng thái chờ sang cần chú ý, không chứng minh giao hàng thất bại.

### 7.2. Correlation và log

Đề xuất envelope cho prompt thường: giữ prefix `⟦mate⟧ ` và thêm token `delivery:<uuid>`; hook/reducer nhận diện token, UI/log hiển thị nội dung đã bỏ metadata. Phải đo token đi qua cả hai harness nguyên vẹn. Envelope chỉ là correlation, không phải idempotency key do harness thực thi.

Không tự prefix slash command vì sẽ đổi nghĩa lệnh. Slash command cần adapter/receipt riêng; nếu chưa có thì trả kết quả không xác nhận thay vì giả một model turn. Nguồn app/Mate/user vẫn ghi đúng; một explicit `mate send` của người dùng không được bị marker mới biến thành hành vi auto. Với prompt vào Mate, metadata cũng phải giữ được logic người dùng giành quyền và tắt `.auto`.

Thay recovery theo chuỗi trong `sent.log` bằng delivery ID. Bổ sung metadata versioned cho record delivery-derived trong `sent.log`, reader tiếp tục đọc record bốn cột cũ. Viết projection dưới khóa log với kiểm tra ID để crash giữa append và đánh dấu projected không nhân đôi log. Không dùng append thành công của app như receipt từ harness.

Hook pre-submit hiện đang ghi `sent.log`: chuyển thông điệp có delivery ID sang telemetry Submitted, để reducer tạo dòng verified khi đủ bằng chứng. Dòng người dùng gõ trực tiếp vẫn được ghi cho lịch sử với nguồn phù hợp; không được dùng nó để settle một delivery khác chỉ vì cùng text.

### 7.3. Khóa và concurrency

- Khóa sender theo agent, dùng chung cho CLI, console, outbox, brief và stow. Khóa không bảo vệ khỏi bàn phím người dùng.
- Thứ tự khi cần nhiều khóa: agent sender lock → outbox lock ngắn hoặc journal/log lock ngắn; không giữ outbox lock xuyên qua terminal I/O và thời gian chờ receipt.
- Reserve liên kết `delivery_id` trong outbox trước gõ, persist rồi thả outbox lock; callback `UpdateOutbox` hiện tại chỉ ghi sau khi callback trả về nên không thể dùng nó để bảo đảm đã persist ý định trước SendText.
- Hook writer chỉ lấy event-file lock, không lấy sender/outbox lock. Nếu hook đồng bộ chờ sender lock trong khi sender đang chờ hook, hệ thống sẽ deadlock.
- Sau ghi một phần hoặc lỗi SendText không có bảo đảm zero-write, giữ ambiguous. Không retry payload dựa trên exit code của lệnh terminal.
- Mỗi agent chỉ có một delivery đang thao tác. Khi unresolved, giữ hàng sau chờ; không thử các payload khác trong cùng composer để “xem có chạy không”.

### 7.4. Những call site bắt buộc đổi

| Luồng | Thay đổi |
| --- | --- |
| `cmd/mate/send.go`, `cmd/mate/console_box.go` | Dùng coordinator chung; log/câu “đã gửi” chỉ dựa vào outcome rõ ràng, không chỉ `err == nil`. CLI trả mã/chi tiết riêng cho unconfirmed và nói không tự gửi lại. |
| `internal/outbox/outbox.go` | Reserve delivery, reconcile, chỉ settle confirmed. `Offer` không rewrite text/cursor của delivery đã prepared; nội dung mới thành item kế tiếp. |
| `internal/store/outbox.go` | Tách `Unresolved()` khỏi `EligibleToSend()`. Giữ mọi prepared/typing/submitted/unconfirmed trong retention; hiện compaction chỉ giữ queued vô hạn nên phải đổi cùng schema. |
| `internal/spawn/crew.go`, `deliverBrief` | Bỏ bypass `PromptAgent + WaitAgent`; brief pointer có journal/receipt như mọi prompt. |
| `internal/outbox/stow.go` | Tách “prompt stow đã nhận” khỏi “turn stow đã hoàn tất”. Timeout không withdraw một message có thể đã gửi; chưa đủ bằng chứng thì báo not confirmed, không giả stowed. |
| `internal/hook/hook.go`, `internal/spawn/claude_settings.go` | Cài observation hook cho cả role; giữ auto-off, recall và nguồn message. |
| query, autopilot, recall, console | Đọc đầy đủ state unresolved mới; không làm biến mất item vì nó không còn là queued cũ. |

## 8. Observer, startup và UX

### Observer

Giữ `.status` là lời khai của crew. Observer đọc observation hợp nhất, không tự gọi composer classifier riêng nữa.

Thêm incident không blocking `observation_degraded`, chỉ vào health/inbox khi kéo dài qua ngưỡng debounce. Đề xuất mặc định ba poll liên tiếp mất cùng khả năng; các thao tác gửi vẫn kiểm tra độ mới ở thời điểm thực hiện. Recovery cần nguồn liên quan hoạt động lại hoặc explicit re-evaluation, không chỉ có pixel thay đổi. Giữ `box.BlockingIncidents` giới hạn stale/runtime_lost.

Chỉ mở stale khi idle/waiting bất thường được chứng minh và không có status waiting hợp lệ, không có progress qua ngưỡng hiện hành. Nếu không chứng minh được lifecycle thì chỉ hiện thiếu quan sát. `runtime_lost` vẫn đòi runtime xác nhận; lỗi kết nối không phải process gone. Quota advisory không thành blocking incident; lỗi rate-limit thực được hiển thị từ nguồn đã xác minh.

### Startup có thể tiếp tục

Phân biệt lỗi process/hạ tầng với trạng thái interactive chưa sẵn sàng:

- Fatal trước khi phiên hợp lệ tồn tại: compensation như hiện tại.
- Agent sống, screen chưa hiểu hoặc cần người dùng: lưu meta nhận diện agent/worktree và `launch_phase=awaiting_input`, giữ `state=spawned` của crew; không chạy brief.
- Process sống và launch sẵn sàng nhưng brief ambiguous: `launch_phase=brief_unconfirmed`, giữ delivery ID, không gửi brief lại.
- Process chết đã xác minh: xử lý failure/cleanup theo saga, lưu lý do.

Thêm thao tác CLI/console “continue startup” cho cả Mate và Crew. Thao tác này adopt đúng process/generation, quan sát lại và tiếp tục pha chưa xong; không gọi spawn mới. Persist manifest của resource do saga tạo trước khi bàn giao ownership sang phiên đang chờ. Nếu không persist được manifest/meta thì vẫn phải compensation, tránh agent mồ côi.

Đây là thay đổi hợp đồng của spec hiện tại: dialog không nhận ra đang làm spawn failed và cleanup. Phải sửa spec, tests compensation và câu thông báo cùng PR, không chỉ nuốt lỗi từ `settleStartupPrompt`.

### Câu thông báo

- “Đang làm · ô nhập có nội dung của bạn”.
- “Đang chờ gửi · chưa nhận diện được ô nhập”.
- “Đã submit · chưa xác nhận tiếp nhận; không tự gửi lại”.
- “Cần xử lý trong pane · phiên vẫn đang chạy”.

Detail mới chứa source, observed-at, reason và delivery ID để debug; không đưa enum/schema nội bộ vào thông điệp thông thường.

## 9. Kế hoạch triển khai theo PR

| PR | Nội dung | Phụ thuộc | Điều kiện hoàn tất |
| --- | --- | --- | --- |
| 0 | Compatibility lab + characterization có nguồn; đo receipt/lifecycle/geometry trên hai binary hiện có | Không | Ghi rõ verified/unsupported/unknown; có bằng chứng hook bị chặn, stop tiếp tục, interrupt và prompt ngắn. Kết luận cụ thể khả năng Accepted trước khi bật auto confirmation. |
| 1 | Observation types, policy thuần, parser input dùng chung; tách busy khỏi input | PR0 cho semantics | Không cho SendText khi có draft dù queue; startup/send đồng ý về cùng vùng input; không fallback unknown thành empty. |
| 2 | Journal/coordinator + unconfirmed; outbox schema/readers/retention; đổi mọi caller và chặn bypass brief | PR1 | Unknown sau Enter không thành sent; crash/retry không tự nhân payload; bảo toàn cursor và mọi item unresolved. Đây là bản vá delivery đầu tiên được release, không release riêng một thay đổi `Delivered()` thiếu recovery. |
| 3 | Hook collector, capability activation, reducer và receipt matcher | PR0–2 | Đúng session/generation, phân biệt submit/accepted, xử lý late/duplicate/gap; chuyển confirmed chỉ trên capability verified. |
| 4 | Nâng parser viewport/style/region; mutation và replay nhiều frame | PR1, kết quả PR0 | Banner/quote/resize không cấp quyền sai; UI chưa hỗ trợ hạ khả năng đúng chỗ; kiểm tra geometry thực của Herdr. |
| 5 | Watcher/health/incidents và recovery startup/stow; cập nhật spec/manual/UI | PR2–4 | Unknown không tự blocked/failed; tiếp tục phiên chờ không respawn; stow không xác nhận khi chỉ im lặng. |
| 6 | Shadow comparison, migration và live acceptance | PR0–5 | Bật theo capability sau evidence; không có duplicate/lost delivery trong kịch bản lỗi; UI nói rõ chế độ degraded. |

2026-10-08: phần "observation types" của PR 1 (tách quan sát khỏi policy) đã làm trong PR 1 của [jev-observer](jev-observer-2026-10-08.md): `internal/screen` (`Observation`, `Observer`) và observer `fixture`; `send`, `watch` và startup settle đọc pane qua `Observer`, policy ở caller. PR 1 ở đây không làm riêng phần đó nữa.

Nếu PR0 không chứng minh được Accepted cho một harness: vẫn triển khai guard, journal và observation; harness đó không bật tự động settle. Ghi limitation cụ thể và chọn bổ sung integration native trong một đề xuất riêng, không sửa semantics receipt để có test xanh.

## 10. Ma trận kiểm chứng

### Unit, property và replay

| Nhóm | Kịch bản và invariant |
| --- | --- |
| Trang trí | Thêm/đổi banner/footer bên ngoài input mà vẫn giữ đầy đủ evidence → quyền thao tác không đổi. Nếu bị crop mất input → unknown, không ép invariant empty. |
| Nội dung trích dẫn | Transcript/prompt chứa marker, rule hoặc câu busy → không được đọc như UI điều khiển. |
| Draft | Busy + draft, draft wrap/nhiều dòng, SGR màu giống placeholder, người dùng sửa sau lần read đầu → không ghi đè/ghép payload. |
| Redraw/menu | Partial redraw, menu xuất hiện giữa type/Enter, trust dialog mới, slash popup → không tự confirm lựa chọn không rõ. |
| Delivery | Empty/busy/unknown sau Enter không tự confirmed; Enter bị nuốt; SendText ghi một phần; lỗi đọc sau gõ; receipt đến trễ. |
| Persistence | Kill trước/sau mỗi record và side effect; restart journal; confirmed nhưng chưa ghi cursor/log; projection lặp không nhân log; lỗi fsync/write phải giữ trạng thái trung thực. |
| Concurrency | Hai console và CLI cùng gửi; hook chạy đồng bộ khi sender đợi; lock order không deadlock; outbox không rewrite payload đã prepared. |
| Event | Event cũ sai generation, duplicate, out-of-order, gap, record hỏng, type mới, compact/clear/resume, collector không hoạt động. |
| Observer | Tool dài không output; spinner/banner thay đổi nhưng không progress; unknown lâu; runtime không liên lạc được khác process gone. |
| Retention/migration | Unconfirmed tồn tại qua retention; dữ liệu v1 không bị replay; state mới không bị `Queued()` cũ làm biến mất. |

Fixture phải có manifest: harness/runtime version, dimensions, nguồn read, plain/ANSI, các action đã làm, expected facts và bằng chứng ground truth. Lưu chuỗi frame/event theo thời gian, không chỉ ảnh cuối. Mutation dùng để kiểm tra invariants, không được giới thiệu như capture live của UI có thật.

### Live acceptance

Chạy session lab riêng cho cả Mate/Crew trên hai harness, pane rộng/hẹp và resize trong turn; prompt thường, draft người dùng, queue nếu supported, permission, interrupt, resume/clear và hook tiếp tục/chặn. Thu capture kèm receipt/event để biết sự thật, không dùng chính regex đang test làm oracle.

Rate-limit/lỗi API kiểm tra bằng fake adapter/replay có contract rõ; không cần làm cạn quota thật. Khi có sẵn binary cũ và mới, chạy cùng corpus để đo tương thích qua update; không tự cài đè harness người dùng.

Mỗi PR có code chạy `make check`; phần concurrency/persistence chạy `make test-race`. Live chạy opt-in bằng `MATE_LIVE=1` với test lab cụ thể, lưu evidence dưới `docs/evidence/`. Test bị skip phải khai báo theo `scripts/gotestreport/expected-skips.txt`; suite xanh có skip live không phải acceptance live.

## 11. Migration, rollout và rollback

1. Trước schema writer mới, dừng các console/CLI writer cũ đang dùng workspace; backup file state liên quan. Không hỗ trợ chạy lẫn hai phiên bản writer.
2. Reader mới đọc được record cũ. `sent` cũ giữ như lịch sử `legacy-unverified`, không biến thành receipt mạnh và không tự gửi lại. `dropped` giữ nguyên.
3. `queued` cũ chưa có attempt và được chứng minh chưa gõ có thể nhập hàng mới. Item đã thử hoặc không đủ bằng chứng phải reconcile/quarantine; không coi tất cả queued cũ là chưa gửi.
4. Shadow mode thu event/observation và báo bất đồng, **không gửi thêm prompt hoặc phím probe**. Guard false-success/journal đã có hiệu lực trước khi thử policy mới; không tiếp tục dùng bug cũ chỉ vì đang shadow.
5. Bật adapter mới theo capability đã chứng minh. Nguồn event lỗi → hạ khả năng; không âm thầm dùng screen để đóng vai receipt.
6. Rollback ưu tiên tắt auto send/adapter mới nhưng giữ reader và journal mới để reconcile. Không chạy binary cũ trên dữ liệu unresolved mới; code cũ không hiểu state đó và có thể compact/xử lý sai.
7. Nếu buộc phải hạ binary: dừng writer, giải quyết hoặc quarantine toàn bộ delivery đang dở, export định dạng tương thích và có evidence trước khi mở lại. Không reset cursor hay xóa journal để “thử lại”.

Các thay đổi spec phải nói rõ delivery ID/receipt là giao thức vận chuyển nội bộ, không thêm vòng đời hỏi–đáp hoặc bắt crew tự ACK. Nới quy tắc “không correlation id/ack” hiện tại đúng ở phạm vi transport; giữ giao tiếp nghiệp vụ bằng `.status` và prompt như trước.

## 12. Tiêu chí hoàn tất toàn phương án

- Có một hợp đồng observation/policy và một coordinator gửi chung cho toàn bộ call site.
- Warning/footer thông thường không làm đổi quyết định khi evidence input/activity vẫn đủ.
- Busy không che mất draft; mất evidence không làm tăng quyền thao tác.
- Không có nhánh unknown → delivered, unknown → idle hoặc unknown → failed chỉ do parser không hiểu.
- Không tự gõ lại một attempt có thể đã gửi; state/cursor sống sót qua crash và retention.
- Startup chưa sẵn sàng nhưng còn sống có đường attach và continue rõ ràng.
- Mọi capability chưa chứng minh đều hiển thị trung thực; không tuyên bố tương thích mọi bản update.
- Live evidence của từng harness/role đủ để bật đúng phạm vi; unit xanh không thay thế bước này.

## Nguồn tham khảo

- [Review và các probe tái hiện](../research/tui-state-review-2026-09-27.md).
- [Spec hiện tại](../mvp.md).
- [Codex hooks](https://learn.chatgpt.com/docs/hooks), [Claude hooks](https://code.claude.com/docs/en/hooks): nguồn để thiết kế lab; semantics và khả năng thực tế vẫn phải đo trên binary được launch.
- [Codex App Server](https://learn.chatgpt.com/docs/app-server): tham khảo cho hướng native transport về sau, không được giả định điều khiển được TUI Herdr bất kỳ đang chạy.
