# Token của Mate: đo trên `hellovietnam` và phương án giảm

Ngày 2026-09-27, bản sửa lúc 22:30 sau khi đối chiếu với phân tích của astra.
Đây là phân tích và đề xuất, chưa đổi dòng code nào.
Nguồn: transcript Claude Code của Mate `hellovietnam` (session `83ecc641`, 2026-09-25 14:23 → 2026-09-27 22:28, Claude Code 2.1.283, model `claude-opus-5-5`, effort `medium`), `mate usage`, `sent.log`, snapshot prompt mà Claude Code ghi trong transcript, ba lượt `claude -p` thăm dò flag, và code của console.
Giá quy đổi là giá API niêm yết của Opus 5.5 theo skill `claude-api` (input $4/M, cache read $0,20/M, cache write TTL 1 giờ ≈ $8/M, output $20/M); Mate chạy bằng tài khoản của captain nên con số tiền chỉ để so sánh tương đối giữa các phương án.

Sửa so với bản đầu trong ngày: transcript ghi một lần gọi model thành nhiều dòng `assistant` (thinking, text, tool_use cùng một `message.id`), bản đầu đếm dòng nên ra 358 lần gọi và 78,5M token; đếm theo `message.id` là 268 lần gọi và 66,5M token, khớp với số của astra (256 lần, 61,4M, đo sớm hơn vài giờ).
Kết luận không đổi, các con số dưới đây là số đã khử trùng lặp.

## Kết luận

- Mate không "hết context" vì nói nhiều.
  Nó trả tiền cho một context ngày càng dày ở mọi lần gọi model, và trong 3 ngày chưa lần nào được dọn.
  Context hiện là 437K token, chưa compact lần nào (cửa sổ 1M); trung bình mỗi lần gọi mang 248K token; 268 lần gọi cộng lại 66,5M token đầu vào, 98% là cache read.
  Riêng hôm nay 133 lần gọi tốn 44,7M, gấp gần ba lần hôm qua (16,6M), vì context đã qua 400K và crew bắt đầu đánh thức Mate 14 lần.
- 87K token đầu tiên của mọi lần gọi là phần cố định, và khoảng 50K trong đó không phải của mate.
  Đó là `~/.claude/CLAUDE.md` và `rules/` của captain, danh sách ~100 skill, ~30 agent, MCP (fusion360 178 tool), hook SessionStart của các plugin, và định nghĩa những tool Mate không bao giờ dùng (riêng `Artifact` 54 KB).
  Đo bằng `claude -p`: chỉ thêm `--setting-sources project --strict-mcp-config` giảm context lượt đầu từ 53K xuống 28K; cắt thêm 8 tool xuống 19K.
- Phần tăng 352K token trong 3 ngày: 55-60% là nội dung nhìn thấy (report scout, `mate diff`, brief và script python trong tool input, câu trả lời cho captain), 5% là 12 ảnh captain dán vào, phần còn lại 130-160K không có trong transcript.
  Trong phần đó, thinking được Opus 5.5 giữ lại giữa các lượt ước 85-97K (output tổng 176K, phần nhìn thấy 77-90K); phần dư là nhắc nhở của harness và sai số tokenizer với tiếng Việt.
- Hai việc của Mate không tốn đều, nhưng trả cùng một giá.
  14 lượt digest hôm nay chiếm 57 trong 268 lần gọi (21%), nhưng nội dung của crew (report, diff, peek, hand-back) chiếm khoảng 75% byte tool result nằm trong context, và mỗi lần crew đánh thức Mate phải trả toàn bộ context của cuộc hội thoại với captain: 57 lần gọi × ~385K ≈ 22M token cache read cho 14 digest, ≈ $4,4, tức ≈ $0,3 cho một lượt review và merge.
- Hai lỗi vận hành astra tìm ra, đã kiểm trong code và workspace: manual của Mate chỉ được sinh lại lúc start, nên Mate chạy từ 25/9 vẫn đọc bản dạy vòng `sleep 20` mà M14 đã bỏ; nút Restart của console gọi `StartMate` với `Resume: true` nên restart không làm context nhỏ đi, dù CLI đã có `mate mate start --fresh`.
- Đề xuất theo thứ tự: (0) hôm nay restart fresh có stow để về ~90K và nạp manual M14, không cần code; (A) cô lập cấu hình và cắt tool bằng flag launch; (B) đo context của Mate trên console, restart fresh có stow ở ranh giới công việc khi vượt ngưỡng; (C) rút manual 82 KB thành lõi cộng skill; (D) đưa report và diff ra khỏi context Mate, review phải độc lập; (E) tách handler digest thành lượt riêng do runtime Go gói context, chỉ khi digest chiếm đa số lượt.
  Ước tính A+B đưa mức hôm nay (44,7M/ngày) về khoảng 8-10M; thêm C+D về 6-7M.

