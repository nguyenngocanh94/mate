# Phương án registry harness: thêm harness mà không sửa lõi

- Ngày: 2026-09-30; sửa theo review 2026-10-01.
- Trạng thái: đề xuất; chưa sửa production code.
- Hai test Console phụ thuộc locale (`TestConsoleGalleryRendersRegisteredProjects`, `TestRefreshUpdatesAsOfAndTheAgesMeasuredFromIt`) là lỗi hygiene độc lập, sửa ở PR riêng, không thuộc phương án này.
- Baseline đã review: `5d93926`, Herdr 0.8.2, Claude Code 2.1.285, codex-cli 0.156.1, pi 0.99.1 trên máy.
- Liên quan: [phương án probe TUI](tui-probe-redesign-2026-09-27.md), [crew observability](crew-harness-observability-2026-09-28.md).
- Spec cần cập nhật khi triển khai: [MVP](../mvp.md) mục 2, 7, 9 và 10.

## 1. Vấn đề

Spec mục 2 nói Crew chạy bằng Claude Code, Codex hoặc pi.
Code chỉ đăng ký `claude` và `codex`, và hiểu biết về hai harness này không nằm sau một ranh giới nào.

Số đo trên baseline:

- `harness.KindClaude` và `harness.KindCodex` được nhắc tên ở 48 dòng trong 15 file thuộc 7 package ngoài `internal/harness/...` (`cmd/mate`, `spawn`, `send`, `timeline`, `runtime`, `outbox`, `quota`), không tính test.
  Tổng cộng 9 package ngoài `internal/harness/...` import `harness`.
- Đếm theo literal (`"claude"`, `"codex"`, `.claude`, `.codex`, `CLAUDE.md`, `CODEX_HOME`) thì hiểu biết về harness còn ở 22 file, thêm `config` (allowlist env), `store` (harness mặc định), `dispatch` (bảng mặc định), `mateassets` (Mate luôn nhận `CLAUDE.md` và `.claude/skills`), `spawn/harness_profile.go` (chụp `.codex/config.toml` và `.claude/settings.json`) và `timeline/telemetry.go` (đọc nối đuôi riêng cho Codex, nhánh `Finalized` riêng cho Claude).
- `Adapter.Validate` trả về `CapabilitySet`, nhưng không có caller production nào ngoài package `harness` đọc nó.
  Đường stop kiểm `handle.Kind == KindClaude` thay vì hỏi `GracefulStop.Available`.
- Với một kind lạ, các nhánh `default` không nhất quán:

| Hành vi | Vị trí |
| --- | --- |
| Từ chối rõ ràng | `harness.AdapterFor`, `send.composerProfileFor`, `timeline.transcriptParser` |
| Im lặng bỏ qua | `spawn.settleStartupPrompt`, `spawn.prepareCrewHarnessFiles`, `harness.profileArgs` |
| Im lặng hạ cấp | `spawn.stopLiveAgent` chuyển sang force |

- `AgentSpec`, `Config` và `LaunchSpec` là hợp của các field riêng từng harness (`ClaudeSessionID`, `CodexHome`, `ManualInCwd`).
  Caller phải `switch kind` để điền đúng field trước khi gọi `BuildLaunchSpec`, nên hai adapter không thay thế được cho nhau.
- `runtime` (cổng Herdr) so sánh kind để biết harness nào có lệnh thoát êm, và fake của nó tự vẽ màn hình sẵn sàng của từng harness.
  Import `harness` để nhận `LaunchSpec` thì hợp lý và sẽ còn sau refactor; hai chỗ kia thì không.
- `query` giữ một bản sao `HarnessKind`, và Console có `harnessOrder` cứng hai phần tử.

Hệ quả: thêm harness thứ ba là sửa rải rác, compiler không chỉ ra chỗ còn thiếu, và sót một chỗ thì có nơi báo lỗi, có nơi chạy sai không ai biết.

## 2. Mục tiêu và ngoài phạm vi

