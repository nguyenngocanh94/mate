# mate MVP

mate (tên cũ matev2, đổi tên 2026-09-24) là bản viết lại của mate v1 (nay chỉ còn ở repo archive `nguyenngocanh94/mate-v1`) với mục tiêu đơn giản hoá triệt để phần giao tiếp giữa Mate và Crew.
Tài liệu này ghi các quyết định đã chốt và danh sách 24 task của MVP.
Mỗi task là một PR review được trong một buổi và có tiêu chí xong đo được.

## 1. Mô hình

| Khái niệm | Vai trò | Vòng đời |
| --- | --- | --- |
| Workspace | Một thư mục chứa nhiều project, mở bằng `mate <dir>`. State nằm trong `.mate/`. | Bền |
| Project | Một đơn vị công việc có tên, đăng ký trong `workspace.yaml`, sở hữu từ không tới nhiều repo git là thư mục con của workspace (M9). | Bền |
| Mate | Agent điều phối của một project. Phân tích yêu cầu, viết brief, spawn Crew, giám sát, review, báo cáo. Không sửa code, không tự tìm hiểu repo. | Dài, có trí nhớ |
| Crew | Agent thực thi do Mate spawn cho một task. Chạy harness interactive trong pane Herdr, cwd là worktree riêng trên đúng một repo của project. | Ngắn, dùng một lần |

Mate là một harness interactive (Claude Code, Codex, pi, Grok) chạy trong một pane Herdr, cwd là `.mate/projects/<p>/mate/`.
Người dùng nói chuyện với Mate bằng cách gõ vào pane đó, xem qua stream mode của console.
Mate không có code trong cwd; muốn biết gì về repo thì gọi `mate` hoặc spawn Crew.

## 2. Quyết định đã chốt

1. Mate chỉ điều phối và ra quyết định. Không sửa code, không tự khảo sát repo. Ràng buộc bằng cấu trúc (cwd không chứa code), không bằng lời dặn.
2. Crew chạy bằng harness có sẵn: Claude Code, Codex, pi, Grok. Mặc định Mate là Claude Code, Crew là Codex. Một harness làm Crew được khi nó được đăng ký trong `internal/harness/catalog` và qua suite hợp đồng ở đó (mọi capability có khai báo, launch dựng được, capture màn hình phân loại đúng, transcript đọc khớp fixture). Một harness chỉ làm Mate được khi capability `Hooks` của nó là `verified`, vì trí nhớ và inbox của Mate dựa vào hook; thiếu thì `mate mate start` từ chối và nêu tên capability, còn vai Crew không bị ảnh hưởng ([phương án registry harness](plans/harness-registry-2026-09-30.md) mục 3.7 và 9.3). pi và Grok chưa có `Hooks` verified nên hôm nay chỉ làm Crew: `mate mate start --harness pi` và `mate mate start --harness grok` bị từ chối, picker tạo Mate của Console không đưa hai harness đó ra (task 70).
3. Runtime terminal là Herdr 0.8.2. Mapping: một Herdr session cho workspace, một Herdr workspace cho project, một tab cho Mate và một tab cho mỗi Crew.
4. Giao tiếp học triệt để từ firstmate (`/Volumes/Work/Workspace/firstmate`), xem mục 4.
5. Hai chế độ giao tiếp: giám sát (mặc định) và tự động, xem mục 5.
6. Persistence cho giao tiếp và trạng thái là file phẳng trong `.mate/`, không SQLite. Chỉ console sửa state; Crew chỉ append vào `.status`. Từ M5 có thêm `.mate/mate.db` (SQLite thuần Go) nhưng chỉ là kho dẫn xuất cho timeline, xây lại được từ file và transcript bằng `mate reindex`; mất DB không mất việc.
7. Console TUI copy từ `internal/ui/console` của v1, giữ stream mode nhúng pane Mate.
8. Trạng thái agent của Herdr (`idle/blocked/done`) là screen scraping, chỉ dùng làm tín hiệu phụ, không bao giờ dùng để kết luận task xong.
9. Token monitor làm sau MVP, nhưng `.meta` ghi `transcript=` và `session_id=` từ ngày đầu.
10. Tên binary và CLI là `mate`, thư mục state `.mate/`, prefix biến môi trường `MATE_`.

## 3. Layout workspace

```text
<workspace>/
├── shop/                                 repo git thật, Mate không cwd vào đây
├── blog/
├── .worktrees/
│   └── shop-k3/                          git worktree của crew k3, branch mate/k3
└── .mate/
    ├── workspace.yaml                    projects, herdr session name, defaults
    ├── WORKSPACE.md                      quy tắc của người dùng cho mọi Mate
    ├── CREW.md                           quy tắc của người dùng cho mọi Crew, nối cuối mọi brief (M7)
    ├── pricing.yaml                      bảng giá token, dùng sau
    ├── .env                              cấu hình riêng máy này, KEY=VALUE (MATE_JEV, MATE_JEV_API_KEY_FILE); tuỳ chọn
    └── projects/
        └── shop/
            ├── project.yaml              repos (name, path, default_branch), mode, yolo
            ├── PROJECT.md                bối cảnh project
            ├── CREW.md                   quy tắc của người dùng cho Crew của project này (M7)
            ├── sent.log                  mọi dòng gửi vào pane Mate và pane Crew
            ├── incidents.log             observer ghi: mở/đóng incident theo crew (task 18)
            ├── mate/                     cwd của Mate
            │   ├── AGENTS.md             operating manual, app sinh lại mỗi lần start
            │   ├── CLAUDE.md             `@AGENTS.md`
            │   ├── memory.md             Mate tự ghi
            │   ├── backlog.md            In flight / Queued / Done
            │   ├── mate.meta             harness= session_id= pane=
            │   ├── .auto                 có mặt = chế độ tự động
            │   ├── .auto-cursor          daemon auto đã digest tới đâu (task 19)
            │   ├── .outbox               hàng đợi gửi vào Mate: [assign] và digest (task 30)
            │   ├── .codex/
            │   │   └── hooks.json        Mate Codex: hook SessionStart in `mate recall` (task 37)
            │   └── .claude/
            │       ├── settings.json     hook UserPromptSubmit, Stop, SessionStart
            │       └── skills/
            └── crews/
                ├── k3.meta               task= repo= harness= pane= worktree= branch= transcript=
                ├── k3.status             append-only, crew ghi bằng echo
                └── k3/
                    ├── brief.md          prompt đầu của crew
                    ├── handback.md       ship: bảng acceptance crew tự kiểm trước wait-mate (M7)
                    ├── report.md         scout: deliverable
                    ├── usage.jsonl       sau MVP
                    └── transcript/       copy lúc teardown, sau MVP
```

Quy ước:

- Repo nằm ngang hàng với `.mate/`, không bắt buộc nằm trong thư mục con nào.
- Một project có từ không tới nhiều repo; một repo thuộc tối đa một project (M9).
- Worktree gom ở `.worktrees/<project>-<crew>/`. Codex vẫn hỏi trust cho worktree mới (đo 2026-09-17), nên spawn luôn chạy settle step.
- `.meta` là `key=value` mỗi dòng một khoá. `.status` là text thuần `state: một dòng`.
- Xoá `.mate/` là xoá toàn bộ state của app. `crews/<id>/` giữ mãi sau teardown, chỉ người dùng xoá tay.
- Mọi đường dẫn ghi ra phải nằm trong workspace; symlink trỏ ra ngoài bị từ chối.

## 4. Giao tiếp

Học từ firstmate, giữ đúng bốn cơ chế và không thêm:

| Chiều | Cơ chế |
| --- | --- |
| Crew → Mate | `echo "state: một dòng" >> $MATE_STATUS`. Ba verb crew được dùng: `working`, `needs-decision`, `wait-mate` (mục 4b). Báo thưa. Nội dung dài nằm trong file, status là con trỏ. |
| Mate → Crew | `mate send <crew> "một dòng"` paste vào pane crew với bracketed paste, kiểm chứng composer trống trước, retry Enter có kiểm tra toàn bộ bản nháp. Dài hơn thì ghi file và trỏ crew đọc. |
| Đọc crew | `mate peek <crew>` đọc 40 dòng cuối pane. `mate state <crew>` trả một dòng state deterministic từ busy regex của pane và dòng status cuối. |
| Đánh thức Mate | Observer trong console theo dõi status file, hash pane, busy regex, inventory Herdr. Chỉ đánh dấu là đáng chú ý khi có verb `needs-decision`/`wait-mate` hoặc khi chính nó mở incident (`blocked`). |

Sửa lỗi gửi 2026-09-28: `mate send` lưu lần gửi vào `crews/<id>.send.json` trước khi nhập,
dưới khóa riêng cho crew. Chạy lại cùng lệnh chỉ phục hồi bằng Enter khi phiên crew, nguồn gửi
và toàn bộ bản nháp còn khớp; không nhập lại nội dung. Composer trống sau một lần gửi chưa xác nhận
không tự cho phép gửi lại. Nội dung khác, phiên mới, ảnh chụp thiếu hoặc dialog đều từ chối phục hồi.
`unknown` sau Enter và `busy → busy` không chứng minh gửi thành công; không ghi `sent.log` cho chúng.
Đây là biên nhận lần nhập, không phải inbox bền vững hay xác nhận crew đã xử lý công việc.

Câu hỏi của crew không có vòng đời.
Crew append `needs-decision:` rồi dừng turn.
Ai đó gõ một dòng trả lời vào pane crew.
Với crew đó chỉ là một prompt mới.
Không interaction row, không wait, không correlation id, không ack.

Message box trong console là view gộp theo thời gian của `crews/*.status`, `sent.log`, và incident của observer.
Không có file box riêng.

Lớp gộp giữ đủ mọi dòng, nhưng console chỉ hiển thị phần chưa được giải quyết - gọi là inbox: một status `needs-decision`, hoặc một incident, mà chưa có ai trả lời.
Mỗi mục inbox là một dòng: giờ, crew, và nó cần gì bằng chữ thường (`needs an answer`, `stuck, quiet too long`, `agent gone`, `send wedged`), không hiện text của status (quyết định 2026-09-19: text đó là bản tóm tắt crew tự viết, đọc nó không thay được việc nhìn pane).
Người dùng hoặc tự vào xem (Enter, hoặc click vào hàng: mở luôn pane của crew đó) hoặc giao cho Mate (`[assign]`); `[all]` mới hiện verb và text đầy đủ của từng dòng.
Một mục rời inbox khi crew đó ghi thêm một dòng status mới (luật chính xác, dựa trên thứ tự byte trong file), hoặc khi `sent.log` có một dòng gửi tới `crew:<id>` sau thời điểm của câu hỏi (luật xấp xỉ, vì status không có timestamp riêng).
`wait-mate` không nằm trong inbox. Từ quyết định 2026-10-07, crew bàn giao rời các hàng crew đang làm và vào nhóm `Handed back (N)` thu gọn mặc định; mở nhóm rồi Enter vào pane crew để đối thoại, review hoặc merge. Crew ghi lại `working` hay `needs-decision` thì trở lại danh sách đang làm; incident đang mở vẫn ưu tiên `blocked` và hiện trong danh sách.

**Đóng crew là quyết định của người dùng hoặc Mate, không phải của crew** (quyết định 2026-09-18).
`wait-mate:` chỉ là báo cáo của crew.
Task kết thúc khi `mate crew stop` chạy: scout đóng khi người dùng nhận report và hài lòng (chủ động bảo Mate đóng), ship đóng khi branch đã merge (người dùng merge, sau này `mate merge`).
Crew đã đóng (`state=finished|failed` trong meta) biến khỏi cây console, khỏi inbox và khỏi `crew list` mặc định (`--all` để xem); `crews/<id>/` giữ nguyên.
Cây console mặc định thu gọn crew `wait-mate` trong nhóm `Handed back`; `crew list` mặc định cũng ẩn chúng (`--all` hiện cả bàn giao và đã đóng). Đây là bộ lọc hiển thị, không đóng task, không dừng pane hay xoá worktree; digest vẫn nhận bàn giao như trước.
Phím `l` dưới focus box bật `[all]`, hiện lại toàn bộ log để debug; mặc định tắt và không lưu lại.

Gửi vào pane Mate là trường hợp đặc biệt vì người dùng cùng sở hữu composer.
Chỉ gửi khi người dùng bấm (chế độ giám sát) hoặc khi chế độ tự động đang bật.
Mọi dòng app tự gửi vào Mate có prefix sentinel `⟦mate⟧ ` để Mate phân biệt với người gõ.
Byte điều khiển `0x1f` từng được dùng cho việc này nhưng không tới được payload `UserPromptSubmit` của Claude (đo ngày 2026-09-17, Herdr 0.8.2, Claude Code 2.1.274); sentinel in được thì sống sót nguyên vẹn.

## 4b. Máy trạng thái của crew

Chốt 2026-09-18 sau khi test tay M2. Bảy trạng thái, ba người đặt, mỗi trạng thái đúng một người được phép đặt.

| Trạng thái | Ai đặt | Ghi ở đâu | Nghĩa |
| --- | --- | --- | --- |
| `spawned` | Mate, qua `crew spawn` | `crews/<id>.meta` `state=spawned` | Đã spawn, crew chưa ghi dòng nào. |
| `working` | Crew | `crews/<id>.status` | Đang làm; dòng mô tả pha. |
| `needs-decision` | Crew | `.status` | Crew hỏi và dừng turn; cần Mate hoặc người dùng trả lời. Vào inbox. |
| `wait-mate` | Crew | `.status` | Crew đã làm hết phần mình (xong, hoặc không xong được và nói vì sao) và giao lại cho Mate. Không vào inbox. |
| `blocked` | Observer | `incidents.log`, không đụng `.status` | Crew không tự nói được nữa: pane treo, agent biến mất khỏi Herdr, kẹt dialog. Chỉ `stale` và `runtime_lost` tạo ra nó; `budget` (M5) chỉ vào inbox, không đổi trạng thái (quyết định 2026-09-20). Vào inbox. Gỡ khi observer thấy crew chạy lại. |
| `finished` | Mate hoặc người dùng, qua `crew stop` | `.meta` `state=finished` | Trạng thái cuối. Scout: người dùng nhận report và bảo đóng. Ship: branch đã merge. |
| `failed` | Mate hoặc người dùng, qua `crew stop --discard`; app, khi spawn thất bại | `.meta` `state=failed` | Trạng thái cuối. Việc bị bỏ, hoặc crew chưa bao giờ lên. |

Quy tắc:

- Crew không bao giờ tự nói `blocked`, `finished`, `failed`, `done`. Thiếu key, thiếu quyền, phân vân hướng đi là `needs-decision`, vì crew còn hỏi được. Không hoàn thành được là `wait-mate: không làm được vì X`, Mate quyết `failed` hay gửi thêm một dòng cho làm tiếp.
- Trạng thái hiển thị của một crew suy ra theo đúng thứ tự: `.meta` có `state=finished|failed` thì lấy nó; không thì có incident mở trong `incidents.log` thì `blocked`; không thì verb cuối trong `.status`; không có dòng nào thì `spawned`. Không còn `reserved`, `stopped`, `parked`, `done`, `unknown` như trạng thái.
- Bên cạnh trạng thái luôn có một cột sức khỏe do quan sát, không phải trạng thái: agent còn trong Herdr không, composer bận hay rảnh, pane đứng yên bao lâu. `mate state` in `state: <trạng thái> · health: <quan sát>`. Herdr `agent_status` không được dùng cho cả hai cột (quyết định 8).
- Inbox: `needs-decision` chưa ai trả lời, và incident đang mở. Luật rời inbox giữ nguyên (dòng status mới của cùng crew, hoặc `sent.log` có dòng tới `crew:<id>` sau câu hỏi); incident rời inbox khi observer ghi dòng `resolved` cho nó. Vì observer không ghi vào `.status`, một incident không bao giờ làm câu hỏi của crew rời inbox.
- Các hàng crew mặc định của console và `crew list` hiện `spawned`, `working`, `needs-decision`, `blocked`. Console có nhóm `Handed back` thu gọn cho `wait-mate`; `crew list --all` hiện cả `wait-mate` và đã đóng. `wait-mate` vẫn là task mở, chỉ `crew stop` mới đóng nó.
- `crew stop` từ chối trước khi giết agent nếu branch chưa landed vào default branch và không có `--discard`. Không còn kết cục "agent đã chết, worktree giữ lại". Scout có branch không commit gì nên luôn sạch.
- Spawn thất bại (dialog không nhận ra, agent không lên, trust không qua) ghi `state=failed` và lý do vào `.meta` ngay tại chỗ, không để lại thư mục mồ côi ở `spawned`.

`incidents.log` là file append-only, tab-separated, mỗi dòng `RFC3339 \t crew \t kind \t open|resolved \t text`.
Kind: `stale` (status và pane không đổi quá ngưỡng mà composer không bận), `runtime_lost` (Herdr không còn agent), `wedged` (gửi vào pane không kiểm chứng được quá lâu), `budget` (sau MVP).
Một incident đang mở khi dòng cuối của cặp (crew, kind) là `open`.
Chỉ observer ghi file này; `box.Load` đọc nó và gộp vào view cùng `.status` và `sent.log`.
Observer sống trong tiến trình console: đóng console thì không ai canh crew và không có `blocked` mới; chấp nhận cho MVP.

## 5. Hai chế độ

Cờ là file `.mate/projects/<p>/mate/.auto`.

Chế độ `manual` (giám sát, mặc định; nhãn đổi từ `supervised` ngày 2026-09-19 vì người dùng không hiểu chữ đó):

- Không dòng nào tự đi vào pane Mate.
- Trên một mục trong inbox (quyết định 2026-09-19: box là chỗ để hành động, không phải chỗ để đọc):
  - Enter, hoặc một click vào thân hàng, mở pane của chính crew mà mục đó nêu tên - đúng như Enter trên hàng crew trong cây; mục có crew là `mate` (incident `wedged` của daemon) mở pane Mate. Trong session view thì stream đang mở được đóng trước, rồi mới mở stream của crew, không bao giờ có hai PTY cùng lúc.
  - `a`, hoặc nút `[assign]` trên hàng, gửi vào Mate một dòng `⟦mate⟧ resolve: <crew> asked: "<status text, một dòng, cắt ở ~200 rune>" — read <đường dẫn tuyệt đối tới status file>, decide, and answer with mate send <project> <crew> "<one line>"` (đường dẫn tuyệt đối vì cwd của Mate là thư mục workspace của nó, không phải thư mục project, nên đường dẫn tương đối như `crews/<id>.status` không trỏ tới đâu cả); với incident là `resolve: incident <kind> <crew> — <text>`. Chữ trên nút là `assign` vì đó là việc người dùng làm (giao đi): nút giao câu hỏi cho Mate **xử lý**, không giao quyền quyết; Mate tự trả lời phần thuộc quyền nó và hỏi lại captain trong pane của nó phần thuộc quyền captain (`decides: captain`, hay một lựa chọn sản phẩm như link checkout), theo skill `decision-authority` (chốt 2026-09-24). Dòng gửi cho Mate vẫn là `resolve:`, đúng như manual của Mate.
  - Từ task 30, `[assign]` không bao giờ bị từ chối vì Mate bận: dòng vào hàng đợi `mate/.outbox` dưới khoá `<file status tương đối project>@<offset>` của mục inbox, thử gửi ngay một lần (Mate rảnh thì nhận luôn), rồi trả về `Assigned: sent to <agent>…` hoặc `Assigned: queued for the Mate (<lý do>)…`.
    Vòng gửi của console thử lại mỗi 2 giây bằng `send.Send` có kiểm chứng cho tới khi composer trống; không bao giờ dùng hàng đợi của harness (`herdr agent prompt`, `QueueWhileBusy`), không gõ đè composer có chữ, không gửi một mục hai lần.
    Bấm lại trên cùng mục không xếp thêm: `Assigned: already assigned HH:MM, still queued` hoặc `…, sent HH:MM`.
    Hàng inbox ghi thêm `· assigned, queued` rồi `· assigned HH:MM` và mất nút `[assign]`, nhưng vẫn nằm trong inbox cho tới khi crew thật sự được trả lời.
    Quá 5 phút chưa gửi được thì mở incident `wedged` trên `mate`, như daemon.
  - `l` đổi giữa hai bộ lọc; `j`/`k` và phím mũi tên di chuyển; `o` mở Actions menu của pane đang xem; `Esc`/`Tab` rời khỏi zone. Không còn `r` (reply) và `p` (peek): muốn nói chuyện với crew thì vào thẳng pane của nó.
- Header của rail chỉ còn hai nút lọc: `[waiting]` (inbox, mặc định) và `[all]` (toàn bộ log), nút đang bật tô accent, nút kia mờ; dòng đếm ngay dưới nói bằng chữ đang xem cái nào (`N waiting`, hay `all · N entries`), nên terminal đơn sắc vẫn đọc được. Không dùng chữ "unread": console không lưu trạng thái đã đọc, và một mục người dùng đã nhìn nhưng chưa giao vẫn đang chờ ai đó xử lý.
- `[← project]`, `[manual|auto]`, `[restart mate]`, `[clear composer]` rời khỏi header (quyết định 2026-09-19). `Esc` vẫn rời session (dòng hint ghi `Esc project`); mode vẫn ở ô MODE và phím `m`; restart Mate và clear composer nằm trong Actions menu của hàng Mate (`a` trong cây, `o` trong box zone), giữ nguyên bước xác nhận và request cũ.
- `resolve` chỉ giao việc, không đóng câu hỏi. Mục rời inbox khi crew thật sự nhận được câu trả lời - `mate send` của Mate ghi `Source: mate` vào `sent.log` - hoặc khi crew tự ghi dòng status mới.

Chế độ tự động (daemon `internal/autopilot`, chốt 2026-09-18 ở task 19):

- Daemon sống trong tiến trình console, cạnh observer. Đóng console thì không có dòng nào tự đi vào pane Mate; đó là hình dạng thành thật của một tính năng mà cả điểm là nó gõ vào composer của người khác.
- Mỗi 90 giây (đồng hồ tiêm được), với mỗi project có `mate/.auto`, daemon gom những gì mới kể từ con trỏ: inbox chưa giải quyết (`needs-decision` của crew, incident đang mở) cộng dòng status **mới nhất** của mỗi crew đang mở nếu dòng đó là `wait-mate`.
  `wait-mate` không nằm trong inbox (mục 4b) nhưng ở chế độ tự động không có người đọc bảng crew, nên Mate là người phải xem nó.
  Chỉ lấy dòng mới nhất: một `wait-mate` mà crew đã ghi đè lên bằng dòng khác là lịch sử, và luật này chặn luôn trường hợp bật `.auto` trên một project đã chạy cả tuần.
- Không có gì mới thì không gửi. Daemon không phải heartbeat.
- Có gì mới thì thành **đúng một dòng**, xếp vào `mate/.outbox` (task 30) rồi gửi bằng `send.Send` (kiểm chứng composer, không bao giờ `herdr agent prompt`); daemon không tự gõ, chung một đường với `[assign]`:

  ```text
  ⟦mate⟧ digest: <k> item(s) — <mục> · <mục> · … — status files under <đường dẫn tuyệt đối tới crews/>; act per AGENTS.md section 10
  ```

  Mỗi `<mục>` là một trong bốn dạng, và chỉ bốn dạng đó:

  ```text
  <crew> needs-decision: "<text crew viết, ≤ 120 rune, gộp khoảng trắng, " đổi thành '>"
  <crew> blocked: <incident kind>, quiet for <thời gian>
  <crew> wait-mate: "<text crew viết, cùng luật cắt>"
  <crew> over budget: <total> of <limit>
  ```

  Dạng thứ tư (task 27) là một incident `budget`: nó không bao giờ là `blocked` (quyết định 2026-09-20, mục 4b) vì vượt ngân sách là một sự thật về chi tiêu, không phải crew ngừng nói được.

  `<k>` luôn là số mục thật. Dòng chỉ trải tối đa 5 mục rồi ghi `· +<n> more`, vì một composer nhận dòng quá dài sẽ wrap và chính `send.Send` không đọc lại được.
  `quiet for` đo từ dòng `open` của incident, tức là cận dưới: observer chỉ mở `stale` sau khi ngưỡng của nó đã trôi qua.
  Incident không gán được cho crew nào in `-` ở chỗ tên crew; app không bịa ra tên.
  Đường dẫn là tuyệt đối vì cwd của Mate là `mate/` của chính nó (cùng lý do với dòng `resolve:`).
  Ví dụ:

  ```text
  ⟦mate⟧ digest: 3 item(s) — k3 needs-decision: "pick A or B" · k9 blocked: stale, quiet for 4m0s · k7 wait-mate: "report.md is ready" — status files under /w/.mate/projects/shop/crews; act per AGENTS.md section 10
  ```

- Mate thấy marker thì tự quyết theo policy trong AGENTS.md. Merge vẫn chờ người dùng trừ khi project bật `yolo`.
- Con trỏ digest là `mate/.auto-cursor`: một offset byte cho mỗi file nguồn, ghi ở dạng `<đường dẫn tương đối project>=<offset>`.
  Nó là file riêng chứ không nằm trong `.auto` vì `.auto` có ba người xoá (hook của Mate, phím `m`, tay người dùng); con trỏ nằm trong đó sẽ chết theo mỗi lần tắt auto, và lần bật lại sẽ gửi lại toàn bộ câu hỏi cũ.
  Chỉ một lần gửi đã kiểm chứng mới đẩy con trỏ, nên digest bị từ chối được chào lại nguyên vẹn ở vòng sau chứ không mất.
  Từ task 30 digest đang xếp hàng mang sẵn con trỏ nó sẽ ghi, và outbox ghi con trỏ đó lúc đánh dấu digest `sent`, không phải lúc xếp hàng.
  Mỗi project có tối đa một digest xếp hàng: vòng sau có mục khác thì viết lại dòng đó tại chỗ (giữ `at`, tức đồng hồ `wedged`), không còn gì để nói thì rút nó (`dropped`).
  `sent.log` ghi trước, con trỏ ghi sau, mục outbox đánh dấu `sent` sau cùng: hỏng ở giữa thì lần thử sau tìm thấy dòng đó trong `sent.log` và đánh dấu `sent` chứ không gõ lại, ngược lại thì mất mục.
