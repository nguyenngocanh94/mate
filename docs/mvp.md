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
| Crew → Mate | `echo "state: một dòng" >> $MATEV2_STATUS`. Năm state: `working`, `needs-decision`, `blocked`, `done`, `failed`. Báo thưa. Nội dung dài nằm trong file, status là con trỏ. |
| Mate → Crew | `matev2 send <crew> "một dòng"` gõ vào pane crew, kiểm chứng composer trống trước, retry Enter cho tới khi composer trống. Dài hơn thì ghi file và trỏ crew đọc. |
| Đọc crew | `matev2 peek <crew>` đọc 40 dòng cuối pane. `matev2 state <crew>` trả một dòng state deterministic từ busy regex của pane và dòng status cuối. |
| Đánh thức Mate | Observer trong console theo dõi status file, hash pane, busy regex, hook turn-end. Chỉ đánh dấu là đáng chú ý khi có verb `needs-decision/blocked/done/failed` hoặc crew im lặng mà không "provably working". |

Câu hỏi của crew không có vòng đời.
Crew append `needs-decision:` rồi dừng turn.
Ai đó gõ một dòng trả lời vào pane crew.
Với crew đó chỉ là một prompt mới.
Không interaction row, không wait, không correlation id, không ack.

Message box trong console là view gộp theo thời gian của `crews/*.status`, `sent.log`, và incident của observer.
Không có file box riêng.

Gửi vào pane Mate là trường hợp đặc biệt vì người dùng cùng sở hữu composer.
Chỉ gửi khi người dùng bấm (chế độ giám sát) hoặc khi chế độ tự động đang bật.
Mọi dòng app tự gửi vào Mate có prefix byte `0x1f` để Mate phân biệt với người gõ.

## 5. Hai chế độ

Cờ là file `.matev2/projects/<p>/mate/.auto`.

Chế độ giám sát (mặc định):

- Không byte nào tự đi vào pane Mate.
- Trên một dòng trong box: Enter gửi `\x1f signal: crews/<id>.status` vào Mate; `r` trả lời crew trực tiếp qua `matev2 send`; `p` peek pane crew.

Chế độ tự động:

- Daemon trong console gom tín hiệu đáng chú ý trong cửa sổ 90 giây thành một digest một dòng, gửi vào Mate có kiểm chứng với prefix `0x1f`.
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
- `herdr agent prompt` báo thành công dù prompt rơi vào modal hoặc nối vào text gõ dở. Dùng `--wait` và kiểm chứng composer.
- Hook `Stop` của Claude và `notify` của Codex không bắn mọi turn. Fallback theo thời gian, `unknown` là trạng thái hợp lệ.
- Test xanh với fake Herdr không chứng minh gì. Mỗi milestone có live test trên Herdr lab session riêng, tên `TestLive*`, chạy khi `MATEV2_LIVE=1`.
- Lệnh báo thành công phải kiểm tra lại hệ thống thật, không tin handle cũ.
- Live test runtime cần một lab session do người chạy cấp qua `MATEV2_HERDR_LIVE_SESSION=fm-lab-...` và `TMPDIR` không đi qua symlink (macOS `/var` → `/private/var`). Herdr báo cwd của pane đã resolve symlink, nên guard so cwd trong `runtime` dùng `samePath` thay vì so chuỗi (sửa 2026-09-17, v1 có cùng lỗi).
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
| 09 | Session view stream mode nối pane Mate, phím detach, header hiện chế độ. | Live test E2E. Đã xong 2026-09-17. |
| 10 | Mate stop và restart với `--resume session_id`. | Live test: Mate nhớ câu trước. Đã xong 2026-09-17. |

### M2. Crew và chế độ giám sát

| # | Task | Xong khi |
| --- | --- | --- |
| 11 | Brief template, skill `brief-writing`, `matev2 crew spawn`: worktree, tab, launch, trust, brief làm prompt đầu, ghi `.meta`. | Live test: crew Codex nhận brief. Đã xong 2026-09-17. |
| 12 ∥ | `internal/send`: phân loại composer `empty/pending/unknown`, gõ một lần, retry Enter, settle cho slash command, prefix `0x1f` tuỳ chọn. | Live test ba case: trống, text dở, popup slash. Đã xong 2026-09-17. |
| 13 ∥ | `matev2 send`, `matev2 peek`, `matev2 state`. | Unit với pane giả, live với crew thật. |
| 14 | `internal/box`: gộp thành view, phân loại verb. | Unit trên fixture. Đã xong 2026-09-17. |
| 15 | Box rail trong console: Enter, `r`, `p`. | Live vòng: crew hỏi, người dùng `r`, crew tiếp tục. Đã xong 2026-09-17. |
| 16 | `matev2 crew stop` và teardown: xác nhận agent chết, xoá tab và worktree, giữ `crews/<id>/`. | Live test. |
| 17 | AGENTS.md: intake, ship/scout, brief, spawn, supervise. Skill `harness-adapters`, `stuck-crew-recovery`. | Acceptance: một yêu cầu đi đến `done:`. |

### M3. Chế độ tự động

| # | Task | Xong khi |
| --- | --- | --- |
| 18 | `internal/watch`: poll status, hash pane, busy regex, `signal/stale`, incident, beacon. | Unit với clock giả, live crew hang. |
| 19 | Daemon auto trong console: digest 90 giây, gửi có kiểm chứng với marker, wedged, dừng khi `.auto` mất, phím bật tắt. | Live: crew hỏi, Mate tự trả lời, người dùng gõ thì tự tắt. |
| 20 | Policy auto trong AGENTS.md. | Acceptance có kịch bản. |

### M4. Review và merge

| # | Task | Xong khi |
| --- | --- | --- |
| 21 | `matev2 diff <crew>` và view trong console. | Xem diff crew đã `done:`. |
| 22 | `matev2 merge <crew>` chỉ fast-forward, từ chối caller agent khi không `yolo`, báo `needs_rebase`. | Test repo tạm, live một crew. |
| 23 | `backlog.md` đồng bộ từ `.meta`, `matev2 backlog`. | Restart Mate thì backlog đúng. |
| 24 | Acceptance end-to-end trên hai project, ghi evidence. | Một task đi hết vòng trên cả hai chế độ. |

Nợ kỹ thuật đã biết:

- `harness.Claude.BuildLaunchSpec` bắt buộc có `ContextPath`, nên `spawn` truyền chính `mate/AGENTS.md` qua `--append-system-prompt-file` trong khi `CLAUDE.md` cũng đã nạp nó từ cwd. Manual vào context hai lần. Với Codex, `spawn` ghi thêm `AGENTS.override.md`. Sửa ở task 17 bằng cách cho phép launch spec không có context file khi cwd đã có manual.

Sau MVP: token monitor gồm locator theo `session_id`, copy parser transcript v1, `usage.jsonl` và view, tín hiệu `budget`.
