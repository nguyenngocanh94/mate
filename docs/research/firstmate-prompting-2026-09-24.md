# firstmate: prompting và giao việc — nghiên cứu cho matev2

Ngày 2026-09-24.
Tài liệu này chỉ là nghiên cứu: không có dòng code hay template nào của matev2 bị sửa.
Mọi trích dẫn firstmate dạng `path:line` là đường dẫn tính từ gốc repo firstmate tại commit ghi ở mục 0.
Trích dẫn matev2 dạng `assets/...` hoặc `docs/...` là đường dẫn trong repo này.

## 0. Commit được nghiên cứu và cách xác định nó là bản mới nhất

- Clone cục bộ `/Volumes/Work/Workspace/firstmate` đứng ở `02f50a30437dc565431a477ba5b6143d984b35fa` (`fix(teardown): make landed PR detection robust (#167)`, 2026-06-30), đọc từ `.git/refs/heads/main` và `packed-refs`.
- Upstream (`gh api repos/kunchenguid/firstmate/commits?per_page=5`) có HEAD là `795e4b58ef182beb2a5485d8433436f709943d9a` (`feat(bin): send dispatch router only the brief's task sections and add per-rule confidence floors (#5478)`, 2026-09-24T03:09Z).
- Clone cục bộ chậm 665 commit; `AGENTS.md`, `bin/fm-brief.sh` và toàn bộ `bin/fm-dod-lib.sh` (file mới, 639 dòng) đã đổi rất nhiều từ đó.
- Vì vậy tài liệu này đọc bản clone mới ở `/private/tmp/claude-501/firstmate-upstream`, commit `795e4b58`.
- Clone cục bộ không bị đụng tới.
- Hệ quả phụ: `AGENTS.md` của matev2 trỏ "firstmate reference" vào clone cục bộ đã cũ; ai copy-and-adapt từ đó sẽ lấy phiên bản trước khi firstmate tách `## Captain's intent` / `## Firstmate spec` (commit `28fb5aca`, 2026-09-03).
  Nên cập nhật clone đó (việc của captain, không phải của tài liệu này).

## Tóm tắt một đoạn

firstmate coi brief là một **hợp đồng hai nửa**: lời captain nguyên văn (`## Captain's intent`) và chỉ dẫn xây dựng của orchestrator (`## Firstmate spec`), còn mọi thứ khác (danh tính, cô lập, giao thức status, definition of done) do script sinh ra và script kiểm tra trước khi spawn.
Orchestrator của firstmate **có đọc repo** (hard rule 1 cho phép đọc), và phần kiểm chứng chất lượng được giao cho pipeline `no-mistakes` chứ không phải cho orchestrator tự review.
matev2 khác ở hai điểm cấu trúc: Mate không được đọc repo, và không có pipeline — Mate tự review diff.
Hai khác biệt đó làm cho matev2 cần **nhiều** hơn firstmate ở đúng hai chỗ: bối cảnh repo trong brief (phải đến từ scout) và tiêu chí chấp nhận có cách kiểm chứng (vì không có reviewer tự động).
Brief buyesp32 hỏng đúng ở các chỗ firstmate đã vá bằng cấu trúc: lời captain bị viết lại, spec tự quyết một lựa chọn sản phẩm, không có bối cảnh, acceptance mơ hồ.

## 1. Intake

### firstmate làm gì

Chọn project trước, hỏi đúng một câu khi mơ hồ:

> "Resolve the project independently for every request. An explicit project wins, a clear follow-up inherits its referent, and otherwise match the request against the registry, work under way, and project code or README. Proceed on one confident match while naming the project in plain language; ask one concise question when multiple or no projects plausibly match." (`AGENTS.md:297-299`)

Tra bằng chứng sẵn có trước khi giao điều tra:

> "Before commissioning an investigation, consult existing reports and established evidence." (`AGENTS.md:308`)

Hai hình dạng task, ship là mặc định, scout chỉ khi bất định có thể đổi *có làm hay làm gì*:

> "**Ship** is the default and produces a project change through the selected delivery mode; once implementation is authorized, dispatch a ship and keep any remaining bounded research inside it unless unresolved uncertainty could materially change whether or what to build." (`AGENTS.md:311`)
>
> "**Scout** produces knowledge in `data/<id>/report.md`, never a PR, and is appropriate for investigation, diagnosis, planning, reproduction, or audit work when the captain explicitly requests a separate knowledge or design deliverable or unresolved uncertainty could materially change whether or what to build." (`AGENTS.md:312`)

Khi nào trả lời thay vì giao, khi nào hỏi:

> "If established evidence already answers an informational question, relay it without a design-only scout; when implementation intent is unclear, answer and ask one concise implementation question when useful rather than dispatching speculative design work. Never both present a likely-enough solution and launch a parallel design exercise that is not expected to change it. A diagnostic request, report, recommendation, or implementation-ready finding is evidence, not authorization to change code." (`AGENTS.md:314-316`)

Bug thì nạp skill chẩn đoán trước khi scope: "Load `diagnostic-reasoning` before scoping a reported bug and before acting on a diagnostic report." (`AGENTS.md:317`).

Readiness / overlap / queue — overlap không phải lý do chờ:

> "Treat file or subsystem overlap as a risk signal rather than an automatic reason to wait, and dispatch isolated work immediately with no concurrency cap when each change can be independently implemented and validated and the selected delivery path can reconcile ordinary rebases or conflicts. Serialize only for a true semantic dependency, shared mutable external state, incompatible concurrent migration, or another concrete condition that makes independent progress or reconciliation unsafe; same-file editing alone is insufficient, and genuine blockers remain durable." (`AGENTS.md:327-328`)

Chống "làm quá":

> "For one-off or infrequent operational work, start with the simplest direct end-to-end path. Do not build wrappers, control planes, policy layers, custom verifiers, or automation unless the direct path exposes a concrete blocker or repeated need that justifies the added machinery." (`AGENTS.md:305-306`)

Chế độ giao hàng được quyết ở intake và ghi lý do lệch chuẩn vào ghi chú backlog (`AGENTS.md:319-325`); effort cũng được chọn theo độ bất định: "use low for well-understood explicit work, xhigh for ambiguous investigation or design, intermediate levels proportionally, and never max without explicit captain preference" (`AGENTS.md:240`, chi tiết ở `.agents/skills/harness-adapters/references/common/model-and-effort.md:15-18`).
Không có quy tắc chia nhỏ task (sizing/splitting) tường minh nào; thứ gần nhất là "Once validation starts, prefer routing new requirements to follow-up work rather than expanding the current task" (`AGENTS.md:378`) và "A new task shape earns its way in only when existing primitives genuinely cannot compose to cover it" (`VISION.md:54`).

### matev2 hôm nay

`assets/mate/AGENTS.md.tmpl` mục 5 có ship/scout và dispatchable/blocked.
Khác firstmate ở ba chỗ:

- Overlap là lý do queue: "**Blocked:** touches the same area as an in-flight Crew" (mục 5) và "Never spawn a Crew for work that overlaps an in-flight Crew's files; queue it instead" (mục 7). firstmate nói ngược lại (`AGENTS.md:327-328`).
- Không có bước "tra report sẵn có trước"; không có quy tắc "scout khi bất định có thể đổi có làm hay làm gì".
- "An unanswered question in the request is not a reason to stop ... Name the gap in the brief as a gap, dispatch" (mục 5). Ý này khớp tinh thần firstmate (ship mặc định, research giới hạn nằm trong ship), nhưng matev2 không phân biệt khoảng trống *nhỏ* (tên nút, trang nào) với khoảng trống *làm đổi việc cần xây* (landing page có tồn tại không).

### Khoảng trống

Trong buyesp32, captain nói `Thêm nút "Mua ngay" lên landing page ESP32, bấm vào thì mở trang thanh toán.` (session Codex của Mate, `~/.codex/sessions/2026/09/17/rollout-2026-09-17T21-56-39-…jsonl`, 2026-09-19T04:06:01Z).
Repo `shop` trống hoàn toàn (thư mục chỉ có `.git`), `PROJECT.md` trống (`## What this project is` không có nội dung), và backlog không có task nào đã xây landing page — chỉ có bốn scout nghiên cứu phần cứng.
Theo quy tắc của firstmate, "landing page ESP32 có tồn tại không" là bất định có thể đổi *cái cần xây* (thêm một nút vs dựng cả site), nên đây là một câu hỏi cho captain hoặc một scout, không phải một khoảng trống để crew tự đụng.
Crew đã tự đụng sau 2 phút: `needs-decision: branch has no app or design system; provide source branch/path or authorize a new local site scaffold` (`crews/buyesp32.status`).
Và scout `esp32research` đã có sẵn "a landing-page-ready feature/message outline" (brief của nó) — bằng chứng sẵn có mà brief ship không hề nhắc tới.

