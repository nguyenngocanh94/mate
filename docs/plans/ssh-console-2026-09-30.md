# Phương án dùng Mate qua SSH

- Ngày: 2026-09-30.
- Trạng thái: đề xuất; chưa sửa production code.
- Baseline: `5d93926` cộng phần tmux/SSH chưa commit (`internal/host/tmux.go`, `sshTmuxCommand` trong `cmd/mate/console.go`), binary `bin/mate` build 2026-09-29 10:25.
- Máy: Mac mini chạy Mate, Herdr 0.8.2, tmux 3.6a với oh-my-tmux; laptop macOS chạy Ghostty; hai máy nối qua Tailscale.
- Spec liên quan: [MVP](../mvp.md) mục cột host (M13), dòng về phiên SSH.

## 1. Bối cảnh

Captain dùng Mac mini làm máy chính.
Khi không ở nhà, captain SSH từ laptop vào Mac mini, `cd` vào workspace rồi chạy `mate console`.
Trải nghiệm qua SSH tệ và không giống khi ngồi trước Mac mini.

## 2. SSH làm gì và không làm gì

SSH là một ống chở byte hai chiều, không truyền "view" và không map phím.
Chương trình trên Mac mini ghi chữ và mã điều khiển vào PTY; `sshd` gửi qua mạng; `ssh` trên laptop ghi nguyên văn vào Ghostty; Ghostty vẽ pixel.
Chiều ngược lại, Ghostty mã hoá phím thành byte (`\r`, `ESC [ A`, ...) và byte đó tới thẳng chương trình.

Chuỗi hiển thị agent qua SSH hiện nay:

```
Claude ─► Herdr server ─► herdr attach ─► tmux ─► sshd ══ mạng ══ ssh ─► Ghostty (laptop)
          (terminal ảo 1)                (terminal ảo 2)
```

Tại chỗ trên Mac mini:

```
Claude ─► Herdr server ─► herdr attach ─► Ghostty (Mac mini)
```

Mỗi terminal ảo ở giữa đọc byte, dựng lại màn hình trong bộ nhớ, rồi tự vẽ lại ra ngoài.
Khả năng nào của terminal ngoài mà tầng giữa không hiểu hoặc không chuyển tiếp thì mất.

## 3. Đo đạc

Đo trên Mac mini bằng script PTY trong scratchpad, cửa sổ 200x50.

| Phép đo | Kết quả |
| --- | --- |
| Echo phím, chương trình chạy thẳng | p50 0.1 ms, p90 0.1 ms |
| Echo phím qua `herdr terminal attach` | p50 2.1 ms, p90 3.8 ms |
| Echo phím qua tmux (`~/.tmux.conf`) rồi `herdr terminal attach` | p50 3.5 ms, p90 4.3 ms |
| Byte ra khi pane chạy spinner 12 Hz giống Claude, qua tmux rồi Herdr | khoảng 1.4 KB/s |
| Byte ra khi console đứng yên trong tmux | khoảng 12 KB trong 20 giây sau lần vẽ đầu |
| `mate console` với `TERM=xterm-ghostty`, không `TERMINFO`, có `SSH_TTY` | thoát ngay: `missing or unsuitable terminal: xterm-ghostty` rồi `mate: remote tmux console: exit status 1` |
| Frame có `ESC[?2026h` khi chương trình vẽ trọn 50 frame, chạy thẳng trong tmux, client `xterm-ghostty` có terminfo | 51 |
| Như trên, client `xterm-256color` | 0 |
| Herdr attach chạy thẳng, với mọi `TERM` đã thử (`xterm-ghostty`, `xterm-256color`, `screen-256color`, `tmux-256color`) | 38–49 |
| tmux rồi Herdr attach, client `xterm-ghostty` có terminfo | 4 |

Đo từ laptop (Claude trên laptop chạy, captain chuyển kết quả lại), lúc đó laptop ở cùng LAN nhà:

| Phép đo | Kết quả |
| --- | --- |
| `tailscale ping macmini` | nối thẳng qua `192.168.100.186:41641`, không qua DERP, 5–13 ms |
| `ping` | p50 9.7 ms, p90 16.9 ms, mất 0% gói; chặng Wi-Fi tới router đã 5–14 ms |
| Echo phím `ssh -tt macmini cat` | p50 10–13 ms, p90 17 ms |
| Echo phím qua thêm tmux | p50 8–10 ms, p90 18–27 ms; tmux không thêm độ trễ đo được |
| Cấu hình SSH | không compression, không ControlMaster, không ProxyCommand; laptop không có mosh |
| `TERM` laptop gửi sang | `xterm-ghostty`; Mac mini không tìm thấy terminfo cho tên này khi đăng nhập qua SSH |

