# Review: nhận diện trạng thái TUI khi harness thay đổi

Ngày: 2026-09-27. Code được review: `f769416`. Binary trên máy: Codex 0.157.1, Claude Code 2.1.283. Đây là review và đề xuất, chưa thay đổi runtime.

## Kết luận

Nên giữ TUI/Herdr hiện tại, nhưng chuyển sang **hook/event cho vòng đời turn, quan sát có cấu trúc cho ô nhập, và receipt riêng cho việc gửi**. Một bộ regex rộng hơn không giải quyết được việc đang dùng cùng một ảnh màn hình để trả lời ba câu hỏi khác nhau.

Không có cách đọc màn hình nào bảo đảm đúng với mọi bản cập nhật. Mục tiêu thực tế: đổi footer/banner không ảnh hưởng; tín hiệu lạ làm giảm khả năng quan sát, không tự biến thành lỗi của crew; khi không chứng minh được an toàn thì hoãn thao tác gõ. Giữ nguyên quyền của `.status`, `.meta` và người dùng đối với trạng thái công việc.

## Phát hiện trong code

### P1 — Mất nhận diện sau Enter vẫn được coi là gửi thành công

`internal/send/send.go:280` thoát vòng kiểm tra khi `after.State != StatePending`; `Report.Delivered()` tại dòng 187 cũng dùng điều kiện phủ định này. `unknown` vì dialog hoặc redraw do đó trả về thành công. Tại `internal/outbox/outbox.go:372`, lỗi nil dẫn đến ghi `sent.log`, đẩy digest cursor và đánh dấu `sent`.

Probe fake runtime: composer trống → gõ → Enter → màn hình `Usage limit dialog: choose an option`. Kết quả thật: `error=nil`, `after=unknown`, `delivered=true`. Không có bằng chứng harness đã nhận prompt. Đây là nguy cơ mất thông điệp, không chỉ lỗi hiển thị.

### P1 — Busy dựa vào câu chữ có thể sai theo cả hai hướng

`internal/send/classify.go:578` tìm substring trong 20 dòng cuối, không phân biệt transcript với vùng trạng thái Codex.

- Transcript nằm gần composer chứa `esc to interrupt` → `busy` dù đang rảnh.
- Dòng giả lập `• Working (5s • esc to stop)` phía trên composer trống → `empty` dù chỉ thay chữ của chỉ dẫn đang chạy.

Claude đã có biện pháp hạn chế đọc nhầm câu trích dẫn bằng cột đầu dòng; Codex vẫn dùng bộ seed chung. Mất busy không chứng minh idle, và busy không chứng minh ô nhập trống.

### P2 — Ô nhập phụ thuộc bố cục; startup và send còn có hai định nghĩa khác nhau

`internal/send/classify.go:374`: Claude yêu cầu marker nằm ngay giữa hai rule. Chèn một dòng cảnh báo trong vùng này → `unknown`. Codex tại dòng 490 vẫn giới hạn 20 dòng không rỗng và loại menu bằng dạng `N. label`.

Placeholder Codex là một chuỗi cố định. Đổi chữ trên bản plain không có thuộc tính faint → `pending`. Bản ANSI có SGR 2 có thể tránh lỗi này, nhưng đó vẫn là quy ước của UI, không phải hợp đồng ô nhập.

`internal/harness/startup_prompt.go:236` tìm placeholder Codex trên toàn snapshot; `send` lại chấp nhận `›` trống. Probe cùng màn hình `›` trống cho kết quả startup `unrecognized`, send `empty`. Startup cũng có thể đọc chữ placeholder được trích trong transcript như tín hiệu ready. Startup timeout tại `internal/spawn/settle.go:200` từ chối launch khi chưa nhận diện; crew spawn ghi thất bại theo hợp đồng hiện tại.

### P2 — Không nhận diện được có thể trở thành incident `stale`