## 2. Brief

### Cấu trúc firstmate — ai viết phần nào

Brief được scaffold bằng `bin/fm-brief.sh`; orchestrator chỉ điền hai chỗ, script sở hữu phần còn lại:

> "Use its scaffold as the contract, then fill `## Captain's intent` (`{TASK}`) with the captain's own ask and any boundary the captain stated, plus the context needed to read it, including the substance of any report, decision, or PR the ask refers to; never widen the ask there into a general goal or an enumerated coverage list, because the reviewer treats that subsection as acceptance criteria. Fill `## Firstmate spec` (`{FIRSTMATE_SPEC}`) with only the build instructions that ask requires, naming what stays out of scope when the ask is narrow; a generalization, consistency sweep, or extra hardening the captain did not ask for is follow-up work to note, not scope to add." (`AGENTS.md:565-566`)
>
> "Keep additions task-specific rather than repeating lifecycle instructions, and alter generated sections only when the task genuinely differs from the standard shape." (`AGENTS.md:568`)
>
> "The scaffold is a safety contract, not a suggestion." (`AGENTS.md:577`)

Khối task mà script sinh ra (`bin/fm-brief.sh:479-486`):

```text
# Task
## Captain's intent
{TASK}

## Firstmate spec
{FIRSTMATE_SPEC}
```

Thứ tự đầy đủ của một ship brief sau khi `fm-spawn` render thành `launch-brief.md` (`bin/fm-spawn.sh:2855-2866`, `bin/fm-brief.sh:582-648`):

| Thứ tự | Section | Ai viết |
| --- | --- | --- |
| 1 | `# Current worker role contract` | Script (`bin/fm-dod-lib.sh:106-120`), chèn lúc spawn |
| 2 | Dòng mở "You are a crewmate: an autonomous worker agent managed by firstmate. Work on your own; do not wait for a human." | Script |
| 3 | `# Task` → `## Captain's intent` | Orchestrator: lời captain |
| 4 | `# Task` → `## Firstmate spec` | Orchestrator: chỉ dẫn xây dựng |
| 5 | `# Herdr isolation` hoặc `# Herdr lifecycle declaration - NOT ENABLED` | Script (`bin/fm-brief.sh:447-477`) |
| 6 | `# Setup` (assert cô lập, tạo branch) | Script |
| 7 | `# Rules` 1-7 | Script, rule 1 theo mode |
| 8 | `# Firstmate instruction inbox` | Script |
| 9 | `# Project memory` | Script |
| 10 | `# Definition of done` + `Delivery contract: mode=...` + `Ship branch: ...` | Script (`bin/fm-dod-lib.sh:309-415`) |
| 11 | `# Home brief additions` (tuỳ chọn) | Captain, qua `config/brief-include.md`; "every other section of this brief takes precedence" (`bin/fm-brief.sh:302-309`) |
| 12 | `# Current no-mistakes intent contract` (chỉ ship no-mistakes) | Script (`bin/fm-dod-lib.sh:187-200`) |

Scout brief có cùng khung nhưng `# Setup`/`# Rules`/`# Definition of done` riêng (`bin/fm-brief.sh:495-555`).

### Lời captain được mang đi thế nào

Nguyên văn, không nhãn người nói, và tự đủ nghĩa:

> "`bin/fm-dod-lib.sh` owns intent authoring without added speaker labels or direct address, its provenance markers, what a no-mistakes worker may pass as `--intent`, and the string's self-sufficiency rule." (`AGENTS.md:567`)
>
> "Author the subsection body and later relays as the actual words, without adding speaker labels or direct address: the heading supplies provenance and is not part of --intent. ... Never scrub literal examples or other content the captain actually supplied. The string passed must be self-sufficient - it plus the codebase reconstructs roughly the same specification - so a report, decision, or PR the intent refers to is written into it as substance, never left as a pointer." (`bin/fm-dod-lib.sh:69-77`)

Lý do lịch sử của quy tắc "tự đủ": "PR #3604 shipped with an intent that was only "do 1, 2, 3, 7 from the report": the real contract lived in a private scout report and never reached --intent" (commit `3b82ebdd`).

Lời captain đến sau cũng được nối vào đúng chỗ đó:

> "When the captain adds or changes an ask mid-task, append the captain's words without added speaker labels or direct address to that brief's `## Captain's intent` and relay those words to the worker; Firstmate build constraints stay in `## Firstmate spec` or the steer." (`AGENTS.md:376`)

Spec của orchestrator không bao giờ được trộn vào intent: "Firstmate-authored constraints, acceptance criteria, implementation details, decisions, and tradeoffs are specification, not captain intent." (`bin/fm-dod-lib.sh:194`).

### Kiểm tra bằng máy trước khi spawn

`fm-spawn` từ chối brief sai hình (`bin/fm-spawn.sh:2830-2842`):

> "error: $BRIEF still contains {TASK} or {FIRSTMATE_SPEC}; fill ## Captain's intent and ## Firstmate spec before spawn"
>
> "error: $BRIEF must contain nonempty ## Captain's intent and ## Firstmate spec subsections (or a nonempty legacy # Task body) before spawn"
>
> "error: $BRIEF ## Captain's intent has an operator-address line: $ADDRESS_LINE; write the captain's actual words without a Captain label or address before spawn, since the heading already records provenance"

Kiểm tra dùng awk trên heading (`bin/fm-dod-lib.sh:168-228`), tức là quyết định bằng cấu trúc, không bằng phán đoán — đúng nguyên tắc "Logic that can be exact lives in deterministic scripts; work that requires understanding lives in an agent" (`VISION.md:34`).
`fm-promote` áp cùng các kiểm tra khi scout được thăng thành ship (`bin/fm-promote.sh:200-221`).

### Bối cảnh repo khi orchestrator không đọc code

firstmate **có** đọc repo: "firstmate reads projects and crewmates change them" (`AGENTS.md:28`), và intake đối chiếu "project code or README" (`AGENTS.md:298`).
Tuy vậy nó vẫn giao điều tra cho crew (`AGENTS.md:22`), và kênh mang bối cảnh vào brief là: "the context needed to read it, including the substance of any report, decision, or PR the ask refers to" (`AGENTS.md:565`).
Bối cảnh code bền vững đi vào `AGENTS.md` của chính project qua crew (`bin/fm-brief.sh:640-645`), và crew tự đọc file đó khi chạy trong worktree — nên brief không phải lặp lại.
firstmate không có section "context" riêng trong brief.

### Acceptance và lệnh kiểm chứng

firstmate **không** có section acceptance riêng và không đặt lệnh test vào brief.
Acceptance là chính `## Captain's intent`: "the reviewer treats that subsection as acceptance criteria" (`AGENTS.md:565`), và reviewer là pipeline `no-mistakes` (review, test, document, lint — `bin/fm-dod-lib.sh:335`).
Với `direct-PR` và `local-only`, firstmate cố ý không thêm reviewer: "When no-mistakes is selected, no-mistakes alone owns review, fixes, tests, documentation, push, PR, and CI; otherwise follow the faster path without adding an independent reviewer." (`AGENTS.md:352`).
Kiểm chứng phía máy là cổng cơ học: `done:` bị từ chối nếu HEAD chỉ tồn tại trong worktree (`bin/fm-dod-lib.sh:587-639`), PR phải non-draft đọc lại từ forge (`bin/fm-dod-lib.sh:369`).

### Quyết định còn mở

Không có section "open decisions" trong brief.
Quyết định được xử lý bằng luật crew (rule 6, mục 3 dưới) và bằng cấm orchestrator mở rộng spec: "a generalization, consistency sweep, or extra hardening the captain did not ask for is follow-up work to note, not scope to add" (`AGENTS.md:566`).
Phân quyền quyết định nằm trong skill `ask-user-authority` (mục 4 dưới).