## 1. Số đo

### 1.1 Phiên

| Chỉ số | Giá trị |
| --- | --- |
| Prompt | 82: 68 của captain, 14 digest của app (M14 bật auto trở lại hôm nay; trước đó Mate tự poll crew bằng `for … sleep 20` trong lượt của captain, 6 lần) |
| Lần gọi model | 268 (431 dòng `assistant`, 163 dòng trùng `message.id`), trung bình 3,3 mỗi prompt |
| Context lượt đầu / cuối / trung bình | 86.653 / 436.969 / 248.219 token |
| Compact | 0 lần |
| Cache read / cache write / uncached / output | 65,2M / 1,32M / 536 / 176K token; đầu vào tổng 66,5M |
| Theo ngày | 25/9: 47 lần gọi, 5,2M · 26/9: 88 lần, 16,6M · 27/9: 133 lần, 44,7M |
| Ghi lại toàn bộ cache sau hơn 1 giờ nghỉ | 6 lần, 1,03M token, tức 78% cache write; TTL cache của tài khoản là 1 giờ (`ephemeral_1h_input_tokens`) |
| Quy đổi giá API | ≈ $27 cho 3 ngày: cache read $13, cache write $10,6, output $3,5 |
| Dashboard | `tokens_today` cộng cả cache read; `context_pct` của Mate là `null` |

### 1.2 Phần cố định của mỗi lần gọi (~87K token, ~280 KB chữ)

| Khối | KB | Của ai | Ghi chú |
| --- | --- | --- | --- |
| Định nghĩa tool | 114 | Claude Code + cấu hình captain | `Artifact` 54, `SendFeedback` 9, `Workflow` 9, `ScheduleWakeup` 8, `AskUserQuestion` 7, `Agent` 5,6 (liệt kê ~30 agent của captain); Mate chỉ dùng `Bash`, `Read`, `Skill` |
| `AGENTS.md` của Mate | 82 | mate | mục 4 (hợp đồng lệnh, kèm ví dụ output) 21 KB, mục 10 (hai chế độ) 11 KB, mục 9 7,5 KB, mục 14 kèm ví dụ 7,5 KB, mục 5 5,6 KB |
| `~/.claude/CLAUDE.md` + 10 file `rules/` | 18 | captain | TDD 80%, bảng agent, patterns, hooks; không liên quan Mate và có chỗ mâu thuẫn với manual ("dùng tdd-guide agent", "dùng planner agent") |
| Danh sách skill | 33,5 | captain | ~100 skill toàn cục; 5 skill của Mate chiếm ~2,5 KB |
| MCP tool deferred + hướng dẫn MCP | 13 | captain | fusion360 178 tool, context7, Claude Docs |
| Danh sách agent | 9,5 | captain | ~30 agent tuỳ biến |
| Hook SessionStart của plugin | 7,5 | captain | superpowers 4,6, lavish-axi 2,3 (cắt từ 12), chrome-devtools-axi 0,3, gh-axi 0,3 |
| Digest `mate recall` | 2,7 → tối đa 9,5 | mate | đúng việc |
| System prompt của Claude Code | ~5 | Claude Code | không đổi được |

Của mate ≈ 85 KB (~21K token); kế thừa từ máy captain ≈ 195 KB (~50K token).
Cách đo: lần gọi đầu tiên ghi `cache_creation_input_tokens` 61.194 và `cache_read_input_tokens` 25.457; các khối trên là attachment `instructions`, `skill_listing`, `deferred_tools_*`, `agent_listing_delta`, `hook_success` và `prompt_snapshot.tools` trong transcript.

### 1.3 Thăm dò flag

Ba lượt `claude -p "reply with the single word ok" --model sonnet` trong thư mục tạm có `CLAUDE.md` 52 byte, đọc `prompt_snapshot` và `usage` của lượt đầu trong transcript của chính lượt đó.