- Hook `UserPromptSubmit` của Mate thấy prompt không có marker thì xoá `.auto`. Daemon đọc lại cờ ở đầu lượt của mỗi project, và outbox đọc lại **ngay trước khi gõ** một digest (cờ mất thì rút digest thay vì gõ), nên người dùng giành composer giữa chừng không bị máy trả lời ngay sau đó.
- Gửi không tới được Mate quá 5 phút thì mở incident `wedged` với crew là `mate`; một lần gửi thành công đóng nó. Incident là nửa bền vững: dòng footer chết theo console, inbox thì không.
  Từ task 30 luật này là của outbox, cho mọi dòng xếp hàng chứ không riêng digest, và đồng hồ là `at` của mục cũ nhất trên đĩa chứ không phải đồng hồ trong tiến trình, nên khởi động lại console không đặt lại nó.
  Đồng hồ 5 phút chạy cho cả hai kiểu hỏng - composer bận/đang có chữ của người dùng, và Mate không chạy - vì auto mode âm thầm không giao gì suốt một tiếng đúng là trạng thái incident này sinh ra để lộ; text của incident luôn nói rõ là nửa nào hỏng.
  Digest bỏ qua chính incident `(mate, wedged)` của mình: báo cáo một lỗi giao hàng qua chính đường giao hàng vừa hỏng là vô nghĩa, và người dùng đã thấy nó trong inbox.
- Lý do từ chối của lượt gần nhất hiện trên dòng thông báo của console, một lần mỗi lượt chứ không phải mỗi mục, và tự biến mất ở lượt gửi được. Ô `MODE` thêm `auto · sent 14:32:10` khi daemon đã gửi ít nhất một lần.

### News: việc Mate giữ cho captain

Chốt 2026-10-03.
Captain có thể bỏ qua câu hỏi của Mate hoặc đi vắng; hỏi lại Mate "còn gì chờ tôi" tốn thêm một lượt.
Detail của hàng Mate (dưới các field của nó) có mục `news`: các câu hỏi Mate đã gửi captain mà chưa được trả lời, tức các mục `asked "…"` trong `## Held for the captain` của `mate/backlog.md` (manual mục 13).
Chỉ câu hỏi của Mate: lời hứa và ghi chú trong cùng section là sổ sách của Mate, không hiện; câu hỏi của Crew đi tới Mate, và Mate tự quyết có đưa lên captain hay không (skill `decision-authority`), nên chỉ thứ Mate đã đưa lên mới thành news.
Console chỉ đọc file đó ở mỗi lần nạp snapshot (`query.MateNode.Held`), không gửi gì cho Mate và không tốn lượt nào.
Mỗi mục là một field: dòng đầu là tên task và tuổi theo ngày (`today`, `3d`), rồi đúng câu Mate đã gửi captain, không kèm `asked` hay `Waits on: …` (đó là ghi chú vận hành của Mate; câu hỏi gửi captain đã phải nói bằng kết quả theo manual mục 13); `y` copy câu hỏi.
Không có backlog thì không vẽ field; backlog có mà không giữ gì thì `news none`; đọc lỗi thì `unknown` kèm lý do.
Ở tầng workspace, detail của Project ghi `news N waiting on you`.
Mục rời news khi Mate xoá dòng đó khỏi backlog, tức sau khi captain trả lời trong pane của Mate; console không có nút trả lời hay đóng mục.
News khác box: box là câu hỏi của Crew chờ ai đó trả lời, news là thứ chính Mate chờ captain.
Giới hạn: news đúng bằng những gì Mate ghi vào backlog; câu hỏi Mate hỏi mà quên ghi, hoặc ghi không theo mẫu `asked "…"`, thì không hiện.

## 6. Trí nhớ của Mate

| Lớp | File | Ai viết |
| --- | --- | --- |
| Operating manual | `mate/AGENTS.md` | App, từ template `embed`, sinh lại mỗi lần start |
| Quy tắc người dùng | `.mate/WORKSPACE.md` | Người dùng |
| Bối cảnh project | `projects/<p>/PROJECT.md` | Mate, từ mục `## Durable facts` của report scout (M7); người dùng sửa |
| Quy tắc người dùng cho Crew | `.mate/CREW.md`, `projects/<p>/CREW.md` | Người dùng; app nối cuối mọi brief (M7) |
| Trí nhớ Mate | `mate/memory.md`, `mate/backlog.md` | Mate qua `mate remember`, `mate backlog` |
| Hội thoại | Session harness | Harness; app lưu `session_id` để resume |

AGENTS.md không inline lớp khác; từ task 37 `mate recall` đóng khung chúng thành một digest có thứ tự, và hook `SessionStart` đưa digest đó vào context mỗi lần khởi động, `/clear` hay compaction.
Crew không đọc trí nhớ của Mate; Mate viết vào brief những gì crew cần.
Tri thức về code đi vào AGENTS.md của repo qua PR của crew.

## 7. Bài học v1 phải giữ

- Trust dialog: Claude highlight mặc định là "No, exit"; gửi Enter mù là chết agent. Nhận diện dialog theo shape, một phím một lần, đọc lại giữa các lần.
  Worktree liên kết KHÔNG thừa kế trust với Codex: đo 2026-09-17 (task 11, Codex 0.154), crew trong `.worktrees/<p>-<id>` vẫn hiện directory-trust dialog vì Codex xác nhận theo từng absolute path.
  ADR 0028 của v1 nói ngược lại; settle step là bắt buộc cho crew, không phải thủ tục.
- Chọn và copy trong terminal của console là việc của console. Đo 2026-09-25 (Herdr 0.8.2, Claude Code 2.1.282, Ghostty): console bắt chuột cả phiên nên Ghostty không tự chọn được; `herdr agent attach` bật bắt chuột ở phía ngoài nhưng không đưa byte chuột nào vào PTY (pane `cat -v` đã bật `?1000h ?1006h` chỉ nhận chữ gõ), và không cho OSC 52 của app trong pane đi ra; Claude chạy thẳng trong tmux thì tự chọn khi kéo và copy bằng OSC 52, qua Herdr thì không.
  Vì vậy kéo chuột trái trong vùng terminal là selection của console: tô đảo màu trên bản chụp màn hình của emulator, nhả chuột thì ghi OSC 52 ra terminal thật, dòng gợi ý báo số ký tự đã copy; chuột trái không còn gửi vào PTY, nút khác và bánh xe vẫn gửi.
- Harness tự cập nhật đổi màn hình khởi động. Đo 2026-09-25: Claude Code tự lên 2.1.282 lúc 02:30, ô soạn tin trống thành `❯` NBSP `Try "edit <filepath> to..."` (gợi ý đổi giữa các lần launch), mọi start Mate Claude chờ 90 giây rồi chết với `startup screen not recognised`.
  Gợi ý chỉ được coi là ô trống khi nằm giữa hai dòng kẻ của ô soạn tin và không có gì sau dấu nháy đóng; chữ khác sau `❯` là có người gõ, không phải ô trống.
  Hệ quả dây chuyền cùng ngày: 90 giây "Running …" trông như treo, captain thoát console giữa chừng, Herdr đã launch agent nhưng `mate.meta` chưa ghi; lần start sau đụng tên với chính agent đó, và bước bù trừ dừng agent theo tên nên giết luôn nó.
  Luật từ đó: bù trừ chạy với context không bị huỷ (tối đa 30 giây) và console chờ action bị bỏ dở xong mới thoát; bù trừ chỉ dừng agent Herdr báo nằm trong pane của chính lần start đó; start thấy agent `mate-<project>` không có trong `mate.meta` thì nhận lại nếu cwd là thư mục Mate, từ chối và không đụng tới nếu ở chỗ khác; dòng "Running" hiện số giây đã chờ.
- Startup không chỉ có một modal. Đo 2026-09-18 (codex-cli 0.154.0 đã cài, 0.155.0 vừa ra): Codex vẽ prompt cập nhật ba lựa chọn TRƯỚC trust dialog, mọi `crew spawn` chết với `target_blocked: startup screen not recognised`.
  Settle phải xử lý một chuỗi dialog (cập nhật → trust → composer), mỗi cái vẫn một phím một lần và xác minh highlight trước Enter, với trần 3 dialog mỗi lần khởi động.
  Trả lời `3. Skip until next version` (không phải `2. Skip`, sẽ hiện lại ngay lần sau; không phải `1. Update now`, chạy `npm install` dưới agent).
  Phòng ngừa: launch mang `-c check_for_update_on_startup=false` (khoá có tài liệu, Codex nhận dưới `--strict-config`) nên prompt không được vẽ; bộ nhận dạng vẫn giữ làm lớp phòng thủ, vì cờ là dự đoán còn pane mới là phép đo.
- `herdr agent prompt` báo thành công dù prompt rơi vào modal hoặc nối vào text gõ dở. Dùng `--wait` và kiểm chứng composer.
- Hook `Stop` của Claude và `notify` của Codex không bắn mọi turn. Fallback theo thời gian, `unknown` là trạng thái hợp lệ.
- Test xanh với fake Herdr không chứng minh gì. Mỗi milestone có live test trên Herdr lab session riêng, tên `TestLive*`, chạy khi `MATE_LIVE=1`.
- Lệnh báo thành công phải kiểm tra lại hệ thống thật, không tin handle cũ.
- Live test runtime cần một lab session do người chạy cấp qua `MATE_HERDR_LIVE_SESSION=fm-lab-...` và `TMPDIR` không đi qua symlink (macOS `/var` → `/private/var`). Herdr báo cwd của pane đã resolve symlink, nên guard so cwd trong `runtime` dùng `samePath` thay vì so chuỗi (sửa 2026-09-17, v1 có cùng lỗi).
- Crew đang chạy lệnh shell KHÔNG phải là crew treo.
  Đo 2026-09-18 (task 18, codex-cli 0.154.0, Herdr 0.8.2): Codex vẽ `• Working (Ns • esc to interrupt)` suốt thời gian lệnh chạy, và `send.ClassifyComposer` xếp đúng là Busy, nên một crew được bảo `sleep 400` không bao giờ thành `stale`.
  Trạng thái mà luật `stale` thật sự nói tới là crew đã kết thúc turn: composer trống, pane đứng yên, `.status` không có dòng mới.
  Live test vì thế dùng brief "trả lời đúng một từ ok và không làm gì khác".
  Cùng lần đo: snapshot 40 dòng cuối của pane Codex rảnh giống nhau từng byte giữa các vòng poll, nên hash pane là tín hiệu "đứng yên" dùng được; với ngưỡng rút ngắn 20s, incident `stale` mở ở 21s, một dòng gửi vào pane gỡ nó ngay vòng sau, và `agent stop` làm `InspectAgent` trả `agent_not_found` nên `runtime_lost` mở trong cùng vòng.
- Observer không bao giờ kết luận từ một lần đọc hỏng.
  Herdr không trả lời, pane không đọc được, `.meta` chưa có agent: cả ba đều là "không nhìn được", không phải bằng chứng crew có vấn đề, nên không mở incident và không ghi health (đúng tinh thần quyết định 8).
  Chỉ câu trả lời dứt khoát `agent_not_found` của Herdr mới mở `runtime_lost`.
  Giá phải trả: mỗi crew mỗi vòng tốn ba lệnh `herdr` (lookup session, inspect, read), nên poll 5s với nhiều crew là chỗ cần đo lại khi số crew tăng.
- AGENTS.md của Mate đã vượt trần mặc định `project_doc_max_bytes` của Codex (32768) khi thêm bảng bảy trạng thái (đo 2026-09-18 ở task 18b): `mate start --harness codex` chết với `required context exceeds delivery limit`, vì `spawn` từ chối thay vì để Codex cắt đuôi im lặng. Sửa: launch Codex truyền `-c project_doc_max_bytes=131072` (khoá có trong tài liệu, 0.154.0 nhận dưới `--strict-config`, khoá lạ thì bị từ chối ngay), và bộ đo của `harness` dùng cùng hằng số `CodexDefaultMaxBytes`, nên cờ và bộ đo không thể lệch nhau. `internal/mateassets` có test ngân sách cách trần 8 KiB, hỏng ngay tại nơi sửa template. Manual còn ghi 26 lần đường dẫn workspace, nên workspace sâu vẫn ăn thêm vài trăm byte.
- Nested-session env (đo 2026-09-17, Claude Code 2.1.274): một Claude Code đang chạy export `CLAUDECODE=1`, `CLAUDE_CODE_SESSION_ID`, và các biến `CLAUDE_CODE_*` khác cho process con. Nếu Herdr server được khởi động từ shell đó thì mọi pane kế thừa chúng, và Claude trong pane coi mình là session con: hook `Stop` vẫn bắn nhưng transcript không bao giờ được ghi. Runtime gỡ `harness.NestedSessionEnv` khỏi env của server lúc spawn và khỏi pane ngay trước `agent start`. Bài học chung: không tin env kế thừa qua Herdr server, mọi biến harness cần đúng phải được set hoặc unset ở pane.
- Composer của Mate thực sự bị chiếm sau mỗi digest, và đó là lý do luật "từ chối rồi thử lại vòng sau" không phải lý thuyết.
  Đo 2026-09-18 (task 19, `TestLiveAutoDigestReachesTheMate`, Claude Code 2.1.274, Herdr 0.8.2): ngay sau khi digest vào composer, Mate vào turn và `send.ClassifyComposer` xếp là Busy suốt hơn 20 giây (`✽ Misting… (3s · thinking…)` rồi `(13s · ↓ 1.1k tokens …)`); dòng của người dùng gõ vào cùng composer bị `send.Send` từ chối hai lần với `target_blocked: agent is mid-turn` trước khi vào được.
  Một daemon dùng `QueueWhileBusy` hoặc `herdr agent prompt` sẽ xếp digest sau turn đang chạy và không ai biết; daemon này để nguyên mục chưa digest, không đẩy con trỏ, và chào lại nguyên vẹn ở vòng sau.
  Hệ quả cho cửa sổ 90 giây: một turn của Mate dài hơn một vòng là bình thường, nên số vòng bị từ chối liên tiếp không phải tín hiệu hỏng - chỉ 5 phút liên tục mới là `wedged`.
- Sentinel `⟦mate⟧ ` sống sót cả với dòng digest chứ không chỉ dòng ngắn.
  Cùng lần đo: dòng `digest: 1 item(s) — k3 needs-decision: "pick A or B" — status files under /private/tmp/…/crews; act per AGENTS.md section 10` (có `—`, `·`, dấu nháy kép và một đường dẫn tuyệt đối dài) tới `UserPromptSubmit` của Claude nguyên vẹn, nên hook ghi `Source: app` và không xoá `.auto`.
  Bằng chứng dùng được là `sent.log` có **hai** bản cùng một dòng - một do daemon ghi sau khi composer sạch, một do hook ghi khi model đọc được - còn một bản chỉ chứng minh chữ tới pane.
  Ngay sau đó một dòng người dùng gõ không có marker xoá `.auto` trong vòng poll đầu tiên, và vòng tick kế tiếp không gửi gì dù đã có câu hỏi mới chờ sẵn.
- `mate diff` đo branch của crew bằng ba chấm (`git diff <default>...<branch>`), không phải hai.
  Hai chấm so branch với đầu hiện tại của default, nên mọi commit default nhận được trong lúc crew làm việc hiện ra trong diff của crew thành dòng bị xoá - đúng lời nói dối mà người review sẽ tin.
  Bản danh sách commit thì ngược lại: `git log --oneline <default>..<branch>` hai chấm mới đúng nghĩa "những commit của riêng branch này".
  `TestDiffIgnoresCommitsTheBaseGainedAfterTheBranchStarted` dựng đúng cảnh đó trên repo thật, nên đổi số chấm là hỏng test chứ không phải hỏng một bản diff không ai đọc lại.
- Đo 2026-09-19 (task 21, `TestLiveConsoleDiffShowsACrewBranch`, codex-cli 0.154.0, Herdr 0.8.2): một crew Codex nhận brief "thêm một dòng vào README.md, commit đúng một lần, rồi ghi `wait-mate`" đi trọn vòng trong 56 giây, và `ActionDiff` qua ActionFunc của console trả về 176 byte gồm dòng commit `0d2d20d note the review in README` và patch của `README.md`.
  Diff chạy trong worktree của crew khi worktree còn, nhưng branch mới là thứ mang công việc: crew đã `crew stop` không còn worktree vẫn diff được từ repo chính, vì worktree liên kết không có kho object riêng.
  Worktree bẩn báo ở một dòng dẫn đầu chứ không phải dòng cuối: patch chỉ là phần đã commit, nên người đọc không được báo sẽ tưởng đó là toàn bộ việc crew đã làm.
- Overlay cuộn trong console không được dùng `clampTop`.
  `clampTop` có việc là giữ *hàng đang chọn* trong khung, nên gọi nó với `sel=0` trên một mặt không có hàng nào được chọn sẽ kéo offset về đầu sau mỗi phím.
  Màn hình chi tiết lỗi (`e`, `session_failure.go`) vì thế vẽ chỉ báo "↓ N more" từ task 05 mà chưa bao giờ cuộn được; phát hiện và sửa 2026-09-19 khi overlay diff của task 21 cần đúng cơ chế đó.
  Cận trên đúng là `total-h+1`, không phải `total-h`: `windowContent` mất một dòng cho chỉ báo "↑ N more" ngay khi offset rời khỏi đầu, nên ở `total-h` dòng cuối cùng vẫn nằm dưới mép.
  Bài học chung: một khung nói là còn nội dung bên dưới mà không tới được còn tệ hơn một khung không cuộn.
- `MATE_CALLER` phải đi qua đường `workspace create --env`, không phải `tab create --env`, vì tab của Mate là root pane của workspace được đổi tên.
  Đo 2026-09-19 (task 22): `Herdr.CreateAgentTab` từ chối thẳng `TabSpec.Env` trên nhánh rename ("pane environment is injected by workspace create --env"), nên chỗ duy nhất đặt được biến cho pane Mate là `ensureProjectWorkspace`; crew thì ngược lại, luôn là `tab create` nên `crewPaneEnv` nhận thêm một dòng.
  Hệ quả phải nhớ: Mate chỉ nhận biến ở lần tạo workspace đầu tiên. Đó là lý do mặc định của `CallerFromEnv` là `user` chứ không phải từ chối - thiếu biến nghĩa là "không phải Mate", và console tự truyền `CallerUser` như hằng số thay vì đọc env, vì console mở từ trong pane Mate sẽ thừa kế `MATE_CALLER=mate` và tự từ chối phím bấm của chính người dùng.
- Merge thành công không ghi gì vào `sent.log`: `sent.log` là những dòng gõ vào pane, mà merge không gõ vào pane nào.
  Bằng chứng live cho "Mate tự merge" vì thế là pane của Mate cộng với `crews/<id>.meta` = `finished` và default branch đã tiến - không có tiến trình nào khác trong test merge được.
  Đo 2026-09-19, `TestLiveMateMergesUnderYolo` (Claude Code 2.1.278, codex-cli, Herdr 0.8.2), 87 giây: Mate đọc `.status` thật, đọc brief, `peek`, đọc diff, tự nói "Yolo is on, so I'll land it", rồi chạy đúng một `mate merge shop k3` và nhận lại đúng một dòng `shop/k3: merged 1 commit(s) into main (e161a9d..df7e0de); crew finished, worktree and branch removed`.
  `TestLiveMergeFromConsoleFinishesTheCrew` (không có Mate, merge qua `consoleAction`) mất 50 giây.
- Mục 9 ("Waiting is your job") và mục 10 của manual Mate mâu thuẫn nhau về việc có nên tự poll `state` sau một digest hay không, sót lại từ trước khi observer và daemon (task 18, 19) tồn tại: mục 9 vẫn viết "Nothing wakes you on its own today" như thể chưa ai canh crew.
  Sửa ở task 20: mục 9 chỉ còn nói tới chế độ giám sát (`sleep 20` là vòng của chế độ đó), mục 10 nói rõ Mate dừng turn ngay sau khi hành động trên một digest - daemon là bên canh giữ tiếp theo, không phải Mate tự poll.
  Đo 2026-09-18, `TestLiveAutoPolicyMateAnswersADigest`: một crew Codex hỏi `needs-decision: choose colour red or blue for the button`, digest tới Mate Claude, Mate đọc `.status` thật (không đoán từ đoạn cắt ≤120 rune trên dòng digest) rồi trả lời crew bằng đúng một `mate send` (`"Use blue for the button."`), crew ghi `wait-mate: chose blue`, và Mate không tự `crew stop` - `crews/k3.meta` vẫn `state=spawned` sau khi Mate trả lời, đúng luật mục 4b rằng đóng crew là lời của người dùng.
  Bản prose viết một lần đã đúng ngay ở lần chạy live đầu tiên, không cần sửa rồi chạy lại.
- Live test phải mở pane đúng kích thước sản phẩm mở, không phải kích thước của console.
  Đo 2026-09-19 (task 24): `streamTerminalSize` trừ rail 54 cột và một cột vách khỏi bề ngang, nên một console 120x36 cho Mate pane **65x33**.
  Ba live test trước đó đều mở stream bằng `TerminalSize{120, 36}` và đều xanh, trong khi `[assign]` trên Mate thật hỏng - vì ở 65 cột thước kẻ composer của Claude dài đúng bằng pane, `herdr agent read --source recent-unwrapped` coi dòng đầy pane là dòng bị wrap và nối thước trên + composer + thước dưới thành một dòng, nên bộ định vị composer không còn thấy hộp nào.
  Đó là cách một món nợ sống sót qua ba bản test xanh: đo sai chỗ thì đo bao nhiêu lần cũng không thấy.
  `console.StreamSize` được export để test hỏi đúng hàm mà console gọi, thay vì chép lại phép tính.
- Bộ phân loại composer không được đọc `--format text`: bản render đó đã vứt mất thuộc tính duy nhất cần đến.
  Đo 2026-09-19 (task 24, Claude Code 2.1.278): sau một lượt kết thúc bằng câu hỏi cho captain, Claude Code mời sẵn một câu trả lời **trong composer**, vẽ mờ - `❯ \x1b[0m\x1b[2mUse checkout-express.html\x1b[0m`.
  Ở `--format text` nó giống hệt một dòng người dùng gõ dở, nên `send.Send` từ chối gõ đè (đúng, với những gì nó nhìn thấy) và `[clear composer]` không gỡ được vì ở đó không có gì để xoá: khác biệt duy nhất là SGR 2.
  Hai lần chạy acceptance chết cứng ở đúng chỗ này, một lần chặn dòng captain gõ, một lần chặn `[assign]`.
  Sửa: `runtime.Adapter.ReadAgentStyled` (`--format ansi`) cho ba nơi phân loại thay vì hiển thị - `internal/send`, `internal/watch`, `mate state`; `ClassifyComposer` bóc thuộc tính cho mọi luật cấu trúc cũ và chỉ dùng chúng cho một câu hỏi: mọi rune nhìn thấy được của nội dung composer có được vẽ mờ không.
  Fail-closed: màn hình không có thuộc tính, không tìm thấy chuỗi, hoặc một rune không mờ đều giữ nguyên `pending`, vì gõ đè lên dòng dở của người khác mới là sai lầm mà trạng thái này sinh ra để chặn.
  Bẫy phải nhớ: Claude vẽ chữ composer của chính nó bằng `38;2;255;255;255`, mà số `2` ở đó là mã chọn màu RGB trực tiếp chứ không phải mã faint - một bộ quét đọc từng tham số rời sẽ gọi chữ người dùng là gợi ý và gõ đè lên.
- Chốt `.auto` sau khi prompt tới được model, không phải sau khi `send.Send` trả về.
  Đo 2026-09-19 (task 24): hook `UserPromptSubmit` xoá `.auto` lúc Claude đọc prompt, không phải lúc composer sạch, nên một cờ bật xen giữa hai thời điểm đó bị chính yêu cầu của captain xoá một giây sau và project âm thầm chạy ở chế độ giám sát suốt phần còn lại.
  Mốc phải chờ là dòng `Source: user` trong `sent.log`, vì cùng cái hook đó ghi nó.
- `[assign]` trên một Mate đang giám sát gần như luôn gặp composer bận.
  Đo 2026-09-19 (task 24): ở chế độ giám sát Mate chạy vòng `sleep 20; mate state` của mục 9, tức là nằm trong một tool call gần như trọn mỗi chu kỳ, nên `[assign]` bị `target_blocked: agent is mid-turn` bốn đến năm lần liên tiếp trước khi vào được, ở cả hai lần chạy.
  Console in nguyên lý do lên dòng thông báo và người dùng bấm lại, nên không phải lỗi; nhưng "nút chính của box thường xuyên bị từ chối" là một phát hiện về sản phẩm, không phải một chi tiết test.
- Mục 9 và mục 10 của manual Mate lại mâu thuẫn, lần thứ hai (lần đầu ghi ở task 20 phía trên).
  Mục 9 bảo Mate ở chế độ giám sát poll crew rồi hành động trên thứ nó thấy; mục 10 viết "In manual mode, never act on a Crew event on your own".
  Sửa ở task 24 bằng cách vạch đúng chỗ ranh giới nằm: crew do chính Mate spawn và đang giám sát thì Mate được hành động, còn thứ chế độ giám sát cấm là với tay sang cái khác - câu hỏi của crew khác, một incident, hộp thư của captain.
  Bài học chung: mỗi lần thêm một cơ chế canh giữ mới (observer, daemon), hai mục này phải được đọc lại cùng nhau, vì chúng mô tả cùng một câu hỏi từ hai phía.
- `agent_session` của Herdr là chuyện của Codex, không phải của mọi harness.
  Đo 2026-09-20 (task 25, Herdr 0.8.2): `agent_session` có mặt trên **mọi** agent Codex với `{"kind":"id","source":"herdr:codex","value":"<uuid rollout>"}`, và **null trên mọi agent Claude**.
  Giá trị đó chính là uuid nằm trong tên file `rollout-<giờ>-<uuid>.jsonl`, nên một lần đọc `agent list` là đủ để định vị transcript của crew Codex mà không phải mở file nào.
  Hệ quả: locator có hai nhánh chứ không phải một - Claude đi bằng `transcript=` của hook Stop rồi `session_id=` cộng luật đặt tên `~/.claude/projects/<slug>/<session-id>.jsonl`, Codex đi bằng `agent_session.value` rồi `AdoptCodexRollout` theo cwd và giờ launch.
  Binding của crew không bao giờ đổi (crew chỉ spawn một lần, không resume), nên đường dẫn đã giải được ghi vào `session` và các vòng sau dùng lại; không có bước đó thì mỗi crew Codex tốn thêm hai lệnh `herdr` mỗi 5 giây chồng lên ba lệnh của observer.
