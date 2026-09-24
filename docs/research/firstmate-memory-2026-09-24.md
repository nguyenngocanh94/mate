# firstmate: dòng chảy trí nhớ — nghiên cứu cho matev2

Ngày 2026-09-24.
Tài liệu này chỉ là nghiên cứu: không có dòng code, template hay spec nào của matev2 bị sửa.
Nó là đợt tiếp theo của `docs/research/firstmate-prompting-2026-09-24.md`, nơi mục 6 chạm qua trí nhớ và mục B9 ("curate memory.md") được hoãn.
Trích dẫn firstmate dạng `path:line` tính từ gốc repo firstmate tại commit ở mục 0.
Trích dẫn matev2 dạng `assets/...`, `internal/...`, `docs/...` là đường dẫn trong repo này, tại `aec3607`.

## 0. Commit được nghiên cứu

- Upstream HEAD ngày 2026-09-24 là `9284978f` (`feat: add opt-in Claude away supervision host (#5488)`, 2026-09-24T04:03Z), mới hơn `795e4b58` mà báo cáo trước dùng một commit.
- Bản clone mới ở `/private/tmp/claude-501/firstmate-upstream-9284978f`; clone `795e4b58` và clone của người dùng ở `/Volumes/Work/Workspace/firstmate` không bị đụng tới.
- Khoảng `795e4b58..9284978f` chỉ thêm supervision host (36 file, 2.801 dòng); `git diff` trên `AGENTS.md`, hai skill `stow`, `bin/fm-session-start.sh` và `bin/fm-startup-memory-budget.sh` chỉ ra ba dòng thêm vào `AGENTS.md`, cả ba về supervision host, không dòng nào về trí nhớ.
- Lịch sử trí nhớ của firstmate đọc từ `git log`: `5777d25a` (2026-06-17, trí nhớ project qua brief ship), `30ee4efb` (2026-07-02, `/stow` đầu tiên), `af5361eb` (2026-07-08, inspect-then-update), `79e62b8b` (2026-07-30, ngân sách trí nhớ khởi động), `5ade9efa` (2026-08-08, tier có hạn), `e518906a` (2026-08-16, lưu bản ghi việc dở trước khi reset), `f170cede` (2026-08-23, horizon theo số lượt).

## Tóm tắt một đoạn

firstmate coi hội thoại là bộ nhớ đệm có thể mất bất cứ lúc nào, và thiết kế mọi thứ để một lần restart hay compaction "là một non-event" (`VISION.md:41-46`).
Tri thức có đúng một chủ cho mỗi loại (`AGENTS.md:279-290`), được ghi bằng inspect-then-update có ngày và bằng chứng, được curate định kỳ bằng skill `/stow` có tier, hạn, ngân sách token và kho lạnh (`.agents/skills/stow/SKILL.md`).
Đường đọc không phụ thuộc vào việc agent nhớ đọc: một script in một digest có thứ tự ở mỗi session start, và hook `SessionStart` của harness đưa digest đó vào context trước lượt đầu, kể cả sau compaction (`bin/fm-session-start.sh`, `docs/sessionstart-nudge.md`).
matev2 có bảng lớp tương tự trên giấy nhưng thiếu cả ba thứ: luật ghi, máy curate, và đường đọc tự động.
Workspace thật `shop` cho thấy hậu quả: sau ba ngày và sáu task, `memory.md` vẫn là 9 byte, `PROJECT.md` vẫn là 53 byte tiêu đề trống, và Mate đã phải gửi lại cùng một lời sửa cho crew vì bài học chỉ sống trong hội thoại.

## 1. Chuyện gì thực sự xảy ra với trí nhớ trong workspace `shop`

### Các file, đo trên đĩa (chỉ đọc)

| File | Kích thước | Sửa lần cuối | Nội dung |
| --- | --- | --- | --- |
| `~/work-matev2/.matev2/projects/shop/mate/memory.md` | 9 byte | 2026-09-17 21:56 (lúc tạo) | `# Memory\n`, header do `internal/mateassets/write.go:30` tạo; chưa bao giờ được ghi |
| `~/work-matev2/.matev2/projects/shop/PROJECT.md` | 53 byte | 2026-09-17 21:56 (lúc tạo) | Hai heading `## What this project is`, `## How to work here`, không nội dung |
| `~/work-matev2/.matev2/WORKSPACE.md` | 135 byte | 2026-09-19 18:03 | "Nothing is written here yet."; chưa tồn tại khi session dài bắt đầu (`sed: .../WORKSPACE.md: No such file or directory`, rollout 01a0afde, 2026-09-17T14:57:55Z) |
| `~/work-matev2/.matev2/projects/shop/mate/backlog.md` | 812 byte | 2026-09-19 11:06 | In flight `buyesp32`; Failed `rpi34`; Completed bốn scout |
| `~/work-matev2/.matev2/projects/shop/mate/mate.meta` | 132 byte | 2026-09-17 21:56 | `harness=codex`, `session_id=` rỗng |

### Các session của Mate, theo cwd

Ba rollout Codex có `cwd` là `~/work-matev2/.matev2/projects/shop/mate` (codex-cli 0.154.0, `~/.codex/sessions/2026/09/17/`):

| Session id | Thời gian (UTC) | Kích thước | Chuyện gì xảy ra |
| --- | --- | --- | --- |
| `01a0afaf-e49b-7a90-a771-e87f33b81394` | 2026-09-17 14:10:33 – 14:12:10 | 185 KB, 79 dòng | Captain: `xin chào, tìm hiểu cho tôi sản phẩm bo mạch esp32 và tiềm năng của nó để tôi sẽ làm trang landing page về nó`. Mate viết brief, spawn `esp32research`, ghi backlog, rồi `turn_aborted` |
| `01a0afb6-0fb0-7cd1-9e0b-1596b153caf7` | 14:12:19 – 14:12:22 | 20 KB, 4 dòng | Khởi động rồi dừng, không có lượt nào |
| `01a0afde-a7ac-7ba3-b592-af1d991a1f1a` | 2026-09-17 14:57:40 – 2026-09-20 03:01:26 | 2,3 MB, 1.232 dòng | Toàn bộ công việc thật: năm scout, một ship, ba lần spawn hỏng |

Giữa session 1 và session 3, `.matev2/` được tạo lại lúc 14:56Z: `ls -la` của chính Mate lúc 14:58:06Z thấy mọi file mang giờ 21:56 địa phương, `backlog.md` 10 byte và `no crews recorded for shop`.
Vậy crew `esp32research` và dòng backlog của session 1 đã bị xoá cùng workspace, và captain phải nói lại yêu cầu bằng lời khác (`tôi muốn bạn tìm hiểu cho tôi mạch esp32 sau đó báo cáo lại, ta sẽ làm landing page cho nó`, 14:57:42Z).
Đó là một lần reset workspace có chủ ý, không phải app làm mất; nhưng nó cho thấy với Mate Codex, mỗi lần khởi động là một session mới: `mate.meta` không ghi `session_id` cho Codex, và `internal/harness/codex.go:75-82` từ chối resume.

Session 3 chạy liền ba ngày, không restart và không compaction: rollout không có bản ghi `compacted` nào, và `token_count` cuối là 137.935 token input trên cửa sổ 258.400 (53%).
Nghĩa là mọi thứ Mate "nhớ" trong ba ngày đó đến từ hội thoại liên tục, không từ file.

### Đọc nhiều, không ghi gì

Trong 145 lời gọi tool của session 3, Mate đọc `memory.md` 10 lần, `PROJECT.md` 10 lần, `WORKSPACE.md` 9 lần, `backlog.md` 10 lần, và ghi `backlog.md` 14 lần; số lần ghi `memory.md` và `PROJECT.md` là 0.
Mate đọc lại bốn file ở đầu gần như mọi lượt của captain (08:36, 09:47, 09:53, 10:19 ngày 18; 03:27, 04:06 ngày 19; 03:00 ngày 20), tức là thói quen đọc đã có: nếu file có nội dung, nó sẽ được dùng.
Cái thiếu là đường ghi.

### Những gì Mate đã học rồi làm mất hoặc làm lại

1. **Bài học được nhớ nhờ hội thoại, không nhờ file.**
   Mate sửa `esp32research` bằng `Please revise the HTML report to use the Lavish CDN fallback (Tailwind v4 plus DaisyUI v5) because no project design system exists` (`shop/sent.log`, 2026-09-17T22:05:51+07:00).
   Bốn brief sau đó (`briefs/esp32s3.md`, `rpi3.md`, `rpi35.md`, `power12.md`) đều mang câu đó.
   Nó sống sót chỉ vì session không bao giờ restart.
2. **Bài học bị quên ngay trong cùng session.**
   Mate gửi `rpi35`: `Keep all report artifacts in your Crew directory only; do not add or commit files in the repository worktree.` (2026-09-18T17:23:36+07:00).
   Brief `power12.md` viết ngày hôm sau (sửa 2026-09-19 10:28) vẫn chỉ có `Do not modify the project repository.`, và Mate phải gửi lại gần nguyên câu cho `power12` lúc 2026-09-19T10:32:01+07:00.
   Không có chỗ nào để ghi bài học đó ngoài hội thoại, và 53% cửa sổ hội thoại đã đủ để nó chìm.