| Lệnh | Context lượt đầu | Còn lại trong prompt |
| --- | --- | --- |
| Mặc định | 52.966 | `CLAUDE.md` + `rules/` toàn cục, 33 KB skill, 8,7 KB agent, MCP, hook plugin, 12 tool 78 KB |
| + `--setting-sources project --strict-mcp-config` | 28.477 | chỉ `CLAUDE.md` của cwd, skill 6 KB (bundled), không hook plugin, không MCP |
| + `--disallowedTools Artifact,Workflow,ScheduleWakeup,SendFeedback,ShareOnboardingGuide,ReportFindings,ListAgents,Agent` | 19.187 | 6 tool, 37 KB |

`--settings` của Mate vẫn được nạp (help của Claude ghi rõ `--settings` áp dụng bất kể nguồn), hook của Mate nằm trong đó, skill của Mate ở `mate/.claude/skills/` là project-level nên còn nguyên; đăng nhập không bị ảnh hưởng.
Chế độ interactive còn nạp `Artifact` 54 KB mà `-p` không nạp, nên phần cắt được cho Mate còn lớn hơn số đo này.
`~/.claude/settings.json` của captain có khoá `model`; với `--setting-sources project` Mate không còn thấy nó, nên app phải truyền `--model` tường minh.

### 1.4 Phần tăng: 352K token qua 268 lần gọi

| Nguồn | Byte | Token ước | Chi tiết |
| --- | --- | --- | --- |
| Prompt của captain, digest, tool result | 337 KB | ~95-110K | Tính đến 20:53: `Read` report scout 95 KB (4 lần, 25-32 KB mỗi report); `mate diff` 74 KB (15 lần, một lần 18,8 KB, một lần 9 MB bị persist); `cd … && cat` 21 KB (đọc lại `PROJECT.md`, `backlog.md` dù recall đã dặn không); `mate peek` 13 KB; python 10 KB |
| Câu trả lời và tool input của Mate | 272 KB | ~80-90K | câu trả lời tiếng Việt kể lại report; brief viết bằng heredoc; 15 script python sửa `backlog.md` |
| Ảnh captain dán vào | 12 ảnh | ~18K | |
| Phần không nhìn thấy | | ~130-160K | thinking giữ lại ước 85-97K (output tổng 176K, phần nhìn thấy 77-90K); còn lại là nhắc nhở của harness và sai số tokenizer |

### 1.5 Một lượt digest giá bao nhiêu (14 digest hôm nay, giờ địa phương)

| Digest | Lần gọi | Context |
| --- | --- | --- |
| 20:28 s3 needs-decision | 3 | 311K |
| 20:42 s3 + be1 wait-mate: review, merge, viết 2 brief, spawn | 8 | 349K |
| 20:45 ios5 needs-decision · 20:46 ios5 wait-mate | 3 · 3 | 351K · 352K |
| 20:52 be2 wait-mate: review, merge, brief, spawn | 7 | 367K |
| 20:58 be3 · 21:04 ios6 · 21:06 be4 · 21:09 ios6 | 5 · 2 · 4 · 2 | 376K → 399K |
| 21:16 be5 · 21:19 be5 · 21:21 ios7 needs-decision | 4 · 4 · 3 | 413K → 419K |
| 21:56 ios7 · 22:09 ios7 | 4 · 5 | 426K · 433K |

57 lần gọi × ~385K ≈ 22M token cache read cho 14 digest, ≈ $4,4 theo giá API; cùng những lượt đó ở context 80K tốn một phần năm.

### 1.6 Hai lỗi vận hành (astra tìm, đã kiểm)

- Manual cũ.
  `mate/AGENTS.md` của workspace có mtime 2026-09-25 14:23 (lúc Mate start) và vẫn dạy `sleep 20; mate state` ở dòng 583 cùng câu "auto mode: end your turn now"; template hôm nay (task 57, M14) đã thay bằng dòng `turn: end it now … Do not poll.`.
  Manual chỉ sinh lại lúc start, nên một Mate sống nhiều ngày không nhận được bất kỳ sửa manual nào; transcript có 6 vòng `for … sleep 20`.
- Restart resume.
  `cmd/mate/console_box.go` (hành động Restart) gọi `spawn.StartMate` với `Resume: true` và không đặt `Fresh`, nên Mate quay lại đúng session 437K.
  CLI đã có `mate mate start <project> --fresh` và `mate mate stop <project>` có stow (`--no-stow` để bỏ), tức đường "stow → fresh → recall" tồn tại nhưng console không dùng.