- Tool `exec` của Codex 0.154 không nhận JSON mà nhận một đoạn JavaScript, và rollout lưu đoạn đó như một **chuỗi JSON**.
  Đo 2026-09-20: `input` là `"const r = await tools.exec_command({\"cmd\":\"…\",\"workdir\":\"…\"});"`, nên bộ đọc nào không bóc lớp chuỗi trước sẽ ghi target của mọi lệnh shell thành phần escape chứ không phải lệnh.
  Cùng tool đó cũng là đường sửa file: Codex gửi một script dựng patch, và header `*** Update File:` nằm trong một string literal JavaScript nơi xuống dòng là hai ký tự `\` và `n` chứ không phải ký tự newline, nên bộ đọc theo dòng không thấy header và mọi lần sửa file thành một "patch" không tên.
  Sửa: bóc chuỗi, rồi tìm header theo chuỗi con chứ không theo dòng; `class` của lời gọi đó được sửa thành `edit` để câu chuyện đọc là "edits README.md" chứ không phải "runs: …".
- Dòng `.status` không có giờ của riêng nó, và mtime của file không thay được.
  mtime là giờ của dòng **cuối cùng**, nên lấy nó cho mọi dòng sẽ đẩy câu hỏi ra sau câu trả lời ngay khi crew ghi thêm một dòng, và `waited_ms` âm ngay ở lần `reindex` đầu tiên.
  Giờ đúng nằm trong transcript của chính crew: `echo "state: …" >> $MATE_STATUS` là một tool call có timestamp, nên dòng status được định giờ bằng lời gọi shell sớm nhất có chứa nguyên văn dòng đó (đo 2026-09-20: khớp cả ba dòng của crew Codex trong acceptance), mtime chỉ là dự phòng và payload ghi rõ luật nào đã chạy.
- Đơn vị "một lượt" của hai harness là một lời gọi model, và Codex nói ra cả hai đơn vị.
  `token_count` của Codex là cộng dồn phiên, nhưng cùng record có `last_token_usage` là chi phí của **lời gọi vừa xong** và `model_context_window`; `task_started`/`task_complete` mang `turn_id` là lượt harness (một prompt và toàn bộ việc nó gây ra).
  Nên `turn` là nhóm giữa hai `token_count` (bằng đúng một message group của Claude), `context_tokens_after` lấy từ `last_token_usage`, còn `turn_id` của Codex và `promptId` của Claude nằm ở `turn.harness_turn_ref`.
- Một dòng digest hoặc `[assign]` nằm **hai lần** trong `sent.log`, và câu chuyện phải biết đó là một lần giao việc.
  Mục 7 phía trên đã ghi cặp đó là bằng chứng sentinel tới được model; đo 2026-09-20 ở `TestLiveTimelineExplainsTheAcceptance`, một timeline coi cả hai là giao việc sẽ kể cùng một `[assign]` hai lần cách nhau một giây.
  Luật: một dòng `app → mate` lặp nguyên văn dòng `app → mate` liền trước trong vòng 10 phút là bản của hook, ghi channel `hook` và `confirms: true`, kể thành "the Mate reads it"; digest bị từ chối không bao giờ được ghi vào `sent.log` nên một bản lặp luôn là của hook chứ không phải một lần chào lại.
- Merge không để lại dấu trong file nào, nên `merge.done` phải suy từ git và nguyên nhân của nó có hai luật.
  `mate merge` xoá branch ngay sau khi merge, nên vòng poll sau đã không còn branch để hỏi: bằng chứng là commit cuối mà ingest đã ghi được trong lúc branch còn sống, cộng `state=finished`, cộng `merge-base --is-ancestor`.
  Nguyên nhân: lượt Mate chạy `mate merge` nếu có (yolo), còn merge từ Console thì không có event nào để trỏ vào cả - luật dự phòng là dòng `wait-mate` mà captain đã đọc, và payload ghi `cause_rule` để người đọc biết luật nào đã chạy.
- Máy trạng thái cảnh phải là một **bảng đầy đủ**, không phải một `switch`.
  Mỗi event kind có ít nhất một hàng không điều kiện trong bảng của crew và trong bảng của Mate, kể cả những kind không bao giờ làm ai di chuyển (`tool.finished`, `health.changed`, `mode.changed`).
  Nhờ vậy `unexplained: <kind>` nghĩa đúng là "một kind chưa ai nghĩ tới", chứ không phải "một trạng thái chưa ai nghĩ tới", và bài kiểm tra độ sâu của task 26 mới có thứ để hỏng vào.
  Test giữ hai chiều: `Kinds()` của `scene` phải bằng `Kinds()` của `timeline`, và mọi hàng phải được một case chạy qua (trừ 11 hàng không event nào định tuyến tới được, liệt kê thẳng trong test cùng lý do).
- Nguyên nhân của **mọi** lượt Mate là dòng cuối vào composer (luật mục 5 của `docs/timeline.md`), nên "lượt có trigger là assign/digest thì Mate đang đọc giấy" sai theo hai hướng khác nhau.
  Hướng thứ nhất, đo trên fixture acceptance: một `[assign]` sinh ra sáu lượt Claude nối nhau, nên cảnh sẽ kể "the Mate reads buybtn's note" sáu lần.
  Hướng thứ hai, đo 2026-09-20 ở lần chạy live đầu tiên của task 26: dòng mà luật nhân quả tìm thấy **không phải** `[assign]` mà là bản echo của hook `UserPromptSubmit` (cùng giây 16:47:57, lượt bắt đầu 16:48:05), nên một cạnh đòi trigger phải là `[assign]` thì không bao giờ chạy và Mate không hề "đọc" gì trong một lần chạy nó đọc thật - test hỏng đúng vào chỗ đó.
  Sửa: `mate.reads` chỉ xét trạng thái chứ không xét trigger (chỉ chạy từ `receiving_digest`, tờ giấy nằm trên bàn cho tới khi một lượt nhặt nó lên), thêm cạnh `mate.reads.echo` lấy chính bản echo của hook làm hành động đọc (đúng như câu kể "the Mate reads it" của mục 6), và `turn.ended` **không** đưa Mate về `idle` khi tay nó còn một tờ chưa đọc.
  Mate Codex không có hook nên không có echo; nó nhặt giấy ở lượt kế tiếp, và cùng một cảnh ra đúng cho cả hai harness.
- Projection phải tính lại toàn bộ mỗi lần, không tăng dần.
  Ingest hoàn toàn có thể chèn một event có `at` cũ hơn event nó đã ghi (commit đọc ngược từ transcript, dòng status định giờ bằng lệnh shell đã chạy trước đó), và một máy trạng thái tăng dần bị nạp event sai thứ tự thì sai vĩnh viễn.
  Giá phải trả là một lần đọc có thứ tự toàn bộ event của project cho mỗi vòng poll **có ghi được gì**; đổi lại `transition` là một hàm của `event` nên hai lần reindex giống nhau từng byte, id transition cũng vậy.
- Incident là một **báo cáo**, không phải cái lồng.
  Một crew bị observer gọi là `stale` rồi ghi `needs-decision` vào `.status` là crew đang thức và đang đứng ở cửa, nên mọi cạnh thường vẫn chạy từ `asleep`/`blocked`, còn dòng `resolved` đến sau thấy crew đã đi chỗ khác thì không làm gì.
  Làm ngược lại (đóng băng tới khi observer gỡ) thì một lần hand back xảy ra trong lúc đang `stale` biến mất khỏi cảnh, vì luật gỡ incident của observer chính là "crew ghi dòng mới".
- `transition.id` khoá theo **thời điểm** chứ không theo số chạy: `<project>|<at>|<n>`.
  Một event đến muộn mang `at` cũ chỉ đánh số lại các transition cùng thời điểm với nó, nên con trỏ của `--follow` vẫn dùng được; nếu id là số chạy toàn cục thì mọi transition sau đó đổi id và người đang theo dõi sẽ thấy in lại cả đuôi câu chuyện.
  Khoá theo thời điểm cũng là lý do `v_now` phá hoà bằng `ORDER BY at DESC, id DESC` đọc đúng trạng thái cuối trong một khoảnh khắc có hai bước (đi tới cửa, rồi đứng đợi).
- `reviewing` và `merging` của Mate không có producer trong luồng acceptance, và cảnh nói thẳng điều đó thay vì giả vờ.
  `review.started` chưa ai phát (mục 4 của `docs/timeline.md`), còn captain xem diff và merge từ Console thì không ghi vào file nào cả (mục 7), nên `merge.done` mang `by: captain` và Mate không hề nhúng tay.
  Cảnh lấy `reviewing` từ chính thứ Mate thật sự làm khi review: lệnh `mate diff <project> <crew>` trong transcript của nó (`mate.reviews.diff`); `merging` chỉ xuất hiện khi `yolo` bật.
  Bài kiểm tra độ sâu vì thế liệt kê hai trạng thái này là "fixture không tới được" kèm lý do, thay vì bịa dữ liệu để tô xanh.
- `event` phải có một khoá tự nhiên, không chỉ `id AUTOINCREMENT`.
  Transcript được đọc lại toàn bộ mỗi vòng (vì `ParseTranscript` cố tình giữ lại message group cuối), nên không có khoá thì cùng một sự kiện sẽ thành hàng thứ hai với id thứ hai, và một dashboard theo `event.id > ?` sẽ phát lại lịch sử như tin mới.
  Thêm đúng một cột `event.dedup UNIQUE` so với schema mục M5; mọi bảng còn lại đã có khoá theo thứ đã sinh ra nó.
- `input_tokens` của Codex đã bao gồm phần cache, `input_tokens` của Claude thì không - cùng tên cột, hai nghĩa khác nhau, và task 25 đã ghi số Codex thẳng vào `turn.input_tokens` mà không trừ phần cache.
  Đo 2026-09-20 trên rollout thật (task 27, `TestLiveUsageMatchesTheHarness`): bản ghi `token_count` cuối là `{"input_tokens":232424,"cached_input_tokens":209152,...,"output_tokens":1544,"total_tokens":233968}`, và `232424+1544=233968` khớp `total_tokens` tuyệt đối - `cached_input_tokens` chỉ là tập con mô tả, không phải một khoản cộng thêm.
  Claude thì ngược lại: ví dụ `turn.started` của mục 4 (`input_tokens:32, cache_read_tokens:57690, cache_write_tokens:739`) cộng đúng bằng `context_tokens_after:58461`, tức cả bốn nhóm token của Claude tách rời nhau, không nhóm nào là tập con của nhóm khác.
  Hậu quả trước khi sửa: mọi tổng bốn nhóm (`v_task_ledger`, `v_now.tokens_today`, ngân sách, `mate usage`) đếm hai lần phần cache của Codex, và giá thành cũng tính tiền phần đó hai lần nếu captain đặt giá cho cả `input_per_m` lẫn `cache_read_per_m`.
  Sửa tại nguồn, trong `internal/timeline/transcript.go`'s `codexTurns`: trừ delta `cache_read` khỏi delta `input` trước khi ghi vào `turn.input_tokens`, để cột đó mang cùng một nghĩa ("tính theo giá input, không phải giá cache") ở cả hai harness; từ đó mọi phép cộng bốn nhóm ở tầng trên không cần biết turn đến từ harness nào.
  `context_tokens_after` của Codex không đổi, vì nó tính thẳng từ `last_token_usage` thô, không đi qua `turn.input_tokens` đã sửa.
- Claude Code hiện chặn `sleep N` chạy tiền cảnh trong tool Bash.
  Đo 2026-09-24 (task 30, lần chạy đầu của `TestLiveAssignQueuesWhileTheMateIsBusy`): Mate được bảo chạy `sleep 45` trả lời "The shell blocks a plain `sleep 45` in the foreground, so I started it in the background instead" rồi kết thúc turn sau 8 giây, nên Mate không hề bận như test định dựng.
  Cùng một lệnh chờ viết thành `python3 -c 'import time; time.sleep(45)'` thì không bị chặn và giữ Mate Busy trọn 45 giây.
  Hệ quả cho vòng `sleep 20; mate state` của mục 9 manual (đo 2026-09-24, task 31, Claude Code 2.1.281): chỉ `sleep` **đứng một mình** bị chặn, lời từ chối nguyên văn là `Blocked: standalone sleep 45. To wait for a condition, use Monitor with an until-loop …`.
  Trong transcript của Mate `shop` ở ba lần chạy acceptance đầu của task 31, vòng giám sát (`sleep 20; mate state …` hoặc `for i in $(seq 1 12); do sleep 20; …; done`) đều chạy tiền cảnh và trả về dòng `state:` sau đúng khoảng chờ, không lần nào bị chặn hay bị đẩy ra nền.
  Vòng giữ nguyên; mục 9 thêm một câu: chạy nó tiền cảnh đúng như một lệnh, vì `sleep` trần bị một số harness từ chối và một lần chờ đẩy ra nền thì không phải là chờ.
- `[assign]` xếp hàng thay vì bị từ chối đổi hẳn trải nghiệm trên Mate bận.
  Đo 2026-09-24 (task 30, Claude Code, Herdr 0.8.2): `[assign]` bấm lúc Mate đang ở giây thứ 10 của một tool call 45 giây trả về ngay `queued for the Mate (the Mate is mid-turn)`, outbox thử 22 lần mỗi 2 giây, và dòng `resolve:` vào composer 3 giây sau dòng `Stop` của turn đó (chờ tổng 43 giây), đúng hai bản trong `sent.log` (outbox và hook), không bản thứ ba.
  Mate đọc status file và trả lời crew trong chưa tới 10 giây sau đó, nên mục rời inbox gần như ngay khi hàng kịp hiện `assigned HH:MM`: hậu tố `assigned HH:MM` sống ngắn trên Mate rảnh, `assigned, queued` mới là chữ người dùng thực sự thấy.
- Bản ghi `sent.log` của app có thể đứng **sau** bản của hook dù app là bên gõ.
  Cùng lần đo: dòng của hook `UserPromptSubmit` (11:08:48) nằm trước dòng outbox ghi (11:08:47 theo giờ bắt đầu lượt thử) trong file, vì `send.Send` còn ngủ 400ms chờ đọc lại composer sau Enter trong khi Claude đã đọc prompt và hook đã ghi.
  Cuộc đua này có từ trước task 30 (đường `[assign]` cũ cũng ghi sau khi `send.Send` trả về); outbox giờ ghi giờ của lúc composer sạch chứ không phải lúc bắt đầu lượt thử, nhưng thứ tự trong file vẫn không bảo đảm, nên luật "bản lặp liền sau là của hook" của timeline chỉ đúng về số lần giao, không đúng về bản nào là của ai.
- Kiểm hình dạng brief ở `crew spawn` nghĩa là mọi nơi gọi `spawn.SpawnCrew` đều phải đưa brief đúng schema, kể cả test.
  Task 32 (2026-09-24): 36 lời gọi trong test, trong đó 17 nằm trong 16 file live test, đưa text một dòng kiểu `"work"`; tất cả giờ đi qua `internal/brief/brieftest.Ship`, giữ nguyên câu lệnh làm cả `## Captain's words` lẫn `## Build`.
  Hệ quả chưa đo: live test giờ chạy với template mới (bàn giao `handback.md`, luật dừng ở open decision), nên một crew được bảo "chỉ ghi `wait-mate` rồi thôi" có thể viết hand-back trước; task 34 phải chạy lại chúng chứ không coi xanh cũ là bằng chứng.
- Nhãn `task=` phải lấy từ dòng đầu của `## Captain's words`, không phải dòng đầu của brief.
  Brief theo schema luôn mở đầu bằng heading đó, nên luật cũ sẽ gán cho mọi crew cùng một nhãn `## Captain's words`.
  Cùng chỗ: `oneLineTask` và `oneLineReason` cắt theo byte ở 160, mà lời captain thường là tiếng Việt, nên một nhát cắt rơi giữa một chữ; giờ cắt theo rune.
- Một Crew không ghi được `PROJECT.md`: nó nằm ngoài worktree và không phải một trong hai file brief cho phép ghi ngoài worktree (status file và `handback.md`/`report.md`).
  Mục 2 của manual cũ nói "scout Crew viết bản đầu" là một lời hứa không ai thực hiện được, đúng như `PROJECT.md` của `shop` vẫn trống sau năm task; M7 đổi thành: report scout kết thúc bằng `## Durable facts`, Mate chép vào `PROJECT.md` kèm nguồn (mục 14).
  Cùng lần đọc lại: mục 6 cũ nói cwd của Mate "là nơi duy nhất được ghi" trong khi mục 14 bảo Mate bổ sung `PROJECT.md`; câu mục 6 giờ nói rõ ngoại lệ đó.
- Test tách section của manual không được cắt ở mọi `\n## `.
  Từ M7 manual trích heading brief (`## Build`, `## Acceptance`) trong ví dụ, nên helper `section` cũ cắt mục 4 và mục 6 ngay giữa ví dụ và test báo "không có ví dụ"; giờ chỉ heading đánh số `## N. ` mới kết thúc một mục.
- Lời dặn đặt trong output của tool thì Mate nghe; lời dặn chỉ nằm trong manual thì không.
  Task 24 đã sửa mục 7 bước 4 để bảo Mate ở chế độ tự động dừng lượt sau khi spawn, và Mate không làm theo ở cả hai lần chạy: nó spawn, giám sát bằng `sleep 20; mate state`, rồi merge trong cùng một lượt, nên daemon không bao giờ có lúc gửi được digest.
  Task 31 đưa đúng lời dặn đó vào dòng cuối của ba lệnh Mate chạy đúng lúc nó muốn ở lại: `crew spawn` (`auto mode: end your turn now; the console will wake you with a digest when <crew> speaks. Do not poll.`), `state` (`auto mode: do not poll; end your turn and wait for the digest.`) và `send` của Mate (`auto mode: end your turn; the digest will tell you when <crew> hands back.`), chỉ khi project có `.auto`.
  Đo 2026-09-24 (Claude Code 2.1.281, codex-cli, Herdr 0.8.2): ở cả bảy lần chạy, Mate `blog` báo captain một câu "đã bắt đầu" rồi kết thúc lượt spawn 25-33 giây sau yêu cầu, không lần nào vào vòng giám sát; ở mọi lần digest tới được, lượt merge là một lượt riêng do digest mở (bằng chứng ở `docs/evidence/m6-debt-auto-turn-2026-09-24.md`).
  Manual chỉ còn một chỗ nói luật này (mục 10, "Ending the turn in auto mode"); mục 7 bước 4 và mục 9 trỏ về đó thay vì nói lại bằng lời khác.
  Bài học chung: manual được đọc một lần lúc bootstrap, output của tool được đọc mỗi lần; một luật hành vi mà Mate hay quên thì nói nó ở chỗ Mate đang nhìn khi sắp phạm.
- `[assign]` giao câu hỏi cho Mate **xử lý**, không giao quyền quyết (chốt 2026-09-24, sau khi M7 merge).
  Trước M7, Mate `shop` tự chọn trang checkout khi nhận `[assign]`, và test hai project đòi đúng điều đó; từ M7, Mate ghi lựa chọn đó là `decides: captain` và `decision-authority` bắt nó hỏi lại captain, nên test hỏng có hệ thống ở bước "Mate trả lời crew" (lần chạy 7 của task 31).
  Chính sách đúng, test sai: test giờ đợi lời hỏi lại nêu cả hai trang, kiểm Mate không gửi crew lựa chọn nào, rồi gõ câu trả lời của captain vào pane Mate và đợi Mate chuyển cho crew.
  Mate chuyển bằng `mate brief append` rồi một dòng trỏ crew đọc phần thêm vào `## Captain's words`, không phải bằng chính lựa chọn; test chấp nhận cả hai đường miễn lựa chọn nằm ở chỗ crew đọc.
- Một Mate Claude đang rảnh bị đọc là bận vì màn hình của nó **trích** màn hình của crew Codex.
  Đo 2026-09-24 (task 31, lần chạy thứ 2 và thứ 4, Claude Code 2.1.281): Mate `blog` đã dừng lượt đúng như dòng `auto mode:` bảo (`✻ Sautéed for 26s · done`), nhưng tool call cuối của nó in pane của crew Codex đang chạy, nên transcript của chính Mate chứa `⎿  • Working (2s • esc to interrupt)`.
  `claudeBusy` rơi về các seed chung, seed `esc to interrupt` khớp dòng trích đó, và digest bị từ chối `agent mate-blog is mid-turn` suốt năm phút tới khi incident `wedged` mở; crew nằm ở `wait-mate` còn test hết giờ.
  Đó là lý do lần 1 và 3 xanh còn lần 2 và 4 đỏ với cùng một bộ phân loại: hỏng hay không tuỳ tool call cuối của Mate có tình cờ in pane của crew đang bận hay không.
  Sửa trong `internal/send`: Claude chỉ bận theo dấu hiệu của chính nó - spinner vẽ ở cột 0, hoặc placeholder hàng đợi - và không dùng seed chung nữa, vì Claude không tự vẽ seed nào (2.1.274 và 2.1.281) còn mọi thứ nó trích đều thụt lề dưới `⏺`/`⎿`.
  Capture nằm ở `internal/send/testdata/screens/claude_idle_quoting_codex_busy.ansi`, và test cũng giữ chiều ngược lại: spinner của chính pane vẫn là bận.
  Bài học chung: bộ phân loại của một harness chỉ được tin dấu hiệu mà harness đó tự vẽ, vì pane của Mate là nơi mọi harness khác được trích ra.
- Lần `crew stop` thứ hai trên một crew đã đóng không được viết lại trạng thái cuối.
  Đo 2026-09-24 (task 34): mọi crew đã merge trong acceptance kết thúc `.meta` bằng `state=failed`, vì cleanup của test gọi `StopCrew(..., discard=true)` trên mọi crew `ListCrews` trả về, kể cả crew đã đóng, và `StopCrew` ghi lại meta như một lần dừng mới.
  `finished`/`failed` là trạng thái cuối (mục 4b); giờ `StopCrew` trên crew đã đóng trả về kết cục đã ghi và không đổi gì, `crew stop` in `already closed, state finished; nothing changed`.
- Mốc để nhận rollout Codex là lúc launch, không phải lúc sẵn sàng.
  `harness.AdoptCodexRollout` từ chối rollout cũ hơn mốc, mà mốc cũ là `started_at`, ghi sau khi agent sẵn sàng và đã nhận brief; Codex mở rollout trước đó.
  Đo 2026-09-24 (task 34): bản ghi đầu của rollout rơi 0.5 giây sau `started_at` ở một lần chạy (nhận được) và 0.2 giây trước ở lần sau (mất), nên `reindex` sau khi crew đã đóng mất transcript theo một cuộc đua dưới một giây; live thì observer đi qua `agent_session` của Herdr nên không thấy.
  Sửa: `crew spawn` và `mate start` ghi `launched_at` ngay trước `agent start`, `crew stop` giữ nó, locator dùng nó; bản ghi cũ dùng `started_at` trừ năm phút (`docs/timeline.md` luật 4).
- Test đọc lời Mate phải đọc được ngôn ngữ của captain.
  Lần chạy đầu của `TestLiveM7EmptyRepoDoesNotGuess` hỏng vì test tìm `?` hoặc chữ tiếng Anh, trong khi Mate trả lời captain bằng tiếng Việt, và lời hỏi của nó là "Anh/chị chỉ cần xác nhận và gửi link thanh toán", không có dấu hỏi.
  Mate đúng, test sai; các khẳng định mang ý nghĩa (không crew, không file trang, `main` đứng yên) giữ nguyên.
- Mate theo bước 7 của bootstrap (đề xuất scout onboarding khi `PROJECT.md` trống) chỉ khoảng một nửa số lần: hai trên năm lần chạy repo trống (task 34).
  Nó luôn trả lời đúng yêu cầu, nên kịch bản vẫn pass; chưa sửa, vì chưa có lần nào thiếu scout làm hỏng việc.
- Nguồn của một mục nhớ viết tương đối **thư mục project** (`.mate/projects/<p>/`), không phải gốc workspace (task 36, 2026-09-24).
  Báo cáo trí nhớ gọi nó là "tương đối workspace", nhưng chính ví dụ của nó (`crews/esp1/report.md`, `sent.log`) chỉ đúng khi đọc từ thư mục project, vì đó là nơi `crews/`, `sent.log` và `PROJECT.md` nằm; `memory check` giải mọi đường dẫn tương đối từ đó và chỉ từ chối khi nó trèo ra khỏi workspace.
  Chữ trong dấu nháy kép bị bỏ qua khi tìm đường dẫn, vì nguồn `captain, "<lời nguyên văn>", <ngày>` hay chứa dấu `/` trong lời captain.
- Mọi crew đã spawn đều có `crews/<id>/brief.md`, nên "`send` tới crew đã có brief" của B9 nghĩa là mọi lần Mate gửi sau spawn, kể cả một câu trả lời `needs-decision` (task 36).
  Dòng nhắc vì thế viết có điều kiện ("if this correction applies to future crews"), và chỉ nguồn `mate` mới thấy: captain gửi từ shell hay console (`--from user`) không có `memory.md` để cập nhật.
- `brief check` không thấy một lựa chọn sản phẩm đặt sai section.
  Đo 2026-09-24 (task 34, lần chạy sau M7 thứ ba): Mate `shop` ghi `## Open decisions` là `none` và đặt luật "không chọn trang checkout, dừng với `needs-decision`" vào `## Build`, đúng điều mục 6 của manual cấm; crew vẫn dừng đúng chỗ nhờ luật 5 của template.
  Một lần trên ba, ghi lại chứ chưa sửa: check cố ý chỉ kiểm hình dạng, không kiểm nghĩa.
- Năm câu hỏi mở của báo cáo trí nhớ (mục 12.9), đo 2026-09-24 ở task 35 trong pane Herdr 0.8.2 của lab `fm-lab-mate-w35-a`, Claude Code 2.1.281, codex-cli 0.154.0 rồi 0.156.1 (codex tự lên bản giữa buổi, xem gạch cuối).
  Mỗi câu có một live test giữ nó; đo tay đi trước, test chạy lại trên bản đã cài lúc chạy.
- A1, khoá tắt auto-memory của Claude: `autoMemoryEnabled: false` trong settings.
  Chuỗi trong binary 2.1.281 nói thẳng: "Enable auto-memory for this project. When false, Claude will not read from or write to the auto-memory directory."; binary còn có biến `CLAUDE_CODE_DISABLE_AUTO_MEMORY`.
  Đối chứng không có khoá, thư mục memory của cwd có `MEMORY.md` gieo sẵn một canary: Claude trả lời đúng canary (`MARIGOLD-4417`), và khi được bảo "remember that the captain prefers short replies" thì ghi `captain-prefers-short-replies.md` và sửa `MEMORY.md`.
  Có khoá thì trả lời `NONE` và thư mục không đổi một byte, trong cả ba cách đặt: file vừa là `.claude/settings.json` của cwd vừa truyền bằng `--settings` (cách của Mate), file chỉ truyền bằng `--settings` (cách của crew), và biến môi trường trong pane; input mỗi lượt nhẹ đi khoảng 0,9K token (38,4K so với 39,3K).
  Chọn khoá settings chứ không chọn biến: pane Mate chỉ nhận env ở lần tạo workspace đầu tiên (mục 7, task 22), còn settings đi theo mọi lần start.
  `ClaudeSettings` ghi khoá, `ensureClaudeSettings` thêm nó vào một settings cũ còn thiếu và giữ nguyên mọi khoá khác (một giá trị captain đã đặt thì không đụng), crew Claude nhận `{"autoMemoryEnabled": false}` trong `crews/<id>/settings.json` vì crew cũng không có lý do gì ghi tri thức ra ngoài workspace.
  Phát hiện phụ: tắt rồi, Claude tự đề nghị "If you want it to carry over, I can add it to ~/.claude/CLAUDE.md" - chỗ chặn đường đó là manual (task 36), và live `TestLiveMateAutoMemoryOff` (26s) cho thấy Mate làm đúng: ghi `- Prefers short replies; keep answers brief. (captain, 2026-09-24)` vào `## Captain` của `mate/memory.md`, thư mục memory của Claude giữ nguyên bản gieo.