3. **Sự thật về project tự mâu thuẫn.**
   Ba brief (`rpi3.md`, `rpi35.md`, `power12.md`) nói "the project has no design system".
   Brief `buyesp32.md` (2026-09-19 11:06) nói `Inspect the existing app ... preserve the existing site design`.
   Crew dừng sau 2 phút với `needs-decision: branch has no app or design system` (`crews/buyesp32.status`).
   Sự thật "repo trống, không có app" chưa bao giờ được ghi vào `PROJECT.md`, nên nó không có mặt ở đúng lúc cần.
4. **Bối cảnh project không được tích luỹ.**
   Bốn report nghiên cứu nói rõ `shop` là gì (một cửa hàng sẽ có landing page cho ESP32, ESP32-S3, Raspberry Pi 3, module nguồn 12 V), nhưng `## What this project is` vẫn trống, và brief `buyesp32` không nhắc tới report nào.
5. **Câu hỏi treo cho captain chỉ nằm trong hội thoại.**
   Mate hỏi captain lúc 2026-09-19T04:07:57Z ("Bạn gửi vị trí mã nguồn landing page ESP32, hoặc xác nhận cho tôi tạo mới...").
   `backlog.md` chỉ ghi `buyesp32` là In flight, không ghi rằng việc đang chờ một câu trả lời của captain.
   Một Mate khởi động lại (Codex luôn khởi động mới) sẽ không biết mình đã hỏi gì.

### Trí nhớ riêng của harness: đo, không đoán

- **Claude Code** (2.1.281 trong transcript của Mate): 97 thư mục `~/.claude/projects/<slug>/` có slug kết thúc bằng `matev2-projects-<p>-mate`, tất cả từ live test; cả 97 có thư mục `memory/` do Claude Code tạo, và cả 97 đều rỗng.
  Auto-memory đang bật (không có khoá nào trong `~/.claude/settings.json`, không có biến môi trường tắt nó), và matev2 không tắt hay chuyển hướng nó (không có tham chiếu nào trong `internal/` hay `cmd/`).
  Mate `shop` thật là Codex nên không có thư mục Claude cho `~/work-matev2/...`.
  Rủi ro chưa xảy ra nhưng có thật: một Mate Claude có thể ghi sở thích của captain vào `~/.claude/projects/<slug>/memory/MEMORY.md` thay vì `mate/memory.md`, tức là ra ngoài `.matev2/`, gắn với đường dẫn tuyệt đối, vô hình với Mate Codex và với app, và sống sót qua `rm -rf .matev2` (trái với quyết định 6 và quy ước "xoá `.matev2/` là xoá toàn bộ state", `docs/mvp.md` mục 3).
- **Codex**: `~/.codex/memories/` rỗng (không đổi từ tháng 5), `~/.codex/memories_1.sqlite` có 0 dòng trong `stage1_outputs` và 0 job; không có trí nhớ Codex nào cho Mate.
- **Codex resume**: `codex resume --help` (0.154.0) in `codex resume [OPTIONS] [SESSION_ID] [PROMPT]` và "picker by default; use --last to continue the most recent".
  Picker chỉ là mặc định khi không có id; `internal/harness/codex.go:76-81` từ chối resume với lý do "`codex resume` opens an interactive session picker", tức là đọc sai help.
  firstmate ghi `codex resume <session-id>, using the id printed on quit` (`.agents/skills/harness-adapters/references/harness/codex.md:13`).
  Chưa kiểm chứng trong pane Herdr (dialog cập nhật/trust có thể vẫn hiện), nên đây là một phát hiện cần live test, không phải một sự thật đã đo.

### Sau M7, trong live test

Ở 13 Mate Claude của các lần chạy `TestLiveAcceptanceTwoProjects` và `TestLiveM7EmptyRepoDoesNotGuess` (scratchpad `runs/a1..a3,e1,e2,before1`), `memory.md` đều 9 byte.
`PROJECT.md` của `shop` trong `a1..a3` lớn lên 970–1.304 byte: Mate chép `## Durable facts` của scout kèm nguồn và chép cả một quyết định của captain với ngày (`the captain chose pages/checkout-express.html as "our checkout page" ... (captain, 2026-09-24)`).
Vậy luật mục 14 của M7 có tác dụng với `PROJECT.md`, nhưng nguồn được ghi bằng đường dẫn tuyệt đối vào scratchpad tạm (khoảng 150 byte mỗi dòng), đúng loại "temporary paths" mà firstmate cấm trong ghi chú bền (`AGENTS.md:560`).
Còn `memory.md` vẫn không có đường ghi nào hoạt động.

## 2. Q1 — Các lớp trí nhớ

### firstmate

| Lớp | Phạm vi | Ai ghi | Ai đọc, khi nào | Trích |
| --- | --- | --- | --- | --- |
| `AGENTS.md` (repo firstmate) | Mọi firstmate | PR qua pipeline | Harness nạp, luôn | `AGENTS.md:634-639`; trần 9.000 từ `VISION.md:39` |
| `data/captain.md` | Một home | firstmate, inspect-then-update | Digest session start | `AGENTS.md:97`, `:281` |
| `data/captain-shared.md` | Primary → secondmate | primary, `secondmate-provisioning` | Digest, chỉ đọc ở secondmate | `AGENTS.md:98`, `:282` |
| `data/learnings.md` | Một home | firstmate, có ngày, có bằng chứng | Digest | `AGENTS.md:99`, `:283` |
| `data/memory-archive.md` | Một home, kho lạnh | `/stow` | Không bao giờ nạp; `grep` khi cần | `.agents/skills/stow/SKILL.md:132-146` |
| `data/projects.md` | Registry mỏng | `project-management` | Digest | `AGENTS.md:100` |
| `data/backlog.md` (tasks-axi) | Hàng việc, phụ thuộc, lịch sử | Script khi spawn/teardown, firstmate cho ghi chú | Digest (bỏ Done) | `AGENTS.md:545-563`, `.tasks.toml:1-6` |
| `data/<id>/brief.md`, `report.md` | Một task | firstmate / crew | Crew; firstmate khi review | `AGENTS.md:102-103` |
| `state/<id>.meta`, `.status` | Một task | Script / crew | Digest; `.status` là sự kiện đánh thức, không phải sự thật | `AGENTS.md:106`, `:119`, `:169` |
| `state/<id>.backlog-close` | Một teardown dở | Script | Bootstrap replay | `AGENTS.md:117` |
| `state/.afk-contract` | Lần vắng mặt | Script, nguyên văn lời captain | Mọi harness | `AGENTS.md:155` |
| `config/startup-memory-budget` | Một home | Captain; bootstrap tạo mặc định 7.500 | `/stow` | `AGENTS.md:82`, `docs/configuration.md:274-286` |
| `AGENTS.md` của mỗi project | Một project | Chỉ crew, qua delivery path | Crew trong worktree | `AGENTS.md:285`, `:288-289`, `bin/fm-brief.sh:640-645` |
| Local skill git-excluded | Tri thức có điều kiện | Chỉ sau captain duyệt | Harness JIT | `.agents/skills/stow/SKILL.md:171-187` |
| Trí nhớ harness | Harness | Harness | Không phải nguồn chính | "canonical even if harness memory mirrors it" (`AGENTS.md:97`), "regardless of harness memory" (`AGENTS.md:170`) |
| Hội thoại | Session | Harness | Bộ đệm | "A restart must be a non-event because durable state and live backend inventory, not conversation memory, are authoritative." (`AGENTS.md:262`) |

Điều đáng chú ý: firstmate **không** có file bối cảnh project do orchestrator viết; `data/projects.md` chỉ là "thin fleet navigation registry" (`AGENTS.md:100`), vì orchestrator đọc được repo (`AGENTS.md:28`).

### matev2 hôm nay

Bảng lớp ở `docs/mvp.md:220-233` và manual `assets/mate/AGENTS.md.tmpl:44-57`: manual (app), `WORKSPACE.md` (captain), `PROJECT.md` (Mate từ `Durable facts`, captain sửa), `CREW.md` hai cấp (captain), `memory.md` + `backlog.md` (Mate), session harness (app giữ `session_id`).
`internal/mateassets/write.go:11-37` sinh lại `AGENTS.md`, `CLAUDE.md` và skill mỗi lần start, và chỉ tạo `memory.md`/`backlog.md` khi chưa có, không bao giờ ghi đè.
Không có kho lạnh, không có ngân sách, không có luật tier.
`matev2.db` là kho dẫn xuất cho timeline (`docs/timeline.md:1-10`), không chứa trí nhớ.

### Khoảng trống

- Bảng của matev2 nói **ai** ghi nhưng không nói **loại tri thức nào** đi đâu; firstmate định tuyến theo loại (`AGENTS.md:279-290`).
- `PROJECT.md` là lớp matev2 phải có mà firstmate không có, vì quyết định 1; nó cần luật riêng về nguồn và độ cũ.
- Trí nhớ harness (auto-memory của Claude) chưa được gọi tên, nên có thể thành lớp thứ bảy vô hình.

## 3. Q2 — Đường ghi

### firstmate: kích hoạt