## 2. Phương án

### 0. Hôm nay, không cần code

`mate mate stop hellovietnam` (stow trước, chờ hết lượt) rồi `mate mate start hellovietnam --fresh`.
Context về ~90K, manual M14 được nạp, crew đang chạy không bị ảnh hưởng vì recall phần 1 liệt kê lại crew sống, inbox và outbox.
Đây cũng là bài đo cho B2: xem stow có giữ đủ năm thứ phải sống qua phiên không (mục tiêu, quyết định kèm lý do, việc đang làm, câu hỏi chờ captain, nguồn bằng chứng).

### A. Cắt phần cố định (không đổi kiến trúc, đo được ngay)

- A1. Launch Mate Claude với `--setting-sources project --strict-mcp-config`, và truyền `--model` tường minh.
  Mate hết thấy CLAUDE.md, rules, skill, agent, MCP và hook của máy captain.
  Hook, `autoMemoryEnabled: false` và skill của Mate không đổi vì đi qua `--settings` và thư mục `mate/.claude/`.
- A2. `--disallowedTools` cho các tool Mate không dùng: `Artifact`, `Workflow`, `ScheduleWakeup`, `SendFeedback`, `ShareOnboardingGuide`, `ReportFindings`, `ListAgents`, và `AskUserQuestion` (Mate hỏi captain trong pane, không qua dialog).
  Giữ `Agent` nếu chọn D1.
  Cần đo lại trong phiên interactive: transcript ghi `prompt_snapshot.tools`, nên kiểm được ngay sau khi launch.
- A3. Rút manual 82 KB thành lõi ≤ 25 KB.
  Giữ mục 1, 3, 5, 6, 8, 13 (danh tính, bootstrap, intake, brief, giao thức status, escalation).
  Mục 4 (hợp đồng lệnh) chuyển sang `mate <lệnh> --help` và một skill `mate-cli`; mục 10 (hai chế độ) và 9 (giám sát, review) thành skill nạp khi có `digest:`/`resolve:`; bỏ các ví dụ output dài vì binary tự in ra đúng thứ đó khi chạy.
  Claude Code chỉ đưa mô tả skill vào prompt, nội dung đầy đủ nạp khi gọi, đúng nghĩa progressive disclosure.
  Đúng nguyên tắc đã chốt: luật thành hàm thì ở binary, prompt chỉ cho phán đoán.

Ước tính: 87K → ~33K token mỗi lần gọi; với 130 lần gọi mỗi ngày là bớt ~7M token/ngày.

### B. Giới hạn context làm việc, giữ danh tính và trí nhớ dài hạn

Ý chính lấy từ astra: Mate là danh tính và trí nhớ lâu dài, nhưng context làm việc phải có ranh giới theo công việc; hội thoại là bộ đệm, mọi thứ đáng giữ đã có chủ trong file (M8).

- B1. Console đo context của Mate từ transcript (lần gọi cuối: `input + cache_creation + cache_read`, khử trùng `message.id`) và hiện ở ô MODE, inspector của hàng Mate, dashboard (`context_pct` đang `null`) và cột CTX% của `mate usage`.
  Không đo thì không biết phương án nào có tác dụng, và ngưỡng ở B2 phải đo mới đặt được.
- B2. Restart fresh có stow ở ranh giới công việc.
  Điều kiện, tất cả cùng đúng: context vượt ngưỡng (đo để chọn; dải làm việc mục tiêu 60-100K theo astra, ước tính của tôi ~80K), Mate rảnh (dòng Stop trong `sent.log`), captain im lặng đủ `QuietAfter`, outbox trống, và `outbox.Stow` trả `stowed`.
  `not stowed: <lý do>` thì không restart, hiện lý do trên console, thử lại ở ranh giới sau.
  Restart dùng `Fresh: true`; hook SessionStart in `recall`.
  Nút Restart của console cũng đổi: sau `stowed` thì fresh là mặc định, resume chỉ khi stow thất bại và captain xác nhận.
  Đường này là task 37 và acceptance M8 (`TestLiveMemorySurvivesRestart`, đã có subtest `claude-fresh`), chỉ thêm điều kiện kích hoạt và đổi mặc định của nút.