- A2, `SessionStart` của Claude trong pane Herdr: bắn với `startup`, `clear`, `compact`, `resume`, mỗi lần đúng một lần (settings nạp cả qua `--settings` lẫn qua cwd không làm hook chạy hai lần), và stdout vào context cả bốn lần.
  Live `TestLiveMateSessionStartHookSources` (55s): hook in `The session canary word is PELICAN-<source>.`, Mate được hỏi canary mới nhất và trả lời đúng `PELICAN-startup`, `PELICAN-clear`, `PELICAN-compact`, `PELICAN-resume`; `/clear` và `/compact` gõ bằng `send.Send` như app gõ slash command.
  Thời điểm: `startup` và `resume` lúc launch, trước prompt đầu; `clear` ngay khi `/clear` vào; `compact` ngay khi nén xong.
  Payload có `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `source`, `model`; transcript ghi output của hook thành attachment `hook_success` có `content`, nên timeline đọc được hook đã nói gì.
  `/clear` sinh `session_id` mới, `compact` và `resume` giữ id; hook `Stop` ghi id mới vào `mate.meta` ở lượt đầu sau `/clear`, nên từ `/clear` tới lúc lượt đó xong meta vẫn trỏ session cũ (payload của `SessionStart` mang sẵn id mới, nếu task 37 muốn đóng khe đó).
  Sau `compact`, output của các lần hook trước chỉ còn khi bản tóm tắt nhắc tới; Claude cũng nạp lại `CLAUDE.md` ở prompt đầu sau `clear` và sau `compact`.
- A3, `SessionStart` của Codex TUI: có bắn, trên cả 0.154.0 (đo tay) lẫn 0.156.1 (live), từ `$CODEX_HOME/hooks.json` và từ `.codex/hooks.json` của cwd khi cwd đã được trust, với `startup`, `resume`, `compact`, `clear`, và stdout vào context.
  Khác Claude ở hai chỗ.
  Một: hook chạy ở **prompt đầu tiên** sau launch, resume, `/compact` hay `/clear`, không phải lúc launch (live kiểm không có dòng nào trước prompt; Codex chỉ mở rollout ở prompt đầu), và output vào context thành message `developer` mang `content_item_kinds: ["hooks.additional_context"]`, đứng ngay trước prompt đó.
  Hai: output dài bị cắt - hook `lavish-axi` toàn cục của người dùng (2.534 token) vào context thành khoảng 10.100 ký tự mở đầu bằng `Warning: truncated output (original token count: 2534)` và một đường dẫn tới bản đầy đủ; binary có khoá theo từng hook `additionalContextLimit`, chưa đo.
  Hook mới hoặc đã đổi mà chưa có `trusted_hash` trong `hooks.state` của `config.toml` thì Codex vẽ thêm dialog `Hooks need review` (1. Review hooks / 2. Trust all and continue / 3. Continue without trusting) ngay sau dialog trust thư mục; `--dangerously-bypass-hook-trust` bỏ qua nó cho một lần chạy.
  Settle không trả lời dialog đó (fixture `codex-0.154.0-hooks-review.txt` giữ nó là unrecognised), vì mate chưa cài hook Codex nào.
  `agent_session` của Herdr cho Codex đến từ chính cơ chế này: script `herdr-agent-state.sh session` trong `~/.codex/hooks.json` của người dùng gửi `pane.report_agent_session`, nên nó chỉ có sau prompt đầu, và một máy không cài integration Herdr thì không bao giờ có.
  Live `TestLiveCodexSessionStartHook` (71s) chạy trong một `CODEX_HOME` lab (auth chép sang, `features.hooks = true`) để không đụng hook và trust thật: Codex trả lời `HERON-global-startup, HERON-project-startup`, rồi `…-resume`, `…-compact`, và sau `/clear` (id mới) chỉ còn `HERON-global-clear, HERON-project-clear`.
- A4, `codex resume <id>` trong pane Herdr: chạy, và `internal/harness/codex.go` trước đây đọc sai help (picker chỉ mở khi **không** có id).
  `codex resume --dangerously-bypass-approvals-and-sandbox -c check_for_update_on_startup=false -c project_doc_max_bytes=131072 <id>` trong thư mục đã trust đi thẳng tới composer, hội thoại cũ vẽ lại phía trên, rollout cũ được viết tiếp, id giữ nguyên, không dialog mới; Codex nhớ từ đã được dặn trước khi dừng.
  Không có cờ cập nhật thì resume vẽ đúng prompt cập nhật cũ (fixture `codex-0.154.0-resume-update-dialog.txt`), nên cờ có tác dụng dưới subcommand.
  Id không có rollout: `ERROR: No saved session found with ID …` rồi về shell, Herdr chỉ báo `agent start` hết giờ sau 60 giây; vì thế start kiểm rollout theo tên file trước khi launch.
  B11 làm theo đó: `StopMate` đọc `agent_session.value` của Herdr trước khi agent mất (Codex không có id lúc launch, và `/clear` đổi id), dự phòng bằng luật nhận rollout của timeline (cwd, `session_meta` không sớm hơn `launched_at`, đúng một ứng viên), còn không thì giữ id meta đang có; một Mate Codex chết mà chưa `mate stop` được thử nhận rollout ở lần start sau.
  Start resume khi meta có id cùng harness và rollout còn; launch resume không lên thì dọn tab, thử đúng một lần bản mới trong tab mới và `mate start` in `note: resuming the codex session <id> failed (…); started a fresh session instead`.
  Live `TestLiveSpawnMateResumeRemembersCodex` (54s, codex-cli 0.156.1): lần đầu trả lời `OK`, stop ghi đúng id Herdr báo, lần sau `resumed=true`, không trust, không update, trả lời `ZEBRA`.
- A5, Claude `--resume` và manual vừa sinh lại: có nạp lại.
  Live `TestLiveMateResumeReloadsManual` (29s): đổi `default_branch` từ `main` sang `trunk` giữa stop và resume, hỏi "theo manual lúc này" mà không cho dùng tool, Mate trả lời `main` trước và `trunk` sau.
  Transcript ghi hai attachment `instructions` cho `AGENTS.md` (75.875 và 75.900 byte), cái thứ hai lúc resume, mang bản mới; bản cũ vẫn nằm trong lịch sử và Mate tự nói bản mới thay bản cũ.
  Đo tay cùng ngày: lúc resume Claude chỉ đính lại file nhớ **đã đổi**; file không đổi thì không đính lại, nên một resume với manual y hệt không tốn gì, còn mỗi lần manual đổi giữa hai lần chạy thì context mang thêm một bản manual đầy đủ, khoảng 19K token.
  Hệ quả cho task 37: hook `resume` không cần in dòng báo manual đã đổi.
- Những gì task 37 dựa vào được khi dựng hook `SessionStart` từ các đo trên.
  Claude: một hook không matcher trong `mate/.claude/settings.json` nhận đủ bốn source trong pane Herdr, mỗi source một lần, stdout của nó là context trước lượt kế tiếp; đọc `source` và `session_id` từ payload trên stdin; `ensureClaudeSettings` không ghi đè settings đã có, nên thêm hook cho Mate cũ phải đi đường `EnsureAutoMemoryOff` đã đi (thêm khoá còn thiếu, giữ phần còn lại).
  Codex: hook trong `mate/.codex/hooks.json` chạy được (Mate dir đã trust), nhưng muộn một nhịp (ở prompt đầu, không phải lúc launch), bị cắt quanh 2,5K token nếu không đặt `additionalContextLimit`, và lần đầu vẽ dialog `Hooks need review` mà settle hiện từ chối; muốn dùng thì phải chọn giữa `--dangerously-bypass-hook-trust` trên launch hoặc dạy settle dialog đó có fixture, và `recall` phải vừa ngân sách đó - nếu không thì Mate Codex vẫn chạy `recall` theo manual như mục 12.7 của báo cáo.
- codex-cli tự lên 0.154.0 → 0.156.1 giữa buổi đo (lúc 15:07, không do mate: mọi prompt cập nhật trong lab đều được trả lời `2. Skip`), và lần start Mate Codex đầu tiên sau đó chết với `codex startup screen not recognised`: dialog trust vẽ lại hoàn toàn ("Folder access", đường dẫn, đoạn "Trust this folder? …", `1. Trust and continue` / `2. Quit`, footer `enter continue · esc quit`).
  Đo trong lab: highlight vẫn mở ở 1 và phím `1` chỉ chọn chứ không xác nhận (màn hình giống từng byte), nên câu trả lời cũ vẫn đúng; profile Codex giờ có hai bố cục cho cùng một dialog trust, `TestStartupDialogLayoutsOfOneScreenShareTheirAnswer` giữ luật mọi bố cục của một dialog chung một chuỗi phím.
  Bài học về dialog khởi động ở đầu mục này ("cờ là dự đoán, pane mới là phép đo") lặp lại đúng hình: một bản harness mới là một bộ dialog mới cho tới khi đo, và mọi crew Codex sẽ chết ở cùng chỗ nếu không có fixture.
- Claude Code cắt output của hook ở 10.000 ký tự, và task 35 chưa thấy vì canary chỉ một dòng (đo 2026-09-24, task 37, Claude Code 2.1.281).
  Hook `SessionStart` in 28K ký tự thì model chỉ nhận `Output too large (27.4KB). Full output saved to: …` và 2 KB đầu, ở cả stdout thường lẫn JSON `hookSpecificOutput.additionalContext`; hằng `1e4` nằm ngay trong binary.
  Vì thế hook của Claude gọi `recall` với 9.500 byte (byte là cận trên của ký tự UTF-16), và thứ tự "trạng thái trước, trí nhớ sau" là thứ quyết định cái gì sống sót: phần 1 luôn nguyên vẹn, dòng cuối nói phần nào mất và bảo Mate tự chạy `recall`.
- Codex cắt output hook khác Claude: giữ đầu và đuôi, bỏ khúc giữa, khoảng 10.000 ký tự tổng (đo 2026-09-24, codex-cli 0.156.1, 28K ký tự filler có canary đầu và đuôi: cả hai canary còn, dòng filler 100-300 mất).
  `additionalContextLimit: 20000` trên hook thì cùng output vào nguyên vẹn, không có dòng `truncated output`, nên đơn vị là token chứ không phải byte; hook của Mate đặt 32000 và `recall --max-bytes 48000`.
  Một digest bị bỏ khúc giữa là tệ nhất trong ba cách cắt, vì nó giữ phần đầu và phần đuôi mà không nói giữa đã mất gì; đó là lý do Codex cũng có `--max-bytes`.
- Dialog `Hooks need review` của Codex chỉ nói số lượng ("1 hook is new or changed."), không nói hook nào, nên `2. Trust all and continue` là trust mù cả hook chưa trust của người dùng (đo 2026-09-24, codex-cli 0.156.1).
  Thứ nêu tên từng hook nằm sau `1. Review hooks`: bảng event (cột Review, dòng `⚠ N hook(s) need review`), rồi `enter` vào event là danh sách `[!] Hook k · new` với Source (`Project config - <path>` hoặc `User config - <path>`), Command, Trust của hook đang chọn; `t` trust đúng một hook đang chọn, `esc` hai lần về composer.
  Settle đi đúng đường đó: đọc mọi hook `[!]` trước, chỉ trust khi tất cả là hook của chính Mate (so Source và Command bỏ khoảng trắng, vì path bị wrap), còn không thì từ chối mà không bấm `t`; không dùng `--dangerously-bypass-hook-trust`, không đọc `~/.codex/hooks.json`.
  Trust được Codex ghi vào `$CODEX_HOME/config.toml` dưới `[hooks.state."<hooks.json>:session_start:0:0"] trusted_hash`, cùng file với trust thư mục; lần launch sau với cùng bytes không có dialog, đổi đường dẫn binary thì review lại và settle lại đi qua.
  Với Mate thật đó là `~/.codex/config.toml` của captain, đúng như trust thư mục mà settle đã trả lời từ task 11; hook Codex trong một lab CODEX_HOME là cách duy nhất để test không ghi vào đó.
- Chỗ wrap của màn review Codex có thể nuốt đúng ký tự nó ngắt (đo 2026-09-24, lần chạy lab đủ bộ thứ hai của task 37): `…/001/.mate/projects/shop/…` vẽ thành `…/001/.mate` rồi `projects/shop/…` ở dòng sau, mất `/`, trong khi lần trước `…/001/` + `codexlab/…` giữ `/` và `mate-` + `session` ngắt sau gạch nối.
  Bản đầu so sánh bằng cách bỏ khoảng trắng nên từ chối hook của chính Mate, và lần chạy xanh trước đó xanh chỉ vì độ dài đường dẫn tạm tình cờ ngắt ở chỗ khác.
  Sửa: giữ từng dòng của giá trị, và ở mỗi chỗ ngắt chỉ tha đúng một khoảng trắng hoặc một `/` bị mất (`wrapMatch`); fixture `codex-0.156.1-hooks-sessionstart-own-wrapped.txt` giữ màn hình đó.
  `.mateprojects` vẽ giống hệt `.mate/projects` ở đó và không bộ đọc nào phân biệt được; không nguy hiểm, vì Codex chỉ nạp project hook từ chuỗi `.codex/hooks.json` của chính cwd Mate.
- Live test của hai package không chạy song song được trên một lab session: `go test ./internal/spawn/ ./cmd/mate/` chạy hai binary cùng lúc, cả hai dùng project `shop` (tên agent `mate-shop` trùng) và marker `session-owners/<session>` của cùng một Herdr session (lần chạy thứ ba báo `herdr session … is owned by workspace mate-…, not mate-…`).
  Test restart giờ dùng project `harbor`, và lệnh chạy lab của task 37 cần `-p 1`; đó là giới hạn của hạ tầng live test từ trước, không phải của sản phẩm (một Herdr session một workspace là luật cố ý).
- Pane chỉ nhận env ở lần tạo workspace (bài học task 22), và `StopMate` đóng tab cuối nên đóng luôn workspace: một live test tạo sẵn workspace có `CODEX_HOME` lab rồi stop và start lại Mate Codex sẽ chạy lần hai trong `CODEX_HOME` thật của người dùng.
  Lần chạy đầu của `TestLiveCodexMateRecallHook` (2026-09-24) đúng như vậy: resume không thấy rollout trong home thật, hết giờ, bản mới trả lời trust thư mục và trust hook, và `~/.codex/config.toml` của người dùng có thêm hai mục cho thư mục tạm `/private/tmp/TestLiveCodexMateRecallHook1100517496/…/mate` (một `[projects."…"]`, một `[hooks.state."…/.codex/hooks.json:session_start:0:0"]`), chưa xoá vì đó là file của người dùng.
  Test giờ chỉ launch Codex một lần và kiểm `/compact` trong cùng session; `TestLiveSpawnMateResumeRemembersCodex` (task 35) lúc đó vẫn chạy trong home thật; từ task 38 mọi live test chạy trong `CODEX_HOME` lab (gạch dưới).
- Mate Codex giờ tự ghi `session_id` (uuid rollout) và `transcript` vào `mate.meta` ở prompt đầu, qua payload `SessionStart`, không còn chỉ dựa vào `agent_session` của integration Herdr lúc `StopMate` (đo: `TestLiveCodexMateRecallHook` trong CODEX_HOME không có hook Herdr nào).
- Restart có stow đo được (2026-09-24, `TestLiveRestartMateStowsFirst`, Claude Code 2.1.281): Mate được dặn một sự thật chỉ trong hội thoại và "đừng ghi file nào", `restart_mate` từ console gõ dòng `stow:` qua outbox, lượt stow xong trong 29 giây, sự thật vào `PROJECT.md`, dòng kết quả `stowed; stopped mate-shop; Mate mate-shop is running on claude in pane …`, và `sent.log` có đúng hai bản dòng `stow:` (outbox và hook).
- Live test không còn đụng `~/.codex` của người dùng (task 38, 2026-09-24).
  Đo trước khi sửa: 115 trên 138 mục `[projects."…"]` trong `~/.codex/config.toml` của người dùng trỏ vào thư mục tạm đã xoá của live test mate, vì mọi crew Codex của live test trust worktree của nó trong home duy nhất nó thấy; mục cũ để người dùng tự quyết, không sửa.
  Sửa ba lớp: `codexlab.Home` (`internal/harness/codex/codexlab`) là một `CODEX_HOME` tạm chỉ có bản chép `auth.json` (codex-cli 0.156.1 lưu login ở đó, `cli_auth_credentials_store` mặc định `file`) và `[features] hooks = true`, cài trong mọi hàm dựng lab (`consoleLiveLab`, `liveLabSession` của spawn, watch, send, runtime), và cleanup của nó làm test hỏng nếu `config.toml` của người dùng có thêm dòng nào nêu đường dẫn tạm; `codex.LaunchCodexHome` từ chối, khi `MATE_LIVE=1`, mọi home ngoài thư mục tạm cho launch Codex và cho pane Mate, trước khi launch gì; và `Herdr.StartAgent` `export` env của launch vào shell của pane trước mỗi lần start, nên relaunch trong `StartMate`/resume cũng mang `CODEX_HOME` lab.
  Hash `config.toml` của người dùng trước và sau mọi lần chạy live của task 38 giống nhau (`908cc00f…`, 571 dòng).
  `TestLiveSpawnMateResumeRemembersCodex` giờ chạy trong lab, nơi không có integration Herdr, nên lấy `session_id` từ hook `SessionStart` của chính Mate thay vì `agent_session`.
- `tab create` của Herdr không thừa kế `--env` của `workspace create` (đo 2026-09-24, Herdr 0.8.2: workspace tạo với `--env FOO_T=ws`, tab tạo sau không có `--env` in `FOO_T` rỗng, tab có `--env FOO_T=tab` in `tab`).
  Hệ quả trước khi sửa: Mate restart khi một crew còn giữ workspace là một `tab create` trần, mất `MATE_AGENT_ROLE`, `MATE_CALLER` và mọi biến identity, nên `send` của nó ghi `Source: user`, nhắc B9 im, và `merge` coi nó là captain.
  Sửa: env của Mate (identity và `CODEX_HOME`) đi cả trên workspace create lẫn trên launch, và runtime `export` nó ở mỗi lần start.
- `restart_mate` từ console khởi động lại Mate bằng harness mặc định của workspace, nên Mate Codex quay lại thành Mate Claude mới (tìm thấy 2026-09-24 khi viết acceptance task 38); giờ restart giữ harness trong `mate.meta`.
- Composer của Codex trống giữa hai tool call, nên "bận rồi trống hai lần" không phải là hết lượt (đo 2026-09-24, task 38, codex-cli 0.156.1): console báo `stowed` trong khi Mate Codex còn đang ghi, và restart cắt lượt đó ("Conversation interrupted").
  Sửa: khi `mate.meta` có `transcript` của Mate Codex, stow chỉ kết thúc khi rollout có `task_complete` sau dòng `stow:` (`codex.CodexTurnCompletedAfter`); luật composer chỉ còn cho Mate Codex chưa có rollout.
- pi chỉ làm Crew (đo 2026-10-01, pi 0.99.1, Herdr 0.8.2, pane 93x39, [evidence](evidence/pi-contract-2026-10-01.md); task 70).
  Pane pi đọc qua `visible`, vì `recent-unwrapped` có lúc gộp hai đường kẻ và hàng composer thành một dòng 664 ký tự, draft nằm kẹp giữa và không còn hàng composer nào để tìm.
  Composer là các hàng giữa hai đường kẻ `─` ngay trên hai dòng footer; khi bận, đường kẻ trên thành `── ⠴ Working ───` và composer vẫn vẽ, trống.
  Cấu hình toàn cục của người dùng chảy vào pane nếu không chặn: extension trong `~/.pi/agent/extensions` (ba cái trên máy đo) và skill trong `~/.agents/skills` được nạp; launch luôn có `--no-extensions --no-skills --no-approve --offline`.
  `--no-approve` bỏ dialog trust và bỏ qua `.pi/` của project; nếu dialog vẫn hiện, mate chọn "Trust (this session only)", lựa chọn duy nhất đã đo không ghi vào `~/.pi/agent/trust.json`.
  Brief đi bằng `--append-system-prompt <đường dẫn>`, không ghi gì vào worktree; pi nhận cả text lẫn đường dẫn, và đường dẫn không tồn tại bị dùng nguyên văn làm text mà không báo, nên file được kiểm trước khi launch.
  `--thinking` nhận cả năm mức `Effort`, nhưng pi kẹp theo model mà không báo (`deepseek-flash`: medium thành high, xhigh thành max); mức thật đọc lại từ bản ghi `thinking_level_change` của session và đi vào telemetry như effort của runtime.
  `/quit` gõ vào composer đang có draft sẽ nối vào draft, và `ctrl+d` chỉ thoát khi composer trống, nên stop êm bấm `ctrl+u` trước rồi mới gõ `/quit`.
  Session đặt tên lúc launch (`--session-dir`, `--session-id`); file chỉ xuất hiện ở prompt đầu tiên, và `--session-id` với id chưa có file thì tạo session mới chứ không báo lỗi.
  Chưa đo: pi nạp `AGENTS.md` (không có thì `CLAUDE.md`) của mọi thư mục cha, đo được tới git root và theo mã nguồn thì tới `/`, nên Crew ở `<workspace>/.worktrees/<project>-<crew>/` có thể nạp cả file context ở `<workspace>/` và phía trên; cần một phép đo ngoài git repo, và mate hiện không chặn cũng không ghi lại các file đó.

## 8. Tái sử dụng từ v1

Copy vào module mới, đổi import, giữ test:

- `internal/runtime`: Herdr adapter, `herdrjson.go`, `session_stream.go`, `names.go`, `wait.go`, mapping lỗi.
- `internal/process/exec.go`.
- `internal/harness`: launch spec Claude và Codex, `codex_trust.go`, `startup_prompt.go` và testdata.
- `internal/ui/console` và các kiểu DTO trong `internal/query` mà console cần.
- `scripts/gotestreport` (đã copy ở task 01).

Không copy: `persistence`, `application`, `orchestration`, `observability`, `fsboundary` (viết lại nhỏ trong `store`), `domain`.

## 9. Cấu trúc package

```text
cmd/mate/              một binary: `mate <workspace>` mở console, `mate <lệnh>` cho agent
internal/config/         build metadata, defaults
internal/ui/console/     copy v1
internal/query/          kiểu DTO console cần, backend mới điền
internal/store/          đọc ghi .mate/, khoá append, layout, ranh giới đường dẫn
internal/box/            gộp status + sent.log + incident thành view
internal/send/           gửi một dòng vào pane agent qua Herdr, kiểm chứng composer
internal/watch/          observer và triage
internal/autopilot/      daemon chế độ tự động: digest 90 giây, xếp vào outbox của Mate
internal/outbox/         hàng đợi `mate/.outbox`, người gửi duy nhất vào composer Mate, `wedged`
internal/spawn/          start Mate, spawn Crew
internal/brief/          schema brief M7: tên section, `brief check`, `brief append`
internal/facts/          `project facts`: chỉ metadata git, không mở file nào
internal/host/           the Console's sibling columns: WezTerm/Ghostty Layout (M10, M13)
internal/panerun/        the program each column runs; swaps what it shows (M13)
internal/runtime/        copy v1
internal/harness/        hợp đồng harness (Profile, capability, Registry, launch Prepare/Build, NewLaunchSpec, ScreenProfile: nguồn đọc pane, dialog startup, composer; TranscriptSource, QuotaProvider); không import package con nào
internal/harness/claude/ mọi thứ riêng Claude Code: profile, launch, màn hình, settings và hook, transcript, capture startup
internal/harness/codex/  mọi thứ riêng Codex: profile, launch, chuỗi chỉ dẫn, màn hình, hook, transcript, telemetry; kèm codexlab
internal/harness/pi/     mọi thứ riêng pi, chỉ vai Crew: profile, launch, màn hình đọc qua `visible`, session và telemetry (mức thinking thật)
internal/harness/catalog/ danh sách harness biên dịch sẵn, harness mặc định theo vai, và suite hợp đồng; chỉ binary import
internal/harness/harnesstest/ fixture dùng chung cho test của các package harness
internal/process/        copy v1
assets/                  AGENTS.md của Mate, brief.md, skills, hook scripts
```

## 10. Danh sách task

∥ nghĩa là có thể chạy song song với task trước nó.

### M0. Nền

| # | Task | Xong khi |
| --- | --- | --- |
| 01 | Khởi tạo module Go, `cmd/mate`, Makefile `check`, gotestreport với `TestLive*` là skip duy nhất được phép | `make check` xanh. Đã xong 2026-09-17. |
| 02 | Copy `internal/runtime`, `internal/process`, `internal/harness` launch spec và startup prompt classifier từ v1. Đổi import, giữ test và testdata. Live test đổi tên thành `TestLive*` và gate bằng `MATE_LIVE=1`. Đổi tên biến môi trường `MATE_*` của v1 thành `MATEV2_*` (từ 2026-09-24 lại là `MATE_*`). | Test unit pass, live test skip có kiểm soát. Đã xong 2026-09-17. |
| 03 ∥ | `internal/store`: layout `.mate/`, đọc ghi `workspace.yaml`, `project.yaml`, `.meta`, append `.status` và `sent.log` có flock, ranh giới đường dẫn trong workspace. | Test với thư mục tạm; symlink ra ngoài workspace bị từ chối. Đã xong 2026-09-17. |
| 04 ∥ | `mate init`, `mate project add/list/remove`. Repo phải là thư mục con của workspace và là git repo. | Đăng ký hai project trên thư mục thật. Đã xong 2026-09-17. |

### M1. Console và Mate sống

| # | Task | Xong khi |
| --- | --- | --- |
| 05 | Copy `internal/ui/console` và DTO `internal/query`, cắt màn hình attempts và incident overlay, nối gallery và project view vào `store`. | Golden test còn giữ pass, `mate .` hiện hai project. Đã xong 2026-09-17. |
| 06 ∥ | Template AGENTS.md của Mate bản đầu, fork từ firstmate và cắt tmux, treehouse, no-mistakes, secondmate, X mode. CLAUDE.md `@AGENTS.md`. Sinh file bằng `embed`. | Snapshot test, review tay. Đã xong 2026-09-17. |
| 07 | `internal/spawn` start Mate: tạo `mate/`, Herdr workspace và tab, `agent start`, trust dialog, ghi `mate.meta`. | Live test: Mate Claude trả lời đúng vai. Đã xong 2026-09-17. |
| 08 | Hook `UserPromptSubmit` và `Stop` cho Mate: ghi `sent.log`, lưu `session_id`, xoá `.auto` khi prompt không có marker. | Live test: `sent.log` có dòng user và dòng mate. Đã xong 2026-09-17. |
| 09 | Session view stream mode nối pane Mate và pane crew (Enter trên hàng crew mở terminal của crew), phím detach, header hiện chế độ. | Live test E2E. Mate xong 2026-09-17; crew nối 2026-09-18 sau khi test tay phát hiện Enter trên crew rơi vào fallback `mate attach` không tồn tại. |
| 10 | Mate stop và restart với `--resume session_id`. | Live test: Mate nhớ câu trước. Đã xong 2026-09-17. |

### M2. Crew và chế độ giám sát

| # | Task | Xong khi |
| --- | --- | --- |
| 11 | Brief template, skill `brief-writing`, `mate crew spawn`: worktree, tab, launch, trust, brief làm prompt đầu, ghi `.meta`. | Live test: crew Codex nhận brief. Đã xong 2026-09-17. |
| 12 ∥ | `internal/send`: phân loại composer `empty/pending/unknown`, gõ một lần, retry Enter, settle cho slash command, prefix `0x1f` tuỳ chọn. | Live test ba case: trống, text dở, popup slash. Đã xong 2026-09-17. |
| 13 ∥ | `mate send`, `mate peek`, `mate state`. | Unit với pane giả, live với crew thật. Đã xong 2026-09-17. |
| 14 | `internal/box`: gộp thành view, phân loại verb. | Unit trên fixture. Đã xong 2026-09-17. |
| 15 | Box rail trong console: Enter, `r`, `p`. | Live vòng: crew hỏi, người dùng `r`, crew tiếp tục. Đã xong 2026-09-17. |
| 16 | `mate crew stop` và teardown: xác nhận agent chết, xoá tab và worktree, giữ `crews/<id>/`. | Live test. Đã xong 2026-09-17. |
| 17 | AGENTS.md: intake, ship/scout, brief, spawn, supervise. Skill `harness-adapters`, `stuck-crew-recovery`. | Acceptance: một yêu cầu đi đến `done:`. Đã xong 2026-09-17, evidence `docs/evidence/m2-acceptance-2026-09-17.md`. |

### M3. Chế độ tự động

| # | Task | Xong khi |
| --- | --- | --- |
| 18 ∥ | Observer `internal/watch`: mỗi crew mở của mọi project, poll `.status`, inventory Herdr, composer classifier, hash pane, clock tiêm được. Mở/đóng incident `stale`, `runtime_lost` vào `incidents.log` theo hợp đồng mục 4b. `box.Load` đọc `incidents.log`, `View.OpenIncidents(crew)`. Console khởi động observer khi mở workspace và vẽ cột sức khỏe từ nó. | Unit với clock giả và pane giả cho từng chuyển tiếp mở/đóng. Live: crew Codex thật đứng im thành `blocked` rồi tự gỡ khi có dòng gửi vào pane; agent bị giết thành `runtime_lost`. Đã xong 2026-09-18; brief `sleep` trong ô này không dùng được, xem mục 7. |
| 18b ∥ | Từ vựng trạng thái theo mục 4b: `crewstate` in `state · health`, `query` bỏ hẳn từ vựng v1 (`succeeded`, `awaiting_review`, `IsFinished`...), `spawn` ghi `state=spawned` lúc spawn, `state=failed` khi spawn thất bại, `crew stop` từ chối trước khi giết và ghi `state=finished|failed`, `box` verb crew là `working`/`needs-decision`/`wait-mate`, brief template, manual Mate (mục 4, 8, 9, 12 và skill stuck-crew-recovery), spec, mọi live test đang chờ `done:`. Tương thích ngược: `.meta` có `stopped_at` mà không có `state=` đọc là `finished`; dòng `done:` cũ đọc là `wait-mate`. | Unit từng quy tắc suy trạng thái; acceptance M2 chạy lại và đi đến `wait-mate:` rồi `crew stop` → `finished`. Đã xong 2026-09-18, evidence `docs/evidence/m3-state-vocabulary-2026-09-18.md`. |
| 19 | Daemon auto trong console (`internal/autopilot`): digest 90 giây từ inbox, incident và `wait-mate`, gửi có kiểm chứng với marker, con trỏ `mate/.auto-cursor` để restart không gửi lại, `wedged` trên crew `mate` sau 5 phút, dừng khi `.auto` mất, chỉ báo `auto · sent hh:mm:ss` ở cột MODE và lý do từ chối ở dòng thông báo. | Unit từng luật với clock giả và pane giả. Live `TestLiveAutoDigestReachesTheMate`: crew Codex hỏi, digest tới `UserPromptSubmit` của Mate Claude (hai dòng `Source: app` trong `sent.log`), người dùng gõ một dòng không marker thì `.auto` mất và vòng sau không gửi gì. Đã xong 2026-09-18. |
| 20 | Policy auto trong AGENTS.md, kể cả cách Mate xử lý `blocked` và `wait-mate` trong digest. | Acceptance có kịch bản. Đã xong 2026-09-18, evidence `docs/evidence/m3-auto-policy-2026-09-18.md`. |

### M4. Review và merge

Quyết định trước khi làm (2026-09-19):

- Merge là hành động kết thúc một ship, nên `mate merge` thành công thì tự chạy `crew stop` và ghi `state=finished`. Đây là chỗ duy nhất trạng thái cuối được đặt mà không phải gõ `crew stop` trực tiếp, nhưng vẫn là quyết định của người dùng, hoặc của Mate khi project bật `yolo`.
- `needs-rebase` không phải trạng thái crew. Nó là kết quả của lệnh merge: branch không fast-forward được vào default branch thì lệnh từ chối, in nguyên nhân, không đổi gì. Mate gửi crew một dòng bảo rebase, crew về `working`.
- Ai gọi merge: người dùng từ CLI hoặc từ console (Actions trên hàng crew đang `wait-mate`), hoặc Mate qua `mate merge` khi `yolo` bật. Khi `yolo` tắt, một lời gọi có `MATE_CALLER=mate` (spawn đặt biến này trong pane Mate) bị từ chối với thông điệp rõ. `yolo` là `project.yaml`, có sẵn `project add --yolo`; thêm `mate project yolo <name> on|off`.
- `backlog.md` là trí nhớ Mate tự ghi, app không ghi vào đó. Task 23 thu hẹp thành `mate backlog <project>`: in một bảng chỉ đọc từ `.meta` và status (crew mở, trạng thái, task, branch, tuổi), Mate đọc để đối chiếu khi bootstrap.

| # | Task | Xong khi |
| --- | --- | --- |
| 21 ∥ | `mate diff <project> <crew>`: `git diff <default>...<branch>` và `git log --oneline <default>..<branch>` của worktree crew, chỉ đọc, in ra stdout; `--stat` cho bản tóm tắt. Console: Actions trên hàng crew có `diff`, mở overlay cuộn được trong project frame với cùng nội dung; Enter/Esc đóng. | Unit trên repo tạm (branch trước, sau, không commit, worktree bẩn cũng hiện). Live: crew đã `wait-mate` xem được diff từ console. Đã xong 2026-09-19. |
| 22 ∥ | `mate merge <project> <crew>`: chỉ `git merge --ff-only` vào default branch trong repo chính, từ chối khi worktree crew bẩn, khi branch không ancestor-clean (`needs-rebase`), khi caller là Mate mà `yolo` tắt; thành công thì `crew stop` → `finished` và in một dòng kết quả. `mate project yolo <name> on\|off`. Console: Actions trên hàng crew `wait-mate` có `merge` với bước xác nhận. Manual Mate mục 9 "Delivery": lệnh merge đã có; khi `yolo` tắt báo captain, khi bật thì merge rồi báo. | Unit trên repo tạm cho từng nhánh từ chối. Live: một crew Codex thật đi từ brief tới `finished` qua merge từ console; và một lần Mate `yolo` tự merge. Đã xong 2026-09-19. |
| 23 | `mate backlog <project>`: bảng chỉ đọc từ `.meta` và status; manual mục 3 dùng nó thay `crew list` khi bootstrap. App không ghi `backlog.md`. | Unit với fixture; restart Mate thì bảng khớp cây console. Đã xong 2026-09-19. |
| 24 | Acceptance end-to-end trên hai project: mỗi project một task ship và một task scout, chế độ manual cho project thứ nhất, auto cho project thứ hai, đi tới `finished` qua merge; ghi `docs/evidence/m4-acceptance-<ngày>.md`. Trả nợ: composer classifier nhận màn hình chào của Claude Code ở kích thước pane console để `[assign]` chạy được trên Mate mới khởi động. | Evidence đầy đủ, `make check` xanh, không sửa tay giữa chừng. Đã xong 2026-09-19, evidence `docs/evidence/m4-acceptance-2026-09-19.md`: hai lần chạy liên tiếp `TestLiveAcceptanceTwoProjects` (353s, 351s) và `TestLiveAssignWorksOnAColdMate` (40s, 52s). |

Nợ kỹ thuật đã biết:

- ~~`harness.Claude.BuildLaunchSpec` bắt buộc có `ContextPath`, nên `spawn` truyền chính `mate/AGENTS.md` qua `--append-system-prompt-file` trong khi `CLAUDE.md` cũng đã nạp nó từ cwd. Manual vào context hai lần.~~ Trả xong ở task 17: `AgentSpec.ManualInCwd` cho phép `ContextPath` rỗng, launch spec dùng `DeliveryCwdManual` và không truyền cờ context nào; `spawn` bật cờ đó cho Mate Claude. Với Codex thì không có nợ: Codex không tự nạp `CLAUDE.md` từ cwd, cơ chế nạp duy nhất của nó chính là file ở cwd, và nó ưu tiên `AGENTS.override.md` hơn `AGENTS.md`, nên manual vào context đúng một lần. `spawn` vẫn ghi `AGENTS.override.md` cho Mate Codex: đó là tên file Codex đọc, và giữ nguyên quy tắc "override che AGENTS.md tracked" mà crew worktree bắt buộc phải có.

- ~~`internal/send` chưa nhận màn hình chào của Claude Code ở kích thước pane mà console stream resize tới: `[assign]` trên Mate chưa có turn nào bị từ chối `agent is showing a screen mate cannot name` (đo 2026-09-19).~~ Trả xong ở task 24: nguyên nhân không phải màn hình chào mà là bề rộng pane. `streamTerminalSize` cho Mate 65 cột trong console 120x36, đúng bằng độ dài thước kẻ composer của Claude, nên `recent-unwrapped` nối thước trên, dòng composer và thước dưới thành một dòng. `splitAtClaudeRules` tách mọi dãy ≥10 `─` về dòng riêng trước khi định vị composer; fixture cũ không đổi phân loại vì một thước vốn đã đứng riêng thì tách xong vẫn y nguyên. `console.StreamSize` được export để live test mở pane đúng hình học sản phẩm mở.

- Digest của daemon chưa được chứng minh trong acceptance hai project (đo 2026-09-19, task 24): Mate ở chế độ tự động nhận yêu cầu của captain rồi spawn, giám sát và merge gọn trong **một lượt** khoảng 90 giây, nên không có lúc nào daemon có mục mới mà Mate đang rảnh. `sent.log` của `blog` không có dòng `digest:` nào trong cả hai lần chạy. Mục 7 bước 4 của manual đã được sửa để bảo Mate ở chế độ tự động dừng lượt sau khi spawn và để daemon đánh thức, nhưng Mate không làm theo trong cả hai lần. Bản thân daemon vẫn có live test riêng ở task 19 và 20; thứ chưa chứng minh được là Mate chịu nhường lượt cho nó sau một task do captain khởi xướng.

- ~~`store.Init` không tạo `.mate/WORKSPACE.md`, nên mọi Mate mở đầu bootstrap bằng `cat: … No such file or directory` trên đúng file mà manual của nó bảo đọc. Vô hại nhưng thấy trong mọi pane của bản ghi task 24.~~ Trả 2026-09-19: `store.Init` seed `WORKSPACE.md` một lần, không ghi đè.

### M5. Timeline: dữ liệu đủ sâu để kể lại mọi thứ

Mục tiêu (chốt 2026-09-20): một người chưa từng thấy mate vẽ được cảnh "Mate là CEO ngồi trong phòng, crew là nhân viên, crew hỏi thì cầm giấy chạy vào phòng CEO đứng đợi" chỉ từ dữ liệu, không cần hỏi thêm.
Token là một thuộc tính của dữ liệu đó, không phải mục tiêu riêng.
Skin (dashboard web, cảnh văn phòng, swimlane) là phụ và làm sau; M5 chỉ chứng minh dữ liệu.

Ba câu hỏi dữ liệu phải trả lời được:

1. Ai đang làm gì, ngay lúc này, và vì sao: chuỗi nhân quả `crew hỏi → digest/assign → Mate đọc → Mate trả lời → crew làm tiếp` nối được bằng `cause_event_id`, không suy theo thời gian gần nhau. Trong một turn thấy được tool nào, file nào, lệnh gì, kết quả, thời lượng; khoảng bận không có tool call ghi là `thinking`, không có hố đen.
2. Chuyển cảnh nào hợp lệ: máy trạng thái cảnh tường minh, mỗi cạnh ghi event kích hoạt. Crew: `arriving → at_desk_working → walking_to_ceo(question) → waiting_at_ceo → at_desk_working → walking_to_ceo(handback) → waiting_review → leaving(merged|closed)`, cộng `blocked`, `asleep`, `gone`. Mate: `idle → reading(crew) → deciding → answering(crew) → reviewing(crew) → merging → idle`, cộng `on_phone(user)`, `receiving_digest`.
3. Đo được bao nhiêu: token bốn loại và model theo turn, cộng dồn theo task và theo Mate, context size sau mỗi turn, sự kiện nén context, thời lượng turn, thời gian chờ ở cửa CEO, số lần hỏi lại trên một task, chi phí khi có `pricing`.

Nguồn: transcript (Claude theo message, Codex rollout cộng dồn; parser v1 trong `internal/harness`), `.status`, `sent.log`, `incidents.log`, `.meta`, git của worktree. Locator: Mate Claude có `transcript=` từ hook Stop; Codex lấy rollout id từ `agent_session.value` của Herdr, dự phòng `AdoptCodexRollout` theo cwd và giờ launch. Observer trong console là writer duy nhất; CLI và dashboard chỉ đọc.

Schema (`internal/db`, SQLite qua `modernc.org/sqlite`, WAL, một file `.mate/mate.db` cho cả workspace, cột `project` ở mọi bảng cần):

```sql
actor(id PK, project, kind /*mate|crew|user|app|observer*/, name, harness, first_seen, last_seen, gone_at)
session(id PK, actor_id FK, harness_session_id, transcript_path, started_at, ended_at, resumed_from_session_id)
task(crew_actor_id PK FK, project, text, brief_path, branch, worktree, spawned_at, closed_at,
     close_state, close_cause_event_id, merged_event_id, question_count, handback_count)