- `/stow` do captain gọi, "before a session reset or context compaction, or periodically" (`.agents/skills/stow/SKILL.md:3`, `AGENTS.md:291`).
- Ngoài `/stow`, luật định tuyến áp dụng mọi lúc: sở thích captain vào `captain.md` "after inspect-then-update" (`AGENTS.md:281`), task note đi với backlog item (`AGENTS.md:284`).
- Việc dở được script ghi, không phải agent: spawn/teardown tự di chuyển dòng backlog (`AGENTS.md:553`), teardown ghi `.backlog-close` trước khi xoá để bootstrap replay (`AGENTS.md:117`), `/afk` ghi nguyên văn lời captain trước mọi việc khác (`AGENTS.md:155`, `:479`).
- Không có ghi tự động theo sự kiện kiểu "sau mỗi task viết một learning"; firstmate dựa vào định tuyến lúc làm và `/stow` định kỳ.

### firstmate: cái gì đáng nhớ

Quét hội thoại tìm năm loại (`skills/stow/SKILL.md:18-24`):

> "User preferences: a working-style, tooling, formatting, or approval preference the user stated in passing rather than through a config file. ... Operational gotchas: a sharp edge, workaround, recurring mistake, or non-obvious cause discovered while working here. Standing decisions: a choice made this session that should outlive it ... Undone next steps: anything left open or agreed to that has not yet been written down anywhere."

Và giữ trong bộ nhớ luôn-nạp chỉ những gì đáng trả tiền mỗi session (`.agents/skills/stow/SKILL.md:92-95`):

> "Keep in always-loaded memory only current captain preferences, authority and safety boundaries, recurring working style, fleet-wide or frequently relevant operating facts, and concise pointers that are expensive to rediscover."

Cái gì không đáng nhớ: bản sao của thứ đã có chủ khác (`skills/stow/SKILL.md:25-26`: "record a one-line pointer to that owner instead of a copy"), "completed incident and release chronology, stale versions and paths, transient task state, resolved alternatives, old metrics, and report-sized procedures" (`.agents/skills/stow/SKILL.md:110`), và bí mật (`skills/stow/SKILL.md:150`).
Với backlog: "Keep free-form notes free of temporary paths, moving versions, ephemeral identifiers, and copied state that will rot." (`AGENTS.md:560`).

### firstmate: hình dạng mục nhớ

Một dòng, marker cuối dòng là HTML comment ngắn, vì byte của marker cũng tính vào ngân sách (`.agents/skills/stow/SKILL.md:19-33`):

```markdown
- Treehouse pool slots share one repo, so workers must create their task branch before editing. <!--a:2026-08-03-->
- While state/.afk exists, the away-daemon owns triage (until the afk-wake fix lands; tracked: afk-pi-wake-bypass-r1). <!--p:2026-07-20-->
- Never restart the shared no-mistakes daemon while runs are active. <!--P-->
```

Ba tier: `pinned` không bao giờ hết hạn; `aging` cũ sau 30 ngày không được củng cố; `perishable` cũ sau 7 ngày và "its prose must name a checkable expiry condition" (`:37-41`).
Mặc định theo file: `captain.md` là pinned, `learnings.md` là aging (`:45`).
Commit `5ade9efa` ghi lý do marker ngắn: bản dogfood đầu làm ngân sách tệ hơn (thâm hụt từ 624 lên 1.107 token), rút marker cắt khoảng 76% chi phí metadata.

### firstmate: dedup, cập nhật tại chỗ, prune

- Inspect-then-update: "read the destination file's current contents in full ... classify the finding against what is already there: new, duplicate, superseding an existing entry, or evidence that an existing entry is now obsolete ... rather than blindly appending" (`skills/stow/SKILL.md:61-65`); "rewrite and prune rather than append forever" (`AGENTS.md:99`).
- Củng cố cần bằng chứng: "**Hard rule: reinforcement requires independent evidence from this session that you can name in the receipt; plausibility, importance, prior knowledge, and the entry's own text are not evidence**" (`.agents/skills/stow/SKILL.md:99`); "re-reading memory is never reinforcement" (`skills/stow/SKILL.md:124`).
- Prune không bao giờ là xoá: "Stale never means deleted: pruning an entry from an editable memory file always means moving it to `data/memory-archive.md`" (`.agents/skills/stow/SKILL.md:134`), với nguồn, tier, ngày củng cố cuối và lý do (`:135-146`).
- Ngân sách: 7.500 token ước lượng cho ba file, ước lượng `ceil(UTF-8 bytes / 3)` (`docs/configuration.md:274-286`, `bin/fm-startup-memory-budget.sh:1-12`); một pass không được kết thúc trên ngân sách, phải mở một quyết định cho captain (`.agents/skills/stow/SKILL.md:118-126`); "A net increase is allowed only for a genuinely new current fact with no stronger owner." (`:128`).
- Offload: tri thức đúng nhưng có điều kiện chuyển sang skill cục bộ git-excluded, chỉ khi đích đã sống (`:148-209`).

### matev2 hôm nay

- Manual: "`matev2 remember ...` lands in a later task. Until then you append to `memory.md` by editing the file yourself." (`assets/mate/AGENTS.md.tmpl:302`) — chữ "append" là đúng điều firstmate cấm.
- Spec nói Mate ghi "qua `matev2 remember`" (`docs/mvp.md:228`), lệnh đó không tồn tại.
- Mục 14 nói `memory.md` giữ "captain preferences, standing instructions, and anything you have learned about how this particular captain wants work handled" (`assets/mate/AGENTS.md.tmpl:685`) nhưng không nói khi nào ghi, hình dạng, ngày, nguồn.
- `PROJECT.md` có trigger rõ (đọc xong report scout, `assets/mate/AGENTS.md.tmpl:687-688`) và nguồn bắt buộc, nhưng không có ngày, không có mốc commit.

### Khoảng trống

Không có trigger nào cho bài học vận hành (case 2 của `shop`) và sự thật tiêu cực về repo (case 3), không có luật inspect-then-update, không có ngày, không có ngân sách, không có kho lạnh.

## 4. Q3 — Đường đọc

### firstmate: bootstrap

Một lệnh, một digest (`AGENTS.md:174-183`, `bin/fm-session-start.sh:1-12`):

> "Every one of those reads is UNCONDITIONAL at every session start, so they belong in a script, not in N agent turns."

Thứ tự in (`AGENTS.md:193-213`): lock, bootstrap, wake queue, khối chỉ dẫn giám sát, hợp đồng read-once, digest trạng thái fleet (backlog, mọi `.meta`, đuôi `.status` có nhãn "wake-EVENT history, not current state", liveness), network, và **cuối cùng** là context digest: toàn văn `projects.md`, `secondmates.md`, `captain.md`, `captain-shared.md`, `learnings.md`.
Lý do trí nhớ đứng cuối (`bin/fm-session-start.sh:86-99`):

> "What a truncated tail drops must therefore be the CHEAPEST thing to lose. Curated memory is stable session to session, is already governed by a captain-set budget ..., and is recoverable with one targeted read; live fleet identity ... changes every session and is exactly what recovery depends on."

File vắng mặt in `ABSENT`, khác với file rỗng, vì vắng mặt có nghĩa (`AGENTS.md:212`, `bin/fm-session-start.sh:369-385`).
Digest không in Done (`bin/fm-session-start.sh:131-133`: "10 done rows cost 3.3KB in an observed main-home digest"), in đủ mọi dòng in-flight/held/blocked, và chỉ giới hạn hàng đợi (`:134-143`).
"Read the complete digest once and trust it" (`AGENTS.md:180-182`).

### firstmate: vào brief

Không có cơ chế tự động nào đưa `learnings.md` vào brief; orchestrator đọc nó ở session start rồi tự viết vào spec.
Kênh chính của tri thức project là `AGENTS.md` của project, mà crew tự đọc trong worktree (`bin/fm-brief.sh:640-645`).
Lời captain và nội dung report đi vào `## Captain's intent` dưới dạng nội dung, không con trỏ (`AGENTS.md:568`); báo cáo trước đã phân tích kỹ.

### firstmate: lúc review

Review dựng lại hợp đồng từ brief và lời captain, không từ trí nhớ (`.agents/skills/ask-user-authority/SKILL.md:25-26`); sự thật dễ đổi phải đọc lại từ nguồn: "Verify volatile details against their authoritative config, live system, or API before acting" (`AGENTS.md:562`); URL PR "copied verbatim from the task's ready status or `pr=` metadata and never assembled from memory" (`AGENTS.md:542`).

### matev2 hôm nay

Manual mục 3 là một checklist tám bước mà Mate tự chạy (`assets/mate/AGENTS.md.tmpl:71-86`): đọc `WORKSPACE.md`, `PROJECT.md`, `memory.md`, `backlog.md`, chạy `matev2 backlog`, chạy `matev2 project facts`, quyết `PROJECT.md` có dùng được không, chờ captain.
Thứ tự ngược với firstmate: trí nhớ trước, trạng thái sống sau.
Không có gì kích hoạt checklist ngoài lượt đầu của captain: `internal/spawn/start.go` không gửi prompt khởi động nào, và hook của Mate chỉ có `UserPromptSubmit` và `Stop` (`internal/spawn/claude_settings.go:15-23`).
Vào brief: `## What we already know` lấy từ `PROJECT.md` và report, mỗi dòng có nguồn (`assets/mate/AGENTS.md.tmpl:361-363`); brief không đọc `memory.md` (`:351`).
Lúc intake: "consult the evidence you already have: `PROJECT.md`, `memory.md`, and the reports and hand-backs of earlier Crews" (`:307`), khớp `AGENTS.md:310` của firstmate.

### Khoảng trống

