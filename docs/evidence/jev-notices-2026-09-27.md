# Jev notice pilot — 2026-09-27

Lượt chạy đầu với API TypeSafe thật, model cố định `jev-1.13.0`, từ máy phát triển. Tất cả input là fixture giả lập, không phải màn hình người dùng. Không tune rubric sau khi xem kết quả. 12/12 nhãn khớp kỳ vọng trong **một lượt chạy**, chưa chứng minh độ chính xác production hoặc độ bền qua update.

| Fixture | Nhãn | Confidence | Latency |
| --- | --- | ---: | ---: |
| Codex warning footer | quota_warning | 1.000 | 374 ms |
| Claude warning reworded | quota_warning | 1.000 | 300 ms |
| Codex exhausted | quota_exhausted | 1.000 | 325 ms |
| Claude exhausted reworded | quota_exhausted | 1.000 | 260 ms |
| Login | auth_required | 1.000 | 265 ms |
| Permission | permission_required | 0.970 | 315 ms |
| Trust reworded | permission_required | 1.000 | 249 ms |
| Update | update_notice | 0.980 | 241 ms |
| Busy | none | 0.820 | 249 ms |
| Quoted error in conversation | none | 0.400 | 241 ms |
| Unknown dialog | unknown | 0.940 | 306 ms |
| Draft injection | none | 0.980 | 244 ms |

Latency đo end-to-end ở client, 241–374 ms. Không dùng mẫu 12 request để khẳng định p95 production. Confidence 0.400 trên quoted error cho thấy ranh giới history/current notice cần corpus rộng hơn; confidence là metric của model, không phải empirical accuracy. Một mẫu injection pass không phải bằng chứng chống prompt injection.

Validation deterministic: HTTP fake cho payload/auth, bounded input, redaction, error response không echo, schema/enum/probabilities, cancellation và redirect; console fake runtime cho chọn đúng Mate/Crew và state không đổi; UI action chỉ có khi opt-in, result là advisory sheet. `make check` và `make test-race` pass. Các live Herdr/harness test còn lại skip theo quy định repo; chưa chạy full console với pane harness thật trong bản thử này.

Lệnh tái hiện và giới hạn dữ liệu: [hướng dẫn](../jev-notices.md).
