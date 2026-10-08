# Thử Jev để đọc notice trong terminal

Console có action **Explain notice (Jev)** cho Mate và Crew đang có binding active. Chọn agent, bấm `a`, rồi `e`. Kết quả mở trong sheet cuộn được; `Esc` đóng. Tính năng mặc định tắt và chỉ gọi API khi người dùng chọn action, không gọi theo mỗi lần refresh.

## Bật bản thử

Lưu API key trong một file ngoài repo, chỉ user đọc được (`chmod 600`). Không đặt key trong workspace, command line hoặc file commit.

Tính năng bật theo từng workspace, trong `.mate/.env` của workspace đó; console không đọc biến môi trường của process, nên workspace khác không bị bật theo:

```sh
# <workspace>/.mate/.env
MATE_JEV=on
MATE_JEV_API_KEY_FILE=~/.config/mate/jev-trial-api-key
```

`MATE_JEV_API_KEY_FILE` chứa **đường dẫn**, không chứa key; `~/` được mở rộng, đường dẫn tương đối tính từ gốc workspace. Đặt `MATE_JEV=off` hoặc bỏ dòng để tắt; mở lại console để đọc `.env`. Nếu `.env` sai cú pháp, thiếu đường dẫn hay file key không đọc được, console vẫn chạy và báo Jev bị tắt ở status line.

## Kết quả có ý nghĩa gì?

Jev phân loại một ảnh chụp text thành một trong bảy nhãn:

| Nhãn | Ý nghĩa |
| --- | --- |
| `quota_warning` | Gần giới hạn, chưa có bằng chứng request đang bị chặn |
| `quota_exhausted` | Notice nói request bị chặn do giới hạn hiện tại |
| `auth_required` | Cần đăng nhập hoặc credential |
| `permission_required` | Có yêu cầu xác nhận quyền hoặc trust |
| `update_notice` | Thông báo cập nhật phần mềm |
| `none` | Không nhận ra notice hiện tại |
| `unknown` | Notice lạ, thiếu hoặc mâu thuẫn thông tin |

Sheet ghi thời điểm capture và confidence từ model. Confidence không phải độ chính xác đã đo; mọi nhãn đều là gợi ý. Màn hình có thể đã đổi khi kết quả tới. `none` không có nghĩa task đã xong, composer an toàn để gửi hay mọi lỗi đã hết.

## Dữ liệu và giới hạn

- Một action đọc tối đa 40 dòng cuối qua adapter Herdr hiện có, gửi tối đa 8 KiB UTF-8 text tới TypeSafe. Nguồn đọc có thể là recent output, không đảm bảo là viewport hiện tại. API key được dùng cho header xác thực.
- Loại ANSI/control sequence, che key của chính client và một số mẫu credential phổ biến. Đây là redaction tốt nhất có thể theo pattern, **không bảo đảm loại hết dữ liệu riêng tư**. Phần terminal gửi đi vẫn có thể chứa prompt, đường dẫn, code hoặc output. Chỉ bật cho nội dung bạn đồng ý gửi tới TypeSafe.
- Dùng `jev-1.13.0` cố định, endpoint HTTPS cố định; không theo redirect, không tự retry; deadline chung 8 giây cho đọc pane và gọi API. Response lỗi, nhãn ngoài enum hoặc phân phối xác suất sai bị từ chối.
- Không ghi màn hình, response hoặc key vào state/log của mate. Result chỉ nằm trong sheet hiện tại. Không cache, không background polling, không ghi nhãn vào timeline/inbox.
- Jev không tham gia composer classifier, sender, receipts, incidents, quota dispatch hoặc task state. Nó không tự nhấn Enter, cấp quyền hay đóng dialog. **Bản thử này chưa sửa lỗi probe hiện tại.**

## Bộ quan sát: `MATE_JEV=observer`

Từ 2026-10-08 ([phương án](plans/jev-observer-2026-10-08.md) mục 4.4), `MATE_JEV=observer` trong `.mate/.env` (cùng `MATE_JEV_API_KEY_FILE`) bật thêm bộ quan sát: **pane của Mate và Crew đi ra TypeSafe mỗi lần màn hình đổi** (và mỗi phút một lần cho màn hình đứng yên), từ observer của console, `mate send`, `mate state` và bước settle khi start, không chỉ khi người dùng chọn action. Nội dung gửi đi giống action ở trên (40 dòng cuối, 8 KiB, che key và credential theo pattern), thêm đánh dấu chữ mờ. Jev hỏi trước, fixture classifier là fallback: Jev lỗi hay confidence dưới `MATE_JEV_THRESHOLD` (mặc định `0.85`, chỉ nhận số trong `[0, 1]`) thì dùng nguyên quan sát của fixture; Jev nói composer trống mà fixture chắc có draft hay đang bận thì fixture thắng. Enter vẫn chỉ đi sau khi so chuỗi trực tiếp, phím dialog chỉ sau khi fixture xác nhận highlight. Mỗi request ghi một dòng vào `.mate/jev.log` (thời điểm, harness, 12 ký tự hash màn hình, latency, nguồn được dùng, nhãn Jev, lý do fallback; không có nội dung màn hình hay key); `go run ./scripts/jeveval -log <workspace>/.mate/jev.log` in số request, latency p50/p95 và số lần fallback. Các mục "Dữ liệu và giới hạn" ở trên mô tả `MATE_JEV=on`, vẫn giữ nguyên nghĩa: chỉ action notice.

## Phương án tiến tới probe tổng quát

1. Thu corpus có gán nhãn từ nhiều phiên bản harness, kích thước terminal, warning, dialog, history và bản dịch; bổ sung negative cases và chuỗi frame theo thời gian.
2. Ưu tiên sự kiện có cấu trúc từ harness/hook khi có; observation giữ riêng process, activity, composer và notice. `unknown` là thiếu bằng chứng, không phải lỗi task hay thành công gửi.
3. Nếu Jev tốt trên corpus độc lập, chạy shadow khi observation thiếu rõ ràng: bounded queue, dedup theo screen/session, cooldown, deadline, huỷ kết quả khi pane/session đổi. Giữ metrics về false positive, abstention, latency và chi phí.
4. Policy deterministic mới quyết định hành động từ bằng chứng. Xác nhận gửi cần receipt riêng; nhận diện notice bằng model không thay được receipt hoặc quyền người dùng.

Không model nào bảo đảm bền vững với mọi update TUI. Vai trò phù hợp của Jev là giảm phụ thuộc vào cách viết notice, bên trong kiến trúc chấp nhận không biết và kiểm chứng hành động độc lập.

## Kiểm thử

```sh
make check
make test-race
MATE_LIVE=1 MATE_JEV_API_KEY_FILE="$HOME/.config/mate/jev-trial-api-key" \
  go test ./internal/notice -run TestLiveJevNotices -v -count=1
```

Live test gửi các fixture **giả lập** trong `internal/notice/testdata/notices.json`, không đọc pane/workspace của người dùng. Biến môi trường `MATE_JEV_API_KEY_FILE` ở đây chỉ dành cho live test; console chỉ đọc `.mate/.env`. Kết quả đầu tiên: [evidence 2026-09-27](evidence/jev-notices-2026-09-27.md). Đây không phải live acceptance của toàn bộ luồng Herdr → console → API.

Hợp đồng request/response theo [TypeSafe API reference](https://docs.typesafe.ai/api); hạn chế model theo [Jev jaggedness](https://docs.typesafe.ai/model-jaggedness/jev-1.13).
