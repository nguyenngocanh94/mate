# Giới hạn chi phí điều phối của Mate

Phân tích ngày 2026-09-27, snapshot 21:26 giờ Việt Nam, workspace `~/newWorkspace`, project `hellovietnam`. Đây là đề xuất; chưa thay code, cấu hình hay phiên đang chạy.

## Kết luận

Mate đang gắn vòng đời của một project với một hội thoại LLM dài. Mọi công việc — bàn sản phẩm với captain, đọc báo cáo, review code, xử lý sự kiện, sửa backlog — làm hội thoại ấy lớn thêm. Các lần gọi về sau mang cả phần tích luỹ đó. Không viết code không đồng nghĩa với ít token: đọc và suy luận trên lịch sử là phần tiêu thụ chính.

Hướng nên chọn: **Mate có danh tính và trí nhớ lâu dài, nhưng context làm việc có giới hạn.** Runtime xử lý trạng thái và giao nhận; model quyết định trên một gói bối cảnh có phạm vi; review chi tiết có context riêng. Làm theo từng bước trên cơ chế file/outbox/recall sẵn có, chưa cần viết lại toàn bộ runtime hoặc chuyển sang API.

## 1. Số liệu được kiểm tra lại

Nguồn gốc: transcript Claude session `83ecc641-243c-476e-9746-2485a7139f6d`, `mate.meta`, manual đã sinh trong workspace, và SQLite mở `mode=ro`. Không chạy thêm lượt model để đo.

| Chỉ số | Kết quả |
| --- | ---: |
| Context đầu / cuối | 86.653 / 418.581 token |
| Context trung bình | 239.737 token/lần gọi |
| Lần gọi model riêng biệt | 256 |
| Tổng đầu vào, gồm cache | 61.372.578 token |
| Cache read | 60.066.209 token, 97,87% đầu vào |
| Cache write | 1.305.857 token |
| Input ngoài cache | 512 token |
| Output, đã bao gồm thinking | 164.583 token |
| Bản ghi compact | 0 |
| Manual trong workspace | 81.986 byte |

Tổng đầu vào theo ngày địa phương: 25/9 là 5,22M (47 calls), 26/9 là 16,57M (88 calls), 27/9 đến snapshot là 39,59M (121 calls). Ngày cuối chưa trọn ngày, workload khác nhau; không dùng chuỗi này làm dự báo cố định mỗi ngày.

Ledger tại thời điểm đọc có 255 calls của Mate: 61,12M token gồm output; crew 55,43M; tỷ trọng Mate **52,44%**, không phải 80% trên cửa sổ toàn bộ ledger này. Captain đã xác nhận 80% là tổng token trên dashboard Mate; chưa xác định cửa sổ thời gian/bộ lọc của lần quan sát đó. Kết luận về context tăng vẫn đúng độc lập với tỷ trọng đó.

Chênh lệch 256/255 có lời giải trong parser: nó giữ lại nhóm message cuối để tránh ghi usage chưa hoàn tất. Không phải bằng chứng ledger mất hàng trăm calls. Xem `internal/harness/claude_transcript.go` và `internal/timeline/transcript.go`.

**Cách đếm:** nhóm assistant records theo `message.id`, lấy usage ở record cuối của mỗi nhóm; không cộng mỗi dòng JSONL. Một API response có thể có nhiều content blocks, mỗi block lặp usage. Ví dụ trước 20:53 có 350 assistant records nhưng chỉ 218 message IDs. Vì vậy không dùng lại tổng 358 calls/78,5M đầu vào trong bản nghiên cứu trước làm baseline. Ledger hiện tại đã nhóm theo message ID; không quy lỗi đếm lặp đó cho sản phẩm.

Không suy token trực tiếp từ byte ảnh/base64. Không quy mọi phần tăng không giải thích được cho thinking; dữ liệu hiện có không đủ tách chính xác toàn bộ thành phần context. Các kích thước file bên dưới là byte, không phải token đã tokenize.