`internal/watch/watch.go:426` dùng `composer != StateBusy`. Khi status/pane đứng yên đủ lâu, không ở `needs-decision`/`wait-mate`, `unknown` cũng đủ để mở incident rồi làm crew hiển thị `blocked`. Chiều ngược lại, hash toàn màn hình ở dòng 402 và `staleCleared` ở dòng 449 có thể coi banner/animation thay đổi là hồi phục.

`internal/crewstate/state.go` đã tách declaration và health — nên phát triển tiếp ranh giới này. Sai nằm ở bằng chứng mở/đóng incident, không cần thay cả máy trạng thái công việc.

## Kiểm chứng

Suite hiện có của `send`, `harness`, `watch`, `outbox`, `spawn`, `crewstate` đều pass. Không chạy live; suite này không chứng minh các phiên harness thật hoạt động đúng.

Chạy thêm 8 probe bằng Go overlay, không sửa source/test trong repo. Các probe xác nhận hành vi hiện tại; “pass” dưới đây nghĩa là tái hiện được kết quả, không nghĩa là hành vi mong muốn:

| Biến thể | Kết quả hiện tại |
| --- | --- |
| Fixture Codex 0.157.1 có `⚠ 1 warning` | `empty`, đúng |
| Đổi placeholder trong fixture plain | `pending` |
| Câu trích `esc to interrupt` ngay trước composer | `busy` |
| Đổi hint busy thành `esc to stop` | `empty` |
| Chèn notice giữa marker Claude và rule dưới | `unknown` |
| Thêm 21 dòng notice dưới composer Codex | `unknown` |
| Màn hình lạ sau Enter | `delivered=true`, lỗi nil |
| Codex có marker trống, không placeholder | Startup từ chối; send coi trống |

Lệnh: `go test -overlay=/tmp/mate-tui-review-20260927/overlay.json ./internal/send -run TestReviewScreenDriftProbes -v`. Probe nằm tại `/tmp/mate-tui-review-20260927/probe_test.go`.

Chưa có capture của đúng cảnh báo limit người dùng gặp. Các biến thể trên là phép thử có kiểm soát trên fixture/fake runtime, không phải khẳng định UI hiện tại thật sự vẽ từng biến thể đó. Footer warning hiện có đã được hỗ trợ.

## Kiến trúc đề xuất

### 1. Quan sát độc lập từng chiều

Thay kết quả duy nhất `empty/pending/busy/unknown` bằng observation gồm:

- **Process:** alive / exited / unknown — từ runtime, không từ chữ trên pane.
- **Activity:** starting / running / idle / waiting-input / interrupted / failed / unknown — vòng đời harness, không phải state task.
- **Input:** empty / occupied / obstructed / unknown — ô nhập và dialog tại thời điểm đọc.
- **Delivery:** queued / typing / submitted / confirmed / unconfirmed / rejected — trạng thái một lần gửi.
- **Notice:** advisory / actionable / unknown — cảnh báo riêng, không tự ghi đè activity.

Mỗi bằng chứng có nguồn, session/generation, thời điểm quan sát, sequence nếu nguồn hỗ trợ, và lý do. Không gộp thành một điểm confidence rồi tự gõ khi vượt ngưỡng. Bằng chứng phải phù hợp với hành động cụ thể.

Ví dụ: `activity=running, input=empty, notice=quota-low` là hợp lệ. “Sắp hết limit” là advisory; rate-limit thực sự từ chối turn là một lỗi thực thi cần thông báo. Hai trường hợp không được gộp thành “không biết màn hình này”.

### 2. Hook/event là nguồn chính, có kiểm tra khả năng thực tế