- B3. Lưới an toàn: `--autocompact 300000` để Claude tự compact nếu vì lý do gì restart không xảy ra; hook `compact` đã in lại `recall`.
- B4. Báo manual cũ: console so bản `AGENTS.md` trên đĩa với bản render từ template của binary đang chạy, lệch thì ghi `manual outdated` ở hàng Mate; restart fresh ở B2 tự xoá cảnh báo.

Ước tính: context trung bình 248K (và tăng ~90K/ngày) → ~80K; đây là đòn bẩy lớn nhất vì nhân với mọi lần gọi và với các lần ghi lại cache sau khi nghỉ.

### C. Bớt thứ vào context ở mỗi lượt; review phải độc lập

- C1. Review ship qua reviewer riêng: `mate review <project> <crew>` chạy một lượt `claude -p` hoặc `codex exec` với context mới gồm brief, hand-back, diff và kết quả kiểm tra, trả về ≤ 15 dòng: kết luận, vấn đề cần quyết, đường dẫn bằng chứng.
  Mate vẫn quyết phần sản phẩm; hand-back của chính crew không thay được kiểm chứng (điểm astra nhấn, đúng).
  Diff không đi qua context Mate nên không còn xuất hiện trong giá của mọi lượt sau; khớp hơn với luật "Mate không đọc code".
- C2. `mate diff` mặc định in `--stat` và bảng hand-back; diff đầy đủ theo từng file khi Mate cần nhìn một chỗ cụ thể.
- C3. Report scout: template bắt buộc `## Summary` ≤ 20 dòng bằng ngôn ngữ của captain; `mate report <project> <crew> [--full]` in summary; Mate đọc summary rồi kể lại, captain mở file đầy đủ trong cột review (M13).
  Với scout, report chính là deliverable nên summary của crew là đủ để Mate chuyển tiếp; khác với hand-back của ship.
- C4. `mate backlog add|move|done` thay 15 script python; `mate peek` mặc định 25 dòng.

Ước tính: bớt 40-50% phần tăng nhìn thấy; giá trị lớn hơn khi repo lớn dần và diff dài ra.

### D. Tách lượt điều phối theo công việc

- D1 (rẻ, thử trước).
  Manual dặn Mate xử lý mỗi `digest:` bằng một sub-agent (`Agent` tool của Claude Code) với prompt gồm dòng digest và đường dẫn brief, hand-back, status, skill `decision-authority`.
  Sub-agent có context riêng ~30K; Mate chỉ nhận về ≤ 20 dòng kết luận và tự quyết phần thuộc captain.
  Đo bằng sidechain trong transcript: token mỗi digest trước và sau.
- D2 (kiến trúc, theo cách astra mô tả).
  Runtime Go lo theo dõi trạng thái, gom sự kiện, retry, chống gửi trùng và kiểm quyền (outbox và autopilot đã làm phần lớn).
  Khi cần phán đoán, runtime gọi model một lượt với gói context liên quan: brief, hand-back, status, `decision-authority`, bảng dispatch.
  Handler trả lời `needs-decision` trong phạm vi brief, chạy C1 và merge khi `yolo`, xong thì kết thúc; chỉ kết luận hoặc câu hỏi cần captain mới về Mate.
  Mate chỉ còn nói chuyện với captain; không cần nhiều agent thường trực.
  Làm khi digest chiếm đa số lượt, tức giai đoạn build nhiều crew mà captain nói ít; hôm nay digest là 21% số lần gọi và đang tăng.

### E. Model và effort (captain quyết)

- Mate đang chạy Opus 5.5, effort `medium` (transcript ghi 366 lần).
  Thinking được giữ lại nên effort ảnh hưởng cả output lẫn context, nhưng không nên đổi effort giữa phiên vì làm mất cache của toàn bộ hội thoại.
- Sonnet 5 rẻ hơn hai lần cho cùng token, nhưng phán đoán là toàn bộ việc của Mate.
- Mate trên Codex là lựa chọn về quota (tuần này còn 91%), không phải về hiệu quả token.

## 3. Thứ tự và tiêu chí xong