### Độ dài và ví dụ

Không có con số độ dài cho brief.
Chuẩn mực là "only the build instructions that ask requires" (`AGENTS.md:566`), "Keep additions task-specific rather than repeating lifecycle instructions" (`AGENTS.md:568`), và "every agent's context stays lean, and every task is achieved with the fewest tokens that do it well" (`VISION.md:37`).
Không có brief mẫu (few-shot) nào trong prompt; các ví dụ duy nhất là ví dụ phản mẫu: "do items 1, 2, 3, and 7 of the report" (`bin/fm-dod-lib.sh:267`), "Protocol regression example" (`AGENTS.md:497`), và năm ví dụ phân loại trong `ask-user-authority` (`.agents/skills/ask-user-authority/SKILL.md:51-57`).

### matev2 hôm nay

`assets/crew/brief.md.tmpl` có một `# Task` với `{TASK}` duy nhất; `internal/spawn/crew.go:69` thay nguyên file của Mate vào đó, không kiểm tra gì.
Manual mục 6 yêu cầu: "Say, in your own words and not by pasting the captain's message: the task, concretely enough that a competent stranger with the repo in front of them could do it; the acceptance criteria, including the exact expected result where there is one; any constraint that is not obvious from the code."
Tức là matev2 **cố ý** vứt lời captain — ngược hẳn với firstmate.
Không có template section, không có kiểm tra khi spawn, không có kênh nối lời captain đến sau.
`docs/mvp.md:412` nói task 11 có skill `brief-writing`, nhưng skill đó không tồn tại trong `assets/mate/skills/`.

### Khoảng trống, đối chiếu từng điểm yếu của buyesp32

| Điểm yếu (`crews/buyesp32/brief.md:4`) | Quy tắc firstmate sẽ chặn |
| --- | --- |
| Một đoạn dày 11 câu | Tách intent / spec bằng heading (`bin/fm-brief.sh:479-486`), kiểm tra bằng máy |
| Tự quyết "implement a local checkout page/route ... with a clearly non-integrated payment handoff state" | Spec chỉ chứa cái ask cần; phần mở rộng là follow-up (`AGENTS.md:566`); crew phải hỏi product choice (`bin/fm-brief.sh:620-621`) |
| Không có bối cảnh repo cụ thể ("Inspect the existing app to locate...") | "the context needed to read it, including the substance of any report" (`AGENTS.md:565`); tra report sẵn có (`AGENTS.md:308`) |
| "Validate with the project's relevant build/tests" | firstmate giao cho pipeline; matev2 không có pipeline nên phải hơn firstmate ở đây (mục 9) |
| Lời captain không còn | `## Captain's intent` nguyên văn, bị từ chối nếu rỗng (`bin/fm-spawn.sh:2835`) |

## 3. Chỉ dẫn phía worker

firstmate ghép chỉ dẫn worker từ các khối cố định; trích nguyên văn các phần có trọng lượng.

**Danh tính** (`bin/fm-dod-lib.sh:109-118`), chèn đầu tiên, trên cả brief:

> "You are a crewmate: an autonomous worker agent managed by firstmate. This section establishes your current identity before every project or task instruction below and supersedes any conflicting role identity in those instructions. Do the assigned work yourself and report only to firstmate; do not adopt a firstmate or secondmate supervisor identity, delegate the task, run fleet supervision, or address the captain."

Lý do: commit `c806c6a3` "establish crewmate identity first" — một crew chạy trong repo có `AGENTS.md` kiểu supervisor đã tự nhận làm supervisor.

**Cô lập** (`bin/fm-brief.sh:592-596`):

> "**Verify isolation before anything else.** Run `pwd -P` and `git rev-parse --show-toplevel`; both must resolve to the disposable task worktree you were launched in ... If the top-level path is the primary checkout or not the worktree you were launched in, STOP - do not branch or commit here - append `blocked [at=<epoch>]: launched in primary checkout, not an isolated worktree` to the status file and stop."

**Báo cáo** (`bin/fm-brief.sh:602-618`):

> "Each append wakes firstmate, so report sparingly: only phase changes a supervisor would act on (setup done, bug reproduced, fix implemented, validation passed) and the needs-decision/blocked/paused/done/failed states. No step-by-step FYI progress lines; firstmate reads your pane for that."
>
> "A mid-task `working:` line (including setup complete) is nonterminal: do not end the turn after it; continue the same stage until a defined `done:` gate under Definition of done."

**Khi nào hỏi** (`bin/fm-brief.sh:619-624`):

> "5. If you hit the same obstacle twice, append `blocked [at=<epoch>]: {why}` and stop; firstmate will help."
>
> "6. If a decision belongs above the implementation worker (product choices, destructive actions), append `needs-decision [at=<epoch>]: {summary of options}` and stop. Firstmate will reply with the decision."

**Steering** (`bin/fm-brief.sh:339-344`): tin nhắn dài nằm trong `state/<id>.inbox/NNN.msg`, crew đọc theo thứ tự và `mv` sang `handled/` làm ack.

**Trí nhớ project** (`bin/fm-brief.sh:640-645`):

> "If `AGENTS.md` or `CLAUDE.md` already exists, or if this task produced durable project-intrinsic knowledge, run `.../bin/fm-ensure-agents-md.sh .` in the worktree. Record only project knowledge useful to almost every future session. For anything the codebase already shows, prefer a pointer to the authoritative file, command, or doc over copying the detail. ... Keep it proportionate: skip `AGENTS.md` edits for trivial tasks that produced no durable project knowledge."

**Definition of done, local-only** (`bin/fm-dod-lib.sh:379-388`):

> "The task is complete only when committed on your branch `$branch`. Do NOT push, do NOT open a PR, do NOT merge. A `done:` is accepted when the named head is on this project's shared local branch, not only on a detached copy ... Keep your branch a clean fast-forward onto the current default branch ... When it is implemented and committed, append `done [at=<epoch>]: ready in branch $branch` to the status file and stop."

**Tự kiểm trước khi giao lại**: firstmate không yêu cầu crew chạy test hay viết checklist; tự kiểm được thay bằng pipeline và các cổng cơ học ("read the PR back from the forge and confirm it is not a draft", `bin/fm-dod-lib.sh:369`; "commit nothing after the run", `:407`).

**Scout DoD** (`bin/fm-brief.sh:548-554`):

> "The report must stand alone: what you did, what you found, the evidence (commands run, output, file:line references), and what you recommend."
>
> "If your findings reveal work that should ship (e.g. you reproduced a bug and the fix is clear), say so in the report; firstmate may promote this task in place, and you would then receive mode-specific ship instructions as a follow-up message."

**Quy ước commit/PR**: branch bất biến `fm/<id>` (prefix cấu hình được, `bin/fm-brief.sh:50-61`), tạo ngay bước đầu (`:596`), không push lên default, không merge (`bin/fm-dod-lib.sh:151-157`), mọi PR nêu đủ URL (`bin/fm-brief.sh:610-612`).
Commit message không có quy tắc riêng ngoài "Never add an agent name as a commit co-author" (`AGENTS.md:49`) và cấm xưng hô "captain" trong artifact (`AGENTS.md:13`).

### matev2 hôm nay

`assets/crew/brief.md.tmpl` đã có tương đương cho: cô lập, không push, ba verb, "same obstacle twice", product choice → `needs-decision` kèm câu "Do not settle it by taking whichever option looks most natural", hai hình dạng done.
Thiếu: khối danh tính đứng trước brief (matev2 đặt dòng danh tính trước `# Task` nhưng không "supersedes" chỉ dẫn danh tính trong repo); `# Project memory`; cấu trúc report của scout ("stand alone ... evidence ... file:line"); lời mời "say so if this should ship"; và bất kỳ yêu cầu tự kiểm nào trước `wait-mate`.
Chỗ cuối là quan trọng nhất cho matev2 vì không có pipeline đứng sau.

## 4. Giám sát và review

### firstmate làm gì khi worker báo xong

