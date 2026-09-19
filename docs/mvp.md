# matev2 MVP

matev2 là bản viết lại của `mate` (v1, `/Volumes/Work/Workspace/mate`) với mục tiêu đơn giản hoá triệt để phần giao tiếp giữa Mate và Crew.
Tài liệu này ghi các quyết định đã chốt và danh sách 24 task của MVP.
Mỗi task là một PR review được trong một buổi và có tiêu chí xong đo được.

## 1. Mô hình

| Khái niệm | Vai trò | Vòng đời |
| --- | --- | --- |
| Workspace | Một thư mục chứa nhiều project, mở bằng `matev2 <dir>`. State nằm trong `.matev2/`. | Bền |
| Project | Một repo git là thư mục con của workspace, đăng ký trong `workspace.yaml`. | Bền |
| Mate | Agent điều phối của một project. Phân tích yêu cầu, viết brief, spawn Crew, giám sát, review, báo cáo. Không sửa code, không tự tìm hiểu repo. | Dài, có trí nhớ |
| Crew | Agent thực thi do Mate spawn cho một task. Chạy harness interactive trong pane Herdr, cwd là worktree riêng. | Ngắn, dùng một lần |

Mate là một harness interactive (Claude Code, Codex, pi) chạy trong một pane Herdr, cwd là `.matev2/projects/<p>/mate/`.
Người dùng nói chuyện với Mate bằng cách gõ vào pane đó, xem qua stream mode của console.
Mate không có code trong cwd; muốn biết gì về repo thì gọi `matev2` hoặc spawn Crew.

## 2. Quyết định đã chốt

1. Mate chỉ điều phối và ra quyết định. Không sửa code, không tự khảo sát repo. Ràng buộc bằng cấu trúc (cwd không chứa code), không bằng lời dặn.
2. Crew chạy bằng harness có sẵn: Claude Code, Codex, pi. Mặc định Mate là Claude Code, Crew là Codex.
3. Runtime terminal là Herdr 0.8.2. Mapping: một Herdr session cho workspace, một Herdr workspace cho project, một tab cho Mate và một tab cho mỗi Crew.
4. Giao tiếp học triệt để từ firstmate (`/Volumes/Work/Workspace/firstmate`), xem mục 4.
5. Hai chế độ giao tiếp: giám sát (mặc định) và tự động, xem mục 5.
6. Persistence là file phẳng trong `.matev2/`. Không SQLite. Chỉ console sửa state; Crew chỉ append vào `.status`.
7. Console TUI copy từ `internal/ui/console` của v1, giữ stream mode nhúng pane Mate.
8. Trạng thái agent của Herdr (`idle/blocked/done`) là screen scraping, chỉ dùng làm tín hiệu phụ, không bao giờ dùng để kết luận task xong.
9. Token monitor làm sau MVP, nhưng `.meta` ghi `transcript=` và `session_id=` từ ngày đầu.
10. Tên binary và CLI là `matev2`, thư mục state `.matev2/`, prefix biến môi trường `MATEV2_`.

## 3. Layout workspace

```text
<workspace>/
├── shop/                                 repo git thật, Mate không cwd vào đây
├── blog/
├── .worktrees/
│   └── shop-k3/                          git worktree của crew k3, branch matev2/k3
└── .matev2/
    ├── workspace.yaml                    projects, herdr session name, defaults
    ├── WORKSPACE.md                      quy tắc của người dùng cho mọi Mate
    ├── pricing.yaml                      bảng giá token, dùng sau
    └── projects/
        └── shop/
            ├── project.yaml              repo, default_branch, mode, yolo
            ├── PROJECT.md                bối cảnh project
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
            │   └── .claude/
            │       ├── settings.json     hook UserPromptSubmit, Stop
            │       └── skills/
            └── crews/
                ├── k3.meta               task= harness= pane= worktree= branch= transcript=
                ├── k3.status             append-only, crew ghi bằng echo
                └── k3/
                    ├── brief.md          prompt đầu của crew
                    ├── report.md         deliverable
                    ├── usage.jsonl       sau MVP
                    └── transcript/       copy lúc teardown, sau MVP
```

Quy ước:

- Repo nằm ngang hàng với `.matev2/`, không bắt buộc nằm trong thư mục con nào.
- Worktree gom ở `.worktrees/<project>-<crew>/`. Codex vẫn hỏi trust cho worktree mới (đo 2026-09-17), nên spawn luôn chạy settle step.
- `.meta` là `key=value` mỗi dòng một khoá. `.status` là text thuần `state: một dòng`.
- Xoá `.matev2/` là xoá toàn bộ state của app. `crews/<id>/` giữ mãi sau teardown, chỉ người dùng xoá tay.
- Mọi đường dẫn ghi ra phải nằm trong workspace; symlink trỏ ra ngoài bị từ chối.