Mục tiêu:

1. Thêm một harness là thêm một package, một dòng đăng ký và một hàng trong bảng dispatch mặc định; không sửa `spawn`, `send`, `watch`, `timeline`, `outbox`, `runtime`, `quota`, `config`, `store`, `mateassets`, `cmd/mate`.
2. Thiếu một khả năng là một trạng thái có tên và có lý do, không bao giờ là nhánh `default` im lặng.
3. Hành vi của Claude và Codex không đổi trong suốt quá trình chuyển.
4. "Onboard xong" được định nghĩa bằng một bộ test hợp đồng, không bằng việc đọc lại 15 file.

Ngoài phạm vi:

- Thay đổi hành vi quan sát, gửi và xác nhận: thuộc phương án probe TUI.
  Phương án này chỉ dựng chỗ đặt cho phần riêng harness của nó.
- Plugin nạp động, harness khai báo bằng YAML, hay harness ngoài cây mã.
- Trừu tượng hoá runtime: `runtime.Adapter` đã là ranh giới đó.
- Đưa logic điều phối ra khỏi `cmd/mate`: đáng làm nhưng là phương án riêng.

## 3. Thiết kế đích

### 3.1. Bố cục package

```text
internal/harness/            hợp đồng: Kind, Profile, capability, LaunchSpec, Registry
internal/harness/claude/     mọi thứ riêng Claude Code
internal/harness/codex/      mọi thứ riêng Codex (kèm codexlab)
internal/harness/catalog/    Default(): danh sách harness biên dịch sẵn
```

`harness` không import package con nào.
Package con import `harness`.
Chỉ `catalog` biết tên cụ thể, và chỉ `cmd/mate` import `catalog`.

### 3.2. Hợp đồng

Phần bắt buộc nhỏ, phần còn lại là capability có trạng thái.

```go
// Profile là mọi điều lõi được phép hỏi về một harness.
type Profile interface {
	Kind() Kind
	Info() Info               // tên hiển thị, kind phía runtime, executable, icon
	Launcher() Launcher       // bắt buộc
	Screen() ScreenProfile    // bắt buộc
	Capabilities() Capabilities
}

type Capabilities struct {
	GracefulStop Cap[GracefulStopper]
	Session      Cap[SessionIdentity]
	Hooks        Cap[HookInstaller]
	Transcript   Cap[TranscriptSource]
	TurnEnd      Cap[TurnEndEvidence]
	Quota        Cap[QuotaProvider]
}

type Cap[T any] struct {
	Status   CapStatus // "" (chưa khai báo) | verified | unsupported | unknown
	Impl     T         // chỉ khi verified
	Evidence Evidence  // verified: version harness, ngày đo, test hoặc capture chứng minh
	Reason   string    // unsupported hoặc unknown: vì sao, và còn thiếu phép đo nào
}
```

Giá trị zero của `Cap` là "chưa khai báo", và là lỗi.
`unknown` là một giá trị tường minh, luôn kèm `Reason`.
Bộ test hợp đồng kiểm ba điều: không capability nào ở zero, `Impl != nil` khi và chỉ khi `verified`, và `verified` có `Evidence`.
Nhờ đó thêm một capability mới buộc mọi harness phải trả lời, thay cho việc Go không kiểm `switch` đủ nhánh.
Code đang đo dở không được mang `Impl` dưới `unknown`; nó thuộc về test và lab, không thuộc catalog.

Ba trạng thái dùng đúng nghĩa của phương án probe TUI mục 5.1, để hai phương án chung một từ vựng.