**Token, giá API và quota là ba đại lượng khác nhau.** Anthropic tính input tổng bằng fresh + cache write + cache read; từng nhóm có mức giá riêng. Giá API niêm yết không chứng minh cách tài khoản subscription trừ quota. Pricing trong DB này không có giá cho model đang chạy, nên chưa có cơ sở so sánh phần trăm tiền giữa Mate và crew. [Prompt caching](https://platform.claude.com/docs/en/build-with-claude/prompt-caching).

## 2. Nguyên nhân có bằng chứng

### Hội thoại tích luỹ và tool loop nhân nhau

Ở gần cuối phiên, một quyết định dùng 5 lần gọi với context khoảng 419K sẽ đọc khoảng 2,1M input token. Câu hỏi ban đầu có thể chỉ dài một dòng. Lượng đọc lại bằng tổng context của từng lần gọi, không bằng độ dài câu hỏi.

Mô hình giải thích: `input tổng = Σ (hướng dẫn nền + lịch sử đang giữ + bằng chứng của lần gọi)`. Nếu lịch sử tăng đều qua các calls và không có giới hạn, tổng token tăng gần bậc hai theo số calls trong đoạn đó. Đây là mô hình dưới giả thiết tăng đều, không phải khẳng định chi phí tiền hoặc số ngày luôn tăng bậc hai.

### Prompt nền quá rộng cho vai trò

Snapshot instructions chứa manual Mate 82 KB, thêm khoảng 18 KB CLAUDE.md/rules toàn cục. Skill listing khoảng 34 KB, agent listing 9,5 KB, deferred tools attachment khoảng 10,8 KB. Chúng chứng minh phiên kế thừa cấu hình phát triển tổng quát của máy captain; không chứng minh mọi định nghĩa đầy đủ của MCP đều đã vào context.

Nên có launch profile dành riêng cho Mate: công cụ điều phối cần thiết, quy tắc dự án/captain thực sự áp dụng, model rõ ràng, hook recall/stop của Mate. CLI có `--setting-sources`, `--strict-mcp-config`, nhưng phải kiểm chứng prompt interactive sau khi áp dụng, giữ lại chính sách và công cụ cần thiết. [CLI reference](https://code.claude.com/docs/en/cli-reference).

### Ranh giới vai trò đang kéo quá nhiều nội dung vào Mate

Manual §9 yêu cầu đọc toàn bộ diff; scout thì đọc toàn bộ report. Transcript có 24 tool calls chứa `mate diff`, bốn Read report và sáu Read ảnh. Tool result của diff khoảng 124 KB, report khoảng 109 KB, chưa kể output qua lệnh khác. Mate không implement nhưng đang làm cả reviewer chi tiết và người tổng hợp sản phẩm trong cùng context.

Review vẫn cần thiết. Chuyển review sang context riêng; Mate nhận kết luận, bằng chứng, vấn đề cần quyết và có quyền mở rộng kiểm tra. Không thay kiểm chứng độc lập bằng việc tin summary tự viết của implementer.

### Phiên thật còn dùng manual cũ

`~/newWorkspace/.mate/projects/hellovietnam/mate/AGENTS.md:578` vẫn dạy `sleep 20; mate state`, còn khẳng định mỗi lần kiểm tra chỉ tốn một dòng output. Điều đó bỏ qua input lịch sử của call tiếp theo. Transcript ghi 15 Bash calls có cả sleep và mate; 14 chứa cấu trúc vòng lặp. Một vòng lặp Bash có thể poll nhiều lần mà không gọi model mỗi lần, vì vậy không quy mỗi tick 20 giây thành một API call.

Template repo đã bỏ vòng poll trong M14 (`assets/mate/AGENTS.md.tmpl:625`). Sửa trong repo chưa đủ nếu manual và prompt snapshot của phiên sống chưa được chuyển sang bản mới.

### Restart hiện tại không giảm context

`cmd/mate/console_box.go:236` gọi StartMate với `Resume: true`. CLI start cũng mặc định resume. Cần phân biệt khôi phục tiến trình với làm mới context. Muốn giảm context: stow → xác nhận lưu xong → session **fresh** → recall. Không tự làm việc này trên phiên đang hoạt động trong đợt phân tích.

### Tách digest thôi chưa đủ

Theo prompt gần nhất mở lượt, 48/256 calls thuộc digest, dùng 17,86M input, khoảng 29,1%. Phần còn lại 43,51M nằm trong lượt captain, trong đó có cả giao việc/review do captain kích hoạt. Phân loại theo trigger không phải phân loại toàn bộ bản chất công việc.

Autopilot đã có lọc sự kiện theo cursor, giới hạn digest và dedup outbox. Không có bằng chứng cho việc cứ mỗi tick 90 giây đều gọi LLM. Cần xử lý context xuyên suốt, không chỉ tăng interval hoặc chuyển riêng digest sang agent khác.

## 3. Kiến trúc đích

| Phần | Trách nhiệm | Bối cảnh |
| --- | --- | --- |
| Runtime Go | Theo dõi state, gom sự kiện, retry/dedup, kiểm tra quyền, kiểm chứng kết quả lệnh | Files và trạng thái thật; không cần gọi model để chờ |
| Mate nói chuyện với captain | Hiểu mục tiêu, chia việc, quyết tradeoff, giải thích và hỏi phần thuộc captain | Recent conversation + trí nhớ tuyển chọn + việc liên quan |
| Lượt xử lý điều phối | Giải quyết một việc hoặc một nhóm việc liên quan | Event, brief, câu hỏi, decision liên quan, state hiện hành |
| Reviewer/scout | Đọc diff, log, repo hay tài liệu đầy đủ | Context riêng theo task; trả verdict và evidence |
| Trí nhớ dự án | Giữ mục tiêu, quyết định có lý do, việc mở, quyền và nguồn | File bền vững; nạp chọn lọc |

Một Mate vẫn là đầu mối của người dùng. Không cần thêm một hội thoại quản lý dài hạn cho từng tầng. Reviewer và handler là các lượt có kết thúc, chỉ sinh khi cần. Có thể dùng harness hiện có; native subagent là một cách thử nhỏ, nhưng không được fork toàn bộ lịch sử rồi gọi đó là tiết kiệm.

**Gói bối cảnh cho một quyết định:** luật/quyền tối thiểu; version sự kiện; trạng thái crew/repo hiện tại; tiêu chí acceptance liên quan; câu hỏi hoặc handback ngắn; decisions đã chốt liên quan; con trỏ evidence. Dữ liệu thiếu thì truy xuất có mục tiêu. Bản đầy đủ vẫn tồn tại để kiểm chứng và phục hồi.

**Kết quả xử lý:** hành động đề xuất, lý do, evidence tham chiếu, điều kiện state/commit còn đúng, và câu hỏi chưa giải quyết. Runtime kiểm tra lại state/quyền trước khi thực thi. Khi hai lượt cùng chạm project, ghi state/hành động theo thứ tự, tránh ghi đè quyết định captain hoặc gửi/merge lặp. Event receipt sau gửi không đồng nghĩa việc đã xử lý xong; cần ghi kết quả cuối riêng và phục hồi khi worker chết. Có thể dùng file/offset/outbox hiện tại, chưa cần broker mới.

Giai đoạn đầu nên giữ một đường điều phối có quyền ghi cho mỗi project. Reviewer chỉ trả kết quả. Khi cần handler riêng mới thêm kiểm tra version, idempotency và cơ chế khôi phục. Không cho worker suy quyền merge từ một summary; quyền hiện hành vẫn được runtime kiểm tra.

## 4. Thứ tự thực hiện đề xuất

### Bước 1 — Sửa vòng đời context và phần nền trước

1. Đo riêng fresh/cache write/cache read/output, input mỗi call, trigger và chuỗi tool calls. Hiện absolute context ngay cả khi chưa biết model context window. `CTX%` đang không có vì pricing không chứa model hiện hành, trong khi số context tuyệt đối đã có.
2. Đưa M14/manual mới vào phiên thật theo quy trình bảo toàn việc dở. Theo dõi version/hash manual đang nạp để thấy lệch giữa binary, file và session.
3. Tạo launch profile hẹp cho Mate. Rút manual thành lõi vai trò/quyền/giao thức và tài liệu lệnh nạp lúc cần. Đặt ngân sách prompt theo token đo thực tế thay vì chỉ trần byte để harness chấp nhận.
4. Thử context hoạt động 60–100K, soft ceiling 120–150K sau khi đã giảm nền. Đây là điểm khởi đầu thử nghiệm, không phải ngưỡng đã chứng minh. Refresh ở ranh giới công việc, sau stow/recall được xác nhận; giữ phiên khi captain đang hỏi hoặc đang có hành động chưa biết kết quả. Stow hỏng thì hoãn tự refresh và báo rõ, không xoá context theo timer.

Không reset sau mọi sự kiện. Cache có giá trị. Ví dụ minh hoạ theo giá API Opus 5.5 tại ngày đọc: 419K input **đã cache** khoảng $0,084; ghi mới toàn bộ 40K với TTL 1 giờ khoảng $0,32. Chưa tính output và các calls tiếp theo. Một context nhỏ nhưng luôn lạnh có thể đắt hơn cho tác vụ chỉ một call. Giữ prefix ổn định, gom việc liên quan, đo cả cache write và chi phí handoff. [Giá cache](https://platform.claude.com/docs/en/build-with-claude/prompt-caching).

### Bước 2 — Chặn nội dung review lan vào mọi cuộc trò chuyện

Ưu tiên tách review chi tiết trước khi tách mọi digest. Reviewer đọc brief nguyên gốc, diff tại SHA cụ thể, handback, kết quả check; trả lỗi phải sửa, verdict có giới hạn và evidence. Mate quyết định kế tiếp theo sản phẩm/quyền. Khi SHA thay đổi thì kết quả review cũ hết hiệu lực.

Report có summary hữu hạn và đường dẫn chi tiết; handback có acceptance/evidence/điểm lệch/việc mở. Lệnh summary/state nên trả một gói nhất quán để bớt vòng `peek → state → cat → read → diff`. Runtime hoá sửa backlog theo cấu trúc, validate brief và các kiểm tra cơ học. Không cắt output mù quáng làm mất lỗi.

### Bước 3 — Tách handler điều phối khi đo cho thấy đáng làm

Handler xử lý một việc hoặc batch liên quan bằng context giới hạn; chỉ đẩy kết luận/câu hỏi có ý nghĩa sang Mate của captain. Không đánh thức Mate chỉ để nhắc lại kết quả deterministic. Dùng model mạnh cho lập kế hoạch, xung đột yêu cầu và quyết định khó; model nhỏ chỉ sau khi bộ tình huống chứng minh chất lượng đủ. Đừng thêm một model router chỉ để quyết định có cần model khác hay không khi loại event đã rõ.

Lợi ích của bước 3 là giới hạn tăng trưởng và cách ly công việc. Chi phí là orchestration phức tạp hơn, handoff, nguy cơ state cũ và mất sắc thái. Chưa cần thay toàn bộ Mate bằng service stateless sau mỗi message.

## 5. Đánh đổi và tiêu chí kiểm chứng

| Hướng | Giá trị | Giới hạn |
| --- | --- | --- |
| Đổi model/effort | Có thể giảm giá/output cho một lớp việc | Không chặn input tích luỹ; có thể tăng sai/rework |
| Rút prompt nền | Giảm thuế trên mọi call | Lịch sử vẫn tăng nếu giữ một phiên mãi |
| Compact/refresh có checkpoint | Giới hạn context; tận dụng M8 | Mất nuance nếu ghi thiếu; phí summarize/ghi cache |
| Review trong context riêng | Nội dung lớn không theo Mate cả đời | Thêm chi phí reviewer; cần evidence và kiểm tra SHA |
| Handler riêng | Giới hạn chi phí theo đơn vị công việc | Cần đồng bộ state/quyền và phục hồi lỗi |

Mục tiêu nên là **chi phí và độ trễ cho một công việc hoàn tất có chất lượng**, cộng với độ dốc context qua nhiều ngày. Không dùng riêng “Mate dưới 20%” vì chỉ cần tăng tiêu thụ crew là đạt tỷ lệ giả.

Thử cùng một tập việc đại diện: hỏi trong phạm vi brief; câu hỏi phải giữ cho captain; ship đạt acceptance; ship thiếu evidence; rebase/merge; scout; câu hỏi đến trong lúc handoff; sự kiện lặp; worker chết sau gửi nhưng trước lưu kết quả. Chạy lại với nhiều lịch sử không liên quan đã tích luỹ. Tính tổng Mate + reviewer + handler + crew, cache write/read, output, số calls, rework, thời gian captain chờ. Đừng chỉ đổi nhãn actor rồi tuyên bố Mate rẻ hơn.

Điều kiện qua: không mất quyết định hay câu hỏi mở, không phát sinh hành động lặp, review vẫn bắt được lỗi đã cài vào fixture, quyền captain được giữ; context cho tác vụ tương tự không tăng theo số ngày của project. Sau khi đơn vị và integration pass, cần live trên harness thật để chứng minh profile prompt, fresh/recall, composer và event delivery; suite skip live không chứng minh những việc đó.

Nếu giữ số calls và đưa context trung bình từ 240K xuống 80K, phép tính input cho mức giảm khoảng **67%**. Đây chỉ là phân tích độ nhạy của input; chưa phải kết quả thực nghiệm, mức giảm tiền/quota hoặc cam kết tiết kiệm. Handler/reviewer mới và cache lạnh có thể làm thay đổi kết quả.

## 6. Nguồn kỹ thuật

- `docs/mvp.md` §M8: hội thoại là bộ đệm, ownership trí nhớ, stow/recall; §M14: kết thúc lượt và mode theo captain.
- `assets/mate/AGENTS.md.tmpl` §9: review full diff; manual thực tế trong workspace vẫn có poll cũ.
- `internal/harness/claude.go`: launch hiện chưa giới hạn nguồn config/MCP cho Mate.
- `internal/harness/claude_transcript.go`: nhóm theo message ID và giữ nhóm cuối; `internal/timeline/transcript.go`: chuẩn hoá usage Claude/Codex.
- `internal/autopilot/digest.go`: Gather/cursor, max 5 items, dedup; `internal/autopilot/autopilot.go`: interval 90s.
- `cmd/mate/console_box.go:236`: restart resume; `cmd/mate/mate.go:41`: start có `--fresh`.
- [Claude Code quản lý chi phí](https://code.claude.com/docs/en/costs): context, compact/clear, chuyển thao tác output lớn sang context riêng; subagent vẫn tiêu thụ usage.

Khuyến nghị: thực hiện bước 1, rồi bước 2; dùng số đo tổng chi phí và chất lượng để quyết định mức cần thiết của bước 3. Đây là sửa vòng đời và phạm vi của context, đồng thời giữ năng lực phán đoán của Mate.

## 7. Đối chiếu báo cáo Fable

Đối chiếu thêm khi captain gửi báo cáo Fable. Giữ nguyên bản nghiên cứu của Fable; các hiệu chỉnh dưới đây nằm trong bản này.

### Hiệu chỉnh trên cùng điểm kết thúc

Tìm đúng response có context 367.356 như báo cáo Fable, timestamp cuối của nhóm là `2026-09-27T13:54:19.623Z` (20:54:19 giờ Việt Nam). Đến đây có **358 assistant records nhưng 224 message IDs**, tổng input sau nhóm là **48.646.099**, gồm cache read 47.387.131, cache write 1.258.520 và fresh 448. Output là 145.420. Vì vậy chênh lệch tổng giữa hai báo cáo không chỉ do khác thời điểm đo.

Digest lúc `13:42:21.612Z` có 13 assistant records, **8 calls**, input **2.741.331**, thay vì 13 calls/4,4M. Cần nhóm cả số calls, output, số cache misses và phân bổ theo trigger trước khi ước tính tác động. Không dùng mức 26M → 9–10M → 6–7M/ngày làm mục tiêu đã có baseline chuẩn.

Phần dư ~140K gán cho thinking cần hạ xuống giả thuyết: output tổng bị ảnh hưởng bởi đếm lặp và phép đổi byte thành token không đo trực tiếp context. Transcript có trường `output_tokens_details.thinking_tokens`; trường này cho biết output reasoning được báo cáo, không tự chứng minh bao nhiêu thinking đang được giữ lại trong context hiện hành.

### Phương án kết hợp

- Giữ A của Fable làm thử nghiệm đầu tiên: nguồn settings/MCP hẹp, model rõ ràng, tập tool cần thiết. Ba phép đo `-p` của Fable là bằng chứng thăm dò hữu ích, nhưng fixture chỉ có CLAUDE.md 52 byte; phải xác minh lại interactive với manual/hook/recall thật trước khi chốt 87K → 33K. Nên thử allowlist tool bên cạnh denylist để tránh tool mới tự xuất hiện khi harness nâng cấp.
- B1 tận dụng context tuyệt đối đã có trong DB; bổ sung hiển thị và metadata cửa sổ model, không cần một pipeline thu usage mới. `context_pct` thiếu không đồng nghĩa chưa đo được context.
- B2 phải là **stow thành công → fresh → recall**, không tái sử dụng nguyên xi restart/resume hiện hành. Đường restart thủ công còn có thể tiếp tục sau kết quả `not stowed`; không bê hành vi đó sang tự động xoá context. Stop là tín hiệu lượt đã kết thúc, cần thêm kiểm chứng checkpoint lưu được việc mở và quyết định cần giữ.
- `claude --help` của bản đang cài xác nhận có `--autocompact <auto|tokens>` (100K–1M), nên B3 là phương án có thể thử; chưa kiểm chứng runtime của flag trong đợt này.
- C giữ summary/backlog CLI, nhưng triển khai reviewer đủ evidence trước khi giảm nội dung Mate buộc phải xem để duyệt. `--stat` chỉ cho biết phạm vi thay đổi, không chứng minh đúng acceptance. Không đặt trần 15 dòng khiến findings quan trọng bị bỏ; trả danh sách vấn đề ngắn và con trỏ report đầy đủ, mở rộng khi cần.
- D nên thử theo **loại công việc** (review/report/phân tích dài), thay vì mọi digest. Digest là đường kích hoạt, không phải một loại công việc đồng nhất. Đánh giá theo số token/chi phí và độ trễ có thể tránh, không chỉ tỷ lệ số calls. Sau thử nghiệm mới cân nhắc handler độc lập.

Thứ tự kết hợp: **baseline chuẩn + context hiển thị → launch profile hẹp và manual thật cập nhật → manual lõi + refresh có checkpoint → cách ly review và lệnh summary/backlog → handler nếu số đo còn đòi hỏi**. Trước mỗi bước sau, kiểm tra cả tổng usage và chất lượng, không cộng cơ học tỷ lệ tiết kiệm của các bước có phần chồng lấn.