## 4. Giao tiếp

Học từ firstmate, giữ đúng bốn cơ chế và không thêm:

| Chiều | Cơ chế |
| --- | --- |
| Crew → Mate | `echo "state: một dòng" >> $MATEV2_STATUS`. Ba verb crew được dùng: `working`, `needs-decision`, `wait-mate` (mục 4b). Báo thưa. Nội dung dài nằm trong file, status là con trỏ. |
| Mate → Crew | `matev2 send <crew> "một dòng"` gõ vào pane crew, kiểm chứng composer trống trước, retry Enter cho tới khi composer trống. Dài hơn thì ghi file và trỏ crew đọc. |
| Đọc crew | `matev2 peek <crew>` đọc 40 dòng cuối pane. `matev2 state <crew>` trả một dòng state deterministic từ busy regex của pane và dòng status cuối. |
| Đánh thức Mate | Observer trong console theo dõi status file, hash pane, busy regex, inventory Herdr. Chỉ đánh dấu là đáng chú ý khi có verb `needs-decision`/`wait-mate` hoặc khi chính nó mở incident (`blocked`). |

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
`wait-mate` không nằm trong inbox: cột STATE của bảng crew đã mang nó, và người dùng vẫn Enter vào pane crew để đối thoại tiếp bất cứ lúc nào (quyết định 2026-09-18).

**Đóng crew là quyết định của người dùng hoặc Mate, không phải của crew** (quyết định 2026-09-18).
`wait-mate:` chỉ là báo cáo của crew.
Task kết thúc khi `matev2 crew stop` chạy: scout đóng khi người dùng nhận report và hài lòng (chủ động bảo Mate đóng), ship đóng khi branch đã merge (người dùng merge, sau này `matev2 merge`).
Crew đã đóng (`state=finished|failed` trong meta) biến khỏi cây console, khỏi inbox và khỏi `crew list` mặc định (`--all` để xem); `crews/<id>/` giữ nguyên.
Cây console vì thế chỉ hiện việc đang chạy, kể cả crew đã nói `wait-mate` mà chưa ai đóng.
Phím `l` dưới focus box bật `[all]`, hiện lại toàn bộ log để debug; mặc định tắt và không lưu lại.

Gửi vào pane Mate là trường hợp đặc biệt vì người dùng cùng sở hữu composer.
Chỉ gửi khi người dùng bấm (chế độ giám sát) hoặc khi chế độ tự động đang bật.
Mọi dòng app tự gửi vào Mate có prefix sentinel `⟦matev2⟧ ` để Mate phân biệt với người gõ.
Byte điều khiển `0x1f` từng được dùng cho việc này nhưng không tới được payload `UserPromptSubmit` của Claude (đo ngày 2026-09-17, Herdr 0.8.2, Claude Code 2.1.274); sentinel in được thì sống sót nguyên vẹn.

## 4b. Máy trạng thái của crew

Chốt 2026-09-18 sau khi test tay M2. Bảy trạng thái, ba người đặt, mỗi trạng thái đúng một người được phép đặt.

| Trạng thái | Ai đặt | Ghi ở đâu | Nghĩa |
| --- | --- | --- | --- |
| `spawned` | Mate, qua `crew spawn` | `crews/<id>.meta` `state=spawned` | Đã spawn, crew chưa ghi dòng nào. |
| `working` | Crew | `crews/<id>.status` | Đang làm; dòng mô tả pha. |
| `needs-decision` | Crew | `.status` | Crew hỏi và dừng turn; cần Mate hoặc người dùng trả lời. Vào inbox. |
| `wait-mate` | Crew | `.status` | Crew đã làm hết phần mình (xong, hoặc không xong được và nói vì sao) và giao lại cho Mate. Không vào inbox. |
| `blocked` | Observer | `incidents.log`, không đụng `.status` | Crew không tự nói được nữa: pane treo, agent biến mất khỏi Herdr, kẹt dialog, hết token. Vào inbox. Gỡ khi observer thấy crew chạy lại. |
| `finished` | Mate hoặc người dùng, qua `crew stop` | `.meta` `state=finished` | Trạng thái cuối. Scout: người dùng nhận report và bảo đóng. Ship: branch đã merge. |
| `failed` | Mate hoặc người dùng, qua `crew stop --discard`; app, khi spawn thất bại | `.meta` `state=failed` | Trạng thái cuối. Việc bị bỏ, hoặc crew chưa bao giờ lên. |

Quy tắc:

