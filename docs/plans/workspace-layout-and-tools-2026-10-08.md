# Phương án layout workspace theo project và registry công cụ

- Ngày: 2026-10-08.
- Trạng thái: nháp, chờ captain duyệt. Chưa có PR nào.
- Baseline đo: `517f425` trên `main`, Herdr 0.8.2, bd 1.3.1, bv 0.25.2, Fresh qua `brew install fresh-editor`.
- Nguồn quyết định: buổi review kiến trúc 2026-10-08 (report `.lavish/architecture-review.html`, không commit).
- Liên quan: [phương án registry harness](harness-registry-2026-09-30.md) là mẫu cho registry công cụ; M9 (project nhiều repo) và M17 (hồi phục liên kết với máy) là mẫu cho chuyển đổi một chiều.
- Spec cần cập nhật khi triển khai: [MVP](../mvp.md) mục 1 (Beads), 3 (layout), 9 (package), 10 (milestone mới); `docs/beads.md`; `GLOSSARY.md`.

## 1. Quyết định của captain

Ba câu chốt ngày 2026-10-08, ghi nguyên nghĩa:

1. Kho trạng thái của mate chỉ gồm `.mate/` (file phẳng) và SQLite (`mate.db`, log hoạt động). Mọi thứ khác không phải state của mate.
2. Beads, Beads Viewer và Fresh là plugin. Dữ liệu Beads không phải data của mate; nó nằm trong source của project. Mate chỉ gắn phím tắt và cung cấp plugin dạng runtime để quản lý dữ liệu đó. Hệ quả: Epic và Task là khái niệm của plugin; lõi mate chỉ biết Crew.
3. Project là một thư mục thật dưới workspace. Repo nằm trong thư mục đó. Project có hay không có repo đều có task được, vì tracker nằm ở cấp thư mục project. Workspace cũ phải migrate, chuyển đổi một chiều, không giữ hai layout song song.

Captain nói thêm: đã muốn đổi layout này từ lâu nhưng chưa có thời gian.

## 2. Vấn đề

### 2.1. Layout hôm nay

Spec mục 3 nói "repo nằm ngang hàng với `.mate/`, không bắt buộc nằm trong thư mục con nào". Code theo đúng câu đó:

- `store.normaliseRepos` (`internal/store/repo.go:130`) chỉ đòi repo nằm trong workspace và ngoài `.mate/`.
- `cmd/mate/project_repo_source.go:46` clone URL vào gốc workspace: `root/<name>`.
- `workspace.yaml` ghi `root:` tuyệt đối (M17) và `project.yaml` ghi `repos[].path` tương đối gốc workspace.
- Không có thư mục nào đại diện cho project ngoài `.mate/projects/<p>/`, là state của mate.

Hệ quả: project không repo không có chỗ nào trên đĩa ngoài `.mate`; cây thư mục không nói repo nào thuộc project nào; và tracker Beads phải nằm trong `.mate` vì không còn chỗ khác.

### 2.2. Beads và Fresh nối cứng

Không có package, interface hay registry nào tên "plugin" hay "tool". Đo trên baseline, Beads và Fresh được nối vào lõi ở sáu chỗ, mỗi công cụ một kiểu:

| Chỗ | Beads | Fresh |
| --- | --- | --- |
| `internal/store` | `BeadsDir`, `LockBeads`, file `.beads.lock` (`store/beads.go`) | không |
| Wrapper | `internal/beads` (bd, bv, export jsonl) | không |
| CLI | `mate tasks`, `mate beads`, `mate task-triage` (`cmd/mate/tasks.go`) | không |
| Recall | `cmd/mate/recall_tasks.go` gọi bd lúc Mate khởi động, timeout 3 giây | không |
| Console | role `tasks`, phím `t` (`cmd/mate/pane.go:24`, `console_stage.go`) | role `review`, phím `e`, `findTool("fresh")` (`console_stage.go:70`) |
| Prompt của Mate | 4 skill và `AGENTS.md.tmpl` nhắc Beads | không |

Dolt của Beads nằm trong `.mate/projects/<p>/.beads/`, do tiến trình `bd` ghi, không qua `store`. Đây là ngoại lệ không tên cho luật "store là nơi duy nhất ghi `.mate/`" (`store/doc.go`). Spec mục 1 gọi nó là "nguồn dữ liệu duy nhất cho epic/task" và `docs/beads.md` nói "xoá `.mate/` là xoá task data". Cả ba câu trái với quyết định 1 và 2.

