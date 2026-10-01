# Phương án dùng Mate qua SSH

- Ngày: 2026-09-30.
- Trạng thái: đã chốt hướng ngày 2026-09-30 (mục 5); chưa sửa production code.
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

Chuột với Herdr, không có tmux.
Trên Mac mini đo bằng PTY giả phiên SSH (`TERM=xterm-ghostty` không có terminfo, có `SSH_TTY`) và chuỗi chuột SGR như Ghostty gửi.
Trên laptop đo qua `ssh -tt macmini` thật, cả tự động lẫn captain thử tay trong Ghostty, laptop ở nhà:

| Phép đo | Mac mini | Laptop |
| --- | --- | --- |
| Client `herdr --session` bật chế độ chuột | 1000/1002/1003/1006; mở được dù thiếu terminfo `xterm-ghostty` | như Mac mini |
| Click chuyển focus giữa hai pane cạnh nhau | được | được; click tới lúc focus đổi p50 khoảng 27 ms, p90 khoảng 36 ms |
| Click và lăn chuột tới chương trình trong pane có xin nhận chuột | tới đủ, tọa độ đổi sang tọa độ pane | như Mac mini |
| Lăn chuột ở pane shell (`seq 1 500`) | chưa đo | cuộn về dòng cũ, không có chữ rác |
| Kéo chọn chữ rồi copy sang laptop | chưa đo | được, mượt như tại chỗ |
| Claude trong pane: lăn chuột, click, Shift+Enter | chưa đo | lăn chuột và click được; Shift+Enter xuống dòng |
| `herdr terminal attach --takeover`, chạy thẳng hoặc lồng trong pane Herdr | lăn chuột qua được, click bị nuốt | như Mac mini |
| Lăn chuột qua attach vào shell không xin nhận chuột | không cuộn; chuỗi chuột thô rơi vào prompt | không cuộn; chữ rác chỉ hiện khi chuỗi bị cắt làm hai lần ghi |
| Lăn chuột và click qua `agent attach` vào Claude | composer không bị gõ rác | chưa đo riêng |
| `herdr --remote macmini` | chưa đo | chưa đo, laptop chưa có Herdr |

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
   `Ctrl-b` bị tmux giữ, và driver tmux bắt captain chuyển window bằng prefix.
   Enter trên agent chạy `select-window` sang window full-width của agent, console biến khỏi màn hình; Esc rơi vào Claude.
   Với oh-my-tmux, `Ctrl-b p` là `paste-buffer`, nên đường về thật là `Ctrl-b Tab` hoặc `Ctrl-b 1`, và captain không tìm ra khi dùng thử.
   Captain đã bỏ tổ hợp có prefix khỏi console từ 2026-09-17, vì một prefix gõ lạc rơi vào composer của Mate có thể chọn nhầm lựa chọn phá hỏng; console dùng vùng focus và chuột.
4. **Không có cột bên cạnh.**
   Driver Ghostty mở cột bằng AppleScript, chỉ làm được với Ghostty chạy trên cùng máy với `mate`.
   Qua SSH, Ghostty nằm trên laptop, nên driver tmux phải dùng window full-width và chuyển bằng `Ctrl-b`.

## 5. Quyết định

Chốt 2026-09-30:

- Bỏ đường tmux.
  Driver `tmux` và `sshTmuxCommand` trong phần chưa commit không vào `main`; Mate không cần tmux.
- Chưa làm `mate remote`.
- Qua SSH, Herdr dàn cột: console Mate là một pane trong Herdr session của workspace, agent nằm ở pane bên cạnh.
  Captain chỉ cần `ssh macmini` rồi mở console; Ghostty trên laptop vẽ client Herdr.

Lý do:

- Herdr vốn bắt buộc.
  Client `herdr --session` qua SSH lo chuột, cuộn, chọn chữ và phím mở rộng, đo ở mục 3.
- Không còn tầng tmux, nên hết lỗi terminfo, `mouse off` của oh-my-tmux và việc bắt dùng `Ctrl-b`.
- Session Herdr sống trên server.
  Rớt SSH thì mở lại client là thấy lại console và agent, như `tmux new-session -A` trước đây.

Ràng buộc:

- Cột agent phải là pane thật của agent trong session, không phải `herdr terminal attach` hay `agent attach`.
  Client attach nuốt click và không cuộn được shell (mục 3).
- Chuyển giữa console và agent bằng click.
  Console không thêm tổ hợp prefix (quyết định 2026-09-17); prefix `ctrl+b` mặc định của Herdr vẫn có nhưng console không dựa vào nó.
- Enter trên agent để focus ở lại console, giống driver WezTerm và Ghostty.

Xong khi: `ssh macmini` rồi `mate console` mở console trong Herdr; Enter trên agent hiện pane thật của agent bên phải, focus ở lại console; click chuyển qua lại; lăn chuột cuộn Claude; Shift+Enter xuống dòng trong Claude; live test qua PTY giả SSH; `make check` xanh.

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
- **Sửa đường tmux** (đề xuất ban đầu của bản này).
  Kiểm `TERM`/`TERMINFO`, bật `mouse` và `extended-keys` cho session do Mate mở.
  Sửa được terminfo, chuột và phím, nhưng vẫn là một tầng terminal ảo làm mất đánh dấu frame `?2026`, và driver dùng window full-width nên vẫn phải chuyển window.
  Herdr làm được cùng việc mà không thêm phụ thuộc.
- **`mate remote`**: laptop chạy `mate host serve`, forward socket bằng `ssh -R`, và dàn cột Ghostty trên laptop với mỗi cột là `ssh -t macmini mate pane serve …`.
  Bỏ được mọi tầng giữa, nhưng cần binary `mate` trên laptop, một giao thức có phiên bản, và console chết khi rớt mạng.
  Để sau, nếu dùng Herdr qua SSH vẫn thấy giật.

## 7. Câu hỏi mở

- Cách đưa pane agent ra cạnh console.
  `herdr pane move` có giữ pane id, terminal id và tên agent không; runtime Mate theo dõi agent theo pane id nên phải biết trước khi dùng.
  Cách khác là đặt console ngay trong tab của agent.
- Console nằm trong session Herdr nào: session của workspace (`mate-<hash>`), cùng chỗ với agent, hay một session riêng.
- Cột agent tại chỗ trong WezTerm/Ghostty cũng đi qua `agent attach`, nên cũng mất click và cuộn như mục 3; sửa cùng lúc hay tách việc.
- Chưa có cảm nhận giật khi Claude stream qua client Herdr, và chưa đo client Herdr có giữ `?2026` không.
- `herdr --remote` chưa đo vì laptop chưa có Herdr.
- Chưa đo khi laptop ở ngoài nhà; độ trễ mỗi phím phụ thuộc Tailscale có nối thẳng được không.