- Crew không bao giờ tự nói `blocked`, `finished`, `failed`, `done`. Thiếu key, thiếu quyền, phân vân hướng đi là `needs-decision`, vì crew còn hỏi được. Không hoàn thành được là `wait-mate: không làm được vì X`, Mate quyết `failed` hay gửi thêm một dòng cho làm tiếp.
- Trạng thái hiển thị của một crew suy ra theo đúng thứ tự: `.meta` có `state=finished|failed` thì lấy nó; không thì có incident mở trong `incidents.log` thì `blocked`; không thì verb cuối trong `.status`; không có dòng nào thì `spawned`. Không còn `reserved`, `stopped`, `parked`, `done`, `unknown` như trạng thái.
- Bên cạnh trạng thái luôn có một cột sức khỏe do quan sát, không phải trạng thái: agent còn trong Herdr không, composer bận hay rảnh, pane đứng yên bao lâu. `matev2 state` in `state: <trạng thái> · health: <quan sát>`. Herdr `agent_status` không được dùng cho cả hai cột (quyết định 8).
- Inbox: `needs-decision` chưa ai trả lời, và incident đang mở. Luật rời inbox giữ nguyên (dòng status mới của cùng crew, hoặc `sent.log` có dòng tới `crew:<id>` sau câu hỏi); incident rời inbox khi observer ghi dòng `resolved` cho nó. Vì observer không ghi vào `.status`, một incident không bao giờ làm câu hỏi của crew rời inbox.
- Cây console và `crew list` hiện mọi crew chưa `finished`/`failed`; `crew list --all` hiện cả đã đóng.
- `crew stop` từ chối trước khi giết agent nếu branch chưa landed vào default branch và không có `--discard`. Không còn kết cục "agent đã chết, worktree giữ lại". Scout có branch không commit gì nên luôn sạch.
- Spawn thất bại (dialog không nhận ra, agent không lên, trust không qua) ghi `state=failed` và lý do vào `.meta` ngay tại chỗ, không để lại thư mục mồ côi ở `spawned`.

`incidents.log` là file append-only, tab-separated, mỗi dòng `RFC3339 \t crew \t kind \t open|resolved \t text`.
Kind: `stale` (status và pane không đổi quá ngưỡng mà composer không bận), `runtime_lost` (Herdr không còn agent), `wedged` (gửi vào pane không kiểm chứng được quá lâu), `budget` (sau MVP).
Một incident đang mở khi dòng cuối của cặp (crew, kind) là `open`.
Chỉ observer ghi file này; `box.Load` đọc nó và gộp vào view cùng `.status` và `sent.log`.
Observer sống trong tiến trình console: đóng console thì không ai canh crew và không có `blocked` mới; chấp nhận cho MVP.

## 5. Hai chế độ

Cờ là file `.matev2/projects/<p>/mate/.auto`.

Chế độ `manual` (giám sát, mặc định; nhãn đổi từ `supervised` ngày 2026-09-19 vì người dùng không hiểu chữ đó):

- Không dòng nào tự đi vào pane Mate.
- Trên một mục trong inbox (quyết định 2026-09-19: box là chỗ để hành động, không phải chỗ để đọc):
  - Enter, hoặc một click vào thân hàng, mở pane của chính crew mà mục đó nêu tên - đúng như Enter trên hàng crew trong cây; mục có crew là `mate` (incident `wedged` của daemon) mở pane Mate. Trong session view thì stream đang mở được đóng trước, rồi mới mở stream của crew, không bao giờ có hai PTY cùng lúc.
  - `a`, hoặc nút `[assign]` trên hàng, gửi vào Mate một dòng `⟦matev2⟧ resolve: <crew> asked: "<status text, một dòng, cắt ở ~200 rune>" — read <đường dẫn tuyệt đối tới status file>, decide, and answer with matev2 send <project> <crew> "<one line>"` (đường dẫn tuyệt đối vì cwd của Mate là thư mục workspace của nó, không phải thư mục project, nên đường dẫn tương đối như `crews/<id>.status` không trỏ tới đâu cả); với incident là `resolve: incident <kind> <crew> — <text>`. Chữ trên nút là `assign` vì đó là việc người dùng làm (giao đi); dòng gửi cho Mate vẫn là `resolve:`, đúng như manual của Mate.
  - `l` đổi giữa hai bộ lọc; `j`/`k` và phím mũi tên di chuyển; `o` mở Actions menu của pane đang xem; `Esc`/`Tab` rời khỏi zone. Không còn `r` (reply) và `p` (peek): muốn nói chuyện với crew thì vào thẳng pane của nó.