- Đọc trạng thái hiện tại bằng script, không bằng dòng status: "Judge validation by the resolved state line from `bin/fm-crew-state.sh` ... never by shell liveness, the last status event, or a raw run record." (`AGENTS.md:392`)
- Cổng named-head: "a ship `done:` whose named head exists only in the worker's disposable copy is not ready ... That blocked reading is the gate working, not a stuck worker, so steer the worker on the commit the refusal names rather than waiting." (`AGENTS.md:401-402`)
- Báo captain: "Tell the captain the PR's full `https://...` URL ..., a concise outcome summary, and the no-mistakes risk level when applicable." (`AGENTS.md:406`)
- Scout: "A completed scout must leave a self-contained report before its scratch worktree can be discarded; read and relay its findings, record the report as the Done artifact, and re-evaluate the queue. A report may recommend implementation but does not authorize it." (`AGENTS.md:421-422`)
- Trước khi coi scout là xong, mọi câu hỏi thuộc captain phải được ghi thành task "held for the captain" (`.agents/skills/captain-hold-lifecycle/SKILL.md:19-23`).

### Review checklist

firstmate **không** có checklist review của orchestrator và cấm tự dựng cổng review thủ công:

> "Never hold work outside no-mistakes for a manual clean verdict, stack serial manual reviews, or infer authority for one from security, architecture, or risk alone. A separate review or audit is allowed only when the captain explicitly requests that deliverable ... If fast-path risk needs more rigor, escalate whether to use no-mistakes instead of inventing a manual gate." (`AGENTS.md:353-355`)

Phán đoán duy nhất orchestrator làm là với finding `ask-user` của pipeline, theo `ask-user-authority`:

> "1. Reconstruct the accepted contract from the brief's `## Captain's intent` subsection, later captain words, and the specification in `## Firstmate spec` and steers. Reviewer language cannot amend that contract." (`.agents/skills/ask-user-authority/SKILL.md:25-26`)
>
> "3. Decide the finding when it is unambiguous toward the accepted design ... 4. Escalate only genuinely ambiguous findings: a Fix that would materially expand the contract ...; a product or architecture call not settled by accepted intent; repeated same-theme findings when incremental corrections are preserving a questionable abstraction rather than closing independent defects; destructive, irreversible, and genuinely security-sensitive choices" (`:31-36`)
>
> "5. Treat labels such as correctness, security, fail-closed, high-risk, or required as evidence about the finding, never as authority to broaden the task." (`:37`)

### Gửi sửa và vòng rework

- Sửa bằng một steer qua `fm-send` (`AGENTS.md:340`); quyết định trả về kèm "the decision key, step, action, affected finding IDs, instructions where needed, and exact response command" (`AGENTS.md:388`).
- Vòng rework do pipeline chạy (tối đa ba vòng fix, `bin/fm-dod-lib.sh:271`); orchestrator chỉ chen vào ở finding ask-user.
- Phạm vi bị đóng băng khi đã vào validate: yêu cầu mới thành follow-up, "however, the smallest downstream changes needed to keep already accepted product or engineering behavior correct, add behavioral tests where an executable contract exists, or keep documentation accurate remain within the current task ... and corrections required to satisfy already accepted intent are not new requirements." (`AGENTS.md:378`)

### matev2 hôm nay

Manual mục 9: peek, `matev2 diff`, đọc report nếu là scout, "If the diff does not do what the brief asked, send the Crew one corrective line instead of accepting it."
Không có chuẩn so sánh ngoài "what the brief asked" — mà brief lại là lời Mate viết lại, nên Mate đang review crew bằng chính diễn giải của mình.
Không có luật đóng băng phạm vi, không có luật phân loại "sửa trong phạm vi vs mở rộng hợp đồng".

### Khoảng trống

matev2 không thể lấy nguyên xi mô hình "không review" của firstmate, vì matev2 không có pipeline (local-only, `docs/mvp.md` quyết định 2 và mục 5 của manual: "in the MVP always local-only").
Cái matev2 cần lấy là **chuẩn** review của firstmate: hợp đồng được dựng lại từ lời captain + spec, không từ ngôn ngữ của reviewer/crew, và bảng phân loại của `ask-user-authority`.

## 5. Scout → ship

### firstmate làm gì

Có, và theo hai đường.
Đường thường: scout viết report tự đủ (`bin/fm-brief.sh:550`), orchestrator đọc và relay; một brief ship mới phải đưa **nội dung** của report vào `## Captain's intent`, không đưa con trỏ (`AGENTS.md:565`, `bin/fm-dod-lib.sh:75-77`).
Đường thăng tại chỗ: "When implementation is separately authorized, promote the existing scout through `bin/fm-promote.sh` rather than creating a duplicate task. The promoted worker must inventory scratch state, return to a clean default-branch base, carry over only intended fix changes, create the ship branch, and follow the project's selected delivery path while leaving scratch commits and debug edits behind and turning a reproduced bug into the regression test." (`AGENTS.md:425-426`)

Chỉ dẫn ship mà scout nhận (`bin/fm-promote.sh:268`, `:235-243`):

> "Your scout task has been promoted to a ship task, mode=$MODE. Your window, worktree, and context stay as they are; only the contract below changes."
>
> "2. Inventory this worktree's scratch state with `git status` and `git log` before changing anything. 3. Return to a clean default-branch base, then create your branch ... 4. Carry over only the intended fix changes. Leave scratch commits, debug edits, and experiment files behind. 5. If you reproduced a bug, turn that reproduction into a regression test. 6. Treat the scout-time Firstmate spec and any unmarked legacy `# Task` text as investigation context, not captain intent or current ship-time instructions."

`## Captain's intent` của scout được giữ nguyên sang ship (`bin/fm-promote.sh:213-218`), và hợp đồng mới được nối vào `brief.md` để một lần relaunch sau không hồi sinh luật scout (`:288-295`, commit `aa921774`).

### matev2 hôm nay

Không có đường nào.
Scout và ship là hai crew độc lập; manual không yêu cầu brief ship dẫn report scout.
Bằng chứng: bốn scout ESP32/RPi/nguồn 12 V đã xong (`mate/backlog.md` → Completed), `esp32research` có sẵn dàn ý nội dung landing page, và brief buyesp32 không nhắc tới report nào.

## 6. Trí nhớ và học

### firstmate làm gì

Bảng định tuyến tri thức, mỗi loại một chủ (`AGENTS.md:277-288`):

> "- Home-domain captain preferences and working style belong in `data/captain.md` after inspect-then-update. ... - Fleet-local operational facts belong in curated, home-local `data/learnings.md`. - Task-scoped notes belong with the backlog item, and investigation findings belong in the scout report. - Knowledge useful to almost every contributor to one project belongs in that project's committed `AGENTS.md`. ... Firstmate never writes a project's `AGENTS.md` directly. A crewmate creates or updates it lazily through the project's selected delivery path ... preferring pointers to authoritative sources over copied detail."

Định dạng và luật cập nhật `learnings.md`: "dated, evidence-backed, curated, and updated with inspect-then-update - rewrite and prune rather than append forever" (`AGENTS.md:98`).
Skill `/stow` (`.agents/skills/stow/SKILL.md`) là cơ chế curate: tier `pinned`/`aging`/`perishable` với marker ngày (`:19-41`), luật cứng "reinforcement requires independent evidence from this session that you can name in the receipt" (`:99`), ngân sách token cho bộ nhớ khởi động (`:80-126`, mặc định 7.500 token theo `AGENTS.md:81`), kho lạnh `data/memory-archive.md` "Stale never means deleted" (`:134`), và quét "open-record persistence" trước khi reset (`:236-245`).
Ghi chú backlog: "Keep free-form notes free of temporary paths, moving versions, ephemeral identifiers, and copied state that will rot. Inspect the current task note before replacing its considered body" (`AGENTS.md:557-558`).

Tri thức chảy vào brief sau bằng hai đường: `AGENTS.md` của project được crew tự đọc trong worktree, và orchestrator đọc `captain.md`/`learnings.md` ở mỗi session start (`AGENTS.md:209`) rồi viết vào spec.
Không có cơ chế tự động nào chèn learnings vào brief.

### matev2 hôm nay

Manual mục 2 và 14 có bảng lớp (manual/WORKSPACE.md/PROJECT.md/memory.md/backlog.md) và luật "tri thức code vào AGENTS.md của repo qua crew".
Thiếu: luật inspect-then-update (hiện `memory.md` chỉ được "append ... by editing the file yourself", manual mục 4), không có ngày/bằng chứng cho mục nhớ, và không có luật "PROJECT.md phải có gì".
Thực tế ở `shop`: `PROJECT.md` trống, `memory.md` chỉ có tiêu đề `# Memory` sau năm task.