| Thành phần | Nhiệm vụ | Thay cho |
| --- | --- | --- |
| `Launcher.Prepare` | Trả về danh sách file cần ghi (đường dẫn, nội dung, có exclude khỏi git không) và dữ liệu mờ cho launch, cho cả hai vai; với vai Mate gồm cả manual dưới tên harness đọc và thư mục skill | `prepareCrewHarnessFiles`, nhánh kind trong `prepareMateDir`, `freshSessionID`, `CLAUDE.md` và `.claude/skills` trong `mateassets`, danh sách file chụp trong `spawn/harness_profile.go` |
| `Launcher.Build` | Dựng `LaunchSpec` từ yêu cầu chung (role, cwd, context, model, effort, env, phiên mới hay resume); khai báo khoá env cần allowlist | `buildLaunchSpec`, `buildCrewLaunchSpec`, `profileArgs`, khoá riêng harness trong `config.LaunchEnvKeys` |
| `ScreenProfile` | Nhận diện cấu trúc: dialog startup dạng dữ liệu, vùng input và nội dung của nó, vật cản, dấu hiệu đang chạy; trả về một quan sát, không trả verdict; màn hình sẵn sàng cho fake | `startupProfileFor`, `composerProfileFor`, phần tìm vùng của `pendingMatches`, `runtime.startupReadyScreen` |
| `GracefulStopper` | Câu lệnh thoát êm | Kiểm `KindClaude` trong `runtime.StopAgent` và `stopLiveAgent` |
| `SessionIdentity` | Id có lúc launch hay sau prompt đầu; khôi phục id lúc stop; kiểm session còn trên đĩa trước resume | `codexSessionAtStop`, `checkCodexResume`, nhánh Codex trong `StartMate` và `StopMate` |
| `HookInstaller` | File hook, hook nào là của mate để settle được trust, trần byte digest, giải mã payload thành sự kiện chung | `ClaudeSettings`, `CodexHooks`, `ownHooks`, `SessionHookCommand`, cờ `--harness` của `mate hook` |
| `TranscriptSource` | Định vị file; đọc tăng dần theo cursor và trạng thái riêng (nối đuôi theo offset, snapshot đã đóng băng); parse; chuẩn hoá usage (delta hay cộng dồn) thành cùng một loại turn | `locateMate`, `locateCrew`, `transcriptParser`, `claudeTurns`, `codexTurns`, nhánh Codex và `Finalized` trong `timeline/telemetry.go`, `TranscriptBatch.CodexUsageSnapshots` |
| `TurnEndEvidence` | Bằng chứng một turn đã kết thúc sau thời điểm cho trước | Nhánh kind trong `outbox.Stow`, điều kiện Claude trong `contextRefresh` |
| `QuotaProvider` | Tên provider và lane của `quota-axi` | Bảng `quota.providers` |

`Prepare` trả dữ liệu, không tự ghi file.
`spawn` vẫn là nơi duy nhất ghi, qua `store` và `gitx`, nên luật đường dẫn của workspace không đổi và package harness không import `store`.

Ranh giới của `ScreenProfile` theo đúng mục 6 của phương án probe TUI: profile chỉ nhận diện cấu trúc, policy dùng chung.
Dấu hiệu đang chạy (`esc to interrupt` của Codex, bộ glyph spinner của Claude) là literal riêng harness nên việc nhận ra nó nằm trong profile; kết luận "đang bận thì không gõ" và việc so draft với payload đã gõ là policy trong lõi, nhận vùng input từ quan sát.
`TranscriptBatch` chỉ còn phần chuẩn hoá; trạng thái riêng như snapshot cộng dồn của Codex đi theo cursor dưới dạng mờ, do harness tự đọc lại.

### 3.3. Những khác biệt giữ nguyên là khác biệt

Không ép hai harness về một hình.

- Session id: Claude có lúc launch, Codex chỉ có sau prompt đầu.
  `SessionIdentity` mô tả cả hai biến thể; lõi hỏi "đã biết id chưa" thay vì hỏi kind.
- Usage: Claude báo delta theo call, Codex báo cộng dồn.
  Việc chuẩn hoá nằm trong `TranscriptSource` của từng harness; `timeline` chỉ nhận turn đã chuẩn hoá.