- Header của rail chỉ còn hai nút lọc: `[waiting]` (inbox, mặc định) và `[all]` (toàn bộ log), nút đang bật tô accent, nút kia mờ; dòng đếm ngay dưới nói bằng chữ đang xem cái nào (`N waiting`, hay `all · N entries`), nên terminal đơn sắc vẫn đọc được. Không dùng chữ "unread": console không lưu trạng thái đã đọc, và một mục người dùng đã nhìn nhưng chưa giao vẫn đang chờ ai đó xử lý.
- `[← project]`, `[manual|auto]`, `[restart mate]`, `[clear composer]` rời khỏi header (quyết định 2026-09-19). `Esc` vẫn rời session (dòng hint ghi `Esc project`); mode vẫn ở ô MODE và phím `m`; restart Mate và clear composer nằm trong Actions menu của hàng Mate (`a` trong cây, `o` trong box zone), giữ nguyên bước xác nhận và request cũ.
- `resolve` chỉ giao việc, không đóng câu hỏi. Mục rời inbox khi crew thật sự nhận được câu trả lời - `matev2 send` của Mate ghi `Source: mate` vào `sent.log` - hoặc khi crew tự ghi dòng status mới.

Chế độ tự động (daemon `internal/autopilot`, chốt 2026-09-18 ở task 19):

- Daemon sống trong tiến trình console, cạnh observer. Đóng console thì không có dòng nào tự đi vào pane Mate; đó là hình dạng thành thật của một tính năng mà cả điểm là nó gõ vào composer của người khác.
- Mỗi 90 giây (đồng hồ tiêm được), với mỗi project có `mate/.auto`, daemon gom những gì mới kể từ con trỏ: inbox chưa giải quyết (`needs-decision` của crew, incident đang mở) cộng dòng status **mới nhất** của mỗi crew đang mở nếu dòng đó là `wait-mate`.
  `wait-mate` không nằm trong inbox (mục 4b) nhưng ở chế độ tự động không có người đọc bảng crew, nên Mate là người phải xem nó.
  Chỉ lấy dòng mới nhất: một `wait-mate` mà crew đã ghi đè lên bằng dòng khác là lịch sử, và luật này chặn luôn trường hợp bật `.auto` trên một project đã chạy cả tuần.
- Không có gì mới thì không gửi. Daemon không phải heartbeat.
- Có gì mới thì thành **đúng một dòng**, gửi bằng `send.Send` (kiểm chứng composer, không bao giờ `herdr agent prompt`):

  ```text
  ⟦matev2⟧ digest: <k> item(s) — <mục> · <mục> · … — status files under <đường dẫn tuyệt đối tới crews/>; act per AGENTS.md section 10
  ```

  Mỗi `<mục>` là một trong ba dạng, và chỉ ba dạng đó:

  ```text
  <crew> needs-decision: "<text crew viết, ≤ 120 rune, gộp khoảng trắng, " đổi thành '>"
  <crew> blocked: <incident kind>, quiet for <thời gian>
  <crew> wait-mate: "<text crew viết, cùng luật cắt>"
  ```

  `<k>` luôn là số mục thật. Dòng chỉ trải tối đa 5 mục rồi ghi `· +<n> more`, vì một composer nhận dòng quá dài sẽ wrap và chính `send.Send` không đọc lại được.
  `quiet for` đo từ dòng `open` của incident, tức là cận dưới: observer chỉ mở `stale` sau khi ngưỡng của nó đã trôi qua.
  Incident không gán được cho crew nào in `-` ở chỗ tên crew; app không bịa ra tên.
  Đường dẫn là tuyệt đối vì cwd của Mate là `mate/` của chính nó (cùng lý do với dòng `resolve:`).
  Ví dụ:

  ```text
  ⟦matev2⟧ digest: 3 item(s) — k3 needs-decision: "pick A or B" · k9 blocked: stale, quiet for 4m0s · k7 wait-mate: "report.md is ready" — status files under /w/.matev2/projects/shop/crews; act per AGENTS.md section 10
  ```

- Mate thấy marker thì tự quyết theo policy trong AGENTS.md. Merge vẫn chờ người dùng trừ khi project bật `yolo`.
- Con trỏ digest là `mate/.auto-cursor`: một offset byte cho mỗi file nguồn, ghi ở dạng `<đường dẫn tương đối project>=<offset>`.
  Nó là file riêng chứ không nằm trong `.auto` vì `.auto` có ba người xoá (hook của Mate, phím `m`, tay người dùng); con trỏ nằm trong đó sẽ chết theo mỗi lần tắt auto, và lần bật lại sẽ gửi lại toàn bộ câu hỏi cũ.
  Chỉ một lần gửi đã kiểm chứng mới đẩy con trỏ, nên digest bị từ chối được chào lại nguyên vẹn ở vòng sau chứ không mất.
  `sent.log` ghi trước, con trỏ ghi sau: hỏng ở giữa thì tốn một digest lặp, ngược lại thì mất mục.