event(id PK AUTOINCREMENT, project, at, actor_id FK, kind, subject_actor_id NULL, turn_id NULL,
      task_actor_id NULL, cause_event_id NULL, payload JSON, ref_path, ref_offset)
  INDEX (project, at), (actor_id, at), (kind, at), (cause_event_id), (task_actor_id, at)
turn(id PK, actor_id FK, session_id FK, ordinal, started_at, ended_at, trigger_event_id, outcome,
     model, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, thinking_tokens,
     context_tokens_after, tool_count, ref_path, ref_offset)
action(id PK, turn_id FK, event_id FK, at, tool, target, summary, duration_ms, ok)  INDEX (target), (turn_id)
message(event_id PK FK, from_actor_id, to_actor_id, channel /*pane|status|digest|assign|hook*/, text, marked)
question(id PK, asked_event_id FK, crew_actor_id FK, text, answered_event_id NULL, answered_by_actor_id NULL, waited_ms NULL)
incident(id PK, actor_id FK, kind, opened_event_id FK, resolved_event_id NULL)
usage_sample(id PK, session_id FK, at, cumulative, input, cache_read, cache_write, output, thinking, ref_path, ref_offset)
transition(id PK, actor_id FK, from_state, to_state, at, event_id FK, target_actor_id NULL, detail)  INDEX (actor_id, at)
pricing(model PK, input_per_m, cache_read_per_m, cache_write_per_m, output_per_m, effective_from)
cursor(source_path PK, byte_offset, updated_at)
schema_version(version, applied_at)
```

View: `v_now` (mỗi actor một dòng: trạng thái cảnh, từ bao giờ, nhắm ai, token hôm nay), `v_task_ledger` (token, chi phí, số turn, số câu hỏi, tổng chờ, spawn→merge), `v_story` (event kèm tên actor, subject, kind của cause).

`event.kind`: `mate.started/stopped`, `crew.spawned/finished/failed`, `mode.changed`, `turn.started/ended`, `tool.called/finished`, `git.committed`, `status.appended`, `message.sent`, `question.asked/answered`, `digest.sent`, `assign.clicked`, `incident.opened/resolved`, `review.started`, `merge.done`, `context.compacted`, `health.changed` (chỉ khi composer đổi trạng thái).
Payload JSON của từng kind, máy trạng thái cảnh và ví dụ được ghi ở `docs/timeline.md`, có version.

Lựa chọn có chủ ý: `transition` là lịch sử để replay và đo thời gian chờ; `usage_sample` giữ mẫu thô vì Codex chỉ có cộng dồn; mọi event có `ref_path/ref_offset` để truy ngược về byte nguồn; dashboard live chỉ cần `event.id > ?` mỗi giây.

| # | Task | Xong khi |
| --- | --- | --- |
| 25 | `internal/db`: mở/migrate `mate.db`, schema trên, `mate reindex <workspace>` dựng lại toàn bộ từ file và transcript (xoá và xây lại trong transaction). `internal/timeline` ingest: locator transcript cho Mate Claude và crew Codex, đọc từ `cursor`, parser v1, sinh `event`/`turn`/`action`/`message`/`usage_sample`/`question`/`incident`/`task`, nối `cause_event_id` theo luật ghi trong `docs/timeline.md`. Observer gọi ingest mỗi vòng poll. `mate events <project> [--follow] [--since]` in JSON lines từ `v_story`, `--narrate` in thành lời kể một dòng một event. | Unit: reindex hai lần cùng kết quả; fixture transcript Claude 2.1.278 và Codex 0.154 ra đúng turn/token/action; câu hỏi nối đúng câu trả lời. Live: chạy acceptance hai project rồi đối chiếu mọi tool call trong transcript và mọi dòng status đều thành event, không cửa sổ 10 giây nào agent bận mà timeline không giải thích. Đã xong 2026-09-20, evidence `docs/evidence/m5-timeline-2026-09-20.md`; hợp đồng dữ liệu ở `docs/timeline.md`. |
| 26 | Projection cảnh: `internal/timeline/scene` với máy trạng thái ở trên, ghi `transition`, view `v_now`; `mate events --scene` in snapshot cảnh rồi transition. Bài kiểm tra độ sâu: từ fixture timeline của acceptance, mọi chuyển cảnh trong máy trạng thái đều có event kích hoạt và không event nào rơi vào trạng thái không xác định; `--narrate` đọc trôi như một câu chuyện (golden). | Unit trên fixture; golden narrate; live trên acceptance hai project. Đã xong 2026-09-20, evidence `docs/evidence/m5-scene-2026-09-20.md`; bảng cạnh và từ vựng cảnh ở `docs/timeline.md` mục 9. |
| 27 | Kinh tế: `pricing.yaml` mẫu ở workspace nạp vào `pricing`, `v_task_ledger`, `context_tokens_after` và `context.compacted` cho Claude và Codex, `mate usage <project> [crew]` in ledger, cột TOKENS trên cây console (từ DB, chỉ đọc), `tokens:` trong health của `mate state`. Incident `budget` khi `project.yaml` có `budget` và task vượt; theo quyết định 2026-09-20 `budget` không làm crew `blocked`, chỉ vào inbox với chữ `over budget` (sửa mục 4b). | Unit; live: tổng của một crew Codex thật bằng `total_token_usage` cuối trong rollout, và một crew Claude bằng tổng usage theo message. Đã xong 2026-09-20, live `TestLiveUsageMatchesTheHarness` (56s): crew Codex ledger 57774 = rollout 57774, Mate Claude ledger 57683 = tổng usage theo message 57683. |

### M6. Dashboard admin

Chốt 2026-09-21: cảnh văn phòng chỉ là ý tưởng minh hoạ độ sâu dữ liệu; dashboard thật là admin phẳng, chỉ đọc, nhìn được từ trình duyệt.
Ba tầng, mỗi tầng một trang:

1. Workspace: danh sách project, mỗi project một thẻ: Mate (harness, đang chạy hay dừng, trạng thái cảnh, token hôm nay), số crew mở theo trạng thái, số mục đang chờ trong inbox, chế độ manual/auto.
2. Project: Mate ở trên (trạng thái, turn gần nhất, token và context %), rồi bảng task (mọi crew, mở và đã đóng, lọc theo trạng thái): id, task, trạng thái, tuổi, token tổng, chi phí, số câu hỏi, thời gian chờ, branch. Inbox đang chờ hiển thị bên cạnh, chỉ đọc.
3. Task: đầu trang là ledger của task (token bốn loại, chi phí, số turn, số tool call, thời gian spawn→đóng); dưới là timeline các turn theo thứ tự, mỗi turn mở ra được: trigger, thời lượng, token, danh sách tool call với target và thời lượng, dòng status crew ghi trong turn, câu hỏi và câu trả lời nối theo `cause`. Cuối trang là diff của branch nếu còn.

Kỹ thuật: `mate dashboard [<workspace>] [--addr 127.0.0.1:7777] [--open]`, HTTP local, hợp đồng API đầy đủ ở `docs/dashboard.md`, UI tĩnh nhúng vào binary (HTML + JS thuần, không build step, không CDN), JSON API đọc từ `mate.db` qua `db.OpenRead` và các view, `GET /api/events?since=<id>` long-poll hoặc SSE để trang tự cập nhật trong 2 giây, không cần bấm refresh.
Chỉ đọc: không có nút nào ghi vào workspace; hành động vẫn ở console TUI.
Mọi số trên trang truy ngược được: mỗi turn và action có link `ref` tới đường dẫn và offset transcript, hiện khi rê chuột.

| # | Task | Xong khi |
| --- | --- | --- |
| 28 | `internal/dashboard`: server, JSON API (`/api/workspace`, `/api/projects/<p>`, `/api/projects/<p>/tasks/<crew>`, `/api/projects/<p>/tasks/<crew>/turns/<id>`, `/api/projects/<p>/tasks/<crew>/diff`, `/api/events?since=&wait=`), đọc từ DB qua view, cache theo `event.id` cuối, từ chối bind không loopback trừ khi `--allow-remote`. `mate dashboard`. | Unit trên fixture db: mọi endpoint trả đúng số từ `v_task_ledger`/`v_now`/`v_story`; live: chạy acceptance rồi so API với `mate usage`. Đã xong 2026-09-21, live `TestLiveDashboardMatchesUsage` (104s): crew `rd1` API total 143896 == dòng `mate usage shop` `18.6k/124.2k/1.1k/143.9k`, `/api/events?since=0` 46 event == story 46. Hợp đồng API ở `docs/dashboard.md` (task 29 dựng UI theo tài liệu đó). |
| 29 | UI ba tầng nhúng, tự cập nhật qua `/api/events`, lọc trạng thái, mở rộng turn, link ref, sáng/tối theo hệ. | Screenshot ba tầng trên workspace thật (Playwright) trong `docs/evidence/`; không request nào ra ngoài localhost. Đã xong 2026-09-21: `internal/dashboard/ui/` (`index.html`, `app.css`, `app.js`, `humanize.js`), sáu screenshot sáng/tối ba tầng trên `~/work-mate` trong `docs/evidence/m6-dashboard-2026-09-21/`, 10/10 request về `127.0.0.1:7791`, bảng thu gọn thành thẻ dưới 720px (scrollWidth == clientWidth). Hợp đồng UI ở `docs/dashboard.md` mục 10; bằng chứng và các phát hiện về dữ liệu ở `docs/evidence/m6-dashboard-2026-09-21.md`. |

Nợ M6 (đo 2026-09-21, task 29): `mate reindex` đặt lại `event.id` từ đầu, nên con trỏ `since` của một trang đang mở trỏ vào tương lai và trang đứng yên mà vẫn báo `live`; UI hiện tự nhận ra khi `last_event_id` nhỏ hơn con trỏ và đồng bộ lại, nhưng đúng ra server nên phát một `generation` đổi sau mỗi reindex. `Task.waited_ms` là số 0 khi câu hỏi còn chờ trong khi `Question.waited_ms` là `null`; hai chỗ nên thống nhất `null`.

### Trả nợ sau M6

| # | Task | Xong khi |
| --- | --- | --- |
| 30 | `[assign]` khi Mate bận: không từ chối nữa mà xếp hàng. Console giữ một hàng đợi gửi vào Mate (ghi ở `mate/.outbox`, mỗi dòng một mục, để console khởi động lại vẫn gửi tiếp), một vòng gửi thử lại mỗi 2 giây bằng `send.Send` có kiểm chứng cho tới khi composer trống; trùng mục thì bỏ; dòng inbox hiện `assigned · queued` rồi `assigned · sent HH:MM`; quá 5 phút chưa gửi được thì mở incident `wedged` trên `mate` như daemon. Không bao giờ dùng hàng đợi của harness (`herdr agent prompt`). Daemon auto dùng chung hàng đợi này thay vì vòng thử lại riêng. | Unit với pane giả: bận rồi trống thì gửi đúng một lần, khởi động lại vẫn gửi, trùng bị bỏ, quá hạn thì `wedged`. Live: Mate Claude đang trong vòng `sleep 20; mate state`, bấm `[assign]` một lần, dòng `resolve:` tới hook của Mate trong vòng một chu kỳ. Đã xong 2026-09-24: hàng đợi `mate/.outbox` (JSON một dòng một mục, viết lại nguyên tử dưới khoá `.outbox.lock`) và người gửi duy nhất `internal/outbox`; daemon chỉ xếp digest vào đó, con trỏ chỉ tiến khi outbox đánh dấu `sent`; chữ trên hàng inbox là `needs an answer · assigned, queued` rồi `· assigned HH:MM`. Live `TestLiveAssignQueuesWhileTheMateIsBusy` (100s): Mate bận trong một lệnh chờ 45s, `[assign]` trả `queued`, 22 lần thử bị từ chối, dòng `resolve:` vào 3s sau khi turn kết thúc (chờ tổng 43s), đúng hai bản trong `sent.log`; `TestLiveAutoDigestReachesTheMate` vẫn xanh qua outbox (58s). |
| 31 | Mate ở chế độ auto phải dừng turn sau khi spawn để digest đánh thức nó. Manual đã dặn mà Mate không nghe (đo 2026-09-19, task 24), nên đưa lời nhắc vào chỗ Mate chắc chắn đọc: output của `mate crew spawn`, `mate state`, `mate send` khi project có `.auto` in thêm một dòng cuối "auto mode: end your turn now; the console will wake you with a digest when <crew> speaks". Rà lại mục 7 và 9 của manual cho một câu duy nhất, không mâu thuẫn. | Live: nửa `blog` của `TestLiveAcceptanceTwoProjects` có ít nhất một dòng `app → mate` `digest:` trong `sent.log` và Mate xử lý nó ở một turn riêng; chạy hai lần liên tiếp đều pass. Đã xong 2026-09-24, evidence `docs/evidence/m6-debt-auto-turn-2026-09-24.md`: ba dòng `auto mode:` trong output, luật một chỗ ở mục 10 manual, test hai project kiểm lượt spawn và lượt merge của Mate `blog` là hai lượt khác nhau và lượt merge do digest mở, sửa `claudeBusy` (mục 7). Hai lần chạy liên tiếp `TestLiveAcceptanceTwoProjects` trên cây cuối đều pass (254.52s, 284.46s); nửa `shop` giờ đi theo `decision-authority`: Mate hỏi lại captain lựa chọn trang checkout được `[assign]`, captain trả lời trong pane Mate, Mate chuyển cho crew. |

### M7. Prompting giao việc theo firstmate

Chốt 2026-09-24: prompting giao việc là business logic chính; refactor theo `docs/research/firstmate-prompting-2026-09-24.md` (firstmate upstream `795e4b58`).
Giữ quyết định 1: Mate không đọc code của repo. Vì vậy mọi thứ firstmate lấy từ việc orchestrator đọc repo, mate lấy từ `PROJECT.md`, report scout, và `mate project facts` (chỉ metadata git: có commit không, số file, thư mục cấp một, file build/test nhận diện được theo tên; không đọc nội dung file nào).

Schema brief (mục 11 của báo cáo). Mate điền `# Task` với đúng các section:
`## Captain's words` (nguyên văn, không nhãn người nói, kèm nội dung report được nhắc tới), `## What we already know` (mỗi dòng có nguồn, dòng cuối nói điều không biết), `## Build` (chỉ cái được yêu cầu, có `Out of scope:`), `## Acceptance` (mỗi tiêu chí có `verify:`), `## Open decisions` (`none` hoặc mỗi mục có `decides: captain|mate`), `## Deliverable` (chỉ scout).
Template cố định thêm: khối vai trò worker ở đầu, luật dừng ở open decision, `# Before you hand back` viết `crews/<id>/handback.md` cho ship, cấu trúc report scout tự đứng được, `# Project memory`, và quy tắc captain viết cho crew ở `projects/<p>/CREW.md` (hoặc `.mate/CREW.md` cho mọi project) nối cuối brief.