`GLOSSARY.md` định nghĩa Epic và Task như ngôn ngữ của mate. Trái với quyết định 2.

### 2.3. Bài học cùng hình

Registry harness (2026-09-30) đã giải cùng bài toán: 48 dòng nhắc tên harness ở 15 file, thêm harness là sửa rải rác, compiler không chỉ ra chỗ thiếu. Lời giải ở đó là hợp đồng `Profile`, `catalog` biên dịch sẵn, capability có tên, và test ratchet đếm literal. Phương án này dùng lại đúng lời giải, không phát minh thêm.

## 3. Mục tiêu và ngoài phạm vi

Mục tiêu:

1. `<workspace>/<project>/` là thư mục thật của project; mọi repo của project nằm dưới nó; `.mate/projects/<p>/` vẫn là state của mate và không đổi.
2. Workspace cũ chuyển đổi một chiều bằng lệnh tường minh, không mất dữ liệu, không mất crew đang mở.
3. Một hợp đồng `tool.Profile` và `catalog`; Fresh và Beads là hai profile; thêm công cụ là thêm package, một dòng catalog, không sửa lõi.
4. Dữ liệu của plugin nằm ngoài `.mate/`; `store` không biết tên plugin nào.
5. Lõi không còn Epic và Task.

Ngoài phạm vi:

- Plugin nạp động, cấu hình YAML, hay công cụ ngoài cây mã. Catalog biên dịch sẵn là đủ cho một người dùng.
- Đưa observer, autopilot, outbox ra daemon riêng. Đáng làm, là phương án khác.
- Rút logic điều phối khỏi `cmd/mate`. Phương án này chỉ chạm `cmd/mate` ở chỗ nối plugin.
- Jev làm bộ quan sát trạng thái: [phương án riêng](jev-observer-2026-10-08.md).

## 4. Thiết kế đích: layout

### 4.1. Cây thư mục

```text
<workspace>/
├── .mate/                         state của mate, không đổi
│   ├── workspace.yaml             root:, projects[]
│   └── projects/<p>/              project.yaml, mate/, crews/, sent.log, …
├── .worktrees/<p>-<crew>/         không đổi
├── shop/                          thư mục project, do người dùng sở hữu
│   ├── .beads/                    tracker Beads, do bd sở hữu; không phải state của mate
│   ├── backend/                   repo git
│   └── web/                       repo git
└── notes/                         project không repo: vẫn có .beads/
    └── .beads/
```

Luật:

- Tên thư mục project bằng tên project (`internal/names` đã có luật tên; thư mục dùng đúng luật đó).
- `repos[].path` trong `project.yaml` vẫn tương đối gốc workspace, nhưng `store.normaliseRepos` đòi nó nằm dưới `<root>/<project>/`. Repo không bắt buộc là con trực tiếp (`shop/services/api` hợp lệ).
- `mate project add <name>` tạo `<root>/<name>/` nếu chưa có. `mate project repo add <p> <url>` clone vào `<root>/<p>/<name>`. `project repo add <p> <path>` từ chối path ngoài thư mục project, thông điệp chỉ cách dời.
- Mate vẫn cwd vào `.mate/projects/<p>/mate/`. Quyết định 1 của spec (Mate không có code trong cwd) không đổi.
- `mate project facts` và recall phần 2 liệt kê repo như hôm nay; nguồn vẫn là `project.yaml`, không quét thư mục, để project có thư mục con không phải repo không bị hiểu nhầm.
- `.beads/` ở thư mục project là của plugin Beads, được mô tả ở mục 5, không phải của layout.

### 4.2. Chuyển đổi một chiều: `mate migrate`

Không tự chạy lúc mở console, vì nó dời thư mục của người dùng. Một lệnh:

```text
mate migrate <workspace> [--dry-run]
```

Phát hiện: project nào có `repos[].path` không nằm dưới `<root>/<project>/` là layout cũ.

Điều kiện trước, kiểm hết rồi mới làm, một mục hỏng thì không dời gì:

- `.mate/migrate.lock` (flock, cùng kiểu `recover.lock`).
- Không crew nào `spawned|working|needs-decision|blocked|wait-mate` trên repo sẽ dời. Crew `finished|failed` bỏ qua. Lý do: worktree liên kết trỏ vào repo bằng đường dẫn tuyệt đối trong `.git`, và một crew đang chạy có shell cwd trong worktree đó.
- Mate của project không chạy (`query.ReadLiveness`), vì manual của Mate ghi đường dẫn repo và sẽ được sinh lại ở lần start sau.
- Thư mục đích `<root>/<project>/<basename>` chưa tồn tại.
- Repo không có thay đổi chưa commit? Không bắt buộc: `os.Rename` giữ nguyên working tree. Ghi nhận nhưng không chặn.

Thực hiện, theo thứ tự, mỗi project một transaction logic:

1. `os.MkdirAll(<root>/<project>)`.
2. `os.Rename(<old repo>, <root>/<project>/<basename>)`. Cùng filesystem vì cùng workspace, nên rename là nguyên tử và không copy.
3. `gitx.RepairWorktree` cho mọi worktree của crew đã đóng mà thư mục còn (M17 đã có).
4. Ghi `project.yaml` với `path` mới (atomic rename như mọi ghi của store).
5. `recovery.ReplaceRoot`-style thay tiền tố đường dẫn repo cũ bằng mới trong `crews/<id>/brief.md` của crew đã đóng, cùng luật M17 (chỉ thay tiền tố đúng đoạn đường dẫn).
6. Ghi một dòng `migrated: <old> -> <new>` vào `.mate/migrate.log`, append-only.

`--dry-run` in đúng danh sách rename và lý do từ chối, không đổi gì.

Khi hỏng giữa chừng (mất điện sau bước 2, trước bước 4): lần chạy sau thấy `project.yaml` trỏ đường dẫn không tồn tại nhưng `<root>/<project>/<basename>` có và là repo git; nó coi bước 2 đã xong và tiếp tục từ bước 3. Không bao giờ rename ngược.

Không làm: dời `.worktrees/` (không cần), sửa `mate.db` (timeline tự tìm lại file, M17), dời `.beads/` cũ trong `.mate/projects/<p>/` (mục 5.4).

### 4.3. Đường đọc sau chuyển đổi

`store.Open` trên workspace còn layout cũ không từ chối mở: console vẫn chạy, status line nói `layout cũ: chạy mate migrate`, và `project repo add`, `crew spawn` trên project đó bị từ chối với cùng câu. Lý do: người dùng phải xem được crew đang chạy trước khi quyết định dời.

Sau khi mọi project đã đúng layout, `workspace.yaml` ghi `layout: 2`. `store` đọc `layout` thiếu là 1. Chỉ là nhãn để status line và test biết; luật đường dẫn mới là thứ thật sự kiểm.

## 5. Thiết kế đích: registry công cụ

### 5.1. Bố cục package

```text
internal/tool/             hợp đồng: Name, Profile, Capabilities, Registry; không import package con
internal/tool/fresh/       Fresh: viewer report
internal/tool/beads/       Beads: dữ liệu task, bd và bv; thay internal/beads
internal/tool/catalog/     Default(): danh sách biên dịch sẵn; chỉ cmd/mate import
internal/tool/tooltest/    fixture và fake Runner dùng chung
```

`internal/beads` biến mất. `store/beads.go` biến mất.

### 5.2. Hợp đồng

```go
// Profile là mọi điều lõi được phép hỏi về một công cụ.
type Profile interface {
	Name() Name
	Info() Info                 // tên hiển thị, binary, phiên bản đã đo, cách cài, tài liệu
	Capabilities() Capabilities
}

type Capabilities struct {
	Viewer  Cap[Viewer]   // mở trong một pane host
	Command Cap[Command]  // mate <tool> <project> -- …
	Recall  Cap[Recall]   // vài dòng cho context của Mate lúc khởi động
	Skill   Cap[Skill]    // markdown cho manual Mate, sinh từ registry
	Data    Cap[Data]     // công cụ sở hữu dữ liệu ở đâu, và luật lock
}

// Cap, CapStatus, Evidence: cùng kiểu với internal/harness, không copy mà
// chuyển harness.Cap sang một package nhỏ dùng chung (internal/capability)
// trong PR 1, để hai registry chung một từ vựng và một bộ test hợp đồng.
```