Tài liệu chính thức hiện mô tả `UserPromptSubmit`, `Stop` và các hook tool/session ở cả hai harness. Codex có `Interrupt`; Claude có `StopFailure` với loại lỗi gồm `rate_limit`. `UserPromptSubmit` chạy trước xử lý prompt và có thể bị hook khác chặn; `Stop` cũng có thể dẫn đến tiếp tục. Vì vậy không ánh xạ thẳng hai hook đó thành “model đã bắt đầu” và “đã idle”. [Codex hooks](https://learn.chatgpt.com/docs/hooks), [Claude hooks](https://code.claude.com/docs/en/hooks).

Repo đã cài hook cho Mate Claude; Mate Codex mới có SessionStart, Crew Claude chưa cài hook vòng đời (`internal/spawn/claude_settings.go`). Mở rộng adapter cho cả Mate/Crew, dùng collector cục bộ ghi event vào file phẳng trong `.mate/`. Hook ghi nhanh và trả ngay; không gọi model hoặc chờ network.

Phải probe trên binary được launch: hook nào được hỗ trợ, có được trust/cài và thực sự phát ra không. Không suy từ tài liệu mới nhất sang phiên đang chạy. Khóa event theo session và lần launch; không để Stop cũ làm session mới idle. Xử lý trùng, đến muộn, mất collector, restart và compact/resume. Khi mất continuity, trả `unknown` cho chiều bị ảnh hưởng và resync; không bịa idle vì không có event.

Transcript có thể bổ sung/reconcile, nhưng không là hợp đồng vĩnh viễn: tài liệu Codex nói rõ format transcript có thể thay đổi. Parser cần giữ và ghi nhận event lạ, không làm sập toàn luồng vì loại mới; record hỏng vẫn phải hiện lỗi quan sát. Không sửa parser timeline bằng cách bỏ qua dữ liệu cần cho báo cáo chính xác.

### 3. Màn hình trở thành bộ kiểm tra ô nhập

Tạo một parser dùng chung cho startup/send, trả về vùng composer, nội dung, vị trí và dấu hiệu obstruction. Tách quan sát khỏi policy có được gõ/confirm hay không.

- Bổ sung snapshot visible với rows/cols, wrap, style và cursor nếu Herdr thực sự cung cấp; cần đo khả năng API, không giả định sẵn có. Giữ recent-unwrapped cho `peek`/transcript.
- Tìm vùng tương tác bằng nhiều đặc trưng: vùng dưới màn hình, ranh giới ô nhập, cursor/focus nếu có, style placeholder và quan hệ với menu. Cursor chỉ là tín hiệu hỗ trợ, không tự chứng minh composer.
- Bỏ đếm chính xác số dòng footer, chữ model, câu hint và màu theme khỏi điều kiện ready. Nội dung notice nằm ngoài vùng tương tác không ảnh hưởng input. Vẫn giữ giới hạn đọc hợp lý; nếu snapshot bị cắt thì báo thiếu dữ liệu.
- Gắn style vào đúng cell/vùng composer, không tìm substring faint ở nơi khác trên màn hình. Nội dung người dùng và nội dung trích trong transcript không được xem là UI điều khiển.
- Đọc lại sau redraw, cần ổn định qua vài mẫu trước thao tác. Recheck ngay trước gõ; một ảnh cũ không cấp quyền gõ mãi. Đây giảm race, không bảo đảm atomic với bàn phím người dùng.
- Dialog mới/không rõ: `input=obstructed|unknown`, giữ prompt chờ và cho người dùng mở pane xử lý. Không tự Enter/Esc “cho qua”, đặc biệt với trust/permission.

Thiết kế này chịu được banner/footer thay đổi. Đổi hoàn toàn editor hoặc marker vẫn có thể cần adapter mới, nhưng ảnh hưởng được cô lập và có thông báo rõ.

### 4. Delivery có receipt và trạng thái không chắc chắn

Persist một `delivery_id` và attempt trước khi gõ. Dùng ID trong envelope in được để receipt từ hook/transcript khớp chính xác session và thông điệp, không chỉ tìm lại chuỗi giống nhau trong `sent.log`.

Luồng đề xuất: queued → typing → submitted → confirmed. `confirmed` chỉ có nghĩa harness đã tiếp nhận đúng prompt vào đường xử lý; không có nghĩa model đã trả lời hoặc task xong. Theo dõi running/rejected/failed riêng. Hook pre-submit chỉ tạo receipt “submitted”; nếu cần xác nhận đã vượt hook chặn thì phải có bằng chứng tiếp theo hoặc API acknowledgement với semantics đã đo.

Sau Enter mà chỉ thấy composer biến mất, trống, busy hay unknown: chờ receipt; timeout → `unconfirmed`. Giữ payload/ID và không đẩy cursor tiêu thụ như một lần gửi đã xác nhận. Reconcile trước retry; không tự gõ lại toàn payload. Chỉ retry Enter khi thấy đúng payload của attempt đó vẫn ở đúng composer, không bị dialog che và không có người dùng thay nội dung. Slash command có đường xác nhận riêng vì có thể không tạo model turn.

Khóa sender theo agent để hai tiến trình Mate không cùng gõ. Khóa này không khóa được bàn phím người dùng. Với TUI dùng chung, exactly-once tuyệt đối không thể được suy ra từ screen; cần trình bày `unconfirmed` trung thực.

### 5. Lỗi quan sát không phải lỗi crew

Unknown kéo dài mở cảnh báo `observation-degraded`, giữ declaration hiện tại; nó không tự tạo `blocked`. Đây là loại cảnh báo quan sát đề xuất, không phải state crew thứ tám. `runtime_lost` vẫn cần bằng chứng runtime. `stale` cần dữ liệu vòng đời/progress đủ tin cậy; im lặng trong một tool dài không đủ. Thay đổi footer không tự đóng incident. Dùng debounce để tránh dao động khi TUI đang redraw.

Startup lạ nhưng process còn sống nên giữ phiên ở launch health “needs attention”, cho attach xử lý rồi quan sát lại, thay vì đồng nhất thành failed ngay. Điều này cần sửa hợp đồng fail/cleanup trong spec và spawn saga có chủ đích; lỗi process thực vẫn phải rollback.

## Lựa chọn dài hạn

**Khuyến nghị cho Mate hiện tại: hybrid như trên.** Giữ trải nghiệm vào trực tiếp TUI của Codex/Claude và thay đổi từng lớp.

Nếu yêu cầu là **không phụ thuộc UI cho cả gửi lẫn trạng thái**, cần backend native protocol và một client hiển thị do Mate kiểm soát. Codex App Server cung cấp JSON-RPC và lifecycle `turn/started`, `turn/completed`; đây là nền cho hướng đó, không phải API tự động điều khiển một TUI Herdr bất kỳ đang chạy. Phải thiết kế quyền sở hữu session và trải nghiệm tương tác. [Codex App Server](https://learn.chatgpt.com/docs/app-server).

Không chọn LLM nhìn screen làm nguồn quyết định duy nhất: nó có thể giúp giải thích màn hình lạ và đề xuất adapter/fixture, nhưng vẫn là suy đoán, có độ trễ và không giải quyết receipt hay race với người gõ.

## Thứ tự triển khai và tiêu chí xong

1. **Delivery + incident semantics:** sửa false success; lưu `unconfirmed` và recovery trước khi bật retry; tách unknown khỏi stale. Chạy test dialog sau Enter, crash trước/sau receipt, không nhân đôi payload, không mất digest cursor.
2. **Hook collector và reducer:** probe riêng hai harness/Mate/Crew, session generation, lifecycle và receipt. Live test prompt, stop/continue, interrupt, permission, compact/resume, lỗi hook, rate-limit mô phỏng tại adapter. Không ép tài khoản thật hết quota để test.
3. **Parser chung:** visible/geometry nếu có; test mutation trên capture thật: banner trước/sau composer, footer dài, resize/wrap, Unicode, theme/SGR, ghost suggestion, text đang gõ, quote chữ busy, popup/menu. Cả false-busy lẫn false-idle phải được đo.
4. **Shadow rollout:** observer mới ghi bằng chứng cạnh observer cũ, chưa cấp quyền gõ; kiểm tra bất đồng, sau đó bật theo capability và cho fallback về chế độ chỉ quan sát khi không đủ bằng chứng. Acceptance live ít nhất hai bản harness khi có sẵn, nhiều kích thước pane; green unit suite chưa đủ.

Đề xuất này sửa các quyết định về send verification, stale và spawn failure trong `docs/mvp.md`; không tự coi là yêu cầu đã được chốt. Không có production code được sửa trong lượt review này.