| # | Task | Xong khi |
| --- | --- | --- |
| 32 | Template và CLI: `assets/crew/brief.md.tmpl` theo schema; `mate brief check <file> [--scout]` kiểm hình dạng (section bắt buộc, không rỗng, không placeholder, `Captain's words` không mở đầu bằng nhãn, mỗi dòng acceptance có `verify:`, open decisions là `none` hoặc mỗi mục có `decides:`, `Build` có `Out of scope:`), không phán nghĩa; `crew spawn` chạy check và từ chối brief sai; `mate brief append <project> <crew>` nối lời captain đến sau vào `Captain's words` của `crews/<id>/brief.md` và gửi crew một dòng trỏ tới nó; `CREW.md` nối cuối; `mate project facts <project>`. | Unit cho từng luật check; golden template; brief `buyesp32` cũ bị từ chối với lý do rõ, bản viết lại trong báo cáo được nhận. Đã xong 2026-09-24: `internal/brief` (tên section export làm nguồn sự thật duy nhất, `Check`, `AppendCaptainsWords`), `crew spawn --scout` (cờ tường minh, không suy từ `## Deliverable`, vì lỗi cần bắt chính là scout quên section đó), template có khối vai trò, `# How to read the task`, `# Before you hand back` ghi `crews/<id>/handback.md`, report scout có `## Durable facts`, `# Project memory`, `# Captain's standing crew rules`; brief `buyesp32` cũ bị từ chối với 6 dòng, bản viết lại ở mục 11 của báo cáo được nhận; `project facts` có test ghi lại mọi lệnh git để chứng minh không đọc nội dung file. Chưa chạy live (task 34). |
| 33 | Manual Mate và skill: mục 3 (bootstrap: `PROJECT.md` trống thì đề xuất scout onboarding), 5 (intake: tra report có sẵn trước khi giao; bằng chứng không phải uỷ quyền; khi nào hỏi captain trước), 6 (viết brief theo schema, đảo luật "không dán lời captain", luật phạm vi), 9 (review đối chiếu `handback.md` với `Captain's words` và `Acceptance`, rồi mới đọc diff; sửa trong phạm vi Mate tự quyết), 13 (escalation: bằng chứng → hậu quả → lựa chọn → khuyến nghị), 14 (scout ghi phát hiện bền vững vào `PROJECT.md` qua Mate). Skill mới `decision-authority` (nạp khi xử lý `resolve:`/`digest:`/review) và `diagnostic-reasoning` (task bug). | Golden manual; test ngân sách; đọc lại toàn manual không còn câu mâu thuẫn với schema. Đã xong 2026-09-24: sửa mục 1 (nguồn hiểu biết thêm hand-back và `project facts`), 2 (CREW.md, bốn skill), 3, 4 (`brief check`, `brief append`, `project facts`, `--scout`), 5, 6, 9 (chỉ đoạn "On `wait-mate`"), 13, 14; manual 58 KB, dưới trần 120 KiB; test `manual_schema_test.go` giữ tên section mục 6 bằng `brief.AllSections()`, ví dụ mục 6 phải qua `brief.Check`, ví dụ `project facts` mục 4 phải bằng output thật. Mục 7 (task 31) vẫn in lệnh spawn không có `--scout`; `crew spawn` từ chối scout thiếu cờ với câu nói rõ cách sửa. |
| 34 | Acceptance: chạy lại `TestLiveAcceptanceTwoProjects` với prompting mới, thêm một kịch bản repo trống kiểu `shop` (Mate phải đề xuất scout onboarding hoặc đặt câu hỏi vào `Open decisions` với `decides: captain`, không tự dựng trang), và so sánh bằng timeline: số câu hỏi/task, số dòng sửa Mate gửi sau `wait-mate`, token/task, trước và sau M7. | Evidence `docs/evidence/m7-prompting-<ngày>.md`, hai lần pass liên tiếp. Đã xong 2026-09-24, evidence `docs/evidence/m7-prompting-2026-09-24.md`: `TestLiveM7EmptyRepoDoesNotGuess` pass bốn lần liên tiếp, lần nào Mate cũng trả lời captain trong 13-18 giây bằng bằng chứng (`project facts`: cây trống), hậu quả, lựa chọn và khuyến nghị, không spawn crew nào, không dựng trang; trước M7 (`buyesp32` thật, 2026-09-19) Mate dispatch một ship dừng lại với 158.2k token crew và câu hỏi không ai trả lời. `TestLiveAcceptanceTwoProjects` pass ba lần liên tiếp sau M7, lần cuối trên cây đã commit; "trước" là một lần chạy live ở `b59f3fe` vì workspace của các lần chạy cũ đã bị `t.TempDir()` xoá. Mọi ship sau M7 viết `handback.md` đủ dòng; số câu hỏi (0/1/0) và rework (0) không đổi; token crew ship +28-38%, token Mate +27-43%, spawn→`wait-mate` chậm hơn. Đo bằng `scripts/m7measure --reindex` trên workspace giữ lại qua `MATE_LIVE_KEEP`; không cần sửa prompting. |

Đợt 2 (sau M7): thăng scout thành ship tại chỗ (`crew promote`), relaunch giữ worktree (`crew relaunch`), chăm `memory.md` có ngày và nguồn, `--effort` khi spawn.

### M8. Trí nhớ của Mate theo firstmate

Chốt 2026-09-24 theo `docs/research/firstmate-memory-2026-09-24.md` (firstmate upstream `9284978f`).
Hiện trạng đo được: sau ba ngày và sáu task, `memory.md` 9 byte, `PROJECT.md` trống; Mate đọc 10 lần, ghi 0 lần; một bài học mất trong cùng session, một sự thật repo tự mâu thuẫn, câu hỏi gửi captain chỉ nằm trong hội thoại; auto-memory của Claude Code bật cho mọi thư mục Mate.

Nguyên tắc: hội thoại là bộ đệm, khởi động mới không được mất gì đã ghi và mọi việc dở phải có bản ghi; mỗi loại tri thức một chủ; script lo hình dạng, ngân sách, thứ tự đọc, Mate lo chọn đích, gộp, viết lại; file phẳng là nguồn, `mate.db` chỉ gợi ý, trí nhớ harness tắt.

File và chủ (mục 12.2 của báo cáo): `WORKSPACE.md` và hai `CREW.md` của captain, Mate chỉ đề xuất; `PROJECT.md` của Mate và captain, sự thật repo có nguồn và mốc `main@<sha>`; `mate/memory.md` của Mate với `## Captain` (không hết hạn) và `## Lessons` (marker `<!--a:YYYY-MM-DD-->` 30 ngày, `<!--p:YYYY-MM-DD-->` 7 ngày), một dòng một mục, có nguồn tương đối workspace; `mate/memory-archive.md` không đọc lúc khởi động; `mate/backlog.md` thêm `## Held for the captain`, giữ 10 Done; `mate.meta` ghi cả `session_id` của Codex; auto-memory của harness tắt.

| # | Task | Xong khi |
| --- | --- | --- |
| 35 ∥ | Đo năm câu hỏi mở của mục 12.9 trên harness đang cài (khoá tắt auto-memory Claude; `SessionStart` với `source=compact` trong pane Herdr và stdout có vào context không; Codex 0.154 TUI có bắn `SessionStart` không; `codex resume <id>` trong pane Herdr và dialog của nó; Claude `--resume` có nạp lại manual vừa sinh không), ghi kết quả vào mục 7. Làm luôn: tắt auto-memory cho Mate Claude (B6); resume cho Mate Codex (B11) nếu đo được là chạy. | Live cho từng điều đo; live `TestLiveSpawnMateResumeRemembers` cho Codex nếu B11 làm được; Mate Claude được bảo "remember X" ghi vào `mate/memory.md` và thư mục memory của Claude vẫn rỗng. Đã xong 2026-09-24, kết quả đo ở mục 7 (Claude Code 2.1.281, codex-cli 0.154.0 và 0.156.1, Herdr 0.8.2): A1 `autoMemoryEnabled: false`, đặt cho Mate trong `mate/.claude/settings.json` (thêm vào file cũ nếu thiếu) và cho crew Claude trong `crews/<id>/settings.json`; A2 Claude bắn `startup`/`clear`/`compact`/`resume` và stdout vào context; A3 Codex TUI cũng bắn cả bốn nhưng ở prompt đầu sau mỗi sự kiện, output bị cắt quanh 2,5K token, hook mới phải qua dialog `Hooks need review`; A4 `codex resume <flags> <id>` chạy, không dialog mới trong thư mục đã trust; A5 `--resume` đính lại manual đã đổi. B11: `StopMate` ghi `session_id` Codex từ `agent_session` của Herdr (dự phòng nhận rollout), start kiểm rollout rồi `codex resume`, hỏng thì một lần bản mới có ghi chú. Kèm theo: profile trust dialog của codex-cli 0.156.1. Live: `TestLiveMateAutoMemoryOff` (26s), `TestLiveMateSessionStartHookSources` (55s), `TestLiveMateResumeReloadsManual` (29s), `TestLiveCodexSessionStartHook` (71s), `TestLiveSpawnMateResumeRemembersCodex` (54s). |
| 36 ∥ | `mate remember <project> --captain\|--lesson [--perishable "<expiry>"] --source <src> "<một dòng>"`, `mate memory check <project>` (hình dạng, nguồn, marker, mục cũ, đường dẫn tuyệt đối, ngân sách 4.000 token ước lượng cho `memory.md` + `PROJECT.md` + `WORKSPACE.md`); `project facts` in `head: <sha>`; skill `stow` (quét, định tuyến, inspect-then-update, lưu việc dở, curate, receipt); manual mục 2, 4, 13, 14 theo A1–A6, B3, B8, B12; nhắc ghi trong output của `crew stop` và `send` sau spawn (B9). | Unit cho mọi luật `memory check`; golden manual và skill; test ngân sách. Đã xong 2026-09-24: `internal/memory` là nguồn sự thật duy nhất của hình dạng (mục, marker, đồng hồ 30/7 ngày, ngân sách 4.000, `ceil(bytes/3)`); một mục là `- <nội dung> [(expires: <điều kiện>)] (<nguồn>) [<!--a:YYYY-MM-DD-->\|<!--p:YYYY-MM-DD-->]`, `## Captain` không marker; nguồn là đường dẫn tương đối thư mục project (`crews/k1/report.md §Durable facts`), đường dẫn tuyệt đối hoặc ra ngoài workspace bị từ chối (exit 2, gợi ý bản tương đối nếu file nằm trong workspace); `remember` chỉ thêm vào cuối mục, tạo mục nếu thiếu, không bao giờ gộp hay xoá. `memory check` in dòng `budget:` rồi `memory ok:`, hoặc mỗi vấn đề một dòng `memory.md:<n>:`/`PROJECT.md:<n>:` và exit 1; dòng repo-state là mọi `- ` dưới `## Layout and state` và `## How to work here` của `PROJECT.md`, phải có `<default>@<sha\|none>`; mốc cũ hơn `head` chỉ là `warning:`. `project facts` in `head: <sha ngắn>` hoặc `head: none` ngay sau dòng `commits:`. `project add` tạo `PROJECT.md` với năm mục của mục 12.3 báo cáo; Mate mới nhận `memory.md` có con trỏ `<!-- memory tiers: see the stow skill -->` và hai mục, `backlog.md` có bốn mục (`Held for the captain` thêm vào). Skill `stow` và dòng app gửi trước restart, nguyên văn cho task 37 (hằng `memory.StowLine`, sau sentinel): `⟦mate⟧ stow: you are about to be restarted; record anything that exists only in this conversation (skill stow), then end your turn`. Manual: mục 2 bảng file/chủ và bảng định tuyến thay bảng lớp, trí nhớ harness không phải nguồn, skill thứ năm; mục 4 `### Memory` thay "Not yet available", dòng `head:`, hai dòng nhắc; mục 13 `Held for the captain`; mục 14 hình dạng mục, khi nào ghi, vệ sinh ghi chú, mốc commit, đường vào brief, 10 Done; mục 3 không đổi (task 37). Nhắc B9 chỉ in cho Mate: `crew stop` theo `MATE_CALLER=mate` (và không in khi crew đã đóng từ trước), `send` theo cùng nguồn `mate` mà dòng `auto mode:` của task 31 dùng, và chỉ khi `crews/<id>/brief.md` có; dòng `auto mode:` vẫn là dòng cuối. Manual 71 KB, dưới trần 120 KiB. Chưa chạy live (task 38). |
| 37 | Sau 35 và 36: `mate recall <project>` theo thứ tự mục 12.5 (trạng thái sống, facts, `PROJECT.md` kèm cảnh báo mốc cũ, backlog không Done kèm id lệch, memory, workspace, ngân sách, gợi ý timeline tuỳ chọn), `ABSENT` khác rỗng; manual mục 3 rút thành "run `mate recall`"; hook `SessionStart` cho Mate Claude (`startup`/`clear`/`compact` in toàn bộ, `resume` chỉ trạng thái sống) theo kết quả đo của 35; restart Mate từ console gửi `⟦mate⟧ stow:` qua outbox, chờ turn kết thúc có trần thời gian, rồi mới restart (B7). | Unit `recall` trên fixture; live hook với `/compact`; live restart có stow. Đã xong 2026-09-24: `mate recall <project> [--live] [--max-bytes N]` (`cmd/mate/recall.go`) in dòng tiêu đề `# mate recall <p> · <giờ>`, một dòng hợp đồng, rồi tám phần `== N. … ==` đúng thứ tự mục 12.5, mỗi file đóng khung `----- begin <tên> (<đường dẫn>) -----`/`----- end <tên> -----`, `ABSENT` khác `(empty)`, file chỉ có heading thì thêm `note:`; phần 4 bỏ `## Done` (đếm số mục) và so id In flight với bảng theo cấu trúc `- <id>` cả hai chiều; phần 8 chỉ khi có `mate.db`; `--max-bytes` giữ phần 1 nguyên vẹn, cắt nguyên dòng từ dưới lên, không bao giờ để heading trơ, và dòng cuối nói phần nào mất và chạy lệnh gì. Hook `mate hook mate-session [--harness codex]`: `resume` → chỉ phần 1, mọi source khác → toàn bộ, ghi `session_id` và `transcript` của payload vào `mate.meta` (đóng khe `/clear` của task 35; Mate Codex có id ngay prompt đầu), lỗi vẫn in một dòng bảo Mate tự chạy `recall`. Claude: `ClaudeSettings` có `SessionStart`, `EnsureSessionHook` thêm vào settings cũ bên cạnh mọi hook có sẵn. Codex: chọn (a), `mate/.codex/hooks.json` với `additionalContextLimit` 32000, settle đi qua review của Codex và chỉ trust hook của chính Mate (mục 7). Manual mục 3 rút thành digest `recall` (giữ luật scout onboarding, thêm luật sau compaction và dòng `stow:`), mục 4 thêm `### Recall`, mục 2/13/14 thêm tham chiếu. Restart từ console (và `mate mate stop` của captain với composer trống): `outbox.Stow` xếp `memory.StowLine` qua outbox, chờ turn kết thúc (dòng Stop trong `sent.log` với Claude, composer bận rồi trống hai lần với cả hai), trần 3 phút, dòng chưa gõ được thì rút khỏi outbox; composer có chữ của captain thì lần bấm đầu hỏi, lần bấm thứ hai trong 2 phút mới restart; dòng kết quả mở đầu `stowed` hoặc `not stowed: <lý do>`. Live (Claude Code 2.1.281, codex-cli 0.156.1, Herdr 0.8.2, lab `fm-lab-mate-w37-final`, `go test -p 1`): `TestLiveMateRecallOnCompact` (37s), `TestLiveCodexMateRecallHook` (31s), `TestLiveRestartMateStowsFirst` (45s, stow 29s, sự thật vào `PROJECT.md`). |
| 38 | Acceptance trí nhớ: Mate nhận một lời sửa của captain cho crew thứ nhất, restart từ console, rồi với crew thứ hai tự đưa bài học vào brief (hoặc đề xuất cho `CREW.md`) mà không cần captain nhắc; một câu hỏi gửi captain trước restart vẫn còn trong `Held for the captain` sau restart; đo bằng timeline số lời sửa lặp lại giữa các crew trước và sau. Chạy cho Mate Claude và Mate Codex. | Evidence `docs/evidence/m8-memory-<ngày>.md`, hai lần pass liên tiếp mỗi harness. Đã xong 2026-09-24, evidence `docs/evidence/m8-memory-2026-09-24.md`, với ngưỡng một lần pass đầy đủ mỗi harness theo quyết định của captain giữa task (không phải hai lần liên tiếp): `TestLiveMemorySurvivesRestart` bốn subtest, `claude` 337s và `claude-fresh` 371s pass ở lần chạy thứ hai đủ bộ, `codex` 469s và `codex-fresh` 451s pass ở lần chạy Codex sau đó; mọi Mate ghi lời sửa vào `## Lessons` và đề xuất dòng cho `CREW.md`, giữ câu hỏi nguyên văn có ngày trong `## Held for the captain`, sau restart (resume hoặc `--fresh`) tự nêu câu hỏi đang chờ và đưa luật vào `## Build` của scout thứ hai, số lời sửa lặp lại sau spawn là 0. Trước khi pass: ba thay đổi prompting (luật cho crew ở lại `## Lessons`, không sang `## Captain`; câu hỏi giữ là bản chép nguyên văn; scout không bao giờ overlap) và ba sửa sản phẩm (env của Mate `export` ở mỗi lần start vì `tab create` không thừa kế `--env`; restart giữ harness; stow Codex kết thúc theo `task_complete` của rollout, không theo composer). Pass của Claude có trước câu về scout overlap. Hash `~/.codex/config.toml` của người dùng không đổi qua mọi lần chạy của task, trừ lần captain tự dọn file. |

### M9. Project nhiều repo

Chốt 2026-09-25.
Một project không phải lúc nào cũng là một repo: một sản phẩm có thể gồm nhiều repo riêng (không phải monorepo), và một project mới tạo từ console có thể chưa có repo nào.
Mô hình cũ ép `project add <name> <repo>`, nên console không tạo được project khi người dùng chưa đưa repo, và project nhiều repo không biểu diễn được.

Quyết định:

- Project có danh sách `repos`, từ không tới nhiều phần tử; mỗi repo có `name`, `path` (tương đối workspace) và `default_branch` riêng.
- Tên repo theo luật tên project và duy nhất trong project. Một đường dẫn repo thuộc tối đa một project trong workspace, vì branch crew là `mate/<crew>` và id crew chỉ duy nhất trong một project.
- Mỗi Crew làm việc trên đúng một repo. Việc chạm nhiều repo thì Mate chia thành nhiều Crew; merge không bao giờ phải nguyên tử qua nhiều repo.
- `crew spawn --repo <name>`: bắt buộc khi project có từ hai repo, mặc định là repo duy nhất khi có một, bị từ chối khi project chưa có repo (thông điệp chỉ `mate project repo add`). `.meta` ghi `repo=<name>`; `crew stop`, merge, diff, dashboard, timeline và Codex trust đọc repo từ `.meta` của crew, không từ project.
- Worktree vẫn là git worktree ở `.worktrees/<project>-<crew>/`; không dùng treehouse. Việc cấp và trả worktree đi qua một interface nhỏ trong `internal/spawn` để sau này có thể thay backend.
- `project facts` và phần 2 của `recall` in một khối cho mỗi repo, hoặc `repos: none`. Mốc trong `PROJECT.md` là `<repo>:<branch>@<sha>`; mốc cũ `<branch>@<sha>` vẫn hợp lệ khi project có đúng một repo. Project không có repo thì mọi dòng repo-state là vấn đề của `memory check`.
- Chuyển đổi một chiều: `project.yaml` cũ (`repo:` và `default_branch:` ở gốc) được đọc thành một repo tên theo tên thư mục và ghi lại theo hình mới ở lần lưu kế tiếp; `projects[].repo` trong `workspace.yaml` bị bỏ khi lưu. `.meta` cũ không có `repo=` thuộc về repo duy nhất của project, và bị từ chối với thông điệp rõ khi project đã có nhiều repo.
- Gỡ repo khỏi project không bao giờ đụng vào thư mục repo, và bị từ chối khi còn crew chưa đóng (`finished` hoặc `failed`) trên repo đó.

| # | Task | Xong khi |
| --- | --- | --- |
| 39 | `internal/store`: `ProjectConfig.Repos`, chuyển đổi `project.yaml` và `workspace.yaml` cũ, luật tên và đường dẫn duy nhất. CLI: `mate project add <name> [<repo-path>] [--repo-name <n>] [--default-branch <b>]` (repo tuỳ chọn), `mate project repo add <project> <repo-path> [--name <n>] [--default-branch <b>]`, `mate project repo list <project>`, `mate project repo remove <project> <name>`; `project list` in tên các repo. Mọi nơi đang đọc `cfg.Repo` đọc repo qua một hàm của store để task 40-42 thay từng chỗ. | Unit cho chuyển đổi (file cũ đọc được, lưu ra hình mới), đường dẫn trùng giữa hai project bị từ chối, gỡ repo còn crew bị từ chối; `make check` xanh. Đã xong 2026-09-25: `store.RepoConfig` (`name`, `path`, `default_branch`), tên trống thì suy từ thư mục bằng `DefaultRepoName`; `SoleRepo()` là cầu tạm cho mọi chỗ chưa có crew để hỏi (spawn, start Mate, facts, recall, memory check), từ chối project không repo hoặc nhiều repo với thông điệp chỉ lệnh sửa; `CrewRepo(meta)` dùng ngay cho `crew stop`, merge, diff, timeline gitlog vì các chỗ đó có `.meta`; spawn đã ghi `repo=`; dashboard tìm branch crew trong mọi repo của project. Start Mate cho project không đúng một repo bị từ chối tới task 41. Thử trên workspace thật tạo bằng bản cũ: `project.yaml` cũ đọc được và ghi lại theo hình mới. |
| 40 | Sau 39: `crew spawn --repo`, `.meta` `repo=`, worktree qua interface cấp/trả; `crew stop`, `mate merge`, `mate diff`, dashboard, `timeline` gitlog, Codex trust theo repo của crew; `.meta` cũ theo luật chuyển đổi. | Unit trên project hai repo: hai crew ở hai repo, mỗi crew stop/merge/diff đúng repo của nó; project không repo từ chối spawn với thông điệp chỉ lệnh sửa. Đã xong 2026-09-25: `internal/spawn/repo.go` chọn repo (tên `--repo`, không thì repo duy nhất), từ chối exit 2 trước khi tạo gì; dòng spawned có `repo <name>`; interface `Worktrees` (`Acquire`, `Release`) trên `spawn.Deps`, mặc định `GitWorktrees` giữ precondition và tangle guard; `crew stop` trước đây viết lại `.meta` làm rơi `repo=`, nay giữ; dashboard hỏi đúng repo của crew. Codex trust không cần sửa (`CheckCodexProjectTrust` chỉ test gọi). Chưa chạy live. |
| 41 ∥ | Sau 39: `project facts`, `recall` phần 2, `memory check` với mốc `<repo>:<branch>@<sha>`; manual Mate (mục 1, 4, 6, 7, 14) liệt kê repo và dạy `--repo`, nói rõ một crew một repo; mẫu `PROJECT.md`. | Golden manual; unit facts/recall/memory cho không, một và hai repo; test ngân sách manual. Đã xong 2026-09-25: `project facts` in một khối mỗi repo (`<project>: repo <name> at <path>, default branch <b>`, `head: <sha> (anchor <repo>:<b>@<sha>)`), hoặc `repos: none` kèm gợi ý, exit 0; mốc `<repo>:<branch>@<sha\|none>` với branch phải là default branch của repo đó, mốc trần hợp lệ khi đúng một repo, bị báo khi nhiều repo (gợi ý dạng có repo), không repo thì mọi dòng repo-state là vấn đề; cảnh báo mốc cũ so với head của đúng repo. Start Mate chạy cho project không repo và nhiều repo; manual liệt kê repo (bảng), mục 5 thêm quyết định "một crew một repo", mục 7 dạy `--repo`; manual 75.157 → 79.437 byte. Chưa chạy live. |
| 42 ∥ | Sau 39: query và console: tên project là tên project (không còn `filepath.Base(repo)`), inspector liệt kê repo, form New project có ô Repo tuỳ chọn (trống = project chưa có repo). | Golden console; unit form gửi request không repo; `mate .` hiện project không repo và project hai repo. Đã xong 2026-09-25: query trả đường dẫn repo tương đối workspace, repo của crew theo `.meta` (Unknown có lý do khi không nói được); inspector project liệt kê `name · branch` rồi đường dẫn, project không repo hiện "none yet · a crew needs one" kèm lệnh; inspector crew hiện repo; form New project có "Repo (optional)", trống thì tạo project không repo. Thử bằng binary thật trong tmux. |
| 43 | Sau 40-42: acceptance live: tạo project không repo từ console, `project repo add` hai repo, Mate chia một yêu cầu chạm cả hai repo thành hai crew ship, mỗi crew merge vào đúng repo. | Evidence `docs/evidence/m9-multi-repo-<ngày>.md`, hai lần pass liên tiếp. |