| Capability | Nhiệm vụ | Thay cho |
| --- | --- | --- |
| `Viewer` | `Keys()` phím console và nhãn; `Argv(ctx ViewerContext)` dựng lệnh cho pane host, nhận thư mục project, repo của crew, đường dẫn report; `Placeholder()` dòng chờ của cột | role `review`/`tasks` cứng, `findTool("fresh")`, `case "t"`/`case "e"` trong `pane.go` |
| `Command` | Passthrough `mate <tool> <project> -- <args>`: dir, env, lock, hậu xử lý sau lệnh (Beads: export) | `mate beads`, `mate tasks`, `beads.Tracker.Run`, `LockBeads` |
| `Recall` | `Render(ctx, project) (text, ok, err)` dưới trần byte và deadline do lõi đặt; `ok=false` là ABSENT | `recall_tasks.go` |
| `Skill` | Nội dung skill `<tool>-usage` và đoạn cho `AGENTS.md`; lõi sinh file, như `harness-adapters` | 4 skill nhắc Beads |
| `Data` | `Dir(project) string` nơi dữ liệu nằm (dưới thư mục project, không dưới `.mate`); `Lock(ctx, project)`; `Exists(project)`; `Init(ctx, project)` | `store.BeadsDir`, `.beads.lock`, `Tracker.ensure` |

Luật:

- `Data.Dir` nằm dưới `<root>/<project>/`, do `tool` kiểm qua một hàm của `store` trả thư mục project; `store` không biết công cụ nào gọi.
- Lock của công cụ là file dưới `.mate/projects/<p>/locks/<tool>.lock`: state của mate là *cái khoá*, không phải dữ liệu. Đây là chỗ duy nhất `.mate` có dấu vết của plugin, và nó là file rỗng.
- `Recall` chạy với deadline 3 giây và trần byte của `mate recall`; lỗi hay quá hạn in một dòng `<tool>: unreadable (<lý do>)` như hôm nay.
- `Command` không dùng shell; args đi nguyên vẹn như `beads.Exec` đang làm.
- Phím console: `Viewer.Keys()` khai báo; hai công cụ khai báo trùng phím là lỗi lúc dựng registry, không phải lúc bấm.

### 5.3. Fresh

- `Viewer` verified: phím `e` trên hàng crew, argv `fresh <report.md hoặc thư mục crew>`, đúng như M13.
- `Command`, `Recall`, `Skill`, `Data`: `unsupported`, lý do "Fresh là editor, không sở hữu dữ liệu của mate".
- Không có Fresh: `Viewer` vẫn verified nhưng `Argv` trả lỗi có câu cài đặt; status line nói như hôm nay.

### 5.4. Beads

- `Data`: `Dir = <root>/<project>/.beads`. `Init` chạy `bd init --prefix <project> --skip-agents --skip-hooks --non-interactive` trong thư mục project. `Exists` là có `.beads/`.
- `Command`: `mate beads <project> -- …` giữ nguyên hành vi: xoá biến môi trường chọn tracker, khoá theo project, export jsonl sau mỗi lệnh.
- `Viewer`: phím `t` trên hàng project, argv `bv --db <Dir>`. Một tracker một project nên bv chạy nguyên bản, không gộp.
- `Recall`: đúng khối "Beads work" hôm nay.
- `Skill`: nội dung `task-management` hiện tại, chuyển thành skill do registry sinh; ba skill còn lại (`review-delivery`, `task-intake`, `mate-commands`) bỏ câu nhắc Beads cụ thể và trỏ sang skill của công cụ.
- `.beads/` cũ ở `.mate/projects/<p>/.beads/`: `mate migrate` **không** dời nó, vì đó là dữ liệu của plugin, không phải của layout. `mate beads <project> -- …` khi thấy tracker cũ ở đó mà chưa có tracker mới thì từ chối với câu: dời `.mate/projects/<p>/.beads` sang `<root>/<p>/.beads` bằng tay, hoặc `mate tasks <p> --init` để bắt đầu trống. Theo đúng tinh thần đoạn `tasks.yaml` của `docs/beads.md`: không khởi tạo đè dữ liệu có thể còn giá trị.
- Crew và worktree: crew làm trong `.worktrees/<p>-<crew>/`, là worktree của một repo *bên trong* thư mục project, nên `.beads/` của project không nằm trong worktree, kể cả khi project commit nó vào một repo nào đó. Crew không chạy bd. Brief mang Beads ID như chuỗi; Mate là bên duy nhất gọi `mate beads`. Skill ghi luật này.

### 5.5. Lõi sau thay đổi