Chưa đo khi laptop ở ngoài nhà.
Khi đó Tailscale có thể không nối thẳng được (NAT của laptop là loại khó), và độ trễ mỗi phím sẽ cao hơn.

## 4. Nguyên nhân

Không phải mạng trong LAN, không phải băng thông, không phải độ trễ do Mate, tmux hay Herdr.
Khác biệt nằm ở tầng tmux thêm vào giữa:

1. **Không mở được.**
   Ghostty tại chỗ tự đặt `TERMINFO=/Applications/Ghostty.app/Contents/Resources/terminfo`, phiên SSH thì không, nên tmux từ chối `xterm-ghostty`.
   Với binary hiện tại, đường SSH từ Ghostty hỏng hẳn.
2. **Mất vẽ trọn frame.**
   Herdr luôn đánh dấu frame bằng `?2026`, nhưng tmux chỉ chuyển ra ngoài 4 trên khoảng 50 frame, hoặc 0 khi không nhận ra `TERM`.
   Khi Claude stream, Ghostty trên laptop có thể vẽ nửa frame và nhìn như giật.
   Mới đo số frame được đánh dấu, chưa quan sát trực tiếp trên màn hình laptop.
3. **Chuột và phím.**
   `~/.tmux.conf` tắt `mouse`, nên không cuộn được.
   oh-my-tmux chỉ bật `extended-keys` với iTerm và mintty, nên Shift+Enter và các tổ hợp Ctrl+Shift không tới Claude.
   `Ctrl-b` bị tmux giữ, và driver tmux bắt captain chuyển window bằng `Ctrl-b p`/`Ctrl-b w`.
   Captain đã bỏ tổ hợp có prefix khỏi console từ 2026-09-17, vì một prefix gõ lạc rơi vào composer của Mate có thể chọn nhầm lựa chọn phá hỏng; console dùng vùng focus và chuột.
4. **Không có cột bên cạnh.**
   Driver Ghostty mở cột bằng AppleScript, chỉ làm được với Ghostty chạy trên cùng máy với `mate`.
   Qua SSH, Ghostty nằm trên laptop, nên driver tmux phải dùng window full-width và chuyển bằng `Ctrl-b`.

## 5. Hướng giải quyết

### Bước 1: sửa đường tmux

Đường tmux vẫn là fallback lâu dài cho terminal không có driver (iPad, Linux) và cho khi cần giữ console qua lần rớt mạng.

- Trước khi mở tmux, `sshTmuxCommand` kiểm tra `TERM` có terminfo trên máy không.
  Nếu không có và `TERM` là `xterm-ghostty`/`ghostty`, trỏ `TERMINFO` sang terminfo trong Ghostty.app khi nó có sẵn.
  Nếu vẫn không có, dùng `xterm-256color` và khai báo `terminal-features` `RGB` và `sync` cho client đó.
- Session tmux do Mate mở bật `mouse` và `extended-keys` ở mức session, không đụng cấu hình tmux chung của captain.
- Phía laptop, captain có thể thêm `shell-integration-features = ssh-terminfo` vào cấu hình Ghostty để Ghostty tự cài terminfo sang máy đích.
  Mate không dựa vào việc này.

Bước này sửa lỗi 1 và 3.
Nó không sửa được lỗi 2: dù tmux nhận đúng `xterm-ghostty`, chỉ 4 trên khoảng 50 frame của Herdr còn được đánh dấu.
Nó cũng không sửa lỗi 4.

Xong khi: `mate console` qua SSH với `TERM=xterm-ghostty` mở được console; cuộn chuột và Shift+Enter tới Claude trong window agent; unit cho việc chọn `TERM`/`TERMINFO`; `make check` xanh.

### Bước 2: `mate remote`

Cột agent và cột review nằm thẳng trong Ghostty trên laptop, còn console, pane runner, Herdr và agent vẫn ở Mac mini.

```
Laptop (Ghostty)                         Mac mini
┌──────────────┬───────────┬────────┐
│ ssh → mate   │ ssh → mate│ssh →   │    console, pane runners,
│ console      │ pane serve│pane    │ ←→ Herdr, Claude/Codex, file state
│              │ (agent)   │(review)│    (như hiện tại)
└──────────────┴───────────┴────────┘
      ▲ mate host serve (laptop)  ◄── ssh -R unix socket ── host driver "remote"
```