- Giao context: file cờ, file trong cwd, hay manual tự nạp từ cwd.
  Đây là chi tiết của `Launcher`; lõi chỉ cần biết context bắt buộc có giao được không.

### 3.4. Registry và tiêm phụ thuộc

`harness.Registry` có `Lookup(kind)`, `Parse(string)` và `Kinds()`.
`cmd/mate` dựng nó từ `catalog.Default()` và truyền qua `Deps` của `spawn`, `send`, `watch`, `outbox`, `timeline`.
Không dùng `init()` để tự đăng ký: test của lõi tiêm harness giả, và danh sách harness là một chỗ đọc được.

`ParseKind` toàn cục biến mất.
Một kind hợp lệ là một kind có trong registry.

`catalog` cũng công bố harness mặc định cho vai Mate và vai Crew.
`cmd/mate` truyền hai giá trị đó vào `store.Init`; `store` vẫn là tầng thấp, không import `catalog`, và không còn tự quyết `DefaultMateHarness`.
Bảng dispatch mặc định vẫn là chính sách của captain về model và effort theo loại task, không sinh từ registry; registry chỉ kiểm tra hàng của nó lúc đọc, như `dispatch.go` đang làm.

### 3.5. `LaunchSpec` vẫn kín

Hôm nay field của `LaunchSpec` không export và chỉ constructor trong package `harness` tạo được spec startable.
Khi constructor chuyển sang package con, bảo đảm đó phải giữ nguyên.

`harness` export đúng một builder.
Builder chạy các kiểm tra chung (cwd tuyệt đối, context tồn tại, env trong allowlist) rồi chạy kiểm tra giao context do harness cung cấp, và không trả spec nếu một trong hai fail.
`ValidateRequiredContext` ở ranh giới runtime vẫn chạy lại như hiện nay.

### 3.6. Runtime và phía đọc

- `LaunchSpec` mang kind phía runtime lấy từ `Info()`.
  `NewAgentStartCommand` không còn gọi `ParseKind`.
- Stop êm nhận câu lệnh từ `GracefulStopper`, và fake lấy màn hình sẵn sàng từ `ScreenProfile`.
  `runtime` không còn so sánh kind; nó vẫn import `harness` để nhận `LaunchSpec`.
- `query` bỏ enum `HarnessKind`; kind là chuỗi mờ ở phía đọc.
  Snapshot mang danh mục harness (tên, icon, thứ tự) lấy từ registry, thay cho `harnessOrder` và `harnessIcon`.
  Console vẫn không import package nào nói chuyện với process.
- Biến môi trường: mỗi profile khai báo khoá cần allowlist và khoá phải unset.
  `NestedSessionEnv` là hợp của mọi profile, vì biến của harness này làm nhiễm pane của harness khác.

### 3.7. Luật khi thiếu khả năng

Một luật duy nhất cho mọi call site: hỏi capability, và nếu không `verified` thì đi đường hạ cấp có tên.

| Capability thiếu | Hành vi |
| --- | --- |
| `GracefulStop` | Force stop, kết quả ghi lý do |
| `Session` không resume được | Khởi động phiên mới, ghi chú vì sao |
| `Hooks` | Từ chối vai Mate với thông báo nêu capability; vai Crew không bị ảnh hưởng |
| `Transcript` | Timeline ghi "không quan sát được"; không có số token, không đoán |
| `TurnEnd` | Dùng composer làm bằng chứng dự phòng như hiện nay |
| `Quota` | Dispatch coi harness là không có số quota |

Không còn nhánh im lặng.
Riêng `settleStartupPrompt` hiện bỏ qua cả bước settle khi không có profile; sau thay đổi, `ScreenProfile` là bắt buộc nên trường hợp đó không thể xảy ra.

## 4. Bộ test hợp đồng

Một suite chạy trên mọi profile trong `catalog.Default()`.