- `GLOSSARY.md` bỏ Epic và Task; giữ Project, Crew, và thêm Tool: "chương trình ngoài mate mà console gắn phím và CLI bọc; dữ liệu của nó không phải state của mate".
- `cmd/mate` có một lệnh chung `mate tool <name> <project> -- …`; `mate beads` giữ làm alias để manual cũ không vỡ trong một bản.
- `recall` hỏi `Registry` mọi công cụ có `Recall` verified, mỗi công cụ một khối, thứ tự theo catalog.
- Console nhận danh sách phím từ snapshot (`query` mang `Tools []ToolBinding{Key, Label, Scope}` lấy từ registry), không có `case "t"` hay `"e"` cứng; `cmd/mate` nối phím với `Viewer.Argv` qua seam như `ReviewFunc` hôm nay.
- `panerun` không đổi: role chỉ là tên socket.

## 6. Bộ test

Hợp đồng, chạy trên mọi profile của `catalog.Default()`:

1. Không capability nào ở zero; `Impl != nil` khi và chỉ khi `verified`; `verified` có `Evidence`.
2. `Data.Dir` của mọi công cụ nằm dưới thư mục project và ngoài `.mate/`, kiểm trên workspace tạm.
3. Hai công cụ không trùng phím.
4. `Viewer.Argv` dựng được với fake `findTool`; thiếu binary thì lỗi có câu cài đặt.
5. `Recall` tôn trọng deadline: fake Runner ngủ quá hạn thì ra `unreadable` trong thời gian chặn trên.

Ratchet, cùng `go/ast` với ratchet harness: literal `"bd"`, `"bv"`, `"fresh"`, `".beads"` và identifier xuất từ `internal/tool/<name>` ngoài `internal/tool/...` và test. Bắt đầu ở con số hiện tại, về 0 ở PR 5. Allowlist: `docs/` không đếm; `dispatch/builtin.go` không liên quan.

Layout và migrate, trên repo git thật trong thư mục tạm:

6. `normaliseRepos` từ chối repo ngoài thư mục project, chấp nhận repo lồng sâu.
7. `project add` tạo thư mục; `repo add <url>` clone vào thư mục project; `repo add <path>` ngoài thư mục bị từ chối.
8. `migrate --dry-run` liệt kê đúng; `migrate` dời hai repo của một project, giữ working tree bẩn nguyên vẹn, `git worktree list` của crew đã đóng còn hợp lệ, `project.yaml` mới, brief đổi tiền tố, chạy lần hai không làm gì.
9. Từ chối khi crew mở, Mate chạy (fake runtime), đích đã tồn tại; không rename gì khi một project trong hai bị từ chối.
10. Hỏng giữa chừng: dựng trạng thái "đã rename, chưa ghi yaml", chạy lại hoàn tất, không rename ngược.
11. `store.Open` trên layout cũ vẫn mở; `crew spawn` từ chối với câu chỉ `mate migrate`.

Live, chỉ `MATE_LIVE=1`: `TestLiveMigrateThenSpawn`: workspace layout cũ có một crew đã đóng, migrate, start Mate, spawn một crew Codex trả lời "ok" trong repo đã dời, stop. Captain quyết có chạy hay không; phương án không bắt buộc.

## 7. Kế hoạch theo PR

Mỗi PR giữ nguyên hành vi với workspace đã đúng layout mới, và PR 1 đến 3 giữ nguyên hành vi với layout cũ.

| PR | Nội dung | Điều kiện hoàn tất |
| --- | --- | --- |
| 0 | Test đặc tả: golden cho `project.yaml`, `workspace.yaml`, argv `fresh`/`bv`/`bd` hôm nay, output `recall` phần Beads; ratchet công cụ ghi con số đầu | `make check` xanh; ratchet có số |
| 1 | `internal/capability` tách từ `harness.Cap`; `internal/tool` hợp đồng, `Registry`, `catalog` rỗng; `query.Tools`; console đọc phím từ snapshot nhưng `cmd/mate` vẫn nối cứng | Golden console không đổi; harness không đổi hành vi |
| 2 | Fresh thành profile; `console_stage.go` bỏ `findTool("fresh")` và role `review` cứng | Ratchet không còn `"fresh"` ngoài `internal/tool/fresh`; `e` hoạt động như cũ |
| 3 | Layout: luật `normaliseRepos`, `project add` tạo thư mục, clone vào thư mục project, `layout:` trong `workspace.yaml`, status line layout cũ, `crew spawn` từ chối trên layout cũ | Test 6, 7, 11; workspace mới tạo ra đúng cây mục 4.1 |
| 4 | `mate migrate` | Test 8, 9, 10; chạy tay trên bản sao workspace `hellovietnam`, evidence ghi số repo dời và thời gian |
| 5 | Beads thành profile ở `internal/tool/beads`; xoá `internal/beads`, `store/beads.go`, `.beads.lock`; `mate tool`; recall qua registry; skill sinh từ registry; GLOSSARY; spec mục 1, 3, 9, 10; `docs/beads.md` | Ratchet bằng 0; diff của `cmd/mate` chỉ là nối dây; `mate beads` cũ vẫn chạy như alias |
| 6 | Dọn: bỏ alias `mate beads` và `mate tasks` sau một bản; bỏ nhánh đọc `layout: 1` | Không còn đường code cho layout cũ |