Kiến trúc hiện tại đã tách phần dàn cột khỏi phần chạy thật.
Mỗi cột chỉ chạy `mate pane serve --role … --socket … --owner …`, và console điều khiển cột qua unix socket (`cmd/mate/console_stage.go`).
Chỉ bước dàn cột phải chạy trên laptop.

1. Trên laptop, `mate remote <host> <workspace-dir>` mở socket `mate host serve`, forward nó sang Mac mini bằng `ssh -R`, rồi chạy `mate console <workspace-dir>` bên kia trong pane hiện tại.
2. Console trên Mac mini thấy socket forward về thì chọn host driver `remote`.
   Driver này implement `host.Host` (`Layout`, `Close`) bằng cách gửi lệnh ngược về laptop.
3. Laptop nhận `Layout` và dùng driver Ghostty/WezTerm hiện có, với argv mỗi cột bọc thành `ssh -t <host> mate pane serve …`.
   Socket và pid owner đều nằm trên Mac mini nên giữ nguyên cách hoạt động.
4. `mate remote` bật `ControlMaster` cho mọi kết nối của nó, nên các cột đi chung một kết nối TCP và chỉ xác thực một lần.

Được:

- Cột nằm cạnh nhau trong Ghostty như tại chỗ.
- Chuỗi hiển thị agent là Ghostty ← ssh ← Herdr attach, không có tmux, nên giữ được vẽ trọn frame, không cần xử lý terminfo, chuột, màu và phím như tại chỗ.
- Độ trễ chỉ còn vòng đi–về của mạng.

Giá:

- Laptop cần binary `mate` và Ghostty hoặc WezTerm; không cần Herdr hay agent.
- Rớt kết nối thì mất các pane trên laptop và console thoát.
  Agent không chết: Herdr server, process harness, chữ gõ dở trong composer và state `.mate/` đều ở Mac mini.
  Pane runner thấy pid owner mất thì chỉ dừng client `herdr agent attach`.
  Nối lại bằng cách chạy lại `mate remote`; có thể cho `mate remote` tự nối lại, đóng pane chết và dàn lại cột.
- Khác với tmux, console không sống qua lần rớt mạng.

Xong khi: live test qua `ssh localhost` dựng đủ cột, Enter attach đúng agent, ctrl+c đóng hết cột trên laptop và runner trên máy chủ; rớt kết nối giữa chừng không làm agent nào dừng.

## 6. Hướng đã cân nhắc và không chọn

- **Bỏ Herdr, resume theo session id của harness.**
  Session id khôi phục hội thoại, không giữ process đang chạy.
  Harness interactive cần một PTY sống do một bên khác cửa sổ đang xem giữ; crew làm việc khi không ai xem, Mate được đánh thức bằng cách gõ vào composer, observer đọc màn hình.
  Không có Herdr thì đóng cửa sổ là harness chết giữa turn và mất lệnh tool đang dở.
  Tự giữ PTY trong Mate là viết lại một Herdr thu nhỏ.
- **Console chạy trên laptop, đọc state từ xa.**
  Màn hình agent vẫn phải stream dưới dạng terminal, nên không giảm lag; phải thêm API đọc state và gọi action từ xa.
- **Harness headless** (`claude -p --resume`, `codex exec resume`).
  Bỏ được Herdr, lớp vỏ, việc đọc màn hình và cả vấn đề SSH, nhưng mất TUI gốc của harness và đảo ngược quyết định 2026-09-17 ("Mate là harness interactive trong pane Herdr, không headless").
  Đây là quyết định sản phẩm riêng; nếu cân nhắc thì làm spike với một crew thật trước.

## 7. Câu hỏi mở

- Lệch phiên bản giữa `mate` trên laptop và trên Mac mini: giao thức `host serve` cần số phiên bản và lời từ chối rõ ràng.
- Forward unix socket bằng `ssh -R` gặp file socket cũ thì bind thất bại nếu `sshd` không có `StreamLocalBindUnlink yes`; dùng đường dẫn socket riêng cho mỗi lần chạy để không phụ thuộc cấu hình server.
- Ghostty trên laptop cần quyền Automation cho AppleScript lần đầu.
- Ở ngoài nhà, độ trễ mỗi phím phụ thuộc Tailscale có nối thẳng được không.
  Cả hai bước đều không giảm được vòng đi–về; mosh giảm được nhưng không dùng chung được với nhiều kết nối SSH theo cột.
  Cần đo lại khi laptop thật sự ở ngoài.