### M10. Host stage: cột host hiện terminal agent

Chốt 2026-09-25.
Captain mở mate trong một split của host terminal (Ghostty, WezTerm, iTerm).
Cột còn lại là sân khấu: click hoặc Enter một hàng Mate/Crew thì host tự hiện `herdr agent attach` của agent đó.
Host sở hữu layout; Herdr sở hữu process agent; mate console chỉ điều phối.

Quyết định:

- Package mới `internal/host`, không nhét vào `internal/runtime` (adapter Herdr).
  Herdr vẫn tạo session/workspace/tab và start agent.
  Host chỉ đặt client attach vào pane của captain.
- Port một việc: `Stage(ctx, StageTarget) (StageHandle, error)`.
  `StageTarget` mang Herdr session name, agent name, kind (mate/crew), id domain.
  Driver được chọn lúc mở console từ môi trường, không từ config.
- Nhận diện, theo thứ tự, dừng ở cái đầu khớp:
  1. `WEZTERM_PANE` khác rỗng → WezTerm
  2. `TERM_PROGRAM=ghostty` → Ghostty (macOS AppleScript)
  3. `TERM_PROGRAM=iTerm.app` → iTerm, để sau
  4. không khớp → `None`: console giữ stream mode nhúng PTY như hiện tại (M11 bỏ stream mode: `None` chỉ nói "no next pane")
- WezTerm: `wezterm cli get-pane-direction --pane-id $WEZTERM_PANE Right`.
  Có pane stage do lần `Stage` trước của **cùng process console** thì `kill-pane` rồi `split-pane --right --percent <giữ>` với argv `herdr --session <s> agent attach <agent>`.
  Chưa có thì `split-pane` thôi.
  Cấm `send-text` vào pane đang attach.
- Ghostty 1.3+: `osascript` `split` pane đang focus `direction right` với `surface configuration.command` là cùng argv attach, `wait after command` bật.
  Lần sau `close` terminal id đã lưu rồi `split` lại.
  Cấm `input text` vào pane đang attach.
  Không có `GHOSTTY_SURFACE_ID`; handle stage sống trong process console, mất khi tắt mate.
- Chỉ giết pane mà chính `Stage` vừa tạo (handle trong bộ nhớ).
  Pane phải có sẵn (nvim, shell của captain) thì từ chối với một dòng, không `kill-pane`.
  Captain tự split trống rồi bấm lần nữa, hoặc để mate tạo split mới khi bên phải chưa có gì.
- Console không import `internal/host` hay `internal/runtime`.
  `cmd/mate` bơm một `StageFunc` seam, cùng kiểu `SessionStreamFactory`.
  Khi host không phải `None`, Enter/click hàng Mate hoặc Crew gọi `Stage` và **ở lại** khung cây/inbox; không mở session view, không PTY trong console.
  Khi `None`, hành vi task 09 giữ nguyên (thay bởi M11).
- Chuột: hàng Mate/Crew trên cây bấm trái cùng nghĩa Enter.
  Inbox vẫn mở đúng crew, qua `Stage` nếu host có.
- `mate console [<workspace>]` mở console và gọi `EnsureSplit`: tạo pane trống bên phải (shell mặc định), giữ focus ở pane mate, ghi handle để `Stage` thay bằng `herdr agent attach`. Pane phải thì in một dòng lên stderr rồi vẫn mở console. `mate` / `mate <dir>` không tự split.

| # | Task | Xong khi |
| --- | --- | --- |
| 44 | `internal/host`: `Detect` từ env, port `Stage`, fake CLI/osascript, driver WezTerm và Ghostty theo quyết định trên; `cmd/mate` chọn driver lúc mở console, bơm `StageFunc`. Console: khi `StageFunc` khác nil thì Enter và click hàng Mate/Crew (cây và inbox) gọi stage, không `beginSession`; host `None` hoặc `StageFunc` nil thì golden session stream không đổi. | Unit: fixture env WezTerm/`ghostty`/trống; fake WezTerm ghi `get-pane-direction` + `split-pane --right` với argv `herdr … agent attach`, lần hai `kill-pane` đúng id đã trả, không có `send-text`; fake Ghostty ghi `split` rồi lần hai `close` + `split`, không có `input text`; pane phải không phải handle của mình thì `Stage` từ chối và không kill. Golden: Enter trên Mate với `StageFunc` không vẽ session frame, có một lời gọi stage; không `StageFunc` thì fixture `session-mate-120x36` còn khớp. `make check` xanh. Đã xong 2026-09-25. |

### M11. Console hẹp: controller 20% bên cạnh host

Chốt 2026-09-25.
Console bỏ hẳn session view nhúng (PTY stream, composer, `mate attach` handover): mate chỉ còn là controller, terminal của agent do host hiện qua `Stage` (M10).
Giao diện theo bundle "20% terminal butler", chép thành chữ ở `docs/console-design.md`.

Quyết định:

- Host None (không phải WezTerm/Ghostty): Enter nói "no next pane" trên status line, không mở gì trong console.
- Chức năng backend chưa có thì không vẽ theo board (tool call cuối, Answered today, New crew, tokens in/out); frame hiện đúng thứ snapshot có.
- Harness vẽ bằng icon thay chữ; Nerd Font chỉ khi `cmd/mate` hỏi font report của host thấy glyph, không thì ✻ ⌬.
- Kind mark đo bằng CSI 6n một lần trước khi TUI chạy, lệch thì lùi về ◆ ◇; `MATE_KINDS` ghi đè (phím gõ trong ≤150ms probe có thể mất).
- WezTerm: stage split theo cell để mate giữ `clamp(cols/5, 40, 48)` cột; CLI lấy từ `WEZTERM_EXECUTABLE_DIR`, rồi PATH, rồi app bundle (bản .app không có `wezterm` trong PATH).
- Attach của stage luôn `--takeover`: stage là nơi captain muốn xem agent, nên lấy terminal từ client nào còn giữ nó.
- Ghostty `close` bỏ surface nhưng để tiến trình con chạy tiếp, vẫn attach (đo 2026-09-25, Ghostty 1.3.1). Trước khi thay stage, `pkill -f -x` đúng dòng lệnh attach của stage cũ (chỉ stage mới có `-` đầu do `login`/`exec -l`), rồi mới `close`, rồi `split`.
- Quy tắc tên project nằm ở `internal/names`, store và form New project dùng chung.

| # | Task | Xong khi |
| --- | --- | --- |
| 45 | Xoá session view nhúng; Enter/click/box chỉ gọi `StageFunc`; không host thì nói rõ. | Golden session/attach bỏ; unit stage (Mate, Crew, stale bị từ chối trước khi hỏi host, lỗi hiện trên status line, `r` retry). `make check` xanh. Đã xong 2026-09-25. |
| 46 | Renderer hẹp: plan một chồng list · detail · box, sheet actions/confirm/new project/harness/diff/keys, status line "→ next pane", key line; phím và chuột theo board I. | Golden `design-*` cho A–K và H1–H4 ở kích thước của board; hợp đồng khung ở mọi kích thước 16–64 × 6–56 và mọi trạng thái; cột hàng đúng board I. `make check` xanh. Đã xong 2026-09-25. |
| 47 | `cmd/mate`: probe kind (CSI 6n), probe icon (font report của host), notice của split lỗi lên status line, WezTerm split theo cell. | Unit probe với fake host; chạy binary thật trong WezTerm.app: split 40/39 ở cửa sổ 80 cột, new project tạo và chọn hàng mới. Đã xong 2026-09-25. |
| 48 | Acceptance live: Enter trên Mate và Crew đang chạy hiện đúng agent ở pane phải trong WezTerm và Ghostty, lần hai thay pane cũ. | Evidence `docs/evidence/m11-console-stage-<ngày>.md`. |

### M12. Crew dispatch: harness, model, effort

Chốt 2026-09-25, học theo firstmate (`config/crew-dispatch.json`, `fm-spawn.sh --harness --model --effort`).

Quyết định:

- `mate crew spawn` nhận `--model <name>` và `--effort low|medium|high|xhigh|max`, cạnh `--harness`. Trống là mặc định của harness, không truyền flag.
- Claude: `--model`, `--effort` (claude 2.1.282). Codex: `-m`, `-c model_reasoning_effort="<e>"` (codex-cli 0.157.0), đặt trước session id khi resume.
- Effort harness không nhận (Codex không có `max`) được ghi `effort=` vào meta nhưng không truyền, có note trên stderr (hợp đồng record-and-omit của firstmate).
- Bảng `.mate/crew-dispatch.json` theo đúng schema firstmate: `rules[]` với `when` bằng lời, `use` là một profile hoặc mảng lựa chọn, `why` tuỳ chọn; `default` cùng dạng. Không có quota/typed resolution.
- Binary không bao giờ khớp rule. Mate đọc `mate crew dispatch` (in bảng thành flag) và tự chọn; lời captain cho từng task thắng bảng, bảng thắng ý Mate; không tự chọn `max`.
- Bảng hỏng (JSON sai, harness lạ, effort sai hoặc harness không nhận, model như flag, field lạ) chặn mọi spawn tới khi sửa (exit 2), không lùi về bảng built-in.
- Bảng built-in (captain chốt 2026-09-26, `internal/dispatch/builtin.go`) áp cho workspace chưa có file; file của workspace thay nó hoàn toàn. Năm rule, mỗi rule một profile Claude và một Codex cùng sức: ship nhỏ sonnet/luna medium; ship vừa sonnet/luna high hoặc opus/terra medium; ship lớn opus/terra high; scout code + nghiên cứu opus/sol high; scout nhẹ sonnet/luna medium. Tên Codex theo catalogue: `gpt-6-luna`, `gpt-5.6-terra`, `gpt-6-sol`. `default` là profile ship vừa, sonnet/luna high.
  Từ task 70, ship nhỏ có thêm lựa chọn thứ ba: pi trên `deepseek/deepseek-flash` effort high, vì với model này pi kẹp medium thành high.
- Spawn thiếu `--harness` chạy profile `default` của bảng trên harness mặc định của workspace, có note trên stderr; `--model`/`--effort` mà thiếu `--harness` bị từ chối (exit 2). `crew dispatch --example` in bảng built-in dạng JSON.
- Meta ghi `model=`, `effort=`; query, `crew list` (cột HARNESS: `codex gpt-5.5/high`) và detail console hiện chúng.

| # | Task | Xong khi |
| --- | --- | --- |
| 49 | `harness`: `Effort`, `ParseModel`, argv hai adapter; `spawn`: request/meta/result; `internal/dispatch`: đọc và kiểm bảng; CLI `--model`, `--effort`, `crew dispatch [--example]`, gate khi có bảng; manual Mate §7 và skill harness-adapters. | Unit cho từng lớp; golden manual; hai CLI thật nhận đúng flag (`claude -p --model haiku --effort low`, `codex exec -m gpt-6-sol -c model_reasoning_effort="low"` in `reasoning effort: low`). `make check` xanh. Đã xong 2026-09-25. |
| 51 | Bảng built-in, `Resolve`, `DefaultFor`; spawn thiếu `--harness` lấy default; manual §7 và mục crew lifecycle. | Unit cho bảng và gate; golden manual; codex nhận `gpt-6-luna`, `gpt-5.6-terra`, `gpt-6-sol` với `model_reasoning_effort="medium"`, claude nhận `--model sonnet --effort medium`. `make check` xanh. Đã xong 2026-09-26. |
| 52 | Skill `crew-dispatch` cho Mate: đọc bảng mỗi lượt spawn, lời captain trước bảng, định cỡ task (blast radius, open decisions, độ lan, khả năng revert), chọn giữa các alternative (lời captain → bằng chứng launch → model lớn hay effort cao → tải theo `crew list`), batch, harness không khởi động được, profile của crew thay thế. Manual §2/§7 và `stuck-crew-recovery` bước 3 trỏ vào skill. | Golden skill và manual; `make check` xanh. Đã xong 2026-09-26. |
| 53 | `internal/quota`: đọc `quota-axi --json --no-credential-refresh` (schema 5 và 6, floor 0.1.34), map codex→`codex` (account `codex-home`, rồi `default`), claude→`claude`, gộp các scope toàn provider (captain chốt 2026-09-26: chỉ xét theo provider, không xét scope từng model), gate `exhausted_now`/0%, xếp theo spendPriority đã biết, unknown không bao giờ là 0. `crew dispatch` in khối quota và harness được ưu tiên; `crew spawn` cảnh báo (không chặn) khi harness đã cạn. Skill `crew-dispatch` §4: lời captain → gate cứng (cạn, runway ngắn hơn task, launch lỗi) → model hay effort → quota → tải. | Unit với snapshot thật 0.1.34 và fixture schema 6; test CLI không đụng quota-axi của máy; binary thật in đúng khối quota khi có và khi thiếu quota-axi. `make check` xanh. Đã xong 2026-09-26. |
| 56 | Crew id đặt theo task (captain chốt 2026-09-26): kebab-case 2–24 ký tự (`fix-cart-total`, `scout-login-timeout`), không đếm kiểu `k3`/`p1`/`m1`. Một luật trong `internal/names` (`ValidCrew`, `CrewPattern`) cho store và backlog; id cũ như `k3` vẫn hợp lệ. 24 vì agent Herdr là `crew-<id>` trong 32 ký tự. Manual §4 dạy Mate đặt tên, mọi ví dụ trong manual và skill dùng tên có nghĩa. | Unit cho luật; test manual không còn id đếm; golden. `make check` xanh. Đã xong 2026-09-26. |
| 50 | Acceptance live: Mate đọc bảng, spawn hai crew khác profile theo hai task khác độ khó, `crew list` và meta khớp. | Evidence `docs/evidence/m12-crew-dispatch-<ngày>.md`. |

### M13. Console, cột agent, và tab report

Chốt 2026-09-26: console hẹp bên trái, terminal agent ở giữa, và - chỉ khi đang xem một crew - file changes bằng Fresh (`fresh`, editor terminal viết bằng Rust, cài qua `brew install fresh-editor`), mở thẳng vào worktree của crew.
Lúc đó file changes là một cột bên phải. Mặc định là hai cột (captain chốt cùng ngày, sau khi dùng thử ba cột). Ban đầu cột file changes là terminal-code (`tode`); captain đổi sang Fresh cùng ngày vì terminal-code lag trên WezTerm và nặng (Chromium vẽ qua kitty graphics).

Chốt 2026-10-06: file changes là một cửa sổ riêng, không phải cột. Cột đó lấy khoảng nửa phần còn lại của cửa sổ (WezTerm 45% sau console, Ghostty chia đều rồi kéo console về 40–48 cột), không đủ chỗ đọc file. Enter trên crew mở cửa sổ (`wezterm cli spawn --new-window`, Ghostty `new window`) chạy cùng `mate pane serve`; đổi crew chỉ đổi thư mục Fresh trong cửa sổ đang mở. Enter trên Mate đóng cửa sổ. Cột agent không đổi. Cửa sổ mới ở phía trước: kéo focus về console sẽ che nó.

Chốt 2026-10-06, cùng ngày, sau khi dùng thử cửa sổ riêng: report là một tab trong cùng cửa sổ, cạnh tab console (`wezterm cli spawn` không có `--new-window`; Ghostty `new tab` trong cửa sổ đang chứa console). Phím `e` trên hàng crew, kể cả crew đã dừng, mở tab đó. Fresh mở `report.md` trong folder của crew (`projects/<p>/crews/<id>/report.md`) khi file đã có, còn không thì mở chính folder; mate không tạo `report.md`. Đổi crew thì cùng tab đổi file và được chọn lại. Enter chỉ hiện agent ở cột, không mở và không đóng tab. Console thoát thì đóng tab.

Quyết định:

- Mỗi bề mặt là một pane host chạy `mate pane serve --role stage|review --socket <path> --owner <pid>` suốt đời bề mặt đó (`internal/panerun`). Cột stage nằm bên phải console. Tab review nằm trong cùng cửa sổ, không phải split. Console nói bề mặt hiện gì qua unix socket; runner thay chương trình con ngay trong pane. Không còn kill-pane, re-split, pkill hay resize mỗi lần đổi: độ rộng cột stage đặt một lần.
- `host.Host` có `Layout`, `Tab`, `Front` và `Close`. Layout chỉ dựng cột stage: giữ cột còn sống, làm lại cột bị đóng, từ chối pane lạ. Tab mở tab review trong cùng cửa sổ (WezTerm `cli spawn --pane-id <console>`; Ghostty `new tab` trong cửa sổ có terminal của console) và để nguyên tab đã mở. Front chọn tab đó (`activate-pane`, Ghostty `select tab`). Close đóng cột và tab. WezTerm tách cột stage bằng cell (console 20%, 40–48 cột). Ghostty chỉ tách đôi, nên sau khi tách nó `equalize_splits` rồi `resize_split` cột console (đo 2026-09-26 lúc còn cột review: 175 cột → 41/66/66).
- Enter trên hàng Mate/Crew: cột agent chạy `herdr … agent attach … --takeover`. Phím `e` trên hàng Crew (list, detail, box), kể cả crew đã dừng: tab report mở nếu chưa có, rồi chạy `fresh <report.md>` khi file đó đã nằm trong folder của crew (`projects/<p>/crews/<id>/`), còn không thì `fresh` mở chính folder. Folder còn sau khi crew dừng. Đổi crew chỉ đổi file trong tab đang mở rồi chọn tab đó. Enter trên Mate không đóng tab. Ship và scout cùng đích này: folder của crew, không phải worktree và không phải `.mate/` của workspace. Fresh chỉ lái được từ ngoài bằng token nó cấp cho terminal bên trong nó, nên mate không tự mở Review Diff; gõ phím qua host thì dễ vỡ (thử 2026-09-26: `ctrl+p` không mở palette, chữ vào thẳng buffer). `Close(roles...)` đóng từng cột và từng tab.
- Lag trên WezTerm với terminal-code (captain báo 2026-09-26; Ghostty không lag): terminal-browser vẽ bằng kitty graphics, WezTerm 20240203 tốn 100–200% CPU mỗi lúc nó vẽ; các biến `TERMINAL_BROWSER_*` không đổi đáng kể. Lý do đổi sang Fresh, vẽ bằng ký tự.
- Cột là mọi tiến trình trên tty của nó, không chỉ con của runner (terminal-code để lại viewer riêng về ppid 1): đổi nội dung thì dọn hết (TERM rồi KILL sau 2s), và cột chỉ về dòng chờ khi tty trống.
- Giữa hai chương trình runner khôi phục termios, rời alt screen, tắt mouse/paste, xoá ảnh kitty và RIS. Chương trình tự thoát thì giữ chữ nó in (lý do herdr từ chối) và chỉ tắt mode.
- Console gửi PATH của nó cho chương trình trong cột (pane Ghostty bắt đầu từ env của login) và tìm `herdr`/`fresh` thêm ở `~/.local/bin`, `/opt/homebrew/bin`, `/usr/local/bin`.
- Console thoát: gửi exit cho hai runner rồi `Close` (Ghostty giữ pane đã hết tiến trình, kể cả với `wait after command` false; đo 2026-09-26). Console chết đột ngột: runner tự thoát trong 1s khi pid console mất.
- Herdr chết (mất điện, restart máy) thì console vẫn chạy trên state file, nhưng không còn re-read được gì. Observer đọc không được nên không kết luận gì về crew (mục 4b); nó hỏi session một lần mỗi vòng poll (độc lập với việc có crew mở hay không) và giữ một dòng `herdr is not running` trên status line, tự mất ở vòng poll đầu tiên Herdr trả lời lại. Bước kiểm tra đó hỏi đúng binary `herdr` mà cột agent chạy (`findTool`), và chỉ nói "không chạy" khi Herdr trả lời dứt khoát session không lên. Enter trong lúc đó bị từ chối trước khi chạy `herdr agent attach`, thay vì để cột agent giữ lỗi của Herdr rồi treo ở đó (đo 2026-09-30, sau khi server Herdr mất vì restart máy). Bằng chứng: [evidence/herdr-down-2026-09-30.md](evidence/herdr-down-2026-09-30.md).
- Đường phục hồi cho crew khi Herdr/pane chết: `mate crew relaunch <project> <id> [--note "<một dòng>"]` (viết tắt `mate crew restart`), và mục tương ứng trong Actions của console (`R`, `Restart crew…`). Nó giữ nguyên branch, worktree và `.status`, dừng agent cũ nếu còn, đưa Herdr trở lại bằng `EnsureSession` (một máy restart làm mất server), mở tab mới `crew-<id>`, chạy lại đúng harness/model/effort trong meta trong một session mới, rồi gửi lại con trỏ brief (kèm ghi chú nếu có). Meta được ghi lại lần cuối với các khoá pane mới; crew đã `finished|failed`, hoặc mất worktree, bị từ chối vì không còn gì để dựng lại. `crew spawn` không làm được việc này: branch và worktree đã tồn tại và spawn từ chối cả hai. Đây là điểm `Đợt 2 sau M7` gọi là "relaunch giữ worktree"; chưa chạy live, có unit test với pane giả (`TestRelaunch*`) và một `TestLiveCrewRelaunchAfterThePaneDies` (skip trừ `MATE_LIVE=1`).
- Không có Fresh: chỉ có cột agent. Status line và `e` nói `brew install fresh-editor`.
- `mate console` dựng cột agent lúc mở; `mate <dir>` dựng ở Enter đầu tiên.

| # | Task | Xong khi |
| --- | --- | --- |
| 54 | `internal/panerun`, `mate pane serve`, `host.Layout`/`Close` cho WezTerm và Ghostty, console dựng và đóng cột, Enter hiện agent và file changes. | Unit cho runner (thay tại chỗ, no-op khi trùng, TERM rồi KILL, viewer tách rời, exit, owner mất), host (layout, idempotent, làm lại cột, pane lạ, thu hẹp Ghostty bằng đo), wiring console. Chạy thật trong Ghostty 1.3.1 và WezTerm: ba cột, Enter hiện lỗi herdr ở cột agent và Source Control của repo ở cột file changes, ctrl+c đóng hết cột và viewer. `make check` xanh. Đã xong 2026-09-26. |
| 55 | Acceptance live với Mate và crew thật: Enter đổi qua lại, cột agent attach đúng agent, cột file changes mở ở crew và đóng ở Mate. | Evidence `docs/evidence/m13-columns-<ngày>.md`. |

### M14. Mate không giữ lượt; mode theo captain

Captain chốt 2026-09-27: Mate không có lượt chạy lâu, trừ khi đang trả lời câu captain hỏi.
Đo 2026-09-26 và 2026-09-27 (Claude Code, project `hellovietnam`): Mate poll crew trong một lượt bằng vòng `for … sleep 20; mate state`, chín phút, tin của captain nằm trong hàng đợi ("Press up to edit queued messages").
Manual cũ dạy vòng đó cho manual mode, vì ở manual mode không có gì đánh thức Mate.

Thiết kế: mode theo captain.
Captain gõ cho Mate thì manual (hook `mate-prompt` xoá `.auto` như trước), tin của crew nằm trong box.
Mate đã trả lời (dòng Stop trong `sent.log` sau prompt cuối của captain) và captain không gõ gì thêm `store.QuietAfter` (5 phút) thì daemon bật lại `.auto`, ghi `auto mode on: …` vào `sent.log`, và digest đánh thức Mate với những gì còn mở.
Phím `m` chọn manual thì giữ (`mate/.manual`), daemon không tự bật lại; chọn auto thì bỏ giữ.
Mate Codex không có hook Stop nên không bao giờ tự bật lại; phím `m` vẫn bật được.
Daemon xét mỗi `DefaultInterval` (90s), nên auto có thể về muộn tới 90s sau mốc 5 phút.

| Task | Việc | Xong khi |
| --- | --- | --- |
| 57 | `autopilot` rearm theo `sent.log` (đọc dần theo offset), `store.SetMode`/`Held`, phím `m` giữ manual; `crew spawn`, `state`, `send` luôn kết thúc bằng dòng `turn:`; manual §4, §7, §9, §10 bỏ vòng poll, Mate kết thúc lượt ở mọi mode. | Unit: im lặng dưới 5 phút không bật, đủ 5 phút bật và digest vào Mate, Mate chưa trả lời không bật, gõ lại thì tính lại, giữ manual không bật, không có Stop không bật; test manual không còn `sleep 20`. `make check` xanh. Đã xong 2026-09-27. |
| 59 | Captain chốt 2026-09-27: mọi thứ trong project là việc Mate tự chạy (repo, crew, brief, memory, backlog); trên project (tạo/xoá project, project khác, workspace, file của captain, `yolo`, mode) là của captain. `mate project repo add` nhận URL git: clone vào gốc workspace (không clone đè thư mục đã có), repo chưa có commit nào thì tạo commit rỗng đầu tiên trên default branch, không bao giờ push. Manual §1 dạy Mate tự thêm/bỏ repo khi captain chỉ tên. | Unit: clone remote rỗng có commit đầu, remote có lịch sử không bị đụng, từ chối clone đè, nhận dạng URL; test manual. Chạy thật với `git@github.com:nguyenngocanh94/hellovietnambackend.git` (repo rỗng) trong workspace tạm. `make check` xanh. Đã xong 2026-09-27. |
| 58 | Acceptance live: Mate giao việc rồi kết thúc lượt, captain hỏi được ngay, 5 phút im lặng thì digest đánh thức Mate. | Evidence `docs/evidence/m14-turns-<ngày>.md`. |