| # | Việc | Xong khi |
| --- | --- | --- |
| 0 | Hôm nay: `mate mate stop hellovietnam` rồi `mate mate start hellovietnam --fresh` | Recall in đủ crew sống; Mate nêu lại câu hỏi trong `Held for the captain`; context lượt đầu ~90K; không còn vòng `sleep 20` |
| 1 | A1 + A2: flag launch cho Mate Claude, `--model` tường minh | Transcript lượt đầu: `instructions` chỉ có `AGENTS.md`, không hook plugin, không MCP; context lượt đầu < 45K; live suite xanh |
| 2 | B1: đo context của Mate | Console, dashboard và `mate usage` hiện CTX% của Mate, khử trùng `message.id` |
| 3 | B2 + B3 + B4: nút Restart fresh sau stow; restart theo ngưỡng ở ranh giới công việc; autocompact làm lưới; báo manual cũ | Live: Mate ở 160K, captain im 5 phút, stow `stowed` → fresh → recall; câu hỏi trong `Held for the captain` còn nguyên; `not stowed` thì không restart và console nói lý do |
| 4 | A3: manual gọn | Manual ≤ 25 KB, golden và live suite xanh, qua lại acceptance M7/M8/M14 |
| 5 | C1-C4: `mate review` độc lập, `diff --stat` mặc định, report summary, `mate backlog` lệnh sửa | Một ship được review mà diff không xuất hiện trong transcript của Mate; token mỗi digest giảm đo được |
| 6 | D1 thử, rồi quyết D2 | Digest đi qua sub-agent; so token mỗi digest trước và sau; D2 chỉ khi digest > 50% số lần gọi |

## 4. Đối chiếu với phân tích của astra

| Điểm | Astra | Bản này | Dùng |
| --- | --- | --- | --- |
| Context 87K → 419K, chưa compact; 98% cache read; dashboard cộng cả cache | Đúng | Khớp (437K lúc 22:28) | Cùng kết luận |
| 256 lần gọi, 61,4M | Đúng | Bản đầu đếm dòng ra 358; đếm theo `message.id` là 268, 66,5M | Sửa số của bản này |
| Manual workspace còn dạy poll 20 giây | Đúng, kiểm dòng 583 | Chưa thấy | Thêm 1.6, B4, việc 0 |
| Nút Restart resume phiên cũ | Đúng, `console_box.go` | Chưa thấy | Thêm 1.6, B2 đổi mặc định của nút |
| Giới hạn context làm việc 60-100K, refresh ở ranh giới công việc, chỉ khi stow xong | Đề xuất | Bản đầu chỉ có ngưỡng token | B2 gộp cả ba điều kiện |
| Review độc lập, hand-back của crew không thay kiểm chứng | Đề xuất | Bản đầu để `diff --stat` trước reviewer | C1 lên đầu, C2 phụ |
| Tách lượt theo công việc, runtime Go gói context, chưa cần nhiều agent thường trực | Đề xuất | D2 cùng hướng | Lấy cách mô tả của astra |
| Flag `--setting-sources`, `--disallowedTools`, đo bằng `claude -p` | Không nêu | Đo được, 53K → 19K | Giữ A1, A2 |
| Tool definition 114 KB, `Artifact` 54 KB | Không nêu | Đo từ `prompt_snapshot` | Giữ A2 |
| Thinking giữ lại là phần lớn của 130-160K không nhìn thấy | Không nêu | Suy từ hiệu số | Giữ ở 1.4, E |
| Giá từng digest | Không nêu | 1.5 | Giữ |

## 5. Rủi ro và câu hỏi mở

- `--setting-sources project` bỏ hook, permission và `model` toàn cục: Mate không dựa vào hook nào ngoài `--settings`; model phải truyền tường minh; đăng nhập đã thử không bị ảnh hưởng.
- Restart fresh làm mất phần hội thoại chưa được stow: skill `stow` đã qua acceptance M8, nhưng ngưỡng và tần suất phải đo trên project thật; điều kiện `stowed` là chốt an toàn.
- Manual gọn hơn có thể làm Mate quên luật: chạy lại live suite và acceptance M7, M8, M14.
- Thinking không tắt được trên Opus 5.5, chỉ chỉnh effort; phần 130-160K là suy luận từ hiệu số vì thinking bị ẩn trong transcript.
- Reviewer riêng tốn thêm một lượt model cho mỗi ship; rẻ hơn nhiều so với diff nằm lại trong context Mate, nhưng phải đo.
- Crew ngoài phạm vi nhưng cùng bệnh: 15 crew tốn 36M token trên Codex, riêng `ios4` 17,8M trong 28 phút (176 lần gọi).
