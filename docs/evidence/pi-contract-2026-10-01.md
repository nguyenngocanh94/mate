# pi 0.99.1 đo theo hợp đồng harness, trước khi chốt interface

- Ngày đo: 2026-10-01, máy Mac mini của captain.
- Phương án: [registry harness](../plans/harness-registry-2026-09-30.md) (đang ở PR <https://github.com/nguyenngocanh94/mate/pull/3>), PR 0 phần 2: mục 7 và hàng rủi ro đầu tiên của mục 8.
- Mục đích: điền mọi ô "Cần đo" của mục 7 bằng phép đo trong pane Herdr, và chỉ ra chỗ hợp đồng mục 3.2 không khớp với pi, trước khi PR 1 chốt interface.
- Chỉ có tài liệu và capture; không sửa production code.
- Capture thô nằm ở [pi-contract-2026-10-01/](pi-contract-2026-10-01/).

## Phiên bản

| Thành phần | Version | Cách đọc |
| --- | --- | --- |
| pi | 0.99.1 (`@earendil-works/pi-coding-agent`, `/opt/homebrew/bin/pi`) | `pi --version` |
| Herdr | 0.8.2 | `herdr --version` |
| Tích hợp pi của Herdr | v8 (`HERDR_INTEGRATION_VERSION=8`) | `herdr integration install pi` vào thư mục scratch, xem bên dưới |
| Model dùng cho turn | `deepseek/deepseek-flash` | khoá `deepseek` có sẵn trong `~/.pi/agent/auth.json`; `pi auth check --provider deepseek --no-refresh --json` trả `ready` |
| Model chỉ dùng để đo mức thinking | `openrouter/deepseek/deepseek-chat`, `openrouter/stealth/ox-alpha` | không gửi prompt nào tới hai model này |

pi 0.99.2 đã phát hành trong lúc đo (banner "Update Available"); mọi số đo ở đây là của 0.99.1.

## Cách đo

Lab Herdr cô lập, dựng từ shell Claude Code với các biến lồng nhau đã gỡ:

```sh
LAB=fm-lab-pi-$$
(env -u CLAUDECODE -u CLAUDE_CODE_SESSION_ID -u CLAUDE_CODE_ENTRYPOINT ... TMPDIR=<scratch> herdr --session "$LAB" server >/dev/null 2>&1 &)
herdr --session "$LAB" workspace create --label w1 --cwd <scratch>/lab/w1 --no-focus
herdr --session "$LAB" agent start pi1 --kind pi --pane w1:p1 --timeout 60000 -- <cờ pi>
herdr session stop "$LAB"; herdr session delete "$LAB"
```

- Script đầy đủ: [lab/lab.sh](pi-contract-2026-10-01/lab/lab.sh) và [lab/poll.sh](pi-contract-2026-10-01/lab/poll.sh); đường dẫn trong đó là worktree scratch của lần đo này.
- TMPDIR và mọi thư mục làm việc nằm dưới `.scratch/tmp` của worktree, đã `realpath`, không qua symlink.
- Env của pane được dump trước khi chạy pi: không còn biến `CLAUDE*` nào; `AI_AGENT=claude-code_2-1-285_agent` vẫn lọt vào vì nó không có trong `harness.NestedSessionEnv`, và không thấy nó ảnh hưởng gì tới pi.
- Pane 93 cột × 39 hàng (`stty size` trong pane; Herdr báo `viewport_rows: 40`).
- Hai lab, `fm-lab-pi-32128` và `fm-lab-pi-82549`, đều đã stop và delete; `herdr session list --json` không còn session `fm-lab-pi`.
- Không ghi gì vào cấu hình toàn cục của pi: `~/.pi/agent/trust.json` và `~/.pi/agent/extensions/` giống hệt trước khi đo, session đi vào `--session-dir` scratch hoặc `--no-session`.
- Tổng chi phí model của mọi turn dưới 0,01 USD theo footer của pi.

Ba cách đọc:

- Màn hình: `herdr agent read <agent> --source visible|recent|recent-unwrapped --format text|ansi`, đúng lệnh `runtime.Herdr.readAgent` dùng (mặc định `recent-unwrapped`).
- Trạng thái Herdr: `herdr agent get` mỗi 0,25 giây, chỉ ghi khi đổi.
- Bên trong pi: một extension đo, [lab/probe.ts](pi-contract-2026-10-01/lab/probe.ts), nạp bằng `-e`, ghi mỗi sự kiện vòng đời (tên, hình payload, session id, session file, model, mức thinking, trust) ra JSONL.
  Ở `session_start` nó đếm marker trong `ctx.getSystemPrompt()`, nên đo được context đã nạp mà không cần gửi prompt.
  Nó không chặn và không trả lời gì, trừ hai chỗ có chủ ý: trả `undecided` cho `project_trust`, và chèn một message khi prompt chứa "secret word" (phép thử chèn context).

Một số khẳng định có đối chiếu với mã nguồn đã đóng gói của pi (`dist/core/resource-loader.js`); những chỗ đó ghi rõ là đọc mã, không phải đo.

## Mục 7: các ô "Cần đo"

| Hợp đồng | Đo được | Bằng chứng |
| --- | --- | --- |
| Giao context: nạp trùng | Không nạp trùng. Mỗi thư mục nạp đúng một file theo thứ tự `AGENTS.override.md` > `AGENTS.md` > `CLAUDE.md`. Thư mục có cả ba thì chỉ override vào context; thư mục chỉ có `CLAUDE.md` thì `CLAUDE.md` vào. | `probe-ctx-c2.jsonl`: `MARK-OVERRIDE 1, MARK-AGENTS 0, MARK-CLAUDE 0`; `probe-ctx-c1.jsonl`: `MARK-CLAUDE 1`; `probe-run1.jsonl`: `w1/CLAUDE.md` có mặt nhưng không vào `contextFiles` |
| Giao context: thư mục cha | Đo được: pi nạp file context của mọi thư mục cha giữa cwd và git root (`lab/ctx/AGENTS.md`, rồi `AGENTS.md` ở gốc worktree). Đo được: worktree lồng trong repo chính thì `AGENTS.md` của repo chính bị bỏ (`findShadowedContextFile`). Đọc mã, chưa đo: vòng lặp đi tiếp tới `/`, không dừng ở git root (máy này không có file context nào phía trên repo để thấy), và `~/.pi/agent/AGENTS.md` cũng được nạp nếu có. | `probe-ctx-c1.jsonl`: `MARK-PARENT 1`; `probe-run1.jsonl` `contextFiles`; `/Volumes/Work/Workspace/mate/AGENTS.md` không vào; `loadProjectContextFiles` trong `dist/core/resource-loader.js` |
| Giao context: `--append-system-prompt` | Nhận đường dẫn file hoặc text; lặp được nhiều lần. Đường dẫn không tồn tại bị dùng nguyên văn làm text, không cảnh báo trên màn hình. | `probe-run1.jsonl`: `appendLen 36, appendMarks 1`; `probe-ctx-c4-missing.jsonl`: `MARK-APPEND 2` (một từ chính chuỗi đường dẫn thiếu); `screens/ctx-c4-missing-startup.txt` |
| Giao context: trần kích thước | Không có trần lúc nạp: `AGENTS.md` 480 KB cộng file append 490 KB cho system prompt 974 403 ký tự, có cả marker đầu lẫn cuối. Giới hạn thật chỉ là context window của model; chưa đo request vượt window. | `probe-ctx-c3-big.jsonl`: `systemPromptLen 974403, MARK-BIG-HEAD 1, MARK-BIG-TAIL 2` |
| Session: định dạng | JSONL, bản ghi đầu `{"type":"session","version":3,"id","timestamp","cwd"}`, rồi `model_change`, `thinking_level_change`, `message` (role `system`, `user`, `assistant`, `toolResult`), `custom_message`; mỗi bản ghi có `id` và `parentId` (cây, không phải danh sách). | `session/*.jsonl` |
| Session: tên file | `<session-dir>/<ISO timestamp với - thay :>Z_<session id>.jsonl`. Không có `--session-dir` thì thư mục mặc định là `~/.pi/agent/sessions/--<cwd, / thành ->--/` (thấy trong `~/.pi/agent/sessions`, không tạo mới). File chỉ được ghi ở prompt đầu; trước đó id và đường dẫn đã có trong pi. | `probe-run1.jsonl` `session_start.sessionFile`; `ls` thư mục session trước và sau prompt đầu |
| Session: id lúc launch | `--session-id <uuid>` nhận UUID do caller sinh; chưa có thì tạo mới với đúng id đó và in một dòng `Warning: No project session found ... creating a new session with that id.`; đã có thì resume. Không có `--session-id` thì pi tự sinh UUIDv7. | `screens/run3-sessid-startup.txt`; `probe-run3-sessid.jsonl`, `probe-run4-resume-sessid.jsonl` cùng `sessionId` và cùng file |
| Session: resume có dialog không | Không. Cả `--session <id rút gọn>` lẫn `--session-id <id đủ>` vào thẳng composer, lịch sử vẽ lại phía trên. `session_start.reason` là `startup` cả khi resume. | `screens/run2-resume-startup.txt`; `probe-run4-resume-sessid.jsonl`: `"reason":"startup"` |
| Session: khi thoát | pi in `To resume this session: pi --session-dir <dir> --session <uuid>`. | `screens/run1-after-ctrl-d.txt`, `screens/run3-after-quit.txt` |
| Model, effort | `--thinking` nhận `off, minimal, low, medium, high, xhigh, max`, nên năm mức `Effort` của mate đều là giá trị hợp lệ. Nhưng pi kẹp theo model, không báo: `deepseek-flash` medium→high, xhigh→max; `ox-alpha` cả `off`→low; `deepseek-chat` mọi mức→off. Mức thật hiện ở footer và ở bản ghi `thinking_level_change`. Không truyền `--model`, `--thinking` thì pi lấy từ `~/.pi/agent/settings.json` của người dùng (ở máy này `openrouter/stealth/ox-alpha`, `high`). | `probe/thinking-levels.tsv`; `screens/run5-medium-clamp-startup.txt`; `session/*c954e7e3*.jsonl`: xin `xhigh`, ghi `max` |
| Trust | Chỉ có một dialog, và chỉ khi cwd có tài nguyên project được bảo vệ (`.pi/settings.json`, `.pi/extensions`, ...). Thư mục chỉ có `AGENTS.md` thì không hỏi. `--approve` bỏ dialog và tin project (nạp cả extension của project); `--no-approve` bỏ dialog, bỏ qua `.pi/` của project và in một dòng thông báo. Highlight mặc định của dialog ở "Trust"; suy từ nhãn, lựa chọn không kèm "(this session only)" là quyết định được lưu vào `~/.pi/agent/trust.json` (chưa chọn thử, để khỏi ghi vào cấu hình của người dùng). Theo `docs/security.md` của pi còn đường thứ ba: extension của người dùng hoặc nạp bằng cờ trả lời sự kiện `project_trust`; probe nhận được sự kiện này khi không có `--approve`. | `screens/trust-c5-startup.txt`, `trust-c5-approve-startup.txt`, `trust-c5-no-approve-startup.txt`; `probe-trust-c5*.jsonl` |
| Dialog khởi động khác | Không thấy dialog nào khác. Banner "Update Available" không chặn composer, xuất hiện không đều giữa các lần chạy, và mất hẳn với `--offline`; model vẫn gọi được khi `--offline`. | `screens/run1-empty-composer.visible.txt`, `run8-offline-startup.txt` |
| Hook | Không có hook dạng lệnh shell. `-e <file.ts>` nạp extension chạy trong process pi, kể cả khi có `--no-extensions`. Đo được các sự kiện: `session_start` (`reason`), `input` (`text`, `source: interactive`; marker `⟦mate⟧ ` đi qua nguyên vẹn), `before_agent_start` (`prompt`, `systemPrompt`, `systemPromptOptions.contextFiles`), `agent_start`, `turn_start`, `turn_end`, `tool_call` (`toolName`, `input`), `message_end` (`usage`, `stopReason`), `agent_end` (`messages`), `agent_settled` (không field), `session_shutdown` (`reason: quit`), `project_trust` (`cwd`). Trả `message` từ `before_agent_start` chèn được context vào đúng turn đó: model thấy câu đã chèn, và session ghi một bản ghi `custom_message`. | `probe-run1.jsonl`, `probe-run7-inject.jsonl`, `session/*3f0c5a8e*.jsonl` |
| Bỏ hỏi quyền | pi không hỏi quyền trước khi chạy tool: lệnh bash `sleep 6; echo pi-ok` chạy ngay, Herdr không lần nào báo `blocked`. Không cần cờ nào. | `screens/run1-busy-*.txt`; `herdr/run1-status-turn1.log`; `probe-run1.jsonl` `tool_call` rồi `tool_result` cách 6 giây |
| Herdr: `agent_status` | Không có tích hợp Herdr: phát hiện bằng màn hình, `idle → working → idle` khớp với turn, `agent_session` không có. Trong lúc dialog trust đang chặn, Herdr báo `idle` và `interactive_ready: true`, và `agent start` vẫn trả thành công. | `herdr/run1-status-turn1.log`, `herdr/start-run1.txt`, `herdr/start-trust-c5.json` |
| Herdr: `agent_session` | Chỉ được điền khi extension tích hợp của Herdr được nạp. Cài `herdr integration install pi` với `PI_CODING_AGENT_DIR` trỏ vào thư mục scratch rồi nạp bằng `-e`: `agent_session = {kind: path, source: herdr:pi, value: <đường dẫn session file>}` ngay lúc khởi động, `screen_detection_skipped: true`, trạng thái do sự kiện `agent_start` và `agent_settled` báo. | `herdr/start-run6-herdr-integration.json`, `herdr/run6-status-turn.log` |
| Thoát êm | `/quit` rồi Enter thoát; `ctrl+d` chỉ thoát khi composer trống, có draft thì không làm gì. Thoát xong Herdr gỡ agent (`agent_not_found`), extension nhận `session_shutdown`. | `screens/run3-after-quit.txt`; `probe-run3-sessid.jsonl` |

## Hợp đồng mục 3.2 áp lên pi

Trạng thái dưới đây là trạng thái pi sẽ nhận nếu khai báo theo số đo hôm nay; chưa có `Impl` nào được viết.

| Thành phần | Trạng thái | Evidence hoặc Reason |
| --- | --- | --- |
| `Info` | đủ dữ liệu | Kind phía runtime `pi`, executable `pi`, tiêu đề terminal `π - <tên thư mục>`; `herdr/start-run1.txt` |
| `Launcher.Prepare` | đủ dữ liệu | Context qua `AGENTS.override.md` trong cwd (cùng cơ chế Codex) hoặc `--append-system-prompt <file>`; session trong một `--session-dir` của mate; hook là một file `.ts` nạp bằng `-e` |
| `Launcher.Build` | đủ dữ liệu | Cờ cần có: `--model`, `--thinking`, `--session-dir`, `--session-id`, `--no-extensions`, `--no-skills`, `-e <hook>`, `--approve` hoặc `--no-approve`, `--offline`; không có khoá env nào bắt buộc ngoài khoá API của provider |
| `ScreenProfile` | đủ capture, có điều kiện | Composer trống, có draft, đang bận, dialog trust và sau khi chọn đều đã chụp (bảng capture bên dưới). Điều kiện: không đọc được composer qua `recent-unwrapped` một cách tin cậy, xem mục không khớp số 1 |
| `GracefulStop` | verified | `ctrl+u`, rồi `/quit` và Enter; `screens/run3-after-quit.txt`, lab thứ hai |
| `Session` | verified | Id do mate sinh, có lúc launch, `--session-id` vừa tạo vừa resume, resume không có dialog; `probe-run3-sessid.jsonl`, `probe-run4-resume-sessid.jsonl` |
| `Hooks` | unknown | Reason: cơ chế đã đo (sự kiện, payload, chèn context), nhưng hợp đồng `HookInstaller` hiện giả định hook là lệnh shell nhận JSON qua stdin; với pi phần giải mã phải nằm trong một shim TypeScript chưa viết, và `session_start` không phân biệt resume với khởi động mới. Còn thiếu: shim gọi `mate hook` và một live test cho Mate trên pi |
| `Transcript` | verified (định dạng), unknown (chuẩn hoá) | JSONL v3 dạng cây; usage theo từng message assistant là delta: `input` không gồm `cacheRead` (turn hai: `input 157`, `cacheRead 5632`), kèm `cost`. Reason cho phần unknown: chưa có fixture nào chạy qua parser của mate |
| `TurnEnd` | verified | Bản ghi `message` role `assistant` có `stopReason: "stop"` sau thời điểm gửi; sự kiện `agent_settled`; Herdr về `idle`; `session/*3f0c5a8e*.jsonl`, `probe-run1.jsonl` |
| `Quota` | unknown | Reason: pi không có quota riêng; provider là của từng model (`deepseek`, `openrouter`, ...), nên lane của `quota-axi` không suy ra được từ harness. Chưa đo `quota-axi` cho provider nào của pi |

## Capture cho ScreenProfile

Pane 93×39, pi 0.99.1, Herdr 0.8.2, đọc bằng `herdr agent read` trừ chỗ ghi khác.

| Trạng thái | File | Nguồn |
| --- | --- | --- |
| Composer trống, lần đầu | `screens/run1-empty-composer.recent-unwrapped.{txt,ansi}`, `run1-empty-composer.visible.txt` | `recent-unwrapped`, `visible` |
| Composer trống, sau một turn | `screens/run6-idle-after-turn.{visible,recent,recent-unwrapped}.{txt,ansi}` | cả ba |
| Có draft | `screens/run1-draft.recent-unwrapped.{txt,ansi}`, `screens/run6-draft.{visible,recent,recent-unwrapped}.{txt,ansi}` | cả ba |
| Sau `ctrl+u` | `screens/run1-after-ctrl-u.txt` | `recent-unwrapped` |
| Đang bận | `screens/run1-busy-1.txt`, `run1-busy-3.{txt,ansi}`, `screens/run8-busy-reads.{visible,recent,recent-unwrapped}.{txt,ansi}` | cả ba |
| Xong turn | `screens/run1-busy-10.txt` | `recent-unwrapped` |
| Dialog trust, highlight mặc định | `screens/trust-c5-startup.{txt,ansi}` | `recent-unwrapped` |
| Dialog trust, highlight ở "Trust (this session only)" | `screens/trust-c5-dialog-third-option.visible.txt` | `herdr pane read --source visible` |
| Sau khi chọn trust một phiên | `screens/trust-c5-after-session-trust.visible.txt` | `herdr pane read --source visible` |
| `--approve`, `--no-approve`, `--offline`, resume, kẹp thinking | `screens/trust-c5-approve-startup.txt`, `trust-c5-no-approve-startup.txt`, `run8-offline-startup.txt`, `run2-resume-startup.txt`, `run5-medium-clamp-startup.txt` | `recent-unwrapped` |

Cấu trúc đo được:

- Composer là các hàng giữa hai đường kẻ `─` dài hết bề ngang, ngay trên hai dòng footer (cwd và nhánh git; usage, `(provider) model • mức thinking`).
- Không có glyph prompt và không có placeholder: draft bắt đầu ở cột 0; con trỏ là một ô đảo màu (`ESC[7m ESC[0m`), chỉ thấy ở bản `ansi`.
- Đang bận: một dòng `── <spinner braille> Working ───…` nằm ngay trên composer, trong khi composer vẫn trống; vậy kiểm "đang bận" phải đứng trước kiểm "composer trống", giống Claude.
- Dialog trust: dòng hỏi `Trust project folder?`, đường dẫn, năm lựa chọn (`Trust`, `Trust parent folder` kèm đường dẫn cha xuống dòng, `Trust (this session only)`, `Do not trust`, `Do not trust (this session only)`), highlight là `→ ` ở đầu lựa chọn, footer `↑↓ navigate  enter select  escape/ctrl+c cancel`.
  Highlight mặc định ở `Trust`, suy từ nhãn là lựa chọn được lưu lại; `down` hai lần rồi Enter chọn `Trust (this session only)` và không ghi gì (đã so `trust.json` trước và sau).

## Chỗ hợp đồng mục 3.2 không khớp pi

Đây là kết quả chính của PR này.

1. **`ScreenProfile` không chọn được nguồn đọc, mà pi cần `visible`.**
   Với pi, `recent-unwrapped` (nguồn mặc định của `runtime.Herdr.readAgent`) có lúc gộp hai đường kẻ và hàng composer thành một dòng logic dài 664 ký tự, nên draft nằm kẹp giữa hai dãy `─` và không còn hàng composer nào để tìm (`screens/run6-draft.recent-unwrapped.txt` so với `run6-draft.visible.txt`).
   Cùng trạng thái đó, `visible` và `recent` đều đúng; ở lần chạy đầu `recent-unwrapped` lại đúng, nên lỗi không ổn định.
   Hợp đồng hiện cho `ScreenProfile` nhận một chuỗi màn hình do runtime chọn nguồn; nó cần khai báo được nguồn đọc (hoặc runtime luôn kèm bản `visible`).

2. **Effort là của model, không phải của harness.**
   `Kind.SupportsEffort(e)` và cờ `effortOmitted` trả lời theo kind.
   Với pi, cùng một `--thinking medium` cho ra `high`, `low` hay `off` tuỳ model, và pi không báo gì.
   `Launcher.Build` không biết mức nào thực sự được áp dụng; muốn biết phải đọc footer hoặc bản ghi `thinking_level_change` sau khi launch.
   Hợp đồng cần chỗ cho "mức xin" khác "mức được áp dụng", hoặc ghi rõ rằng với pi `Effort` chỉ là yêu cầu.

3. **`Quota` không gắn với harness.**
   pi là harness nhiều provider; provider đi theo `--model`.
   `QuotaProvider` ở mục 3.2 là một thuộc tính của profile; với pi nó phải là hàm của model trong launch, hoặc capability này ở `unknown` vĩnh viễn.

4. **`HookInstaller` giả định hook là lệnh shell, pi có hook là code chạy trong process.**
   Claude và Codex gọi `mate hook ...` với JSON qua stdin; pi chỉ có extension TypeScript nạp bằng `-e`.
   Muốn dùng lại `mate hook`, mate phải sinh và giữ một shim `.ts` tự gọi lệnh đó; phần "giải mã payload thành sự kiện chung" vì vậy nằm một nửa trong TypeScript, không nằm trọn trong Go.
   Chèn context cũng khác: Claude chèn bằng stdout của hook `SessionStart`, pi chèn bằng giá trị trả về của `before_agent_start` theo từng turn (đã đo, model nhận được).
   `Prepare` trả được file kèm cờ launch như mục 7 của phương án đã đoán; phần còn thiếu là hợp đồng phải chấp nhận hook là một file mã do mate sở hữu, có version, chứ không chỉ là một file cấu hình.

5. **`session_start` không phân biệt resume.**
   `hook.SessionStart.LiveOnly()` dựa vào `source == "resume"`.
   pi báo `reason: "startup"` cả khi `--session-id` mở lại một session đã có lịch sử (`probe-run4-resume-sessid.jsonl`).
   Shim phải tự suy ra resume, ví dụ session đã có bản ghi `message` hay chưa; hợp đồng không nên coi `source` là thứ mọi harness cung cấp.

6. **Kiểm session trên đĩa trước resume phải dựa vào glob, và thiếu file thì pi im lặng mở phiên mới.**
   Tên file có timestamp đứng trước id, nên `SessionIdentity` không dựng được đường dẫn từ id; phải tìm `*_<id>.jsonl` trong `--session-dir`, hoặc đọc `agent_session` của Herdr khi có tích hợp.
   `--session-id` với id không có trên đĩa không báo lỗi, chỉ in một dòng cảnh báo rồi tạo phiên mới; đường hạ cấp "khởi động phiên mới, ghi chú vì sao" ở mục 3.7 phải do mate tự kiểm trước khi launch, không thể dựa vào pi từ chối.

7. **Readiness của Herdr sai khi dialog trust đang mở.**
   `agent start` trả thành công với `idle` và `interactive_ready: true` trong khi dialog chặn composer (`herdr/start-trust-c5.json`).
   Điều này khớp quyết định 8 (trạng thái Herdr chỉ là tín hiệu phụ), nhưng nghĩa là với pi bước settle phải đọc màn hình, hoặc tốt hơn là launch với `--approve`/`--no-approve` để dialog không bao giờ xuất hiện.
   Hợp đồng nên cho `Launcher` cách tránh dialog bằng cờ, thay vì giả định mọi dialog đều được `ScreenProfile` trả lời.

8. **Cấu hình toàn cục của người dùng chảy vào pane nếu mate không chặn.**
   Không có `--no-extensions` và `--no-skills`, pi nạp extension trong `~/.pi/agent/extensions` (ở máy này ba extension của Orca) và skill trong `~/.agents/skills`; không có `--model` và `--thinking`, pi dùng mặc định của người dùng.
   Đọc mã: `~/.pi/agent/AGENTS.md` được nạp nếu có, và `--no-context-files` tắt nó cùng với mọi context file khác, kể cả file mate cần.
   Hợp đồng `Launcher` cần một danh sách "phải tắt" ngang với danh sách env phải unset của mục 3.6; với pi đó là cờ, không phải biến môi trường.

9. **Phạm vi context khác cả hai harness hiện có.**
   pi nạp file context của các thư mục cha (đo được tới git root; theo mã nguồn thì tới `/`), trong khi Codex dừng ở git root.
   Nếu đúng như mã nguồn, crew ở `<workspace>/.worktrees/<project>-<crew>/` sẽ nạp cả file context ở `<workspace>/` và mọi thư mục phía trên nó; phần này cần một phép đo ngoài git repo trước PR 7.
   Mục 3.3 chỉ hỏi "context bắt buộc có giao được không"; với pi còn phải hỏi "có gì khác lọt vào không", và câu trả lời phụ thuộc vào nơi đặt workspace.

10. **`--append-system-prompt` nhận cả đường dẫn lẫn text.**
    Đường dẫn sai thành text, không báo lỗi.
    Kiểm tra chung "context tồn tại" của builder ở mục 3.5 bắt được lỗi này, nên đây là lý do giữ kiểm tra đó bắt buộc, không phải lỗi của hợp đồng.

Những phần khớp, không cần đổi hợp đồng:

- `SessionIdentity` biến thể "id có lúc launch" của Claude dùng được nguyên cho pi, và còn gọn hơn vì một cờ vừa tạo vừa resume.
- `GracefulStopper` là một chuỗi phím, đúng hình đã định; khác Claude ở chỗ phải `ctrl+u` trước khi gõ `/quit`.
- `TurnEndEvidence` và `TranscriptSource` có đủ dữ liệu; usage là delta theo call như Claude, nên chuẩn hoá đi theo nhánh Claude chứ không theo snapshot cộng dồn của Codex.
- Giao context qua `AGENTS.override.md` trong cwd hoạt động như Codex: file đó che `AGENTS.md` và `CLAUDE.md` tracked của cùng thư mục.

## Chưa đo được

- Request vượt context window của model: không gửi, vì tốn quota và không đổi hợp đồng.
- Một shim `mate hook` thật trong pi: ngoài phạm vi PR chỉ có tài liệu.
- Composer nhiều dòng, dán văn bản dài, và hành vi khi gõ trong lúc đang bận (`input.streamingBehavior` là `steer` hoặc `followUp` theo kiểu dữ liệu, chưa đo).
- `~/.pi/agent/AGENTS.md`: máy này không có file đó; chỉ biết qua mã nguồn.
- Thư mục làm việc ngoài mọi git repo, và file context phía trên git root: không tạo được trong worktree cô lập này, nên giới hạn "tới `/`" chỉ từ mã nguồn.
- `quota-axi` cho bất kỳ provider nào của pi.
