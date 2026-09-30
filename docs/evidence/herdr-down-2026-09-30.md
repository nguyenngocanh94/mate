# Herdr chết: console cảnh báo và từ chối attach

Ngày 2026-09-30. Máy Mac mini của captain, Herdr 0.8.2, sau khi restart máy
(server Herdr mất). PR: <https://github.com/nguyenngocanh94/mate/pull/4>.

## Bối cảnh

Captain báo: sau khi máy restart, `mate console` vẫn chạy trên snapshot đọc từ
file, không cảnh báo gì. Nhấn Enter vào Mate thì cột agent chạy
`herdr … agent attach …`, Herdr in `no herdr server is running …` rồi thoát.
Console tưởng pane ổn nên cột giữ nguyên chữ lỗi, và trên phiên SSH (tmux)
người dùng bị kẹt ở terminal đó.

Ba tầng, kiểm lại trong code:

- Observer nuốt lỗi. `pollCrew` (`internal/watch/watch.go`) trả `nil` cho mọi
  lỗi `Handle` - kể cả session Herdr không chạy - và `run()` bỏ lỗi `Poll` trả
  về, nên không có tín hiệu nào tới Console. `query.Load` chỉ đọc file, nên cây
  trên màn hình vẫn trông mới.
- `consoleStage` (`cmd/mate/console_stage.go`) chạy attach mà không hỏi session.
- `panerun` (`internal/panerun/panerun.go`) coi attach thành công ngay khi tiến
  trình con `Start()` không lỗi; chữ Herdr in ra ở lại cột khi nó thoát.

## Đo được

Herdr 0.8.2, cùng máy, lúc không còn server Herdr nào chạy:

- `herdr session list --json` trả exit 0 và mọi session (`mate-7984ac9f`, …)
  với `running: false`. Đây là thứ `runtime.Herdr.LookupSession` đọc, nên
  "session không chạy" là một câu trả lời thành công `(handle, false, nil)`,
  không phải lỗi.
  [session-list.txt](herdr-down-2026-09-30/session-list.txt)
- `herdr agent get --session mate-7984ac9f probe` trả exit 1 và stderr JSON
  `{"error":{"code":"server_not_running","message":"no herdr server is running
  at …; run \`herdr session attach …\` to start or attach it"}}` - đúng dòng lỗi
  captain thấy.
  [agent-get.txt](herdr-down-2026-09-30/agent-get.txt)

## Thay đổi

- Observer giữ một dòng "herdr is not running" (`Watcher.RuntimeNotice`), hỏi
  session một lần mỗi vòng poll qua seam mới `watch.Deps.Session`, độc lập với
  việc có crew mở hay không. `cmd/mate/console_watch.go` wire seam đó vào
  `herdrSession`. Notice tự mất ở vòng poll đầu tiên Herdr trả lời lại; observer
  vẫn không mở incident và không ghi health cho runtime không đọc được (quyết
  định 8).
- `cmd/mate/console.go` trỏ runtime adapter vào đúng binary `herdr` mà
  `findTool` tìm được (`~/.local/bin`, `/opt/homebrew/bin`, …), tức binary cột
  stage chạy, để bước kiểm tra và lệnh attach không bao giờ hỏi hai executable
  khác nhau.
- `herdrSession` chỉ đổi câu thành "herdr is not running" khi có bằng chứng
  dương: `running: false` hoặc `herdr_code=server_not_running`
  (`runtime.IsServerNotRunning`). Lỗi khác - executable không chạy được,
  transport - trả nguyên văn, vì attach là subprocess riêng và có thể vẫn là
  đường chạy được.
- `consoleStage` gọi `herdrSession` trước `c.show`; bị từ chối thì TUI hiện
  `Show refused: …` trên status line và không cột nào bị đụng.
- `internal/query/types.go` thêm `Snapshot.Runtime`; `internal/ui/console/chrome.go`
  vẽ notice đó như một warn line, ưu tiên trên notice của daemon và dòng
  "N field unknown". `docs/mvp.md` mục M13 ghi lại hành vi.

## Kiểm chứng

- `make check` exit 0: gofmt, `go vet`, toàn bộ `go test ./...` (62 live test
  skip có khai báo trong `scripts/gotestreport/expected-skips.txt`).
- Hồi quy:
  - `internal/watch`: notice dựng khi session không trả lời, mang lý do, mất khi
    hồi phục; và dựng cả khi workspace không có crew mở, với `HandleFunc`
    `t.Fatal` nếu bị gọi.
  - `cmd/mate`: Enter trên Mate có session đã chết bị từ chối và không cột nào
    bị đụng; `withRuntimeNotice` điền snapshot, `query.Load` một mình để trống.
  - `internal/ui/console`: notice render thành warn line, ưu tiên hơn field
    warning, và trả lại field warning khi mất.
  - `internal/runtime`: `IsServerNotRunning` chỉ nhận `server_not_running`,
    không nhận lỗi "executable failed to run".
- Test cũ `TestWatchConcludesNothingWhenHerdrCannotAnswer` vẫn xanh: observer vẫn
  không kết luận gì về crew từ một lần đọc hỏng.

## Giới hạn

- Không chạy lại được repro live trong console thật với một server Herdr chết và
  agent đang attach: phiên SSH tmux là phần đang dở, chưa commit (xem
  `docs/plans/ssh-console-2026-09-30.md`). Bằng chứng live ở đây là đầu vào thật
  mà `LookupSession` đọc (`running: false`) cộng dòng `server_not_running` đúng
  như captain thấy; phần console là test hồi quy ở tầng Go.
- `mate` chưa tự dựng lại Herdr. Đường phục hồi vẫn là `s` (start/resume Mate),
  tạo hoặc adopt session và tab mới; crew đang mở coi như mất agent. Ngoài phạm
  vi PR.
- Notice hỏi session mỗi vòng poll (5s), thêm một lệnh `herdr` mỗi vòng trên chi
  phí poll đã ghi ở `docs/mvp.md` mục 4b.
- Chỉ `mate console`/`mate <dir>` được console dựng cột có preflight; `mate
  attach` trực tiếp (CLI, không qua Console) vẫn chạy `herdr agent attach` và có
  thể in lỗi của Herdr - đúng như trước.
