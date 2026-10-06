# Console: "20% terminal butler"

Nguồn: bundle thiết kế `mate · 20 terminal butler.html` (captain gửi 2026-09-25), 11 board A–K và bảng spec I.
Bản này là chữ của các board, đủ để code theo mà không cần mở bundle.
Mỗi board có một golden cùng tên trong `internal/ui/console/testdata/golden/design-*.txt`, vẽ từ `designTree()`.

## Vai trò

mate là pane bên trái, khoảng 20% cửa sổ, rộng 32–48 cột.
Bên phải là cột agent của host (WezTerm, Ghostty), dựng lúc mở console (M13): Enter trên hàng Mate/Crew cho nó chạy `herdr agent attach <agent>`.
Phím `e` trên hàng Crew (kể cả crew đã dừng) mở một tab trong cùng cửa sổ, cạnh tab console, và chạy Fresh trên `report.md` trong folder của crew (`projects/<p>/crews/<id>/`) khi file đó đã có, còn không thì mở chính folder. Đổi crew thì cùng tab đổi file và được chọn lại. Enter không mở và không đóng tab đó. Tab, không phải cột và không phải cửa sổ mới: cột chỉ còn khoảng nửa phần cửa sổ còn lại, còn cửa sổ mới che console.
mate không vẽ terminal của agent, không PTY, không session header.
Không có title bar, chữ "mate console", đường dẫn workspace, đồng hồ, khung cửa sổ.

## Board

| Board | Cảnh | Golden |
| --- | --- | --- |
| A | Workspace, 40×36: list project, peek project đang chọn, box toàn workspace | `design-a-workspace-40x36` |
| B | Project, 40×36: Mate, crew, Completed; detail dưới list | `design-b-project-40x36` |
| C | Detail có focus: ↑↓ đi field, `y` copy | `design-c-detail-focused-40x36` |
| D | Box có focus: item mở đủ câu hỏi, detail co còn 8 dòng | `design-d-box-focused-40x36` |
| E | Project, 40×24: mỗi hàng một dòng | `design-e-project-40x24` |
| F | Sheet actions | `design-f-actions-40x36` |
| G | Sheet confirm stop | `design-g-stop-confirm-40x36` |
| J | Project, 48×48: status lên dòng 1, dòng 2 có agent id và tuổi | `design-j-project-48x48` |
| K | Workspace 40×24 và sheet new project | `design-k-workspace-40x24`, `design-k-new-project-40x24` |
| H1–H4 | Workspace rỗng, đọc lỗi, đang đọc, quá nhỏ | `design-h1-…` … `design-h4-…` |

## Lưới

- 30+ dòng: mỗi hàng hai dòng, có key line; dưới 30: một dòng, status line kết bằng `? keys`.
- Dưới 32×14: màn too-small và không gì khác; dưới 20 cột chỉ một dòng.
- Dưới 20 dòng không đủ chỗ xếp chồng: Tab thay detail vào chỗ list.
- 48 cột: status lên dòng 1 (cột 39–46), dòng 2 thêm agent id và tuổi.
- Hàng: marker cột 0, kind cột 2–3 (luôn đúng 2 cell), tên từ cột 5, `!` ở cột 36 (hai dòng) hoặc 31 (một dòng), status một dòng ở cột 33–39.
- Pane: một chồng list · detail · box, không bao giờ cạnh nhau.
- List cao theo nội dung, luôn giữ hàng đang chọn trong khung, có `↑ N more` / `↓ N more`.
- Sheet (actions, confirm, new project, harness, diff, keys) thay box và phần đuôi detail, không bao giờ che hàng đang chọn.

## Bốn tín hiệu

| Tín hiệu | Vẽ bằng | Khi bỏ màu |
| --- | --- | --- |
| selection | marker một cell ở cột 0 + nền sel cả hàng | marker |
| focus | tiêu đề pane acc + bold; `▌` ở pane có focus, `▏` ở pane khác | bold, `▌` vs `▏` |
| status | một chữ, chỉ tô màu khi cần captain | chữ |
| attention | `!` + số, cột `!` | `!` |

Action không chạy được vẫn nằm đúng chỗ, cột phím là `·`, lý do ở mép phải.

## Token

fg là màu chữ mặc định của terminal (SGR 39), không phải 15, vì theme sáng hay map 15 gần trắng.
dim 7, faint 8, nền sel 8, acc 14 bold, amber 11, red 9, green 10.
Lift rule: trên hàng có nền sel, faint lên dim, dim lên fg.

## Glyph

- Kind: 👨‍💻 Mate, 🤖 Crew. Lúc khởi động `cmd/mate` in 👨‍💻 rồi hỏi CSI 6n một lần (chờ tối đa 150ms); lệch khỏi 2 cell thì dùng ◆ ◇. `MATE_KINDS=emoji|symbol|ascii` ghi đè và bỏ qua probe (`ascii` là @ o).
- Probe đọc cùng tty mà Bubble Tea đọc ngay sau đó: phím gõ trong lúc chờ câu trả lời bị mất. Vì vậy chỉ đo một lần.
- Harness vẽ bằng icon, không bằng chữ (captain chốt 2026-09-25, khác board I): Nerd Fonts 3.5 `cod-claude` U+EC82 và `cod-openai` U+EC81 khi font trên host có hai glyph đó, còn lại ✻ và ⌬; ASCII `*` `#`. Detail giữ chữ cạnh icon.
- `cmd/mate` hỏi chính terminal: Ghostty `+show-face --cp=0xec82`, WezTerm `ls-fonts`. `MATE_ICONS=nerd|unicode` ghi đè. Ghostty 1.3.1 với MesloLGS NF không có hai glyph này (đo 2026-09-25), nên máy đó hiện ✻ ⌬ cho tới khi cài font Nerd 3.5.
- ASCII fallback: `─ -`, `▌ >`, `▏ :`, `▸ +`, `▾ -`, `… ~`, `· .`, `› >`, `→ >`, `↑↓ ^v`, `█ _`, `× x`.

## Phím và chuột

| Phím | Việc |
| --- | --- |
| ↑ ↓ | di chuyển (field trong detail) |
| enter | show in next pane · mở project · bật tắt Completed |
| e | report của crew: tab Fresh trên `report.md` trong folder crew |
| a | sheet actions của hàng đang chọn |
| tab · esc | pane kế · về list |
| n · r · q | new project · refresh (hoặc retry stage lỗi) · quit |
| s · m | start/resume/create Mate · đổi mode |
| y | copy field dưới con trỏ (OSC 52) |
| l | box: cả log / chỉ inbox |
| ? | danh sách phím tới phím kế |

Click hàng như Enter, click rule focus pane đó, click `[assign]` giao cho Mate, wheel cuộn pane dưới con trỏ.
mate không tự vẽ vùng chọn: shift+drag là của terminal.

## Chưa làm vì backend chưa có

Board vẽ vài thứ query chưa cho; frame hiện thứ snapshot có thay vì bịa.

- `last tool_call Bash · pnpm test`: query chỉ có loại event cuối (status/message/incident) và giờ.
- `tokens 182k in · 21k out`: chỉ có tổng.
- `▾ Answered today`: chưa có cờ "đã trả lời".
- `c New crew…`: console không spawn crew, Mate làm việc đó.
- `[assign] to me · snooze 1h`: assign chỉ giao cho Mate.
- Loading từng hàng (H3): snapshot về một lần, nên chỉ có một màn đang đọc.