Đường đọc của matev2 phụ thuộc vào việc Mate nghe manual; bài học chung của task 31 là "manual được đọc một lần lúc bootstrap, output của tool được đọc mỗi lần" (`docs/mvp.md` mục 7).
firstmate biến đường đọc thành output của một script, và biến việc chạy script thành việc của harness.

## 5. Q4 — Compaction và liên tục

### firstmate

- Hai tầng giao digest (`docs/sessionstart-nudge.md:8-13`): tầng Run (Claude, `codex exec`, Pi, omp, Cursor) chạy `fm-session-start.sh` qua hook và đưa output vào context trước lượt đầu; tầng Nudge chỉ nhắc agent chạy.
- Định tuyến theo `source` của hook (`docs/sessionstart-nudge.md:24-32`):

| Source | Hành động | Lý do (nguyên văn) |
| --- | --- | --- |
| `startup`, `new` | Digest đầy đủ | "a true session start that has not taken the helm" |
| `clear`, `compact` | `--reemit` | "This process normally has the helm and lost only its context" |
| `resume`, `reload`, `fork` | Nudge | "Prior context is restored, so re-running is redundant" |
| không đọc được | Digest đầy đủ | "Taking the helm redundantly is cheap and idempotent; not taking it is the bug this tier exists to fix." |

- "Compaction is covered where a tracked adapter delivers that source because a compacted session has lost exactly the digest it needs, and resume is excluded from the run because it restores that digest instead of losing it." (`docs/sessionstart-nudge.md:31-32`).
- `--reemit` bỏ các sweep có tác dụng phụ đã chạy lúc startup nhưng vẫn in lại mọi thứ khác (`bin/fm-session-start.sh:194-209`).
- Claude: một hook `SessionStart` không matcher trong `.claude/settings.json`, đọc `source` từ payload (`docs/sessionstart-nudge.md:73`).
- Codex interactive TUI: không có đường nào; "Codex 0.146.0 does not fire the tracked project `SessionStart` hook in its interactive TUI; Firstmate ships no global hook, has no tracked compaction or re-emit channel" (`docs/sessionstart-nudge.md:13`, `:75`).
- Trước khi mất context: `/stow` với "Open-record persistence" (`.agents/skills/stow/SKILL.md:236-245`):

> "A reset destroys whatever exists only in this session, and that includes what you have learned about work already under way, not just facts worth remembering. So before the reset, make sure the important open work you are holding in context is durably recorded: file what was never filed, and correct what you now know is stale."

  Commit `e518906a` ghi sự cố: "A shipped PR with no backlog item, a queued umbrella whose phases had merged, and four decision holds left open after their answers shipped all survived repeated stows."
  Và giới hạn của nó: "It is not a reconciliation of durable records against repository or forge reality ... and must never be reported as one." (`:244`).
- Receipt kết thúc bằng một phán quyết reset-safe trung thực: "nothing this session knew has been lost", không phải "các bản ghi đều đúng" (`:268-272`); bản công khai thêm "RESUME POINTER" liệt kê file cần nạp (`skills/stow/SKILL.md:87-88`).
- Resume harness không phải cơ chế liên tục: "`resume` is not a verb. ... `relaunch` covers the same need ..., because the brief on disk - not a harness-private session - is the durable instruction" (`docs/agent-control.md:58-60`).
- "A low context reading is not wedging; modern harnesses auto-compact and keep going." (`.agents/skills/stuck-crewmate-recovery/SKILL.md:80`).

### matev2 hôm nay

- Claude Mate: `--resume <session_id>` khi `mate.meta` có id cùng harness (`internal/spawn/start.go:187-207`, `:451-455`); có live test `TestLiveSpawnMateResumeRemembers` (`internal/spawn/live_resume_test.go:92`, `:152`).
- Codex Mate: luôn khởi động mới (`internal/harness/codex.go:75-82`), và `session_id=` không được ghi (`mate.meta` của `shop`).
- Không có hook nào cho startup, compact hay clear; không có gì chạy trước khi app restart Mate (Actions menu "restart mate", `docs/mvp.md` mục 5).
- Manual chỉ có: "You may have been restarted mid-session. All truth lives in the files above and in the Crew panes themselves; your conversation memory is a cache, not the source of truth." (`assets/mate/AGENTS.md.tmpl:85-86`).

### Khoảng trống

Câu của manual đúng tinh thần `AGENTS.md:262`, nhưng không có cơ chế nào làm cho nó đúng: sau compaction của Claude không có gì tái nạp; trước restart không có gì lưu; với Codex mọi restart là mất hết hội thoại, và file thì trống (mục 1).

## 6. Q5 — Tri thức project, tri thức orchestrator, sở thích captain

### firstmate

Bảng định tuyến (`AGENTS.md:279-290`), nguyên văn:

> "Route durable knowledge to its most specific owner:
> - Home-domain captain preferences and working style belong in `data/captain.md` after inspect-then-update.
> - Captain preferences shared across secondmate domains belong in the primary home's `data/captain-shared.md` under the `secondmate-provisioning` contract.
> - Fleet-local operational facts belong in curated, home-local `data/learnings.md`.
> - Task-scoped notes belong with the backlog item, and investigation findings belong in the scout report.
> - Knowledge useful to almost every contributor to one project belongs in that project's committed `AGENTS.md`.
> - Knowledge general to every firstmate user belongs in this repo's shared tracked surface.
>
> Firstmate never writes a project's `AGENTS.md` directly. ... Keep fleet delivery posture and captain-private strategy out of project memory."

Tri thức crew phát hiện chảy về qua `AGENTS.md` của project, trong chính PR của task (`bin/fm-brief.sh:640-645`), với file `CLAUDE.md` là con trỏ `@AGENTS.md` thật (`bin/fm-ensure-agents-md.sh:1-28`).
Report scout là đích của phát hiện điều tra (`AGENTS.md:284`).
Học từ secondmate: "Keep every `data/learnings.md` fully local by captain decision; route fleet-general machinery facts into tracked documentation" (`.agents/skills/secondmate-provisioning/SKILL.md:129`).
Can thiệp trực tiếp của captain vào pane crew là "authoritative and reconcile it at the next supervision review" (`AGENTS.md:40`).

### matev2 hôm nay

- Tri thức code: `AGENTS.md` của repo qua crew (`assets/mate/AGENTS.md.tmpl:682-684`, `assets/crew/brief.md.tmpl:66-70`); khớp firstmate.
- Bối cảnh project: `PROJECT.md` từ `## Durable facts` của scout (`assets/crew/brief.md.tmpl:60`, `assets/mate/AGENTS.md.tmpl:686-688`); không có tương đương ở firstmate.
- Sở thích captain và "standing instructions": `memory.md` (`assets/mate/AGENTS.md.tmpl:685`); nhưng quy tắc captain cho mọi Mate nằm ở `WORKSPACE.md`, cho crew ở `CREW.md`, cả hai do captain viết (`:49`, `:59-60`).
- Hand-back ship: sửa `PROJECT.md` khi `Still open` nói `## What we already know` sai (`:688`).

### Khoảng trống

- Không có đích cho **bài học vận hành** (loại `learnings.md` của firstmate): "crew scout ghi report vào worktree nếu brief không cấm" không phải sở thích captain, không phải sự thật project, không phải tri thức code.
- Không có đường cho **quy tắc crew mà Mate phát hiện**: case 2 của `shop` là một quy tắc cho mọi crew của project, đúng chỗ là `projects/shop/CREW.md`, nhưng file đó của captain; Mate cần một luật "đề xuất cho captain".
- Quyết định sản phẩm của captain: live test cho thấy Mate đã tự để nó vào `PROJECT.md` (mục 1); firstmate để quyết định ở backlog item bị "hold" và ghi nguyên văn câu trả lời (`.agents/skills/captain-hold-lifecycle/SKILL.md:30`).

## 7. Q6 — Backlog và trạng thái task như trí nhớ

### firstmate

- Backlog là hàng việc bền, "It tracks work items only, never agents" (`AGENTS.md:547-548`); dispatch và completion tự di chuyển dòng trong cùng script tạo/xoá bản ghi task (`AGENTS.md:553`, `docs/configuration.md:121`).
- Quyết định là task bị giữ: "A decision is simply a task held for the captain" (`AGENTS.md:550`); đóng chỉ bằng lời captain thật: "`bin/fm-captain-hold.sh answer` writes his exact words into the task" (`.agents/skills/captain-hold-lifecycle/SKILL.md:30`).
- "Obligations are closed by records, not by recollection: a promised reply, an open decision, or a queued wake is retired only by the durable event that answers it." (`VISION.md:45`).
- Sau restart: bootstrap hoàn tất các close dở từ `.backlog-close` và đánh dấu In flight mọi item home đang có worker (`bin/fm-bootstrap.sh:89-99`); rồi "reconcile reality with durable records before taking new work" (`AGENTS.md:251`); dòng `.status` là "wake event, not current-state truth" (`AGENTS.md:169`).
- Giữ lại: `done_keep = 10`, phần còn lại vào `data/done-archive.md` (`.tasks.toml:1-6`); digest khởi động bỏ Done (`bin/fm-session-start.sh:131-133`).
- Ghi chú task: "Inspect the current task note before replacing its considered body, and archive the superseded body when recoverability matters rather than appending by default." (`AGENTS.md:561`).

### matev2 hôm nay

