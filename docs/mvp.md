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

Lớp gộp giữ đủ mọi dòng, nhưng console chỉ hiển thị phần chưa được giải quyết - gọi là inbox: một status `needs-decision`/`blocked`, hoặc một incident, mà chưa có ai trả lời.
Một mục rời inbox khi crew đó ghi thêm một dòng status mới (luật chính xác, dựa trên thứ tự byte trong file), hoặc khi `sent.log` có một dòng gửi tới `crew:<id>` sau thời điểm của câu hỏi (luật xấp xỉ, vì status không có timestamp riêng).
`wait-mate` không nằm trong inbox: cột STATE của bảng crew đã mang nó, và người dùng vẫn Enter vào pane crew để đối thoại tiếp bất cứ lúc nào (quyết định 2026-09-18).

**Đóng crew là quyết định của người dùng hoặc Mate, không phải của crew** (quyết định 2026-09-18).
`wait-mate:` chỉ là báo cáo của crew.
Task kết thúc khi `matev2 crew stop` chạy: scout đóng khi người dùng nhận report và hài lòng (chủ động bảo Mate đóng), ship đóng khi branch đã merge (người dùng merge, sau này `matev2 merge`).
Crew đã đóng (`state=finished|failed` trong meta) biến khỏi cây console, khỏi inbox và khỏi `crew list` mặc định (`--all` để xem); `crews/<id>/` giữ nguyên.
Cây console vì thế chỉ hiện việc đang chạy, kể cả crew đã nói `wait-mate` mà chưa ai đóng.
Phím `a` dưới focus box bật `[all]`, hiện lại toàn bộ log để debug; mặc định tắt và không lưu lại.

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

Chế độ giám sát (mặc định):

- Không dòng nào tự đi vào pane Mate.
- Trên một mục trong inbox: Enter (`[resolve]`) gửi vào Mate một dòng `⟦matev2⟧ resolve: <crew> asked: "<status text, một dòng, cắt ở ~200 rune>" — read <đường dẫn tuyệt đối tới status file>, decide, and answer with matev2 send <project> <crew> "<one line>"` (đường dẫn tuyệt đối vì cwd của Mate là thư mục workspace của nó, không phải thư mục project, nên đường dẫn tương đối như `crews/<id>.status` không trỏ tới đâu cả); với incident là `resolve: incident <kind> <crew> — <text>`. `r` trả lời crew trực tiếp qua `matev2 send`; `p` peek pane crew.
- `resolve` chỉ giao việc, không đóng câu hỏi. Mục rời inbox khi crew thật sự nhận được câu trả lời - `matev2 send` của Mate ghi `Source: mate` vào `sent.log` - hoặc khi crew tự ghi dòng status mới.

Chế độ tự động:

- Daemon trong console gom tín hiệu đáng chú ý trong cửa sổ 90 giây thành một digest một dòng, gửi vào Mate có kiểm chứng với prefix sentinel `⟦matev2⟧ `.
- Mate thấy marker thì tự quyết theo policy trong AGENTS.md. Merge vẫn chờ người dùng trừ khi project bật `yolo`.
- Hook `UserPromptSubmit` của Mate thấy prompt không có marker thì xoá `.auto`. Console thấy file mất thì dừng daemon.
- Gửi thất bại quá lâu thì ghi flag wedged và hiện trong box.

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
- AGENTS.md của Mate gần chạm trần `project_doc_max_bytes` của Codex (32768). Đo 2026-09-18 ở task 18b: thêm khoảng 1.9 KB cho bảng bảy trạng thái làm `mate start --harness codex` chết với `required context exceeds delivery limit`, không phải vì render sai mà vì `spawn` từ chối thay vì để Codex cắt đuôi im lặng. Manual còn ghi 26 lần đường dẫn workspace, nên workspace sâu ăn thêm vài trăm byte nữa. Guard: `internal/mateassets` có test ngân sách 30000 byte, hỏng ngay tại nơi sửa template thay vì ở một live test cách đó ba package. Mọi lần thêm mục vào manual phải cắt chỗ khác.
- Nested-session env (đo 2026-09-17, Claude Code 2.1.274): một Claude Code đang chạy export `CLAUDECODE=1`, `CLAUDE_CODE_SESSION_ID`, và các biến `CLAUDE_CODE_*` khác cho process con. Nếu Herdr server được khởi động từ shell đó thì mọi pane kế thừa chúng, và Claude trong pane coi mình là session con: hook `Stop` vẫn bắn nhưng transcript không bao giờ được ghi. Runtime gỡ `harness.NestedSessionEnv` khỏi env của server lúc spawn và khỏi pane ngay trước `agent start`. Bài học chung: không tin env kế thừa qua Herdr server, mọi biến harness cần đúng phải được set hoặc unset ở pane.

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
| 19 | Daemon auto trong console: digest 90 giây từ inbox và incident, gửi có kiểm chứng với marker, wedged, dừng khi `.auto` mất, phím bật tắt. | Live: crew hỏi, Mate tự trả lời, người dùng gõ thì tự tắt. |
| 20 | Policy auto trong AGENTS.md, kể cả cách Mate xử lý `blocked` và `wait-mate` trong digest. | Acceptance có kịch bản. |

### M4. Review và merge

| # | Task | Xong khi |
| --- | --- | --- |
| 21 | `matev2 diff <crew>` và view trong console. | Xem diff crew đã `wait-mate:`. |
| 22 | `matev2 merge <crew>` chỉ fast-forward, từ chối caller agent khi không `yolo`, báo `needs_rebase`. | Test repo tạm, live một crew. |
| 23 | `backlog.md` đồng bộ từ `.meta`, `matev2 backlog`. | Restart Mate thì backlog đúng. |
| 24 | Acceptance end-to-end trên hai project, ghi evidence. | Một task đi hết vòng trên cả hai chế độ. |

Nợ kỹ thuật đã biết:

- ~~`harness.Claude.BuildLaunchSpec` bắt buộc có `ContextPath`, nên `spawn` truyền chính `mate/AGENTS.md` qua `--append-system-prompt-file` trong khi `CLAUDE.md` cũng đã nạp nó từ cwd. Manual vào context hai lần.~~ Trả xong ở task 17: `AgentSpec.ManualInCwd` cho phép `ContextPath` rỗng, launch spec dùng `DeliveryCwdManual` và không truyền cờ context nào; `spawn` bật cờ đó cho Mate Claude. Với Codex thì không có nợ: Codex không tự nạp `CLAUDE.md` từ cwd, cơ chế nạp duy nhất của nó chính là file ở cwd, và nó ưu tiên `AGENTS.override.md` hơn `AGENTS.md`, nên manual vào context đúng một lần. `spawn` vẫn ghi `AGENTS.override.md` cho Mate Codex: đó là tên file Codex đọc, và giữ nguyên quy tắc "override che AGENTS.md tracked" mà crew worktree bắt buộc phải có.

Sau MVP: token monitor gồm locator theo `session_id`, copy parser transcript v1, `usage.jsonl` và view, tín hiệu `budget`.