## 7. Skill và prompt con liên quan đến giao việc

| Skill / file | Để làm gì | Khi nạp |
| --- | --- | --- |
| `bin/fm-brief.sh` + `--help` | Scaffold brief ship/scout/secondmate; sở hữu Setup/Rules/Inbox/Memory | Mỗi task (`AGENTS.md:564`) |
| `bin/fm-dod-lib.sh` | Danh tính worker, rule 1 theo mode, DoD, hợp đồng `--intent`, kiểm tra heading | Script dùng; orchestrator không nạp |
| `bin/fm-promote.sh` | Thăng scout thành ship tại chỗ | Khi implementation được cho phép sau scout (`AGENTS.md:425`) |
| `diagnostic-reasoning` | Tách trigger/masking/symptom, so đường hỏng và đường chạy đúng, counterfactual nhỏ nhất, bằng chứng phản bác; định nghĩa brief chẩn đoán phải hỏi gì | Trước khi scope bug và trước khi hành động theo report chẩn đoán (`AGENTS.md:317`, `:591`) |
| `ask-user-authority` | Ai quyết finding: orchestrator hay captain; năm yếu tố của một escalation | Trước khi quyết mọi finding ask-user (`AGENTS.md:367`, `:592`) |
| `captain-hold-lifecycle` | Cổng hoàn tất scout: mọi câu hỏi thuộc captain phải thành task được giữ; đóng chỉ bằng lời captain thật | Trước khi coi scout/review là xong (`AGENTS.md:600`) |
| `stuck-crewmate-recovery` | Thang escalation: peek → trả lời một dòng nếu brief đã có → interrupt + một dòng sửa → relaunch cùng worktree với ghi chú tiến độ → failed | Stale, loop, nhầm lẫn lặp, hỏi điều brief đã trả lời, steer thất bại (`AGENTS.md:598`) |
| `harness-adapters` (+ `references/common/model-and-effort.md`) | Sự thật từng harness; chính sách effort | Trước mỗi spawn/recovery (`AGENTS.md:222`) |
| `quota-array-dispatch`, `bin/fm-dispatch-resolve.sh`, `config/crew-dispatch.json` | Chọn harness/model/effort theo luật ngôn ngữ tự nhiên + quota | Mỗi intake khi có profile (`AGENTS.md:227-239`) |
| `firstmate-coding-guidelines` | Luật viết cho repo firstmate; brief phải bắt crew nạp | Task đụng repo firstmate (`AGENTS.md:571`) |
| `stow` | Curate trí nhớ, định tuyến tri thức, lưu việc dở | `/stow`, trước reset (`AGENTS.md:289`) |
| `project-management` | Đăng ký project, posture giao hàng mặc định | Thêm/xoá project (`AGENTS.md:264`) |
| `config/brief-include.md` | Chỉ dẫn đứng của captain nối cuối mọi brief, luôn nhường section khác | Tự động khi tồn tại (`AGENTS.md:86`) |

Các skill còn lại (`afk`, `quiet`, `ahoy`, `bearings`, `fmx-respond`, `secondmate-provisioning`, `process-event-sources`, `bootstrap-diagnostics`, `updatefirstmate`, `firstmate-orca`, `firstmate-codexapp`) là vận hành/hiển thị, không tham gia viết brief.

matev2 có hai skill: `harness-adapters`, `stuck-crew-recovery`.

## 8. Xử lý thất bại trong prompting

firstmate:

- Worker lặp lại cùng trở ngại hai lần thì dừng và báo (`bin/fm-brief.sh:619`).
- Worker hỏi điều brief đã trả lời: "answer in one line" (`.agents/skills/stuck-crewmate-recovery/SKILL.md:75`).
- Nhầm lẫn/loop: "interrupt ..., then redirect with one corrective line" (`:76`).
- Kẹt thật: "relaunch it with `bin/fm-control.sh <task-id> relaunch --note '<progress so far>'`, which stops the agent, carries the brief plus that note into a replacement in the same local copy" (`:77`); "A low context reading is not wedging" (`:80`).
- Relaunch lần hai thất bại: "write `failed` to the backlog and tell the captain the plain failure, preserved work, and consequence" (`:82`).
- Sai hướng lặp lại trong review: "repeated same-theme findings when incremental corrections are preserving a questionable abstraction" → escalate thay vì thêm một vòng fix (`.agents/skills/ask-user-authority/SKILL.md:35`, ví dụ `:55`).
- Report chẩn đoán thiếu một mắt xích chịu lực: "route a focused follow-up investigation instead of treating confidence or implementation detail as proof" (`.agents/skills/diagnostic-reasoning/SKILL.md:51`).
- Crew tự kết luận "pipeline chết": orchestrator tự đọc hai nguồn có thẩm quyền trước khi tin (`.agents/skills/stuck-crewmate-recovery/SKILL.md:51-68`) — mẫu "không tin kết luận của crew về trạng thái hệ thống".

Câu chữ escalation (`AGENTS.md:519-521`):

> "Every escalation must stand alone and remain concise. Lead directly with concrete evidence, then the consequence, options when applicable, and a recommendation. Use the same evidence-first form for objections or clarifying challenges rather than unsupported deference."

Với quyết định mở rộng phạm vi (`.agents/skills/ask-user-authority/SKILL.md:41-47`):

> "1. The original requirement or accepted task criterion. 2. The proposed product or engineering contract expansion. 3. The smallest alternative that complies with the accepted contract without the expansion. 4. The concrete consequences of accepting and declining the expansion. 5. A recommendation with the reason it best serves the accepted intent."

matev2: `stuck-crew-recovery` đã có thang tương đương (peek → một dòng → relaunch với `Already tried` → failed), nhưng relaunch là crew mới với id mới vì matev2 không có interrupt.
Manual mục 13 có "write the question as a sentence, say what you will do with each answer", nhưng không có hình dạng evidence → consequence → options → recommendation, và không có năm yếu tố cho mở rộng phạm vi.
Thực tế buyesp32: Mate chuyển câu hỏi của crew thành "Bạn gửi vị trí mã nguồn landing page ESP32, hoặc xác nhận cho tôi tạo mới..." (2026-09-19T04:07:57Z) — có hai lựa chọn nhưng không có bằng chứng (repo trống), không hậu quả, không khuyến nghị; và ngày hôm sau lộ cơ chế nội bộ ra captain ("chế độ xem transcript ... nhấn `q`").

## 9. Những điều cố ý khác của firstmate mà matev2 thiếu

1. **Tách danh tính khỏi repo.** Khối role đứng đầu và tự tuyên bố quyền ưu tiên (`bin/fm-dod-lib.sh:109-118`). matev2 sẽ gặp đúng lỗi này khi một crew chạy trong repo có `AGENTS.md` kiểu orchestrator (chính repo matev2).
2. **Luật "không mở rộng" viết thành chữ.** "a generalization, consistency sweep, or extra hardening the captain did not ask for is follow-up work to note, not scope to add" (`AGENTS.md:566`); "Standing autonomy ... exercised only within the captain's original request, and it never quietly widens" (`VISION.md:27`).
3. **Bằng chứng không phải uỷ quyền.** "Evidence is never authorization: a diagnosis, a report, or a recommendation authorizes nothing by itself." (`VISION.md:28`, `AGENTS.md:316`). matev2 chưa có câu này; một Mate đọc report scout nói "nên sửa X" có thể tự spawn ship.
4. **Không trình bày một giải pháp đủ tốt song song với một bài thiết kế không đổi được nó** (`AGENTS.md:315`).
5. **Máy kiểm tra hình dạng, agent phán nghĩa** (`VISION.md:34-35`), áp cho brief ở `fm-spawn`.
6. **Standing instructions cho crew tách khỏi template** (`config/brief-include.md`, `bin/fm-brief.sh:302-309`), luôn nhường mọi section khác.
7. **Effort theo độ bất định** (`model-and-effort.md:15-18`) và "never downgrades the intelligence doing the work without the captain's standing, explicit permission" (`VISION.md:67`).
8. **Tin nhắn cuối cùng phải tự đứng được** (`AGENTS.md:494-497`), có ví dụ phản mẫu.
9. **Dịch thuật ngữ nội bộ** có bảng thay thế cụ thể (`AGENTS.md:501-513`), ví dụ "brief -> instructions", "worktree -> local copy". matev2 chỉ có danh sách cấm.
10. **Mọi lần thêm luật đều kèm lý do sự cố** trong commit (ví dụ `3b82ebdd` về intent chỉ là con trỏ). matev2 làm tương tự trong `docs/evidence/m4-acceptance-2026-09-19.md` mục "What the Mate's behaviour forced into the manual" — nên giữ.