- `backlog.md` do Mate tự ghi, app không bao giờ đụng (`assets/mate/AGENTS.md.tmpl:690`); `matev2 backlog` là bảng chỉ đọc của `.meta`/`.status` để đối chiếu (`cmd/matev2/backlog.go:17-21`, manual `:77`, `:286`, `:691`).
- Không có mục "đang chờ captain"; không có giới hạn Done (`shop` có Completed và Failed tích luỹ); không có luật ghi chú.

### Khoảng trống

`matev2 backlog` đã là phần "reconcile với thực tế" tốt; cái thiếu là hai loại việc dở không có crew nào đại diện: câu hỏi đang treo cho captain (case 5 của `shop`) và lời hứa của Mate ("tôi sẽ chạy lại ngay khi bạn khởi động lại", `shop` 2026-09-18T09:52:45Z).

## 8. Q7 — Tính đúng: tránh trí nhớ cũ hoặc sai

### firstmate

- Tier có đồng hồ; `aging` phải "re-prove itself ... never kept by inertia alone" (`.agents/skills/stow/SKILL.md:38`); `perishable` phải nêu điều kiện hết hạn kiểm được (`:39-40`).
- Củng cố chỉ bằng bằng chứng trong session (`:99`); mục cũ không rõ tuổi được một chu kỳ ân hạn `<!--g-->` rồi hoặc có bằng chứng hoặc vào kho lạnh (`:247-256`).
- "Verify volatile details against their authoritative config, live system, or API before acting, and correct or delete stale prose immediately." (`AGENTS.md:562`).
- Nguồn có thẩm quyền được đặt tên cho từng loại trạng thái: `fm-crew-state.sh` cho trạng thái crew (`AGENTS.md:169`), forge cho PR (`AGENTS.md:542`), live backend inventory cho endpoint (`AGENTS.md:262`).
- Receipt không được nói nhiều hơn điều nó kiểm: "It is never a claim that the home's durable records are correct, because this pass checks no record the session did not name." (`.agents/skills/stow/SKILL.md:271`).
- Ưu tiên con trỏ hơn bản sao để ghi chú không cũ độc lập với nguồn (`skills/stow/SKILL.md:25-26`, `bin/fm-brief.sh:643`).

### matev2 hôm nay

- Đúng tinh thần ở một chỗ: `matev2 backlog` thắng `backlog.md` sau restart (`assets/mate/AGENTS.md.tmpl:691`).
- `PROJECT.md` bắt buộc có nguồn (`:687`) và `## What we already know` bắt buộc có dòng `Unknown:` (`:363`).
- Không có ngày hay mốc commit trên sự thật về repo; một dòng "main has six files" viết trước ba lần merge vẫn trông như hiện tại.

### Khoảng trống

Sự thật về trạng thái repo cũ đi theo commit, không theo lịch; firstmate dùng đồng hồ vì nó không có lớp tương đương `PROJECT.md`.
matev2 cần một mốc hợp với quyết định 1: `main@<sha>` từ `matev2 project facts`, là metadata git, không đọc file nào.

## 9. Q8 — Skill và lệnh

| firstmate | Làm gì | Trích |
| --- | --- | --- |
| `/stow` (nội bộ, 309 dòng) | Quét tri thức, định tuyến, lưu việc dở, curate tier/ngân sách/kho lạnh, receipt, cascade sang secondmate | `.agents/skills/stow/SKILL.md:3`: "Sweep the current session for uncaptured durable knowledge, file it to disk, persist the open work records this session knows are unfiled or now wrong, and curate the home's tiered, decaying startup memory before a context reset." |
| `stow` công khai (151 dòng) | Cùng kỷ luật, không đường dẫn riêng, fallback `.stow-notes.md` | `skills/stow/SKILL.md:11-14` |
| `bin/fm-session-start.sh` | Digest khởi động một lệnh | `AGENTS.md:174-177` |
| `bin/fm-sessionstart-run.sh` | Định tuyến `source` của hook | `docs/sessionstart-nudge.md:21-22` |
| `bin/fm-startup-memory-budget.sh read|report` | Ngân sách và đo | `bin/fm-startup-memory-budget.sh:1-12` |
| `bin/fm-ensure-agents-md.sh` | Quy ước `AGENTS.md` + con trỏ `CLAUDE.md` trong worktree crew | `bin/fm-ensure-agents-md.sh:1-28` |
| `bin/fm-captain-hold.sh hold|answer` | Quyết định là task bị giữ, đóng bằng lời captain | `AGENTS.md:550`, `.agents/skills/captain-hold-lifecycle/SKILL.md:30` |
| `bin/fm-control.sh relaunch --note` | Thay agent, mang brief + ghi chú tiến độ | `.agents/skills/stuck-crewmate-recovery/SKILL.md:77`, `docs/agent-control.md:62-64` |

Receipt của `/stow` bắt buộc một hành động cho mỗi file, chỉ từ bảy động từ `unchanged`, `added`, `rewritten`, `pruned`, `routed`, `archived`, `proposed-offload` (`.agents/skills/stow/SKILL.md:263`).

matev2: không có skill trí nhớ; bốn skill hiện có là `harness-adapters`, `stuck-crew-recovery`, `decision-authority`, `diagnostic-reasoning` (`assets/mate/AGENTS.md.tmpl:62-67`); `matev2 remember` chưa có (`:302`).

## 10. Q9 — Những điều cố ý khác mà matev2 thiếu

1. **Script lo cơ chế, agent lo phán đoán** áp cho trí nhớ: đo ngân sách, marker, định dạng là script; dedup và chọn đích là agent ("A rigid script must never adjudicate meaning", `VISION.md:35`).
2. **Chi phí trí nhớ là chi phí thật**: "every agent's context stays lean" (`VISION.md:37`); marker ngắn vì marker cũng tốn (`.agents/skills/stow/SKILL.md:19`, commit `5ade9efa`).
3. **Vắng mặt khác rỗng** (`AGENTS.md:212`).
4. **Tri thức có điều kiện không trả tiền mỗi session**: offload sang nơi nạp khi cần (`.agents/skills/stow/SKILL.md:150`).
5. **Lời captain nguyên văn là bản ghi bền** ở mọi chỗ có quyền lực: `.afk-contract` (`AGENTS.md:155`), `answer` của captain hold (`.agents/skills/captain-hold-lifecycle/SKILL.md:30`), `## Captain's intent` (`AGENTS.md:568`).
6. **Ước lượng thận trọng, không cần tokenizer**: `ceil(bytes/3)` (`docs/configuration.md:283`).
7. **Không bao giờ nói reset-safe khi trên ngân sách** (`.agents/skills/stow/SKILL.md:130`).
8. **Tự quản trị file trí nhớ**: mỗi `AGENTS.md` có mục "Maintaining this file" với "Prefer rewriting or pruning existing entries over appending new ones." (`AGENTS.md:634-639`), được `fm-ensure-agents-md.sh` chèn vào mọi project.

## 11. Danh sách áp dụng, xếp hạng

Ký hiệu cỡ: S = một buổi, M = một task PR, L = nhiều task.
Xếp theo mức mất mát phòng được (đo ở mục 1) trên đơn vị công sức.

### Áp dụng nguyên xi

**A1. Bảng định tuyến tri thức, mỗi loại một chủ.**
Cái gì: bản matev2 của `AGENTS.md:279-290`, xem bảng ở mục 12.2.
Vì sao: case 2, 3, 4 của `shop` là tri thức không có đích.
Ở đâu: manual mục 2 (thay bảng lớp) và mục 14; `docs/mvp.md` mục 6.
Cỡ: S.

**A2. Inspect-then-update; viết lại và prune, không append.**
Cái gì: `AGENTS.md:99`, `skills/stow/SKILL.md:61-65`; bỏ chữ "append" ở manual mục 4.
Vì sao: một file chỉ lớn lên sẽ bị bỏ không đọc, hoặc đọc thấy mâu thuẫn như case 3.
Ở đâu: manual mục 4 và 14.
Cỡ: S.

**A3. Việc dở được đóng bằng bản ghi: câu hỏi cho captain và lời hứa nằm trong `backlog.md`.**
Cái gì: `VISION.md:45`, `AGENTS.md:550`; thêm mục `## Held for the captain` trong `backlog.md`, mỗi dòng: task, ngày hỏi, câu hỏi nguyên văn đã gửi, đang chờ gì; khi captain trả lời thì ghi nguyên văn câu trả lời vào task rồi mới bỏ dòng.
Vì sao: case 5 của `shop`; Codex Mate khởi động lại sẽ không biết mình đã hỏi.
Ở đâu: manual mục 13 (escalation) và 14.
Cỡ: S.

**A4. Trí nhớ harness không phải nguồn chính.**
Cái gì: "canonical even if harness memory mirrors it" (`AGENTS.md:97`, `:170`) thành một câu manual: `memory.md` là trí nhớ duy nhất, không bao giờ ghi vào auto-memory của harness.
Vì sao: 97/97 cwd Mate Claude có thư mục `memory/` của Claude Code; một lần ghi vào đó là tri thức nằm ngoài `.matev2/`.
Ở đâu: manual mục 2; phần tắt hẳn ở B6.
Cỡ: S.

**A5. Củng cố cần bằng chứng; đọc lại không phải củng cố.**
Cái gì: `.agents/skills/stow/SKILL.md:99`, `skills/stow/SKILL.md:123-124`.
Ở đâu: skill `stow` (B4).
Cỡ: S, đi cùng B4.