- Hook `UserPromptSubmit` của Mate thấy prompt không có marker thì xoá `.auto`. Daemon đọc lại cờ ở đầu lượt của mỗi project **và** ngay trước khi gõ, nên người dùng giành composer giữa lượt không bị máy trả lời ngay sau đó.
- Gửi không tới được Mate quá 5 phút thì mở incident `wedged` với crew là `mate`; một lần gửi thành công đóng nó. Incident là nửa bền vững: dòng footer chết theo console, inbox thì không.
  Đồng hồ 5 phút chạy cho cả hai kiểu hỏng - composer bận/đang có chữ của người dùng, và Mate không chạy - vì auto mode âm thầm không giao gì suốt một tiếng đúng là trạng thái incident này sinh ra để lộ; text của incident luôn nói rõ là nửa nào hỏng.
  Digest bỏ qua chính incident `(mate, wedged)` của mình: báo cáo một lỗi giao hàng qua chính đường giao hàng vừa hỏng là vô nghĩa, và người dùng đã thấy nó trong inbox.
- Lý do từ chối của lượt gần nhất hiện trên dòng thông báo của console, một lần mỗi lượt chứ không phải mỗi mục, và tự biến mất ở lượt gửi được. Ô `MODE` thêm `auto · sent 14:32:10` khi daemon đã gửi ít nhất một lần.

## 6. Trí nhớ của Mate

| Lớp | File | Ai viết |
| --- | --- | --- |
| Operating manual | `mate/AGENTS.md` | App, từ template `embed`, sinh lại mỗi lần start |
| Quy tắc người dùng | `.matev2/WORKSPACE.md` | Người dùng |
| Bối cảnh project | `projects/<p>/PROJECT.md` | Crew scout viết bản đầu, người dùng sửa, Mate bổ sung |
| Trí nhớ Mate | `mate/memory.md`, `mate/backlog.md` | Mate qua `matev2 remember`, `matev2 backlog` |
| Hội thoại | Session harness | Harness; app lưu `session_id` để resume |

AGENTS.md không inline lớp khác mà bảo Mate đọc chúng ở bootstrap.
Crew không đọc trí nhớ của Mate; Mate viết vào brief những gì crew cần.
Tri thức về code đi vào AGENTS.md của repo qua PR của crew.

## 7. Bài học v1 phải giữ

- Trust dialog: Claude highlight mặc định là "No, exit"; gửi Enter mù là chết agent. Nhận diện dialog theo shape, một phím một lần, đọc lại giữa các lần.
  Worktree liên kết KHÔNG thừa kế trust với Codex: đo 2026-09-17 (task 11, Codex 0.154), crew trong `.worktrees/<p>-<id>` vẫn hiện directory-trust dialog vì Codex xác nhận theo từng absolute path.
  ADR 0028 của v1 nói ngược lại; settle step là bắt buộc cho crew, không phải thủ tục.
- Startup không chỉ có một modal. Đo 2026-09-18 (codex-cli 0.154.0 đã cài, 0.155.0 vừa ra): Codex vẽ prompt cập nhật ba lựa chọn TRƯỚC trust dialog, mọi `crew spawn` chết với `target_blocked: startup screen not recognised`.
  Settle phải xử lý một chuỗi dialog (cập nhật → trust → composer), mỗi cái vẫn một phím một lần và xác minh highlight trước Enter, với trần 3 dialog mỗi lần khởi động.
  Trả lời `3. Skip until next version` (không phải `2. Skip`, sẽ hiện lại ngay lần sau; không phải `1. Update now`, chạy `npm install` dưới agent).
  Phòng ngừa: launch mang `-c check_for_update_on_startup=false` (khoá có tài liệu, Codex nhận dưới `--strict-config`) nên prompt không được vẽ; bộ nhận dạng vẫn giữ làm lớp phòng thủ, vì cờ là dự đoán còn pane mới là phép đo.
- `herdr agent prompt` báo thành công dù prompt rơi vào modal hoặc nối vào text gõ dở. Dùng `--wait` và kiểm chứng composer.
- Hook `Stop` của Claude và `notify` của Codex không bắn mọi turn. Fallback theo thời gian, `unknown` là trạng thái hợp lệ.
- Test xanh với fake Herdr không chứng minh gì. Mỗi milestone có live test trên Herdr lab session riêng, tên `TestLive*`, chạy khi `MATEV2_LIVE=1`.
- Lệnh báo thành công phải kiểm tra lại hệ thống thật, không tin handle cũ.
- Live test runtime cần một lab session do người chạy cấp qua `MATEV2_HERDR_LIVE_SESSION=fm-lab-...` và `TMPDIR` không đi qua symlink (macOS `/var` → `/private/var`). Herdr báo cwd của pane đã resolve symlink, nên guard so cwd trong `runtime` dùng `samePath` thay vì so chuỗi (sửa 2026-09-17, v1 có cùng lỗi).
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
- Sentinel `⟦matev2⟧ ` sống sót cả với dòng digest chứ không chỉ dòng ngắn.
  Cùng lần đo: dòng `digest: 1 item(s) — k3 needs-decision: "pick A or B" — status files under /private/tmp/…/crews; act per AGENTS.md section 10` (có `—`, `·`, dấu nháy kép và một đường dẫn tuyệt đối dài) tới `UserPromptSubmit` của Claude nguyên vẹn, nên hook ghi `Source: app` và không xoá `.auto`.
  Bằng chứng dùng được là `sent.log` có **hai** bản cùng một dòng - một do daemon ghi sau khi composer sạch, một do hook ghi khi model đọc được - còn một bản chỉ chứng minh chữ tới pane.
  Ngay sau đó một dòng người dùng gõ không có marker xoá `.auto` trong vòng poll đầu tiên, và vòng tick kế tiếp không gửi gì dù đã có câu hỏi mới chờ sẵn.