## 10. Danh sách áp dụng, xếp hạng

Ký hiệu cỡ: S = một buổi, M = một task PR, L = nhiều task.
Xếp theo mức lỗi phòng được trên đơn vị công sức, đầu danh sách là quan trọng nhất.

### Áp dụng nguyên xi

**A1. Tách `{TASK}` thành lời captain nguyên văn + spec của Mate.**
Cái gì: section `## Captain's words` chứa nguyên văn mọi lời captain về task, không nhãn người nói, cộng nội dung (không phải con trỏ) của report/quyết định được nhắc tới; section spec riêng cho chỉ dẫn của Mate.
Vì sao: buyesp32 mất câu gốc `Thêm nút "Mua ngay" lên landing page ESP32, bấm vào thì mở trang thanh toán.`; crew và Mate khi review chỉ còn diễn giải của Mate. firstmate thêm luật này sau sự cố PR #3604 (commit `3b82ebdd`).
Ở đâu: `assets/crew/brief.md.tmpl` (hai placeholder hoặc một placeholder có heading cố định), manual mục 6 (đảo câu "not by pasting the captain's message"), `internal/spawn`.
Cỡ: M.

**A2. Nối lời captain đến sau vào brief và relay nguyên văn.**
Cái gì: khi captain đổi hoặc thêm yêu cầu giữa task, Mate nối lời đó vào `## Captain's words` của `crews/<id>/brief.md` (bản app giữ) và gửi crew một dòng trỏ tới nó (`AGENTS.md:376`).
Vì sao: không thì brief lưu trữ và thứ crew đang làm lệch nhau; review sau đó đối chiếu sai hợp đồng.
Ở đâu: manual mục 6/9; có thể là `matev2 brief append <project> <crew> -` để Mate không phải ghi vào `crews/` (Mate chỉ ghi được trong cwd của mình).
Cỡ: S (manual) + S (CLI).

**A3. Luật phạm vi: spec chỉ chứa cái ask cần, nêu rõ ngoài phạm vi, mở rộng là follow-up.**
Cái gì: chép gần nguyên văn `AGENTS.md:566` và `:378` vào manual.
Vì sao: brief buyesp32 thêm "keyboard-accessible, responsive", "Use the Sites-building guidance", và tự quyết "implement a local checkout page/route" — không cái nào captain yêu cầu.
Ở đâu: manual mục 6 (viết) và 9 (review).
Cỡ: S.

**A4. "Tra report sẵn có trước khi giao; bằng chứng không phải uỷ quyền."**
Cái gì: `AGENTS.md:308` và `:314-316`, cộng `VISION.md:28`.
Vì sao: `esp32research` đã có dàn ý landing page nhưng brief ship không dùng; và không có luật chặn Mate tự spawn ship từ khuyến nghị của một scout.
Ở đâu: manual mục 5.
Cỡ: S.

**A5. Skill `diagnostic-reasoning`.**
Cái gì: gần như nguyên văn (`.agents/skills/diagnostic-reasoning/SKILL.md`, 53 dòng); phần "A diagnosis brief should ask for ..." (`:48`) trở thành danh sách mục bắt buộc trong brief scout chẩn đoán.
Vì sao: matev2 chưa có hướng dẫn nào cho task bug; lời dặn cá nhân của captain ("reproduce it end-to-end") trùng hoàn toàn với skill này.
Ở đâu: `assets/mate/skills/diagnostic-reasoning/SKILL.md.tmpl`, trigger ở manual mục 5.
Cỡ: S.

**A6. Hình dạng escalation: bằng chứng → hậu quả → lựa chọn → khuyến nghị; năm yếu tố cho mở rộng phạm vi.**
Cái gì: `AGENTS.md:519-521` và `.agents/skills/ask-user-authority/SKILL.md:41-47`.
Vì sao: câu hỏi buyesp32 gửi captain (04:07:57Z) không có bằng chứng "repo trống", không có hậu quả, không có khuyến nghị.
Ở đâu: manual mục 13.
Cỡ: S.

**A7. Scout report phải tự đứng được, có cấu trúc.**
Cái gì: "what you did, what you found, the evidence (commands run, output, file:line references), and what you recommend" + "say so if this should ship" (`bin/fm-brief.sh:550-554`).
Vì sao: report scout là nguồn bối cảnh repo **duy nhất** của Mate (Mate không đọc repo), nên nó phải có file:line và lệnh chạy để brief ship sau trích được.
Ở đâu: phần scout của Definition of done trong `assets/crew/brief.md.tmpl`.
Cỡ: S.

### Áp dụng có điều chỉnh

**B1. `matev2 brief check` và kiểm tra khi `crew spawn`.**
Cái gì: từ chối brief thiếu section bắt buộc, section rỗng, còn placeholder, hoặc `## Captain's words` mở đầu bằng nhãn ("Captain:", "Người dùng nói:"), như `bin/fm-spawn.sh:2830-2842`.
Điều chỉnh vì: matev2 có thêm section bắt buộc mà firstmate không có (`## Acceptance`, `## Open decisions`, xem mục 11); kiểm tra thêm: mỗi dòng acceptance có `verify:`, `## Open decisions` hoặc là `none` hoặc mỗi mục có "who decides".
Chỉ kiểm hình dạng, không phán nghĩa (`VISION.md:34`).
Vì sao: manual mục 6 đã yêu cầu acceptance nhưng Mate vẫn viết một đoạn; lời dặn không đủ, cấu trúc thì đủ (chính quyết định 1 của `docs/mvp.md`: "Ràng buộc bằng cấu trúc ..., không bằng lời dặn").
Ở đâu: lệnh CLI mới trong `cmd/matev2`, gọi bởi `internal/spawn` trước khi tạo worktree; manual mục 4/6 mô tả lỗi.
Cỡ: M.

**B2. Section `## Open decisions` và luật "không tự quyết lựa chọn sản phẩm trong spec".**
Cái gì: firstmate không có section này vì orchestrator đọc được repo và có ask-user-authority phía pipeline; matev2 cần nó vì Mate viết brief mù và crew là nơi đầu tiên thấy repo.
Mỗi mục: câu hỏi, các lựa chọn đã biết, ai quyết (captain/Mate), và câu cố định "when you reach this, append `needs-decision:` and stop; do not pick".
Điều chỉnh vì: ba trạng thái crew của matev2 (`needs-decision` là cơ chế hỏi duy nhất, câu hỏi không có vòng đời, `docs/mvp.md` mục 4) — section này biến "Name the gap in the brief as a gap" (manual mục 5) từ lời dặn thành chỗ cụ thể.
Vì sao: buyesp32 pre-decide "local checkout page ... non-integrated payment handoff".
Ở đâu: template + manual mục 5/6 + `brief check`.
Cỡ: S (đi cùng B1).

**B3. Section `## Acceptance` có cách kiểm chứng, và hand-back có bằng chứng.**
Cái gì: mỗi tiêu chí là một hành vi quan sát được + `verify:` (lệnh nếu Mate biết từ PROJECT.md/report; nếu không thì "find the project's own check for this, run it, and quote the command and its output"); crew trước `wait-mate` phải ghi `crews/<id>/handback.md`: bảng tiêu chí → pass/fail + lệnh + output rút gọn, lệch khỏi spec, câu hỏi còn mở.
Điều chỉnh vì: firstmate dựa vào pipeline `no-mistakes` và không cần cái này (`AGENTS.md:352`); matev2 local-only không có pipeline, Mate là reviewer duy nhất trước captain, và Mate chỉ nhìn thấy diff.
Vì sao: "Validate with the project's relevant build/tests" của buyesp32 không kiểm được; `docs/evidence/m4-acceptance-2026-09-19.md` mục 3 cho thấy crew "shipped a guess ... and mentioned the assumption afterwards" — hand-back có mục "lệch khỏi spec" bắt nó phải nói ra trước khi Mate đọc diff.
Ở đâu: template (section cố định "Before you hand back" + ngoại lệ ghi file ngoài worktree giống report scout), manual mục 9 (review = đối chiếu handback với `## Captain's words` + `## Acceptance`, rồi đọc diff).
Cỡ: M.