Đồ thị:

```text
PR0 ─ PR1 ─ PR2 (Fresh) ──────────────┐
          └─ PR3 (layout) ─ PR4 (migrate) ─┴─ PR5 (Beads) ─ PR6 (dọn)
```

PR 2 và PR 3 độc lập. PR 5 chờ cả hai vì Beads cần thư mục project (PR 3) và hợp đồng đã được Fresh thử (PR 2). PR 5 là phép thử của phương án, như PR 7 của registry harness: nếu Beads đòi sửa lõi thì hợp đồng sai.

Rollout trên workspace thật: build binary PR 4, đóng console, `mate migrate --dry-run`, đọc, `mate migrate`, mở console. Beads cũ dời tay theo mục 5.4 sau PR 5.

## 8. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| Rename repo trong lúc có tiến trình giữ cwd trong đó (editor, shell của captain) | `migrate` không phát hiện được; tài liệu bảo đóng; rename trên macOS vẫn thành công, tiến trình cũ chỉ mất cwd |
| Crew đã đóng còn worktree với `.git` trỏ đường dẫn cũ | `gitx.RepairWorktree` ở bước 3; test 8 kiểm `git worktree list` |
| Hợp đồng `tool` rút từ hai mẫu quá khác nhau (editor và tracker) | Không capability nào có trước call site; Fresh đi trước để thấy hợp đồng tối thiểu, Beads sau để thấy phần dữ liệu |
| `capability` tách khỏi `harness` làm rebase PR harness đang mở | PR 1 chỉ đổi import, dùng `git mv`; mở khi không có PR harness nào đang mở trên `contract.go` |
| Người dùng chạy `bd` thô trong thư mục project, không qua lock | Như hôm nay; `docs/beads.md` đã ghi; lock chỉ bảo vệ giữa các lệnh của mate |
| Project có thư mục con không phải repo (docs, assets) bị tưởng là repo | `project.yaml` vẫn là nguồn; không quét thư mục |

## 9. Câu hỏi chờ captain

1. **Tên lệnh**: `mate migrate` hay `mate workspace migrate`? Đề xuất: `mate migrate`, vì chỉ có một thứ để migrate và lệnh phải dễ gõ lúc đang kẹt.
2. **Repo lồng sâu** (`shop/services/api`): cho phép hay bắt là con trực tiếp? Đề xuất: cho phép, vì một số project có monorepo con; luật chỉ là "dưới thư mục project".
3. **Alias `mate beads`** giữ bao lâu? Đề xuất: một bản, bỏ ở PR 6.
4. **Live test** `TestLiveMigrateThenSpawn` có chạy không? Đề xuất: chạy một lần trước rollout trên workspace thật, vì migrate đụng thư mục của người dùng.

## 10. Tiêu chí hoàn tất

- Mọi repo của mọi project nằm dưới `<root>/<project>/`; `store` từ chối mọi path khác.
- Workspace `hellovietnam` đã migrate, Mate và crew mới chạy bình thường, evidence có ngày.
- Không còn literal tên công cụ ngoài `internal/tool/...`, ratchet bằng 0.
- `.mate/` không chứa dữ liệu của công cụ nào; chỉ chứa file lock rỗng dưới `locks/`.
- GLOSSARY không có Epic, Task.
- Thêm công cụ thứ ba là thêm package và một dòng catalog, chứng minh bằng việc PR 5 không sửa package lõi nào ngoài nối dây ở `cmd/mate`.