- `MATEV2_CALLER` phải đi qua đường `workspace create --env`, không phải `tab create --env`, vì tab của Mate là root pane của workspace được đổi tên.
  Đo 2026-09-19 (task 22): `Herdr.CreateAgentTab` từ chối thẳng `TabSpec.Env` trên nhánh rename ("pane environment is injected by workspace create --env"), nên chỗ duy nhất đặt được biến cho pane Mate là `ensureProjectWorkspace`; crew thì ngược lại, luôn là `tab create` nên `crewPaneEnv` nhận thêm một dòng.
  Hệ quả phải nhớ: Mate chỉ nhận biến ở lần tạo workspace đầu tiên. Đó là lý do mặc định của `CallerFromEnv` là `user` chứ không phải từ chối - thiếu biến nghĩa là "không phải Mate", và console tự truyền `CallerUser` như hằng số thay vì đọc env, vì console mở từ trong pane Mate sẽ thừa kế `MATEV2_CALLER=mate` và tự từ chối phím bấm của chính người dùng.
- Merge thành công không ghi gì vào `sent.log`: `sent.log` là những dòng gõ vào pane, mà merge không gõ vào pane nào.
  Bằng chứng live cho "Mate tự merge" vì thế là pane của Mate cộng với `crews/<id>.meta` = `finished` và default branch đã tiến - không có tiến trình nào khác trong test merge được.
  Đo 2026-09-19, `TestLiveMateMergesUnderYolo` (Claude Code 2.1.278, codex-cli, Herdr 0.8.2), 87 giây: Mate đọc `.status` thật, đọc brief, `peek`, đọc diff, tự nói "Yolo is on, so I'll land it", rồi chạy đúng một `matev2 merge shop k3` và nhận lại đúng một dòng `shop/k3: merged 1 commit(s) into main (e161a9d..df7e0de); crew finished, worktree and branch removed`.
  `TestLiveMergeFromConsoleFinishesTheCrew` (không có Mate, merge qua `consoleAction`) mất 50 giây.