**B4. Chuẩn review lấy từ `ask-user-authority`.**
Cái gì: khi review `wait-mate` hoặc trả lời `needs-decision`, Mate dựng hợp đồng từ `## Captain's words` + spec + lời captain sau, không từ lời crew; sửa trong phạm vi thì Mate tự quyết; mở rộng hợp đồng, product call chưa được lời captain quyết, sửa cùng chủ đề lặp lại, hành động phá huỷ → captain (`.agents/skills/ask-user-authority/SKILL.md:25-37`).
Điều chỉnh vì: matev2 không có "finding" của pipeline; áp cho câu hỏi của crew (`resolve:`) và cho kết quả review diff.
Ở đâu: manual mục 9/10 hoặc một skill `decision-authority` nạp khi xử lý `resolve:`/`digest:`.
Cỡ: S.

**B5. PROJECT.md phải có bối cảnh; project trống thì task đầu là scout "onboarding".**
Cái gì: firstmate không cần vì orchestrator đọc repo (`AGENTS.md:28`); matev2 cấm đọc (quyết định 1), nên nguồn bối cảnh là `PROJECT.md` + report scout.
Quy tắc: nếu `PROJECT.md` rỗng hoặc không trả lời được "repo này có gì, chạy/test bằng lệnh gì", ship đầu tiên phải đi sau một scout ngắn viết bản nháp PROJECT.md (stack, cấu trúc thư mục, lệnh build/test/chạy, trạng thái hiện tại như "repo trống") — đúng như `docs/mvp.md` mục 6 đã nói "Crew scout viết bản đầu" nhưng chưa có gì bắt nó xảy ra.
Và: mọi brief phải chép phần liên quan của PROJECT.md vào `## What we already know` (crew không đọc PROJECT.md).
Vì sao: repo `shop` trống, PROJECT.md trống; Mate viết "Inspect the existing app to locate the ESP32 landing page" cho một repo không có file nào.
Ở đâu: manual mục 3 (bootstrap: nếu PROJECT.md trống thì nói với captain và đề xuất scout onboarding), mục 5; có thể một kiểm tra rẻ `matev2 project facts` (đếm file/có commit nào trên default branch) — đây là metadata của git, không phải đọc code, nên giữ được quyết định 1; cần captain chốt ranh giới đó.
Cỡ: S (manual) + S (CLI, tuỳ chọn).

**B6. Thăng scout thành ship tại chỗ.**
Cái gì: `bin/fm-promote.sh` — crew scout giữ ngữ cảnh, nhận chỉ dẫn ship: inventory scratch, về base sạch, chỉ mang thay đổi dự định, biến repro thành regression test.
Điều chỉnh vì: matev2 không có steering inbox, chỉ có `matev2 send` một dòng; scout đã có branch và worktree (`docs/mvp.md` mục 4b: "Scout có branch không commit gì"). Nên: `matev2 crew promote <project> <id> --brief <file>` ghi phần ship vào `crews/<id>/brief.md` (để relaunch không hồi sinh luật scout, như commit `aa921774`) và gửi một dòng trỏ tới file.
Vì sao: giữ được ngữ cảnh điều tra; tránh việc brief ship thứ hai lại viết mù.
Ở đâu: CLI mới + manual mục 9 (scout đóng) + template (câu "say so if this should ship").
Cỡ: M.

**B7. Khối danh tính worker đứng đầu, tự tuyên bố ưu tiên.**
Cái gì: `bin/fm-dod-lib.sh:109-118`.
Điều chỉnh vì: từ vựng matev2 (Crew/Mate/captain, ba verb).
Vì sao: crew chạy trong repo có `AGENTS.md` kiểu orchestrator (repo matev2, repo firstmate) có thể nhận vai sai; firstmate đã gặp (commit `c806c6a3`).
Ở đâu: đầu `assets/crew/brief.md.tmpl`.
Cỡ: S.

**B8. Section `# Project memory` cho crew ship.**
Cái gì: `bin/fm-brief.sh:640-645`, bỏ phần `fm-ensure-agents-md.sh`, giữ luật "only knowledge useful to almost every future session", "prefer a pointer", "skip for trivial tasks".
Điều chỉnh vì: matev2 không có script canonical cho AGENTS.md; manual mục 14 đã nói tri thức code đi qua crew nhưng template crew không nói gì với crew.
Vì sao: đây là đường duy nhất tri thức repo tích luỹ mà không vi phạm "Mate không đọc repo".
Ở đâu: template.
Cỡ: S.

**B9. Curate memory.md: inspect-then-update, có ngày, có bằng chứng.**
Cái gì: `AGENTS.md:98`, `:277-288`, và bản rút gọn của `/stow` (định tuyến + "rewrite and prune rather than append" + ngày trên mỗi mục); bỏ tier/ngân sách/kho lạnh cho tới khi memory.md thật sự lớn.
Điều chỉnh vì: một Mate một project, không có secondmate hay home.
Vì sao: `memory.md` của `shop` trống sau năm task; không có gì chảy vào brief sau.
Ở đâu: manual mục 14; `matev2 remember` (task tương lai trong manual mục 4) nên nhận ngày và nguồn.
Cỡ: S.

**B10. Standing instructions cho crew do captain viết.**
Cái gì: `config/brief-include.md` (`bin/fm-brief.sh:286-309`).
Điều chỉnh vì: matev2 có `WORKSPACE.md` nhưng đó là quy tắc cho Mate; cần một file riêng, ví dụ `.matev2/CREW.md` hoặc `projects/<p>/CREW.md`, nối vào cuối brief với câu nhường ưu tiên.
Vì sao: quy tắc của captain cho người làm (ví dụ "reproduce end-to-end trước khi sửa", "một câu một dòng trong Markdown") hiện không có đường nào tới crew.
Ở đâu: `internal/mateassets/render.go`, `internal/spawn`.
Cỡ: S.

**B11. Relaunch giữ worktree, mang ghi chú tiến độ.**
Cái gì: `stuck-crewmate-recovery` bước 4 (`:77-81`).
Điều chỉnh vì: matev2 không có interrupt; hiện skill bảo Mate spawn crew mới với id mới và `Already tried`, mất worktree nếu `crew stop` từ chối.
Vì sao: commit trên branch cũ phải được mang theo, không phải bị bỏ lại bên cạnh.
Ở đâu: một `matev2 crew relaunch <project> <id> --note <file>` + skill.
Cỡ: M.

**B12. Effort theo độ bất định.**
Cái gì: `model-and-effort.md:15-18`.
Điều chỉnh vì: `crew spawn` chỉ có `--harness`; cần `--effort` nếu harness hỗ trợ.
Ở đâu: CLI + manual mục 7.
Cỡ: S-M; ưu tiên thấp.

### Không áp dụng

- **Pipeline `no-mistakes`, hợp đồng `--intent`, ask-user finding, `--yes` ban** (`bin/fm-dod-lib.sh:251-285`): matev2 local-only, không pipeline; lấy chuẩn phân quyền (B4), không lấy máy.
- **Delivery mode và `yolo` theo từng task, forge Gerrit, branch prefix** (`AGENTS.md:319-325`): matev2 một mode, `yolo` theo project.
- **Steering inbox với ack bằng `mv`** (`bin/fm-brief.sh:339-344`): matev2 đã quyết câu hỏi không có vòng đời, không ack (`docs/mvp.md` mục 4); tin dài đã đi bằng file + một dòng.
- **Keyed decisions, `resolved [key=...]`, captain-hold backlog** (`.agents/skills/captain-hold-lifecycle/SKILL.md`): cùng lý do; matev2 có luật rời inbox riêng.
- **Secondmate, quota-array-dispatch, typesafe dispatch resolver** (`AGENTS.md:227-239`): một Mate một project, hai harness; router bằng model ngoài là thêm một lớp giữa ý định và hành động mà `VISION.md:38` của chính firstmate cảnh báo.
- **Tier/ngân sách token/kho lạnh của `/stow`**: chưa có vấn đề kích thước; lấy luật curate (B9), bỏ máy.
- **Xưng "captain" bắt buộc và gia vị hàng hải** (`AGENTS.md:11-15`): phong cách, không phải chức năng; manual matev2 đã chọn "plainly and professionally".
- **Luật firstmate "overlap không phải lý do chờ"** (`AGENTS.md:327-328`): hấp dẫn nhưng dựa vào pipeline tự rebase/giải conflict; matev2 merge chỉ fast-forward và rebase là việc của crew qua một dòng Mate gửi (manual mục 9). Giữ luật queue hiện tại cho tới khi có bằng chứng nó gây chậm; ghi lại như một câu hỏi mở.
- **Lavish board, Herdr lab contract** (`bin/fm-brief.sh:447-493`): công cụ riêng của firstmate.