1. Không capability nào ở zero; `Impl != nil` khi và chỉ khi `verified`; `verified` có `Evidence`, `unsupported` và `unknown` có `Reason`.
2. `Launcher` dựng được spec cho vai Crew trên thư mục fixture; vai Mate dựng được hoặc từ chối nêu đúng capability thiếu.
3. Mỗi profile kèm capture có manifest (version, kích thước pane, nguồn đọc): tối thiểu composer trống, đang bận, có draft, và từng dialog nó khai báo.
   Suite chạy phân loại trên chính các capture đó.
4. Nếu `Transcript` là `verified`: fixture transcript parse không có record hỏng, và tổng usage khớp số ghi trong manifest.
5. Live, chỉ khi `MATE_LIVE=1`: `TestLiveConformance/<kind>` spawn trong lab cô lập, settle, gửi một dòng, thấy bận rồi rảnh, stop.

Thêm một test ratchet, chạy bằng `go/ast` trên mọi file không phải test ngoài `internal/harness/...`.
Nó đếm hai loại tham chiếu: identifier riêng harness xuất từ `harness` (`KindClaude`, `Claude{}`, `CodexRolloutPath`, `ClaudeSettings`, ...) và literal chuỗi chứa `claude`, `codex`, `CLAUDE.md`, `.claude`, `.codex`, `CODEX_HOME`, `CLAUDE_CONFIG_DIR`.
Đếm chỉ bằng `harness.Kind*` là không đủ: con số đó về 0 trong khi `config`, `store`, `dispatch`, `mateassets`, `spawn/harness_profile.go` và `timeline/telemetry.go` vẫn biết tên harness.
Một allowlist có lý do cho từng mục giữ các tên là tên chung chứ không phải hiểu biết về harness, ví dụ `CLAUDE.md` trong `facts.docNames` là một tên tài liệu của repo.
Con số bắt đầu ở mức hiện tại, fail nếu tăng, và phải về 0 ngoài allowlist ở PR 6.

## 5. Quan hệ với hai phương án đang có

**Probe TUI.**
Phương án đó thay hành vi: observation, policy, journal gửi, và một parser màn hình chung trong `internal/screen`.
Nó cần "layout profile riêng cho từng họ UI" và "adapter theo harness dịch tín hiệu về hợp đồng chung".
`ScreenProfile` và `HookInstaller` ở đây chính là chỗ đặt cho hai thứ đó, và ranh giới profile/policy ở mục 3.2 lấy đúng định nghĩa của mục 6 bên đó.
Hai phương án cùng sửa `send/classify.go`, `harness/startup_prompt.go`, `spawn/crew.go` và `hook/hook.go`, nên phải xếp thứ tự, không chạy song song trên các file này.
Đồ thị phụ thuộc nằm ở mục 6.

**Crew observability.**
Phương án đó mở rộng những gì parser rút ra từ transcript.
`TranscriptSource` là nơi các fact mới được sinh ra; không có xung đột, nhưng PR 5 ở dưới nên đi sau các thay đổi ingest đang dở để tránh rebase lớn.

## 6. Kế hoạch triển khai theo PR

Mỗi PR từ 1 đến 6 giữ nguyên hành vi của Claude và Codex.