- Mục 9 ("Waiting is your job") và mục 10 của manual Mate mâu thuẫn nhau về việc có nên tự poll `state` sau một digest hay không, sót lại từ trước khi observer và daemon (task 18, 19) tồn tại: mục 9 vẫn viết "Nothing wakes you on its own today" như thể chưa ai canh crew.
  Sửa ở task 20: mục 9 chỉ còn nói tới chế độ giám sát (`sleep 20` là vòng của chế độ đó), mục 10 nói rõ Mate dừng turn ngay sau khi hành động trên một digest - daemon là bên canh giữ tiếp theo, không phải Mate tự poll.
  Đo 2026-09-18, `TestLiveAutoPolicyMateAnswersADigest`: một crew Codex hỏi `needs-decision: choose colour red or blue for the button`, digest tới Mate Claude, Mate đọc `.status` thật (không đoán từ đoạn cắt ≤120 rune trên dòng digest) rồi trả lời crew bằng đúng một `matev2 send` (`"Use blue for the button."`), crew ghi `wait-mate: chose blue`, và Mate không tự `crew stop` - `crews/k3.meta` vẫn `state=spawned` sau khi Mate trả lời, đúng luật mục 4b rằng đóng crew là lời của người dùng.
  Bản prose viết một lần đã đúng ngay ở lần chạy live đầu tiên, không cần sửa rồi chạy lại.

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
cmd/matev2/              một binary: `matev2 <workspace>` mở console, `matev2 <lệnh>` cho agent
internal/config/         build metadata, defaults
internal/ui/console/     copy v1
internal/query/          kiểu DTO console cần, backend mới điền
internal/store/          đọc ghi .matev2/, khoá append, layout, ranh giới đường dẫn
internal/box/            gộp status + sent.log + incident thành view
internal/send/           gửi một dòng vào pane agent qua Herdr, kiểm chứng composer
internal/watch/          observer và triage
internal/autopilot/      daemon chế độ tự động: digest 90 giây, gửi có kiểm chứng vào pane Mate
internal/spawn/          start Mate, spawn Crew
internal/runtime/        copy v1
internal/harness/        copy v1
internal/process/        copy v1
assets/                  AGENTS.md của Mate, brief.md, skills, hook scripts
```

## 10. Danh sách task

∥ nghĩa là có thể chạy song song với task trước nó.

### M0. Nền

| # | Task | Xong khi |
| --- | --- | --- |
| 01 | Khởi tạo module Go, `cmd/matev2`, Makefile `check`, gotestreport với `TestLive*` là skip duy nhất được phép | `make check` xanh. Đã xong 2026-09-17. |
| 02 | Copy `internal/runtime`, `internal/process`, `internal/harness` launch spec và startup prompt classifier từ v1. Đổi import, giữ test và testdata. Live test đổi tên thành `TestLive*` và gate bằng `MATEV2_LIVE=1`. Đổi tên biến môi trường `MATE_*` thành `MATEV2_*`. | Test unit pass, live test skip có kiểm soát. Đã xong 2026-09-17. |
| 03 ∥ | `internal/store`: layout `.matev2/`, đọc ghi `workspace.yaml`, `project.yaml`, `.meta`, append `.status` và `sent.log` có flock, ranh giới đường dẫn trong workspace. | Test với thư mục tạm; symlink ra ngoài workspace bị từ chối. Đã xong 2026-09-17. |
| 04 ∥ | `matev2 init`, `matev2 project add/list/remove`. Repo phải là thư mục con của workspace và là git repo. | Đăng ký hai project trên thư mục thật. Đã xong 2026-09-17. |

### M1. Console và Mate sống

| # | Task | Xong khi |
| --- | --- | --- |
| 05 | Copy `internal/ui/console` và DTO `internal/query`, cắt màn hình attempts và incident overlay, nối gallery và project view vào `store`. | Golden test còn giữ pass, `matev2 .` hiện hai project. Đã xong 2026-09-17. |
| 06 ∥ | Template AGENTS.md của Mate bản đầu, fork từ firstmate và cắt tmux, treehouse, no-mistakes, secondmate, X mode. CLAUDE.md `@AGENTS.md`. Sinh file bằng `embed`. | Snapshot test, review tay. Đã xong 2026-09-17. |
| 07 | `internal/spawn` start Mate: tạo `mate/`, Herdr workspace và tab, `agent start`, trust dialog, ghi `mate.meta`. | Live test: Mate Claude trả lời đúng vai. Đã xong 2026-09-17. |
| 08 | Hook `UserPromptSubmit` và `Stop` cho Mate: ghi `sent.log`, lưu `session_id`, xoá `.auto` khi prompt không có marker. | Live test: `sent.log` có dòng user và dòng mate. Đã xong 2026-09-17. |
| 09 | Session view stream mode nối pane Mate và pane crew (Enter trên hàng crew mở terminal của crew), phím detach, header hiện chế độ. | Live test E2E. Mate xong 2026-09-17; crew nối 2026-09-18 sau khi test tay phát hiện Enter trên crew rơi vào fallback `matev2 attach` không tồn tại. |
| 10 | Mate stop và restart với `--resume session_id`. | Live test: Mate nhớ câu trước. Đã xong 2026-09-17. |

### M2. Crew và chế độ giám sát

| # | Task | Xong khi |
| --- | --- | --- |
| 11 | Brief template, skill `brief-writing`, `matev2 crew spawn`: worktree, tab, launch, trust, brief làm prompt đầu, ghi `.meta`. | Live test: crew Codex nhận brief. Đã xong 2026-09-17. |
| 12 ∥ | `internal/send`: phân loại composer `empty/pending/unknown`, gõ một lần, retry Enter, settle cho slash command, prefix `0x1f` tuỳ chọn. | Live test ba case: trống, text dở, popup slash. Đã xong 2026-09-17. |
| 13 ∥ | `matev2 send`, `matev2 peek`, `matev2 state`. | Unit với pane giả, live với crew thật. Đã xong 2026-09-17. |
| 14 | `internal/box`: gộp thành view, phân loại verb. | Unit trên fixture. Đã xong 2026-09-17. |
| 15 | Box rail trong console: Enter, `r`, `p`. | Live vòng: crew hỏi, người dùng `r`, crew tiếp tục. Đã xong 2026-09-17. |
| 16 | `matev2 crew stop` và teardown: xác nhận agent chết, xoá tab và worktree, giữ `crews/<id>/`. | Live test. Đã xong 2026-09-17. |
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

- Merge là hành động kết thúc một ship, nên `matev2 merge` thành công thì tự chạy `crew stop` và ghi `state=finished`. Đây là chỗ duy nhất trạng thái cuối được đặt mà không phải gõ `crew stop` trực tiếp, nhưng vẫn là quyết định của người dùng, hoặc của Mate khi project bật `yolo`.
- `needs-rebase` không phải trạng thái crew. Nó là kết quả của lệnh merge: branch không fast-forward được vào default branch thì lệnh từ chối, in nguyên nhân, không đổi gì. Mate gửi crew một dòng bảo rebase, crew về `working`.
- Ai gọi merge: người dùng từ CLI hoặc từ console (Actions trên hàng crew đang `wait-mate`), hoặc Mate qua `matev2 merge` khi `yolo` bật. Khi `yolo` tắt, một lời gọi có `MATEV2_CALLER=mate` (spawn đặt biến này trong pane Mate) bị từ chối với thông điệp rõ. `yolo` là `project.yaml`, có sẵn `project add --yolo`; thêm `matev2 project yolo <name> on|off`.
- `backlog.md` là trí nhớ Mate tự ghi, app không ghi vào đó. Task 23 thu hẹp thành `matev2 backlog <project>`: in một bảng chỉ đọc từ `.meta` và status (crew mở, trạng thái, task, branch, tuổi), Mate đọc để đối chiếu khi bootstrap.

| # | Task | Xong khi |
| --- | --- | --- |
| 21 ∥ | `matev2 diff <project> <crew>`: `git diff <default>...<branch>` và `git log --oneline <default>..<branch>` của worktree crew, chỉ đọc, in ra stdout; `--stat` cho bản tóm tắt. Console: Actions trên hàng crew có `diff`, mở overlay cuộn được trong project frame với cùng nội dung; Enter/Esc đóng. | Unit trên repo tạm (branch trước, sau, không commit, worktree bẩn cũng hiện). Live: crew đã `wait-mate` xem được diff từ console. |
| 22 ∥ | `matev2 merge <project> <crew>`: chỉ `git merge --ff-only` vào default branch trong repo chính, từ chối khi worktree crew bẩn, khi branch không ancestor-clean (`needs-rebase`), khi caller là Mate mà `yolo` tắt; thành công thì `crew stop` → `finished` và in một dòng kết quả. `matev2 project yolo <name> on\|off`. Console: Actions trên hàng crew `wait-mate` có `merge` với bước xác nhận. Manual Mate mục 9 "Delivery": lệnh merge đã có; khi `yolo` tắt báo captain, khi bật thì merge rồi báo. | Unit trên repo tạm cho từng nhánh từ chối. Live: một crew Codex thật đi từ brief tới `finished` qua merge từ console; và một lần Mate `yolo` tự merge. Đã xong 2026-09-19. |
| 23 | `matev2 backlog <project>`: bảng chỉ đọc từ `.meta` và status; manual mục 3 dùng nó thay `crew list` khi bootstrap. App không ghi `backlog.md`. | Unit với fixture; restart Mate thì bảng khớp cây console. Đã xong 2026-09-19. |
| 24 | Acceptance end-to-end trên hai project: mỗi project một task ship và một task scout, chế độ manual cho project thứ nhất, auto cho project thứ hai, đi tới `finished` qua merge; ghi `docs/evidence/m4-acceptance-<ngày>.md`. Trả nợ: composer classifier nhận màn hình chào của Claude Code ở kích thước pane console để `[assign]` chạy được trên Mate mới khởi động. | Evidence đầy đủ, `make check` xanh, không sửa tay giữa chừng. |

Nợ kỹ thuật đã biết:

- ~~`harness.Claude.BuildLaunchSpec` bắt buộc có `ContextPath`, nên `spawn` truyền chính `mate/AGENTS.md` qua `--append-system-prompt-file` trong khi `CLAUDE.md` cũng đã nạp nó từ cwd. Manual vào context hai lần.~~ Trả xong ở task 17: `AgentSpec.ManualInCwd` cho phép `ContextPath` rỗng, launch spec dùng `DeliveryCwdManual` và không truyền cờ context nào; `spawn` bật cờ đó cho Mate Claude. Với Codex thì không có nợ: Codex không tự nạp `CLAUDE.md` từ cwd, cơ chế nạp duy nhất của nó chính là file ở cwd, và nó ưu tiên `AGENTS.override.md` hơn `AGENTS.md`, nên manual vào context đúng một lần. `spawn` vẫn ghi `AGENTS.override.md` cho Mate Codex: đó là tên file Codex đọc, và giữ nguyên quy tắc "override che AGENTS.md tracked" mà crew worktree bắt buộc phải có.

- `internal/send` chưa nhận màn hình chào của Claude Code ở kích thước pane mà console stream resize tới: `[assign]` trên Mate chưa có turn nào bị từ chối `agent is showing a screen mate cannot name` (đo 2026-09-19). Trả ở task 24.

Sau MVP: token monitor gồm locator theo `session_id`, copy parser transcript v1, `usage.jsonl` và view, tín hiệu `budget`.