**A6. Vệ sinh ghi chú: không đường dẫn tạm, không bản sao sẽ cũ, con trỏ hơn bản sao.**
Cái gì: `AGENTS.md:560-563`; nguồn viết tương đối workspace (`crews/esp1/report.md §Durable facts`), không tuyệt đối.
Vì sao: `PROJECT.md` trong live test mang đường dẫn scratchpad tuyệt đối khoảng 150 byte mỗi dòng.
Ở đâu: manual mục 14.
Cỡ: S.

### Áp dụng có điều chỉnh

**B1. `matev2 recall <project>`: một digest khởi động có thứ tự.**
Cái gì: bản matev2 của `bin/fm-session-start.sh` chỉ phần đọc: (1) trạng thái sống trước — bảng `matev2 backlog`, inbox chưa giải quyết, outbox đang chờ, chế độ; (2) `project facts`; (3) `PROJECT.md`; (4) `backlog.md`; (5) `memory.md`; (6) `WORKSPACE.md`, và dòng con trỏ tới hai `CREW.md`; mỗi file có khung rõ, `ABSENT` khác rỗng; một dòng hợp đồng "read once, trust it"; dòng cuối báo ngân sách (B5) và những id trong `backlog.md` In flight không có trong bảng (so khớp cấu trúc dòng `- <id>`, không phán nghĩa).
Điều chỉnh vì: Mate không đọc code, nên digest chỉ đọc `.matev2/` và metadata git; file phẳng là nguồn, `matev2.db` chỉ là phần tuỳ chọn (B10); không có lock hay sweep có tác dụng phụ như firstmate, vì một project một Mate và `refuseIfLive` đã chặn Mate thứ hai (`internal/spawn/start.go:122-124`).
Vì sao: task 31 đã chứng minh output của tool được đọc còn manual thì không; và thứ tự "trạng thái trước, trí nhớ sau" chịu được cắt đuôi (`bin/fm-session-start.sh:86-99`).
Ở đâu: lệnh mới trong `cmd/matev2`; manual mục 3 rút thành "run `matev2 recall`, then decide whether `PROJECT.md` can do its job".
Cỡ: M.

**B2. Hook `SessionStart` cho Mate Claude.**
Cái gì: `matev2 hook mate-session` đọc `source` từ payload, theo bảng `docs/sessionstart-nudge.md:24-32`: `startup`, `clear`, `compact` → in toàn bộ `recall` vào context; `resume` → chỉ in phần trạng thái sống (bước 1 của B1), vì hội thoại còn nhưng thế giới có thể đã đổi trong lúc Mate tắt; source lạ → in toàn bộ.
Điều chỉnh vì: firstmate đẩy `resume` sang nudge vì fleet của nó được reconcile riêng; ở matev2, restart Mate thường đi cùng restart console, lúc crew đã đổi trạng thái.
Codex: không có tầng Run cho TUI (`docs/sessionstart-nudge.md:13`, đo trên 0.146.0); `~/.codex/hooks.json` của người dùng có `SessionStart` toàn cục cho Herdr nhưng chưa ai đo nó có bắn trong TUI 0.154 không, nên Mate Codex vẫn chạy `recall` theo manual ở lượt đầu, như Mate `shop` đã tự đọc lại file mỗi lượt.
Ở đâu: `internal/spawn/claude_settings.go` (thêm `SessionStart`), `cmd/matev2/hook.go`; live test compaction bằng `/compact` trong pane lab.
Cỡ: M.

**B3. Hình dạng mục nhớ: một dòng, có nguồn, có marker tier ngắn.**
Cái gì: marker `<!--a:YYYY-MM-DD-->`, `<!--p:YYYY-MM-DD-->` của `.agents/skills/stow/SKILL.md:19-33`; `## Captain` mặc định pinned (không marker), `## Lessons` mặc định aging (30 ngày), perishable (7 ngày) chỉ khi nêu được điều kiện hết hạn kiểm được; nguồn trong ngoặc trước marker.
Điều chỉnh vì: một file (`memory.md`) hai mục thay cho hai file `captain.md`/`learnings.md`; bỏ horizon theo lượt (`docs/configuration.md:288-296`) và ân hạn `<!--g-->` vì không có trí nhớ cũ nào để di trú (13/13 Mate đo được đều 9 byte).
`PROJECT.md` không dùng đồng hồ mà dùng mốc `main@<sha>` cho sự thật về trạng thái repo (B8).
Ở đâu: manual mục 14, skill `stow`.
Cỡ: S.

**B4. Skill `stow` cho Mate.**
Cái gì: bản rút gọn của `.agents/skills/stow/SKILL.md`: quét hội thoại theo năm loại (`skills/stow/SKILL.md:18-24`), định tuyến theo A1, inspect-then-update, lưu việc dở (`:236-245`), curate (decay, gộp, kho lạnh), receipt với bảy động từ và câu reset-safe trung thực.
Trigger: captain nói stow/"ghi nhớ lại", dòng `⟦matev2⟧ stow:` app gửi trước khi restart Mate (B7), `recall` báo trên ngân sách, và sau `crew stop` nếu task đó đã sinh ra một lời sửa (B9).
Điều chỉnh vì: bỏ cascade secondmate (`:275-301`), bỏ offload sang skill (`:148-209`), bỏ `captain-shared.md`.
Mate không ghi được ngoài cwd trừ `PROJECT.md`, nên đích quy tắc crew và quy tắc workspace là **đề xuất cho captain** (receipt ghi `routed: proposed to captain for projects/shop/CREW.md`).
Ở đâu: `assets/mate/skills/stow/SKILL.md.tmpl`, danh sách skill ở manual mục 2.
Cỡ: M.

**B5. `matev2 remember` và `matev2 memory check`.**
Cái gì: `remember <project> --captain|--lesson [--perishable "<expiry>"] --source <src> "<một dòng>"` thêm một mục đúng hình dạng B3 với ngày hôm nay; `memory check <project>` in ngân sách (ước lượng `ceil(bytes/3)`, `docs/configuration.md:283`) cho `memory.md` + `PROJECT.md` + `WORKSPACE.md`, mục thiếu nguồn hoặc marker, mục aging/perishable đã cũ, nguồn là đường dẫn tuyệt đối hay nằm ngoài workspace.
Điều chỉnh vì: script lo hình dạng, agent lo dedup (`VISION.md:34-35`): `remember` không bao giờ tự gộp hay xoá; Mate vẫn sửa file tại chỗ khi viết lại, và `memory check` là cổng hình dạng giống `brief check`.
Ngân sách đề xuất: 4.000 token ước lượng (12 KB) cho ba file, nhỏ hơn 7.500 của firstmate vì manual của Mate đã chiếm 58 KB.
Ở đâu: `cmd/matev2`; manual mục 4 (thay "Not yet available").
Cỡ: M.

**B6. Tắt auto-memory của Claude cho Mate.**
Cái gì: Mate Claude khởi chạy với auto-memory tắt, qua `settings.json` của Mate hoặc biến môi trường của pane.
Điều chỉnh vì: firstmate chỉ nói bằng lời (`AGENTS.md:97`); matev2 đã chọn "ràng buộc bằng cấu trúc, không bằng lời dặn" (quyết định 1).
Tên khoá chính xác chưa được đo trong nghiên cứu này; task phải kiểm trên Claude Code đang cài rằng một Mate được bảo "remember X" ghi vào `mate/memory.md` và thư mục `~/.claude/projects/<slug>/memory/` vẫn rỗng.
Ở đâu: `internal/spawn/claude_settings.go` hoặc launch env trong `internal/spawn/start.go`.
Cỡ: S, cộng một live test.

**B7. Restart Mate lưu trước khi mất.**
Cái gì: Actions menu "restart mate" (và `matev2 mate stop` khi Mate rảnh) gửi `⟦matev2⟧ stow: you are about to be restarted; record anything that exists only in this conversation (skill stow), then end your turn` qua outbox, chờ turn kết thúc (hook Stop, có trần thời gian), rồi mới restart.
Điều chỉnh vì: firstmate để captain gọi `/stow` (`AGENTS.md:291`); matev2 có một điểm restart duy nhất do app sở hữu, nên làm được bằng cấu trúc.
Luật gửi vào pane Mate vẫn giữ: chỉ khi captain bấm (chính cú bấm restart) hoặc ở chế độ auto; composer có chữ thì hỏi captain thay vì gõ đè.
Ở đâu: `internal/ui/console` (action), `internal/outbox`, `internal/spawn`.
Cỡ: M.

**B8. Sự thật về trạng thái repo trong `PROJECT.md` có mốc commit.**
Cái gì: dòng về layout/lệnh/trạng thái ghi `(crews/k1/report.md §Durable facts, main@3f2a91c, 2026-09-24)`; `project facts` in thêm dòng `head: <sha>`; `recall` báo "PROJECT.md layout facts are anchored at main@3f2a91c; main has 4 newer commits" để Mate coi chúng là gợi ý.
Điều chỉnh vì: firstmate dùng đồng hồ (`.agents/skills/stow/SKILL.md:37-41`) và kiểm lại nguồn sống (`AGENTS.md:562`); Mate không đọc được nguồn sống nên cần một tín hiệu cũ không cần đọc file.
Ở đâu: `internal/facts`, manual mục 14, `recall`.
Cỡ: S.