## 11. Schema brief đề xuất cho matev2

Nguyên tắc: template (app) sở hữu mọi thứ không phụ thuộc task; Mate chỉ điền các section dưới đây trong `# Task`; `matev2 brief check` kiểm hình dạng trước khi spawn.
Mỗi section có đúng một chủ.

### Phần Mate điền

| Section | Phải chứa | Ai điền | `brief check` kiểm |
| --- | --- | --- | --- |
| `## Captain's words` | Nguyên văn mọi lời captain về task này, ngôn ngữ gốc, không nhãn người nói, không viết lại. Nếu lời đó nhắc tới report/quyết định/task trước ("như report ESP32"), chép **nội dung** liên quan vào ngay dưới, đánh dấu nguồn. Lời captain đến sau được nối vào cuối. | Mate chép; app nối lời đến sau | Có, không rỗng, không mở đầu bằng nhãn |
| `## What we already know` | Những gì Mate biết về repo và task, mỗi dòng kèm nguồn: `PROJECT.md`, report scout (đường dẫn tuyệt đối + mục), diff của crew trước, lời captain. Dòng bắt buộc cuối: những gì Mate **không** biết. Không bịa trạng thái repo. | Mate, từ PROJECT.md và report scout | Có, không rỗng |
| `## Build` | Chỉ dẫn xây dựng mà lời captain đòi hỏi, không hơn. Dòng `Out of scope:` liệt kê cái cố ý không làm. Không chứa lựa chọn sản phẩm chưa được lời captain quyết. | Mate | Có, có dòng `Out of scope:` |
| `## Acceptance` | Danh sách hành vi quan sát được, mỗi dòng một tiêu chí, suy ra từ `## Captain's words`. Mỗi dòng có `verify:` — lệnh cụ thể nếu biết, hoặc "find and run the project's own check, quote command and output". | Mate | Có, mỗi mục có `verify:` |
| `## Open decisions` | `none`, hoặc mỗi mục: câu hỏi, lựa chọn đã biết, `decides: captain` hoặc `decides: mate`. | Mate | Có; `none` hoặc mỗi mục có `decides:` |
| `## Deliverable` (chỉ scout) | Đường dẫn tuyệt đối `crews/<id>/report.md` và các câu hỏi report phải trả lời. | Mate | Có với scout |

### Phần template cố định (thêm vào template hiện tại)

- `# Current worker role` đứng đầu (B7).
- Luật đọc `## Open decisions`: "When you reach one of these, append `needs-decision:` naming it, and stop. Do not pick an option, even the obvious one."
- `# Before you hand back` (ship): chạy mọi `verify:`; ghi `crews/<id>/handback.md` với bảng `criterion | pass/fail | command | output (short)`, mục `Deviations from Build`, mục `Still open`; rồi mới `wait-mate`.
- Scout DoD: report tự đứng được — what you did, what you found, evidence (commands, output, file:line), recommendation; "if this should ship, say so and propose the acceptance lines".
- `# Project memory` (B8).
- `# Captain's standing crew rules` nối cuối từ file captain viết (B10).

### Scout report chảy vào brief ship thế nào

1. Scout viết report theo cấu trúc trên, có file:line và lệnh.
2. Mate đọc report (việc của Mate, không phải đọc repo), relay phát hiện cho captain dưới dạng phát hiện.
3. Khi captain cho làm: hoặc thăng scout tại chỗ (B6), hoặc brief ship mới trong đó `## What we already know` chép **nội dung** các phát hiện liên quan với nguồn `report.md §n`, `## Acceptance` lấy từ đề xuất của scout nhưng phải truy được về `## Captain's words`, và mọi câu hỏi report để ngỏ đi vào `## Open decisions`.
4. Phát hiện bền vững về repo (stack, lệnh test) được Mate thêm vào `PROJECT.md` để brief sau không cần scout lại.

### Ví dụ: buyesp32 viết lại theo schema

Giả định đúng với tình trạng thật ngày 2026-09-19: `PROJECT.md` trống, repo chưa bao giờ được scout, bốn report nghiên cứu phần cứng đã có.
Theo B5, cách làm tốt nhất là một scout onboarding ngắn trước; nếu Mate vẫn dispatch ship ngay (manual mục 5 cho phép), brief trung thực trông như sau.

```markdown
# Task

## Captain's words
Thêm nút "Mua ngay" lên landing page ESP32, bấm vào thì mở trang thanh toán.

Earlier, when commissioning the ESP32 research (2026-09-17):
tôi muốn bạn tìm hiểu cho tôi mạch esp32 sau đó báo cáo lại, ta sẽ làm landing page cho nó

## What we already know
- The Mate has not read this repository and has no record of its contents: PROJECT.md is empty and no earlier task changed code here.
- No earlier task built an ESP32 landing page; the backlog holds only research tasks. Whether one exists in the repo is unknown.
- A research report with a landing-page content outline exists: /Users/erics/work-matev2/.matev2/projects/shop/crews/esp32research/bao-cao-esp32-devkit.md, section "4) Khung nội dung sẵn cho landing page". Read it only if Open decision 1 is answered "build it".
- No payment provider, checkout URL or payment account has ever been mentioned by the captain.
- Unknown: the stack, the build and test commands, and whether a checkout page exists.

## Build
- Find the ESP32 landing page and any existing checkout page or checkout URL.
- If both exist: add a button labelled exactly "Mua ngay" to the landing page that navigates to the existing checkout page.
- Out of scope: creating a landing page, creating a checkout page, any payment provider or payment processing, restyling the page, deploying.

## Acceptance
- The ESP32 landing page shows a button whose visible text is exactly "Mua ngay". verify: render the page with the project's own dev server or build output and quote the element.
- Activating the button (click or Enter) opens the checkout page. verify: the project's own test or a scripted browser/HTTP check; quote the command and its output.
- The project's existing build and tests still pass. verify: find the project's own build/test commands, run them, quote command and result.

## Open decisions
1. There is no ESP32 landing page in the repository. Options: the captain points to where it lives; or a new landing page is built (a separate, larger task, content from the research report above). decides: captain
2. There is no checkout page or checkout URL. Options: the captain provides the URL; or a placeholder checkout page with no payment is built. decides: captain
3. There is more than one candidate landing page or checkout page. Options: list them with paths. decides: captain
```

Khác biệt so với bản gốc, từng điểm yếu:

- Lời captain nằm nguyên văn ở đầu, kèm câu "ta sẽ làm landing page" cho thấy landing page là việc tương lai — manh mối mà bản gốc làm mất.
- Lựa chọn "tự dựng trang checkout local" chuyển từ spec sang `## Open decisions` với `decides: captain`; `Out of scope` chặn crew tự làm.
- Bối cảnh nói thật là Mate không biết gì, trỏ tới report có sẵn, và không bịa "the existing app".
- Acceptance có ba dòng kiểm được, mỗi dòng có `verify:`; "keyboard-accessible, responsive" bị bỏ vì captain không yêu cầu (có thể ghi thành follow-up).
- Với repo trống, crew sẽ dừng ở Open decision 1 trong vài phút như bản gốc — nhưng câu hỏi đến captain đã được đặt sẵn đúng hình dạng, và một Mate đọc `PROJECT.md` trống + backlog lẽ ra đã nhận ra Open decision 1 gần như chắc chắn xảy ra, nên theo `AGENTS.md:314` hỏi captain một câu trước khi dispatch là hợp lý hơn.