Kèm theo (2026-09-26/27, đã xong): codex-cli 0.157.1 thêm dòng footer thứ hai (`? for shortcuts`, `⚠ 1 warning · f2 to view`) nên mọi `mate send` tới crew Codex rảnh bị từ chối là màn hình lạ; composer Codex giờ tìm theo cấu trúc (dòng `›` cuối, không có hàng menu `N. …` bên dưới), không đếm dòng footer.
Herdr 0.8.2 từ chối `agent read --source recent-unwrapped` khi Codex đang chạy (`agent_not_idle`); `Herdr.readAgent` đọc lại bằng `--source visible`.
Từ 2026-10-01 (phương án registry harness, PR 3) nguồn đọc là của `ScreenProfile` từng harness: Claude và Codex giữ `recent-unwrapped` kèm fallback đó, harness khai báo `visible` thì được đọc thẳng bằng `visible`.

Sau M8: replay theo tốc độ cho content; skin tuỳ biến (`.mate/dashboard/`) nếu còn cần.

### M15. Giới hạn chi phí context của Mate

Captain chốt implement 2026-09-27; triển khai 2026-09-28, từ hai research note
`docs/research/mate-token-2026-09-27.md` và `mate-coordination-cost-2026-09-27.md`.

- Đo theo message id/call, không theo số record assistant. Tổng input gồm fresh,
  cache-read, cache-write; output/thinking không được cộng trùng. Không suy chi phí
  tiền hay quota từ tỷ lệ tổng token. CLI usage tách hai cache bucket; dashboard
  và console hiện token context tuyệt đối cả khi chưa biết context window.
- Mate Claude có profile riêng: settings nguồn project, strict MCP, built-in
  tools Bash/Read/Write/Edit/Glob/Grep/Skill; model opus, effort medium; autocompact
  300000 là lưới an toàn. `project.yaml` có `mate.model`, `mate.effort` để captain
  ghi đè. Crew giữ profile dispatch riêng. Manual lõi ≤25 KiB; hợp đồng dài nằm
  trong skill có trigger, giữ recall, phân quyền và quy tắc kết thúc lượt ở lõi.
- Console xét refresh trước digest: context của đúng session hiện tại ≥150000,
  auto bật và không held, Mate đã trả lời captain và im ≥5 phút, outbox trống,
  composer trống. `mate.refresh_context` đổi ngưỡng; số âm tắt tự động. Codex
  chưa có tín hiệu quiet tương đương nên không tự refresh.
- Refresh là stow → checkpoint receipt → Stop thật → fresh start → recall;
  không dùng resume. `mate mate refresh <project>` dùng cùng đường an toàn,
  bỏ điều kiện ngưỡng/5 phút vì captain chủ động gọi. Restart thường vẫn resume.
  Thiếu receipt, không có turn-end, file đổi sau receipt, captain nói thêm hoặc
  giành lại manual thì giữ phiên cũ. Retry tự động cách nhau ít nhất 30 phút.
  Maintenance flock ngăn console khác gửi assign/digest trong khi đổi phiên;
  hàng đợi bền trên đĩa. Receipt gắn nonce, session và hash memory/backlog/PROJECT;
  kiểm tra mọi crew mở còn có trong backlog. Nội dung bàn giao vẫn do Mate chịu
  trách nhiệm, không có bộ kiểm tự động chứng minh mọi suy nghĩ đã được ghi.
- Trước thay mate.meta, giữ provenance session trong `mate/sessions/*.meta`.
  Sau Stop được xác nhận, copy transcript vào snapshot bất biến rồi mới đánh dấu
  finalized. Xác nhận nghĩa là `agent get` báo không thấy và `agent list` không
  còn tên; Herdr không báo session đang chạy (mất khỏi list hoặc running=false)
  chỉ là suy ra, nên archive giữ transcript sống, không finalized, và nhóm
  message cuối vẫn treo. Reindex tính đủ cả call cuối của phiên đóng có xác
  nhận, không làm token biến mất sau refresh. Transcript đang chạy vẫn chờ message id kế tiếp để chốt nhóm
  cuối; context có thể trễ một call hoặc chưa có ở call đầu, không giả thành 0.
- Scout report có Summary ≤6000 ký tự, gồm giới hạn và evidence. `mate report
  <project> <crew> --summary` không fallback sang toàn bộ report nếu thiếu mục.
  `mate diff` mặc định stat, `--full` vẫn là đường đọc toàn bộ patch.
- `mate review <project> <crew> --id <reviewer> --harness ... --model ... --effort ...`
  tạo scout riêng đọc full diff và acceptance evidence. Token vẫn thuộc Crew
  ledger; Mate kết thúc lượt trong lúc reviewer làm. `review --check <reviewer>`
  và `merge --review <reviewer>` từ chối khi SHA/base/brief/hand-back đổi, reviewer
  chưa hand-back, hoặc verdict không pass. Merge dùng đúng reviewed SHA.
  Tóm tắt của implementer hay diff stat không thay được review.
- `mate backlog add|move|done` sửa một mục dưới lock, giữ nguyên câu hỏi/nội dung
  nhiều dòng; Done giữ 10 mục, ghi archive trước khi bỏ mục khỏi file nóng.
  Đây là ngoại lệ có chủ đích cho quy tắc cũ “app không sửa backlog”.
- Chưa thêm daemon digest LLM riêng. Chỉ xét sau khi số liệu sau rollout cho
  thấy cần; không coi ước lượng token/ngày của research là kết quả đã đo.

| Task | Việc | Kiểm chứng |
| --- | --- | --- |
| 60 | Context tuyệt đối, cache buckets, profile Mate và lõi manual | Unit DB/CLI/UI/argv, golden manual và skills; live context trong evidence M15. |
| 61 | Checkpoint/fresh recall, guard trạng thái và lưu transcript cũ | Unit failure paths, queue/maintenance, current-session threshold, reindex; live giữ nguyên câu hỏi đang chờ qua fresh session. |
| 62 | Summary, backlog commands, reviewer riêng và merge theo SHA | Unit với git thật, schema brief và stale review; live reviewer phải phát hiện implementation sai dù hand-back ghi pass. |

Rollout: build binary mới, đóng console cũ (sender cũ chưa biết maintenance lock); chạy `bin/mate mate refresh <project> --workspace <dir>`
lúc Mate rảnh, rồi mở lại console bằng binary mới để daemon dùng điều kiện M15.
Chỉ sửa AGENTS.md trên đĩa hoặc resume phiên cũ không chứng minh context đã nhỏ đi.
Evidence: `docs/evidence/m15-context-2026-09-28.md`.

### M16. Registry harness

Theo [phương án registry harness](plans/harness-registry-2026-09-30.md); captain chốt 2026-10-01 rằng chuỗi này không chạy test live, lỗi chỉ lộ ra khi dùng thật thì sửa sau.
Mỗi PR từ 1 đến 6 giữ nguyên hành vi của Claude và Codex.

| Task | Việc | Kiểm chứng |
| --- | --- | --- |
| 63 | PR 0: test đặc tả argv và file sinh ra, ratchet tên harness | Golden `TestLaunchCharacterization*`; ratchet ghi con số ban đầu. |
| 64 | PR 1: hợp đồng, `Registry`, `catalog`, tiêm qua `Deps` | Không call site nào gọi `AdapterFor` hay `ParseKind` toàn cục. |
| 65 | PR 2: launch qua `Prepare` và `Build`, một builder kín cho `LaunchSpec` | Argv và file sinh ra giống từng byte. |
| 66 | PR 3: bảng màn hình vào `ScreenProfile`, đọc pane qua nguồn của profile | Mọi capture phân loại như cũ. |
| 67 | PR 4: stop, session, hook, turn-end thành capability | Test resume, stow, recall hook pass trên cả hai harness. |
| 68 | PR 5: transcript và quota thành capability | `mate usage` và dashboard cho cùng số trên corpus fixture. |
| 69 | PR 6: Claude và Codex vào `internal/harness/claude` và `internal/harness/codex`; mặc định và danh mục harness cho store, query, Console lấy từ registry; skill `harness-adapters` sinh từ registry; suite hợp đồng mục 1 đến 4 | Ratchet bằng 0 ngoài allowlist; suite pass cho cả hai. |
| 70 | PR 7: pi, chỉ vai Crew | Diff chỉ gồm package mới, một dòng catalog, một hàng dispatch, fixture và tài liệu, cộng ba chỗ sửa hợp đồng: `LaunchPlan.ContextFlag`, `GracefulStopper.ClearKeys`, `query.Harness.Mate`. Suite hợp đồng pass cho pi; Mate trên pi bị từ chối nêu `Hooks`. Đã xong 2026-10-01, không chạy test live (captain chốt). |

### M17. Console tự hồi phục khi mở

Chốt 2026-10-07: tắt máy (hay restart) không làm hỏng dữ liệu trên đĩa; meta, brief, worktree, `.status` còn nguyên, chỉ mất tiến trình (Herdr server, pane). Mở `mate console` sau đó mà mate không chạy là sai. Console khi mở kiểm tra sức khoẻ và hồi phục mọi thứ hồi phục được, không hỏi: Herdr tắt thì bật server mới; pane của Mate/Crew không còn thì mở pane mới và resume session id của harness. Chép workspace sang máy khác (hay dời trên cùng máy) đi cùng đường đó: dữ liệu không hỏng, chỉ các liên kết với máy phải dựng lại.

Trước M17: pane ghi trong meta được coi là sự thật (`internal/query/load.go`: có `pane` là `running`), nên sau restart `s` bị khoá ("Mate is recorded running") dù status line bảo bấm `s`; `crew relaunch` luôn chạy hội thoại mới; worktree tạo với đường dẫn tuyệt đối và mate không bao giờ chạy `git worktree repair`; hook trong `mate/.claude/settings.json` giữ đường dẫn binary cũ; dời workspace trên cùng máy làm `EnsureSession` từ chối vì owner marker (`<configHome>/mate/session-owners/<session>`) ghi workspace id của đường dẫn cũ.

Quyết định:

- Một package `internal/recovery` chạy nền khi console mở (cả `mate console` lẫn `mate <dir>`), console dùng được trong lúc nó chạy. Thứ tự: (1) liên kết với máy, (2) Herdr server, (3) Mate và Crew. Mỗi bước tự phát hiện việc của nó, chạy lại nhiều lần vẫn an toàn, và một mục hỏng không chặn mục khác.
- Liên kết với máy, chỉ có việc khi workspace đã bị chép hay dời:
  - Worktree của crew không gắn với repo (`gitx.WorktreeAttached` sai): `git -C <repo> worktree repair <worktree>`.
  - Hook trong `mate/.claude/settings.json` trỏ tới binary `mate` không tồn tại: viết lại bằng binary hiện tại, giữ mọi hook khác.
  - Owner marker ghi workspace id khác id của đường dẫn hiện tại: workspace đó không còn trên đĩa thì nhận lại marker; còn (chép trên cùng máy) thì đặt tên session mới vào `workspace.yaml`, không bao giờ dùng chung session với bản kia.
  - `workspace.yaml` có thêm `root:` (đường dẫn tuyệt đối lúc ghi gần nhất). Khác đường dẫn hiện tại thì thay tiền tố gốc cũ bằng gốc mới trong `crews/<id>/brief.md`, rồi ghi `root:` mới. Workspace cũ chưa có `root:` thì suy gốc cũ từ `gitdir` trong file `.git` của một worktree; không suy được thì bỏ qua bước này.
  - Không viết lại đường dẫn trong `mate.db`: timeline đã tự tìm lại transcript khi file không còn.
- Herdr: session không chạy thì `EnsureSession`.
- Mate và Crew, đối chiếu meta với `ListAgents` của session:
  - Bật lại: meta còn ghi `pane` mà agent không có trong `ListAgents` (đang chạy thì máy chết).
  - Để yên: meta không ghi `pane` (stop có chủ đích đã xoá nó), crew `finished|failed`, và mọi agent Herdr còn liệt kê là sống.
  - Mate: `StartMate` với `Resume:true`, đúng harness trong meta (đường của Restart trong console).
  - Crew: relaunch có thêm resume: `session_id` trong meta, đúng harness/model/effort, `--resume <id>` với Claude, `codex resume <id>` với Codex. Resume không được (hội thoại nằm ở `~/.claude`, `~/.codex` của máy cũ, hay launch hỏng) thì chạy mới một lần với con trỏ brief kèm ghi chú hội thoại trước không còn, như fallback của `StartMate`. `mate crew relaunch` tay vẫn chạy mới như cũ.
- Khoá `.mate/recover.lock` (flock): hai console mở cùng lúc thì chỉ một cái hồi phục, cái kia chờ rồi thấy không còn gì để làm.
- Loader của console (`query`) đọc trạng thái Mate/Crew theo đối chiếu với Herdr, không theo `pane` trong meta: session không chạy hay agent không còn là `stopped`, nên `s` và nhãn Resume dùng được kể cả khi hồi phục hỏng.
- Hiển thị: status line `recovering <n> of <m>…` rồi một dòng tổng kết (`recovered <k>`, kèm mục hỏng). Hàng hỏng giữ lỗi của nó; `R` vẫn dùng được trên hàng đó.
- Không làm: hồi phục khi Herdr chết trong lúc console đang mở (vẫn chỉ báo trên status line; mở lại console để hồi phục); pane còn nhưng harness bên trong đã thoát; tự bật lại crew đã dừng có chủ đích.

| # | Task | Xong khi |
| --- | --- | --- |
| 71 | Loader đối chiếu với Herdr: Mate/Crew có `pane` mà agent không sống là `stopped`; `s` mở lại được sau restart. | Unit với `runtime.Fake`: Herdr tắt, agent mất, agent sống, stop có chủ đích. Đã xong 2026-10-07: `query.ReadLiveness` hỏi `LookupSession` + `ListAgents` (không bật server), `query.LoadLive` nhận kết quả; `pane` mà Herdr không liệt kê là `stopped` cho Mate (`s` thành Resume, `start` bị khoá) và binding Absent cho Crew; hỏi không được (lỗi vận chuyển) thì giữ trạng thái ghi như cũ; không có `pane` vẫn là `created`. Console gọi `LoadLive` mỗi lần nạp (giới hạn 3s) và `s` trên Mate đã dừng truyền `Resume:true`. Unit với `runtime.Fake`: Herdr tắt, agent mất, agent sống, stop có chủ đích, không hỏi được. Không chạy test live. |
| 72 | Crew relaunch có resume (`spawn.RelaunchCrew` nhận `Resume`), fallback chạy mới một lần kèm ghi chú. | Unit: argv resume cho Claude và Codex; resume hỏng thì chạy mới và gửi con trỏ brief có ghi chú; relaunch tay vẫn chạy mới. Đã xong 2026-10-07: `spawn.RelaunchCrew` nhận thêm `RelaunchOptions{Resume}` (tham số cuối, tuỳ chọn nên `crew relaunch` tay không đổi và vẫn chạy mới); dùng lại `decideResume`/`checkResume` của `StartMate`, Codex chưa ghi `session_id` lúc spawn thì lấy từ rollout trong worktree như lúc stop Mate; resume hỏng thì chạy mới đúng một lần trong tab mới với con trỏ brief kèm câu hội thoại trước không còn; meta ghi `resumed`/`resumed_from`. Unit: argv `--resume` cho Claude, `codex resume <id>` cho Codex, rollout mất và resume hỏng đều chạy mới có ghi chú, relaunch tay không resume. Không chạy test live. |
| 73 | Liên kết với máy: `git worktree repair`, hook Claude, owner marker, `root:` và tiền tố trong `brief.md`. | Unit trên repo git thật trong thư mục tạm, dời thư mục rồi hồi phục; owner marker ba trường hợp (khớp, workspace cũ mất, workspace cũ còn); hook giữ hook lạ. Đã xong 2026-10-07: `internal/recovery/links.go` (`RepairLinks`), `workspace.yaml` có `root:` (ghi ở `Init`, cập nhật khi hồi phục; workspace cũ suy gốc từ `gitdir` của worktree), `gitx.RepairWorktree`, `runtime.SessionOwner`/`ReclaimSessionOwner`. Hook đi qua hợp đồng harness (`HookInstaller.Repoint`: Claude viết lại binary mất, Codex không làm gì vì `hooks.json` được ghi lại mỗi lần start Mate) vì ratchet tên harness cấm `internal/recovery` nhắc `claude`. Unit trên repo git thật: dời thư mục rồi hồi phục (worktree gắn lại, brief đổi tiền tố nhưng không đụng đường dẫn chỉ trùng tiền tố, `root:` mới, chạy lần hai không làm gì), workspace không có `root:`, ba trường hợp owner marker, hook giữ hook lạ và binary còn sống. Không chạy test live. |
| 74 | `internal/recovery` và console: chạy nền khi mở, khoá, status line, lỗi theo hàng. | Unit thứ tự và khoá với `runtime.Fake`; golden status line; test wiring console. `TestLiveRecoverAfterHerdrDies` (skip trừ `MATE_LIVE=1`): tắt Herdr, mở console, Mate và một crew Claude sống lại với `resumed=true`. Đã xong 2026-10-07: `recovery.Run` (liên kết, rồi hỏi Herdr, rồi `EnsureSession`, rồi bật lại Mate rồi Crew; từng mục hỏng không chặn mục khác) dưới `.mate/recover.lock` (`store.LockRecover`, flock); console chạy nó nền ở `runConsole` (cả hai đường vào), status line `recovering <n> of <m>…` rồi dòng tổng kết 30s, lỗi nằm ở `Error` của hàng. Unit: thứ tự (worktree gắn trước khi bật Herdr, server trước agent), khoá giữ/nhả, hai lần chạy đồng thời chỉ start mỗi agent một lần, stop có chủ đích và crew đã đóng để yên, test status line và wiring console. Lệch spec: `EnsureSession` chỉ gọi khi có agent cần bật lại (workspace không có gì mất thì không bật server chỉ vì mở console). `TestLiveRecoverAfterHerdrDies` đã viết nhưng chưa chạy (cần `MATE_LIVE=1` và lab session); nó làm mất agent bằng force-stop thay vì giết server vì lab do runner sở hữu và test live không bật Herdr server. Không chạy test live. |

### Xoá project

Chốt 2026-10-07: `mate project remove` trước đây chỉ bỏ project khỏi `workspace.yaml`, để Mate và Crew chạy mồ côi không ai quản. Quyết định:

- Một hàm `spawn.RemoveProject` dùng chung cho CLI và console: dừng mọi crew đang chạy như `mate crew stop` (không `--discard`), dừng Mate như `mate mate stop` (captain dừng thì stow trước khi composer trống), rồi `store.Workspace.RemoveProject`. `projects/<p>/` (memory, backlog, hồ sơ crew) và repo vẫn trên đĩa; `mate project add` lại cùng tên thì lịch sử quay lại. Crew đã landed thì worktree và branch đi cùng lần stop, như `crew stop`.
- Một lần dừng hỏng (crew có việc chưa landed, stow hỏng) thì không bỏ project khỏi `workspace.yaml`; lỗi nêu từng agent không dừng được. Project không bao giờ bị bỏ khi agent của nó còn chạy không ai quản.
- "Đang chạy" theo Herdr (`query.ReadLiveness`): agent có `pane` trong meta mà Herdr không liệt kê chỉ bị xoá meta chạy, như các đường stop xử lý agent đã mất. Crew `finished|failed` và agent không có `pane` bị bỏ qua. Herdr tự đóng workspace khi tab cuối đóng nên không thêm lệnh đóng workspace.
- Console: mục `Remove project…` ở Actions của hàng project, sheet xác nhận nói sẽ dừng Mate và N crew đang chạy, dữ liệu còn trên đĩa; `y` xác nhận (không gõ tên vì làm lại được bằng `project add`), Esc huỷ.

| # | Task | Xong khi |
| --- | --- | --- |
| 75 | `spawn.RemoveProject`, `mate project remove` và mục `Remove project…` của console. | Unit với `runtime.Fake`: Mate và mọi crew dừng trước khi bỏ đăng ký (stow chạy sau crew, trước Mate); một crew không dừng được thì project còn đăng ký và lỗi nêu crew đó; agent Herdr không liệt kê chỉ bị xoá meta chạy; remove rồi add lại giữ `memory.md` và hồ sơ crew. Unit `cmd/mate` cho CLI và cầu nối console, test console cho mục menu và sheet (`y` chạy, Esc/Enter huỷ, phím của mục không xác nhận). Đã xong 2026-10-07: `internal/spawn/project_remove.go`; `mate mate stop` và `project remove` dùng chung `stowBeforeStop`. Lệch spec: bước đóng workspace không có code riêng (Herdr đóng workspace khi tab cuối đóng, không có phương thức runtime đóng workspace, và `collapseEmptyProjectWorkspaces` chỉ gỡ bản trùng khi `EnsureProjectWorkspace`); stop crew vẫn gỡ worktree và branch của crew đã landed, và crew còn việc chưa landed chặn việc xoá cho tới khi landed hoặc `crew stop --discard`. Không chạy test live. |

### Token review: task nào tốn, vì sao, sửa harness của project ở đâu

Captain yêu cầu 2026-10-03, sau khi M15 đã giới hạn context của chính Mate: Mate cần chỉ ra được task nào tiêu thụ quá nhiều token và đề xuất skill, docs cho repo của project.

- Skill `token-review` (`assets/mate/skills/token-review/`): tìm task đắt, đọc nguyên nhân, đối chiếu vào bảng "dấu hiệu → nguyên nhân → nơi sửa", tách pattern khỏi tai nạn, ghi review vào `mate/token-reviews/<ngày>.md` với từng đề xuất có đích đến, nguyên văn, bằng chứng và con số sẽ kiểm lại.
  Mate chỉ đề xuất: thay đổi trong repo (`AGENTS.md`, skill, docs, script) do một ship Crew commit sau khi captain đồng ý; `CREW.md` và bảng dispatch là của captain; lesson và `PROJECT.md` là của Mate.
- Mate không đọc transcript hay repo để giải thích chi phí.
  Hai lệnh có đầu ra chặn trên là nguồn duy nhất: `mate usage <project> --top <n>` (Mate và n task lớn nhất theo TOTAL, dòng total vẫn cộng mọi task) và `mate usage <project> <crew> --why` (projection `performance` của trang Task trong dashboard, in thành một trang chữ).
  `--why` đi qua `dashboard.Server.CrewPerformance`, cùng đường với API, và không tự đo gì thêm.
- Giới hạn giữ nguyên từ diagnostics: finding chồng lấn nên không cộng token của chúng; `?` là chưa biết, không phải 0; mọi dòng `not measured` là điều không được kết luận.
- Chưa làm: digest cho chính hàng Mate (chi phí điều phối), so sánh tự động theo kind ship/scout, và live test cho một Mate thật chạy skill này.
  Đã kiểm: unit cho hai cờ, golden cho skill và manual, chạy tay chỉ đọc trên workspace thật `~/work-matev2` (task `esp32research`, 1,5M token, 22 call).

### Card Steps: agent đã làm gì để xong task, và dùng skill nào

Captain yêu cầu 2026-10-03: trang Task cần một card cho thấy các bước agent đã làm, tool call và model call được phân loại (không cần tới mức file nào, prompt nào), cùng các skill đã dùng.

- Card `Steps` trên trang Task, ngay dưới bốn câu trả lời: mỗi prompt một dải ribbon theo token và một danh sách bước theo thứ tự.
  Một bước là một chuỗi model call liên tiếp cùng loại việc, kèm số call, số tool call, token, phần trăm của prompt và thời gian.
  Bước `Mixed activity` ghi rõ nó trộn những loại nào; bước cuối của prompt đang chạy mang nhãn `running`.
  Prompt dài hiện 12 bước đầu, phần còn lại mở bằng một cú bấm; ribbon luôn vẽ cả prompt.
- Dữ liệu là `overview.steps` của từng prompt và `performance.skills` (`internal/diagnostics`), cùng một phép gán loại với `categories`, nên token của các bước cộng lại đúng bằng prompt.
- Skill: Claude Code qua tool `Skill` (tên lấy từ input, và giờ là `target` của action), Codex và pi qua việc đọc `SKILL.md` bằng shell hoặc tool đọc file (tên là thư mục chứa file).
  Card ghi số lần nạp, cách nạp và bước nào nạp.
  Nạp không có nghĩa là đã làm theo; card nói rõ điều đó.
- Captain cho phép một suy luận theo thời gian, vì Codex không ghi tool call cha của lệnh native: lệnh được gắn vào tool call duy nhất đang mở lúc nó bắt đầu (`docs/timeline.md`, mục Crew diagnostic evidence).
  Hai call cùng mở thì không gắn; mỗi lệnh được gắn mang `parent_link`, và dòng coverage ghi số lệnh.
- Phân loại chỉnh theo dữ liệu thật: tool trong wrapper được phân loại theo tên (`tools.web__run` là research); dòng status ghi kèm việc khác không còn làm call thành mixed; `if`/`then`/`for`, guard `[ … ]` và comment không phải loại việc; tìm hay liệt kê `AGENTS.md`/`SKILL.md` là research, đọc mới là instructions; call kết thúc lượt (`end_turn`, `stop`) mà không chạy tool là `Respond`.
- Đã kiểm: unit cho luật gắn lệnh, bước, skill của cả ba harness và các luật phân loại; test node cho card; golden số liệu dashboard cập nhật (tổng token không đổi); mở card thật ở 1280px và 480px, sáng và tối, trên bản sao workspace `~/work-matev2`.
  Trên task `esp32research` (Codex, 1,5M token) phần Unclassified + Mixed giảm từ 78% xuống 44% token.
- Chưa làm: card cho các exchange của Mate (dữ liệu `steps` đã có trong overview của exchange); dòng skill trong `mate usage --why`; kiểm trên recording thật của Claude và pi (workspace thật chỉ có Crew Codex, hai harness kia mới có unit test); token theo từng skill không đo được, chỉ có token của call nạp nó.

### Thử nghiệm: Jev notice advisor

Bật theo từng workspace trong `.mate/.env` (`MATE_JEV=on`, `MATE_JEV_API_KEY_FILE=<file key ngoài workspace>`; console không đọc biến môi trường của process): trên Mate/Crew có binding active, `a` → `e` gọi Jev để giải thích notice trong tối đa 40 dòng cuối terminal. Chỉ hiển thị gợi ý có thời điểm capture; không đổi composer, task state, incident, send hay receipt. Không gọi API khi refresh. Lỗi cấu hình/API không ảnh hưởng observer và sender. Đây là bản thử thủ công để đánh giá semantic classification, chưa thay probe. Hướng dẫn và phương án tiếp theo: [jev-notices.md](jev-notices.md).