**B9. Nhắc ghi ở chỗ Mate đang nhìn (bài học task 31).**
Cái gì: output của `crew stop` in thêm một dòng cuối "record: update backlog.md Done; if this task taught you anything durable, route it (skill stow)"; output của `matev2 send` gửi tới crew đã có brief (một lời sửa sau khi spawn) in "if this correction applies to future crews, route it: memory.md Lessons or propose it for CREW.md".
Điều chỉnh vì: firstmate không cần, vì `/stow` + digest; matev2 đã đo được Mate không nghe manual ở đúng loại hành vi này.
Ở đâu: `cmd/matev2/crew.go`, `cmd/matev2/send.go`.
Cỡ: S.

**B10. Timeline gợi ý, không lưu.**
Cái gì: khi `matev2.db` có mặt, `recall` thêm một mục cuối, bỏ qua được: "corrections you sent after spawn in the last 14 days" (bảng `message`, Mate → `crew:<id>` sau sự kiện spawn của crew đó) và "questions crews asked" (bảng `question`), để Mate tự quyết có gì đáng nhớ; và tỷ lệ context của lượt Mate mới nhất (`turn.context_tokens_after` trên cửa sổ của model) để console hiện "Mate context 82%" và gợi ý stow.
Điều chỉnh vì: db là dẫn xuất (quyết định 6); `recall` phải chạy đủ khi không có db, và db không bao giờ chứa một mục nhớ.
Không phán nghĩa bằng script: không regex tìm "luôn/never" trong lời captain (`VISION.md:35`).
Ở đâu: `internal/db`, `recall`, `internal/ui/console`.
Cỡ: M, ưu tiên thấp.

**B11. Resume cho Mate Codex.**
Cái gì: ghi `session_id` của Mate Codex (Herdr `agent_session.value` là uuid rollout, `docs/timeline.md` luật locator 3) và khởi chạy `codex resume <id>` thay vì từ chối.
Điều chỉnh vì: firstmate không dựa vào resume cho liên tục (`docs/agent-control.md:58-60`); ở matev2 đây là tiện lợi, còn khởi động mới vẫn phải không mất gì nhờ B1–B7.
Vì sao: `internal/harness/codex.go:76-81` đọc sai help của 0.154.0; hiện mọi restart của Mate Codex mất toàn bộ hội thoại.
Ở đâu: `internal/harness/codex.go`, `internal/spawn/start.go`; live test giống `TestLiveSpawnMateResumeRemembers` cho Codex, kể cả chuỗi dialog settle.
Cỡ: M.

**B12. Backlog: giữ 10 Done, bỏ Done khỏi `recall`.**
Cái gì: `.tasks.toml:1-6` (`done_keep = 10`), `bin/fm-session-start.sh:131-133`; Done cũ hơn chuyển xuống `mate/backlog-archive.md` hoặc chỉ còn trong `matev2 backlog --all`.
Điều chỉnh vì: `backlog.md` do Mate ghi, app không đụng; luật nằm ở manual, `recall` chỉ in In flight, Held, Queued.
Ở đâu: manual mục 14, `recall`.
Cỡ: S.

### Không áp dụng

- **Cascade `/stow` sang secondmate, `captain-shared.md`, kế thừa theo home** (`.agents/skills/stow/SKILL.md:275-301`, `.agents/skills/secondmate-provisioning/SKILL.md:127-129`): matev2 một Mate một project, không có cấp; `WORKSPACE.md` đã là lớp chung do captain viết.
- **Offload sang local skill git-excluded** (`.agents/skills/stow/SKILL.md:148-209`, `docs/verification/stow-memory.md`): skill của Mate là nội dung app sinh và bị ghi lại mỗi lần start (`internal/mateassets/write.go:43-45`), nên một skill Mate tự tạo không có vị trí sống ổn định; với ngân sách 12 KB, gộp và kho lạnh đủ. Xem lại khi `memory check` thường xuyên báo vượt.
- **Horizon theo số lượt** (`docs/configuration.md:288-296`): tinh chỉnh nhịp cho home stow hàng ngày; chưa có dữ liệu nhịp nào.
- **Di trú mục cũ bằng `<!--g-->`** (`.agents/skills/stow/SKILL.md:247-256`): không có trí nhớ cũ; mọi Mate đo được có `memory.md` 9 byte.
- **Session lock, `--reemit` bỏ sweep, baseline hash `AGENTS.md`** (`bin/fm-session-start.sh:188-221`, `docs/sessionstart-nudge.md:34-38`): cơ chế chống hai session cùng giành một home; matev2 đã có `refuseIfLive` và không có sweep có tác dụng phụ lúc bootstrap.
- **tasks-axi, captain-hold có khoá, `RECORD DIVERGENCE`** (`.agents/skills/captain-hold-lifecycle/SKILL.md:35-54`): matev2 đã chốt câu hỏi không có vòng đời và không có correlation id (`docs/mvp.md` mục 4); A3 chỉ lấy phần "ghi lại nguyên văn", không lấy máy.
- **`COMPACT_ADVISER_DISABLE`** (commit `1bb72cc5`): công cụ riêng trong môi trường của tác giả firstmate, không phải tính năng harness.
- **Mate ghi thẳng `AGENTS.md` của repo**: firstmate cũng không (`AGENTS.md:288`); matev2 giữ nguyên đường qua crew.

## 12. Thiết kế trí nhớ đề xuất cho matev2

### 12.1 Nguyên tắc

1. Hội thoại là bộ đệm; khởi động mới (Codex hôm nay, Claude sau compaction) phải không mất gì mà `/stow` đã ghi, và mọi việc dở phải có bản ghi.
2. Mỗi loại tri thức một chủ; Mate chỉ ghi trong `mate/` và `PROJECT.md`; mọi thứ của captain thì Mate đề xuất.
3. Script lo hình dạng, ngân sách, thứ tự đọc; Mate lo chọn đích, gộp, viết lại.
4. File phẳng là nguồn; `matev2.db` chỉ gợi ý; trí nhớ harness tắt.
5. Đọc bằng output của tool, không bằng lời dặn trong manual.

### 12.2 File, chủ, định dạng

| File | Chủ | Chứa gì | Không chứa gì | Đọc lúc |
| --- | --- | --- | --- | --- |
| `.matev2/WORKSPACE.md` | Captain | Quy tắc cho mọi Mate | Bất cứ gì Mate tự viết | `recall`, cuối |
| `.matev2/CREW.md`, `projects/<p>/CREW.md` | Captain | Quy tắc cho mọi Crew; app nối vào brief | Bản sao trong brief | `recall` in một dòng con trỏ |
| `projects/<p>/PROJECT.md` | Mate + captain | Project là gì, layout, cách build/test/chạy, trạng thái, nghiên cứu đã có, quyết định sản phẩm của captain | Tri thức code chi tiết (thuộc `AGENTS.md` của repo), sở thích captain về cách làm việc | `recall`; nguồn của `## What we already know` |
| `mate/memory.md` | Mate | `## Captain`: sở thích, cách làm việc, ranh giới quyền (pinned); `## Lessons`: bài học vận hành với crew/harness/app ở project này (aging hoặc perishable) | Sự thật về repo, trạng thái task, bản sao report | `recall` |
| `mate/memory-archive.md` | Mate | Mục đã rời `memory.md`, kèm nguồn, tier, ngày, lý do | — | Không bao giờ ở bootstrap; `grep` khi cần |
| `mate/backlog.md` | Mate | In flight, Held for the captain, Queued (`blocked-by:`), Done (10 mới nhất) | Đường dẫn tạm, bản sao status | `recall`, đối chiếu với bảng `matev2 backlog` |
| `crews/<id>/brief.md`, `handback.md`, `report.md` | Mate / Crew | Hợp đồng và kết quả của một task | — | Khi review; `PROJECT.md` trỏ tới |
| `mate/mate.meta` | App | `harness=`, `session_id=` (cả Codex, B11) | — | App, khi resume |
| `~/.claude/projects/<slug>/memory/` | Không ai (tắt, B6) | — | — | — |
| `.matev2/matev2.db` | App (observer) | Dẫn xuất | Mục nhớ | `recall` mục gợi ý, dashboard |

Định tuyến khi Mate học được điều gì:

| Mate vừa biết | Đi đâu |
| --- | --- |
| Captain thích/không thích một cách làm việc của Mate | `memory.md` `## Captain` |
| Quy tắc captain muốn mọi crew của project theo | Đề xuất cho captain thêm vào `projects/<p>/CREW.md`; tạm thời `memory.md` `## Lessons` và `## Build` của brief |
| Quy tắc cho mọi project | Đề xuất cho captain thêm vào `WORKSPACE.md` hoặc `.matev2/CREW.md` |
| Sự thật về project (có gì, chạy thế nào, trạng thái, không có gì) | `PROJECT.md`, có nguồn và mốc `main@<sha>` |
| Quyết định sản phẩm của captain | `PROJECT.md` `## Captain's decisions`, nguyên văn, có ngày |
| Tri thức code chi tiết | Crew ghi vào `AGENTS.md` của repo (đã có) |
| Bài học vận hành (crew, harness, app ở project này) | `memory.md` `## Lessons` |
| Câu hỏi đang chờ captain, lời hứa của Mate | `backlog.md` `## Held for the captain` |
| Lỗi hay giới hạn của chính matev2 | Nói với captain; `memory.md` `## Lessons` dạng perishable cho tới khi sửa |