| PR | Nội dung | Điều kiện hoàn tất |
| --- | --- | --- |
| 0 | Chuẩn bị: bổ sung test đặc tả argv và file sinh ra cho cả hai harness, cả hai vai; thêm test ratchet; đo nhanh pi để thử hợp đồng trên giấy | Ratchet ghi con số hiện tại và allowlist; bảng khớp hợp đồng của pi nằm trong evidence |
| 1 | Hợp đồng, `Registry`, tiêm qua `Deps`; Claude và Codex hiện thực `Profile` bằng lớp bọc mỏng tại chỗ; xoá `Validate` và `CapabilitySet` | Không call site nào gọi `AdapterFor` hay `ParseKind` toàn cục; test đặc tả không đổi |
| 2 | Launch: `Prepare` và `Build`; thu gọn `AgentSpec`; builder kín cho `LaunchSpec` | `spawn` không còn `switch kind` cho launch; argv và file sinh ra giống từng byte |
| 3 | Màn hình: startup, vùng input và dấu hiệu đang chạy về `ScreenProfile` dưới dạng quan sát; `send`, `watch`, `mate state` và fake nhận profile; khớp draft và `CanType` thành policy chung | Toàn bộ capture hiện có phân loại như cũ; không còn nhánh bỏ qua settle |
| 4 | Stop, session, hook, turn-end thành capability; `runtime` bỏ so sánh kind; `outbox`, `context_refresh` và `mate hook` hỏi capability | Live hiện có về resume, stow và recall hook pass trên cả hai harness |
| 5 | Transcript, usage và quota thành capability, gồm đọc tăng dần và snapshot đã đóng băng; `timeline` còn một đường chuẩn hoá turn; sửa doc comment cũ của `ParseTranscriptFinal` | `mate usage` và dashboard cho cùng số trên corpus fixture trước và sau; `telemetry.go` không còn nhánh kind |
| 6 | Chuyển file vào `harness/claude` và `harness/codex`; harness mặc định và danh mục harness cho `store`, `query` và Console lấy từ `catalog`; skill `harness-adapters` sinh từ registry; ratchet về 0; suite hợp đồng đầy đủ | Ratchet bằng 0 ngoài allowlist; suite pass cho cả hai; diff của PR này chỉ là di chuyển và nối dây |
| 7 | Onboard harness thứ ba, vai Crew trước | Diff chỉ gồm package mới, một dòng trong `catalog`, một hàng trong bảng dispatch mặc định, fixture và tài liệu; suite hợp đồng và live conformance pass |

Đồ thị phụ thuộc, gồm cả phương án probe TUI:

```text
PR0 ─ PR1 ─ PR2 ─┬─ PR4 (stop, session, hook, turn-end) ─┐
                 ├─ PR5 (transcript, usage, quota) ──────┼─ PR6 (di chuyển) ─ PR7 (pi)
                 └─ probe-TUI PR1 (thay PR3) ────────────┘
probe-TUI PR0 (lab tương thích) chạy song song từ đầu
probe-TUI PR3 (hook collector) đi sau PR4, vì PR4 dựng HookInstaller làm chỗ đặt
```

PR4 và PR5 không chờ màn hình; chỉ PR6 chờ tất cả.
Nếu probe TUI chưa sẵn sàng, PR3 ở đây vẫn làm được như một bước chuyển chỗ thuần tuý, và probe-TUI PR1 đổi hình bên trong `ScreenProfile` sau đó.

PR 6 cố ý đi sau: tách "đổi chỗ ở" khỏi "đổi hình dạng" để từng diff đọc được.
PR 7 là phép thử của cả phương án: nếu phải sửa lõi để thêm harness thì thiết kế chưa đạt, và chỗ phải sửa là lỗi của hợp đồng.
Hàng dispatch mặc định của harness mới là chính sách của captain, nên nó nằm trong diff của PR 7 mà không phải là sửa lõi.

Mỗi PR chạy `make check`; PR 2 đến 5 chạy thêm các test live liên quan với `MATE_LIVE=1` và lưu evidence dưới `docs/evidence/`.

## 7. Thử hợp đồng trên giấy với pi

Chỉ đọc từ `pi --help` của bản 0.99.1; chưa đo gì trong pane Herdr.