### 12.3 Ví dụ từng file (dựng lại từ `shop`)

`mate/memory.md`:

```markdown
# Memory
<!-- tiers: see the stow skill -->

## Captain
- Speaks Vietnamese; answer and escalate in Vietnamese. (captain, 2026-09-17)
- Checks that research is delegated, not done by the Mate; say which Crew is doing it when reporting. (captain, "bạn có đang spawn 1 crew mới ko? hay tự làm đó", 2026-09-18)

## Lessons
- Scout Crews commit report files to their worktree unless the brief says to keep every report file under crews/<id>/ and commit nothing; proposed to the captain for projects/shop/CREW.md. (sent.log to rpi35 2026-09-18, power12 2026-09-19) <!--a:2026-09-19-->
- Crew spawn failed three times in a row with no pane or meta (rpi3, rpi33, rpi34); a spawn 30 minutes later worked; stop after two and tell the captain until the spawn fix lands. (backlog Failed rpi34) <!--p:2026-09-18-->
```

`projects/shop/PROJECT.md`:

```markdown
# shop

## What this project is
- A storefront for maker hardware; the captain plans landing pages for ESP32, ESP32-S3, Raspberry Pi 3 and a 12 V power module. (captain, 2026-09-17..19)

## Layout and state
- main has no commit and an empty tree: no app, no design system, no landing page, no checkout page. (matev2 project facts, main@none, 2026-09-19; crews/buyesp32.status)

## How to work here
- Unknown: no build, test or run commands exist yet.

## Research on file
- ESP32 DevKitC: crews/esp32research/bao-cao-esp32-devkit.md; section 4 is a landing-page content outline.
- ESP32 vs ESP32-S3: crews/esp32s3/bao-cao-esp32-s3.md.

## Captain's decisions
- (none yet)
```

`mate/backlog.md`:

```markdown
# Backlog

## In flight
- buyesp32 (ship, matev2/buyesp32): "Mua ngay" button on the ESP32 landing page; stopped at needs-decision.

## Held for the captain
- buyesp32, asked 2026-09-19: "send the location of the ESP32 landing page, or confirm I should have a new local landing page built". Waiting on: the captain's answer; then brief append and one send.

## Queued
- none

## Done (10 most recent)
- power12 (scout, 2026-09-19): crews/power12/report.md
- rpi35 (scout, 2026-09-18): crews/rpi35/report.md
```

`mate/memory-archive.md`:

```markdown
## 2026-10-20 stow
- (from memory.md, tier: perishable, reinforced: 2026-09-18) Crew spawn failed three times in a row ... [archived: spawn fix landed, expiry condition met]
```

### 12.4 Khi nào ghi

| Sự kiện | Ai kích hoạt | Mate ghi gì |
| --- | --- | --- |
| Đọc xong report scout | Mate (manual mục 9, 14) | `PROJECT.md` từ `## Durable facts`, có nguồn và mốc commit |
| Hand-back có `Still open` sửa `## What we already know` | Mate | Sửa dòng tương ứng trong `PROJECT.md` tại chỗ |
| Gửi một lời sửa cho crew sau khi spawn | Output của `matev2 send` (B9) | Nếu áp cho crew sau: `## Lessons` và đề xuất `CREW.md` |
| Captain sửa cách Mate làm việc, hoặc nêu sở thích | Mate | `## Captain`, inspect-then-update |
| Mate hỏi captain một câu, hoặc hứa một việc | Mate (manual mục 13) | `## Held for the captain` |
| Captain trả lời | Mate | Nguyên văn vào task (`brief append` nếu đang chạy), quyết định sản phẩm vào `PROJECT.md`, bỏ dòng Held |
| `crew stop` | Output của lệnh (B9) | `backlog.md` Done; chạy phần định tuyến của `stow` nếu task có lời sửa |
| App sắp restart Mate | Dòng `⟦matev2⟧ stow:` (B7) | Toàn bộ skill `stow`, receipt |
| `recall` báo trên ngân sách hoặc mục cũ | Output của `recall` | `stow`: decay, gộp, kho lạnh |
| Captain nói stow hoặc "ghi nhớ lại" | Captain | Toàn bộ `stow` |

### 12.5 Thứ tự đọc

Lúc khởi động mới (Claude qua hook `SessionStart` `startup`; Codex do manual bảo chạy ở lượt đầu):

1. Trạng thái sống: bảng `matev2 backlog`, inbox chưa giải quyết, outbox đang chờ, chế độ `manual|auto`.
2. `matev2 project facts`, gồm `head:`.
3. `PROJECT.md`, kèm cảnh báo mốc commit đã cũ.
4. `backlog.md` (không Done), kèm các id In flight lệch bảng.
5. `memory.md`.
6. `WORKSPACE.md`, rồi một dòng con trỏ tới hai `CREW.md`.
7. Ngân sách và mục cũ từ `memory check`.
8. (Tuỳ chọn, khi có db) gợi ý từ timeline.

Lúc resume Claude (`--resume`, hook `resume`): chỉ bước 1, vì hội thoại còn nhưng crew có thể đã đổi.
Sau compaction hoặc `/clear` của Claude (hook `compact`, `clear`): toàn bộ, như khởi động mới.
Codex sau compaction: không có hook đã đo; manual bảo "if you cannot name every In flight crew and every Held question from context, run `matev2 recall` before acting", và Mate `shop` đã có thói quen đọc lại file mỗi lượt.

### 12.6 Trí nhớ đi vào brief

- `## What we already know` chép **nội dung** các dòng liên quan của `PROJECT.md`, giữ nguồn và mốc (`PROJECT.md "Layout and state", main@none`), chứ không trỏ crew tới `PROJECT.md` mà nó không đọc.
- `## Lessons` áp cho crew (khi chưa thành `CREW.md`) đi vào `## Build` như một ràng buộc hoặc vào `Out of scope:`; khi captain đã đưa nó vào `CREW.md`, app tự nối và Mate bỏ khỏi brief.
- `## Captain` không bao giờ vào brief: nó nói về Mate, không về task.
- `Durable facts` của scout là đường vào `PROJECT.md`; một brief ship sau scout vẫn chép phát hiện từ `report.md` như M7 đã chốt, còn `PROJECT.md` giữ phần "gần như mọi task sau cần".
- Với case 3 của `shop`: dòng `main has no commit and an empty tree` trong `PROJECT.md` sẽ nằm trong `## What we already know` của `buyesp32`, và theo manual mục 5 Mate sẽ hỏi captain trước thay vì spawn một ship sẽ dừng.

### 12.7 Compaction và resume, theo harness

| Tình huống | Claude Mate | Codex Mate |
| --- | --- | --- |
| Khởi động mới | Hook `startup` in `recall` vào context | Manual: chạy `recall` ở lượt đầu |
| Restart từ console | B7 gửi `stow:`, chờ Stop, rồi `--resume` | B7 gửi `stow:`, chờ, rồi khởi động mới (B11: `codex resume <id>` sau khi đo) |
| Auto-compact | Hook `compact` in lại `recall` | Không hook; luật manual 12.5 |
| `/clear` | Hook `clear` in lại `recall` | — |
| Harness chết, pane mất | Console báo; khởi động lại; trí nhớ là những gì `stow` hoặc các trigger 12.4 đã ghi | Như Claude |
| Context gần đầy | B10 hiện tỷ lệ; Mate có thể tự `stow` | Như Claude (`turn.context_tokens_after` có cho cả hai) |

### 12.8 Timeline giúp gì

- Tìm ứng viên, không lưu: lời sửa sau spawn, câu hỏi của crew, lời captain gửi Mate, đều đã có trong `message` và `question` (`internal/db/schema.go:145-165`), nên `recall` có thể nhắc "you sent 2 similar corrections to rpi35 and power12" mà không phán nghĩa.
- Đo chính trí nhớ: task 34 đã định so sánh "số dòng sửa Mate gửi sau `wait-mate`" trước và sau M7; cùng truy vấn đó đo được thiết kế này (số lời sửa lặp lại giữa các crew nên về 0 sau khi bài học vào `CREW.md`).
- Sức khoẻ context: `turn.context_tokens_after` (`internal/db/schema.go:104-126`) cho tỷ lệ context của Mate; Mate `shop` dừng ở 53% sau ba ngày, nên ngưỡng gợi ý stow nên cao (ví dụ 80%).
- Mất `matev2.db` không làm mất một mục nhớ nào, vì không mục nào nằm ở đó.

### 12.9 Câu hỏi mở cần đo trước khi làm

- Tên khoá tắt auto-memory trên Claude Code đang cài, và nó có tắt cả việc nạp `MEMORY.md` lẫn việc ghi hay không (B6).
- Hook `SessionStart` của Claude có bắn với `source=compact` cho Mate trong pane Herdr, và stdout có vào context không (B2; firstmate đo trên Claude, `docs/sessionstart-nudge.md:73`).
- Codex TUI 0.154 có bắn `SessionStart` toàn cục không; nếu có, Codex lên tầng Run như Claude.
- `codex resume <id>` trong pane Herdr có hiện dialog nào ngoài chuỗi settle đã biết không (B11).
- Khi Claude `--resume`, `CLAUDE.md` → `AGENTS.md` vừa sinh lại có được nạp lại không, hay Mate chạy với manual cũ trong hội thoại; nếu không, hook `resume` phải in một dòng báo manual đã đổi.