| Hợp đồng | Dấu hiệu từ help | Cần đo |
| --- | --- | --- |
| Giao context | `--append-system-prompt` nhận text hoặc nội dung file; tự tìm `AGENTS.md` và `CLAUDE.md` trừ khi `--no-context-files` | Có nạp trùng hai lần như Claude không; trần kích thước |
| Session | `--session-id <id>`, `--session <path\|id>`, `--session-dir <dir>` | Định dạng và tên file session; resume có dialog không |
| Model, effort | `--model`, `--thinking off..max` | Ánh xạ sang năm mức `Effort` |
| Trust | `--approve` tin file cục bộ của project cho lần chạy này | Còn dialog nào khác lúc khởi động không |
| Hook | Không có cờ hook; cơ chế mở rộng là `--extension` | Extension có cho sự kiện prompt, stop, session start không |
| Bỏ hỏi quyền | Không thấy cờ | pi có hỏi quyền trước khi chạy tool không |
| Herdr | `pi` có trong danh sách `--kind` của Herdr 0.8.2 | Chuyển trạng thái `agent_status`; `agent_session` có được điền không |

Một điều rút ra ngay cho hợp đồng: hook của pi, nếu có, đi qua một file nạp bằng cờ chứ không qua file settings trong cwd.
Vì vậy `HookInstaller` và `Prepare` phải trả được cả file lẫn tham số launch, đúng như mục 3.2 đã đặt.

## 8. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| Hợp đồng rút từ hai mẫu, sai hình với mẫu thứ ba | Đo pi ở PR 0, trước khi chốt interface ở PR 1; chỉ thêm capability khi một call site của lõi thật sự cần |
| Hành vi trôi trong lúc refactor | Test đặc tả theo byte cho argv và file; capture hiện có; live theo từng PR |
| Đụng file với phương án probe TUI | Chốt thứ tự trước khi bắt đầu (mục 9, câu 1) |
| `LaunchSpec` mất tính kín khi constructor rời package | Một builder duy nhất, luôn chạy kiểm tra; test chứng minh spec tự lắp bị từ chối |
| Trừu tượng hoá quá tay | Không interface nào có trước call site dùng nó; không plugin, không cấu hình động |
| Live test chậm và tốn quota | Suite live tối thiểu cho conformance; lab cô lập như `codexlab` cho từng harness |

## 9. Câu hỏi cần captain chốt

1. **Thứ tự với phương án probe TUI.**
   Đề xuất: đồ thị ở mục 6; PR 0 đến 2 ở đây đi trước, PR 0 của probe TUI chạy song song, PR 1 của probe TUI đáp vào `ScreenProfile` thay cho PR 3 ở đây, PR 3 của probe TUI đi sau PR 4 ở đây.
2. **Harness thứ ba là pi, và chỉ vai Crew trước?**
   Đề xuất: đúng cả hai; vai Mate chờ kết quả đo hook.
3. **Harness không có hệ hook tương đương thì không làm Mate được.**
   Đề xuất: chấp nhận, và ghi vào spec mục 2.
4. **Giữ `--harness` trên `mate hook mate-session`, hay suy ra từ `mate.meta`?**
   Đề xuất: giữ cờ, vì hook chạy trước khi meta chắc chắn có.

## 10. Spec phải cập nhật khi triển khai

- Mục 2, quyết định 2: nêu điều kiện để một harness được dùng cho vai Crew và vai Mate.
- Mục 7: ghi các số đo của harness mới, theo cùng cách ghi ngày và version.
- Mục 9: bố cục package mới dưới `internal/harness/`.
- Mục 10: một milestone mới cho các PR trên, số task cấp khi thêm vào bảng.
- Skill `harness-adapters`: mục "Adding a harness" trỏ tới suite hợp đồng thay cho câu "chỉ Codex và Claude được kiểm chứng".

## 11. Tiêu chí hoàn tất toàn phương án

- Không còn tham chiếu tới tên harness cụ thể ngoài `internal/harness/...`, trừ allowlist có lý do của ratchet.
- Mọi capability của mọi harness đã đăng ký là `verified` có evidence, hoặc `unsupported`, hoặc `unknown` có lý do.
- Không còn nhánh nào làm một việc khác đi trong im lặng vì không nhận ra harness.
- Claude và Codex cho cùng argv, cùng file sinh ra, cùng phân loại màn hình và cùng số usage như trước.
- Harness thứ ba được thêm mà không sửa package lõi nào.
