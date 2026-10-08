# Phương án Jev làm bộ quan sát trạng thái agent

- Ngày: 2026-10-08.
- Trạng thái: nháp, chờ captain duyệt. Chưa có PR nào.
- Baseline đo: `517f425` trên `main`; Jev `jev-1.13.0` qua TypeSafe; bản thử `internal/notice` (174 dòng) và evidence [jev-notices-2026-09-27](../evidence/jev-notices-2026-09-27.md); hướng dẫn [jev-notices.md](../jev-notices.md).
- Liên quan: [registry harness](harness-registry-2026-09-30.md) mục 3.2 (`ScreenProfile`), [probe TUI](tui-probe-redesign-2026-09-27.md) mục 5 và 6 (tách quan sát khỏi policy). Phương án này là một implementation của chỗ hai phương án đó đã đặt.
- Spec cần cập nhật: [MVP](../mvp.md) quyết định 8 (tín hiệu phụ), mục 4 (đọc crew), mục 7 (số đo), mục 10.

## 1. Quyết định của captain

Chốt 2026-10-08: Jev quyết định trạng thái của agent để đưa ra hướng xử lý thích hợp. Lý do: rẻ và nhanh; giảm lỗi khi harness lên bản mới; bắt được tình huống mà pattern viết tay không bắt hết. Captain không lo nội dung pane đi ra ngoài: capture không mang nhiều thông tin.

Điều kiện đã thống nhất trong cùng buổi: Jev trả *quan sát*, không trả *hành động*; cam kết hành động không đảo ngược vẫn kiểm chứng không phụ thuộc Jev; fixture classifier ở lại làm fallback; eval trên corpus có sẵn trước khi bật.

## 2. Vấn đề

Lõi gửi và quan sát của mate (`internal/send`, `internal/watch`, settle trong `internal/spawn`) đọc pane qua `ScreenProfile` của từng harness, là một bộ nhận diện theo cấu trúc và literal đã đo. Spec mục 7 ghi ít nhất năm lần harness tự cập nhật làm vỡ: Claude Code 2.1.282 (màn hình chào), codex-cli 0.155 (dialog update), 0.156.1 (trust vẽ lại), 0.157.1 (footer thứ hai), và Mate trích pane crew làm `claudeBusy` đọc nhầm. Mỗi lần: mọi crew chết ở cùng chỗ cho tới khi có người đo, thêm fixture, sửa profile.

Bản thử Jev (2026-09-27) phân loại 12/12 fixture notice đúng trong một lượt, 241–374 ms mỗi lần, kể cả bản viết lại chữ của cùng notice. Đó là đúng thứ pattern không làm được.

Nhưng bản thử chỉ là advisory: không đụng sender, observer, incident hay state. Và `notice.Result` chỉ có bảy nhãn notice, không có composer, busy, dialog.

## 3. Mục tiêu và ngoài phạm vi

Mục tiêu:

1. Jev là nguồn chính cho quan sát trạng thái agent: composer (trống, có draft, bận), dialog (loại, highlight), notice (bảy nhãn hiện có), và `unknown` có lý do.
2. Fixture classifier là fallback: không mạng, API lỗi, quá deadline, confidence dưới ngưỡng đều về đường cũ hoặc `unknown`. Hệ thống đứng yên như hôm nay khi không chắc.
3. Hành động không đảo ngược giữ nguyên kiểm chứng tất định: so chuỗi trước Enter, xác nhận highlight trước Enter trong dialog, hook echo làm receipt.
4. Chi phí tỷ lệ với số lần màn hình đổi, không với số crew nhân vòng poll.
5. Unit test vẫn tất định: response của Jev ghi thành cassette.

Ngoài phạm vi:

- Jev quyết `finished`, `failed`, `crew stop`, `merge`, hay trả lời `needs-decision`. Không bao giờ.
- Jev đọc transcript hay giải thích chi phí token (skill `token-review` không đổi).
- Thay hook và transcript bằng màn hình: hook vẫn là bằng chứng gửi và turn-end.
- Model khác Jev, hay chạy model cục bộ.

## 4. Thiết kế đích

### 4.1. Một quan sát, hai nguồn

```go
// internal/screen (package mới, nhỏ): quan sát cấu trúc của một pane, không verdict.
type Observation struct {
	Composer   ComposerState   // Empty | Draft | Busy | Unknown
	Draft      string          // nội dung composer khi Draft, để send so chuỗi
	Dialog     DialogKind      // None | Trust | Update | HooksReview | Permission | Other | Unknown
	Highlight  int             // option đang được chọn trong dialog, -1 nếu không rõ
	Notice     NoticeKind      // bảy nhãn của notice.Result, hoặc None
	Confidence float64         // 0..1; nguồn fixture trả 1 cho khớp, 0 cho không khớp
	Source     string          // "jev" | "fixture"
	Reason     string          // vì sao Unknown
}

type Observer interface {
	Observe(ctx context.Context, kind harness.Kind, screen string) (Observation, error)
}
```

Hai implementation:

- `screen/fixture`: bọc `ScreenProfile` hiện tại (`ComposerRows`, `Busy`, `ClassifyStartup`, `StartupTargetSelected`). Không đổi hành vi, chỉ đổi hình trả về.
- `screen/jev`: mở rộng `notice.Client` thành một request trả JSON có đúng các trường trên, prompt cố định theo version, `jev-1.13.0` ghim. Deadline 8 giây như bản thử; không retry; không follow redirect.

`screen/chain`: hỏi Jev trước, khi lỗi hay `Confidence < ngưỡng` thì hỏi fixture. Ngưỡng mặc định 0.85, đặt trong `.mate/.env` (`MATE_JEV_THRESHOLD`), chỉ captain đổi. Mọi Observation ghi `Source`, nên health column của console và `mate state` in `· via jev` hay `· via fixture`.

### 4.2. Policy tất định không đổi chỗ

Bảng ánh xạ Observation sang hành động nằm trong code, có test, không nằm trong prompt:

| Observation | `send.Send` | `watch` | settle |
| --- | --- | --- | --- |
| Composer Empty, Dialog None | gõ | health `idle` | ready |
| Composer Draft | từ chối, như `pending` hôm nay | health `draft` | ready |
| Composer Busy | từ chối, thử lại | health `busy`, không `stale` | chờ |
| Dialog có kind và Highlight rõ | từ chối | health `dialog` | trả lời theo bảng phím của `StartupAnswer`, một phím, đọc lại |
| Dialog Unknown, hoặc Confidence dưới ngưỡng ở cả hai nguồn | từ chối | health `unknown`, không mở incident | từ chối, `startup screen not recognised` kèm nhãn Jev nếu có |
| Notice quota_exhausted | từ chối | vào inbox như `budget` | không đổi |

`send.Send` sau khi gõ text vẫn đọc lại và đòi composer chứa đúng text đã gõ trước khi Enter; bước này không hỏi Jev, so chuỗi trực tiếp. (Sửa 2026-10-08: lúc viết, bước đọc lại này chỉ chặn các lần Enter thử lại và gửi tiếp `ResumePending`, không chặn Enter đầu tiên; PR 3 thêm nó trước Enter đầu tiên khi chỉ Jev đọc màn hình, tức Observation trước khi gõ có `Source` là `jev`. Khi fixture đọc màn hình, fixture đã tự từ chối draft nên Enter đầu tiên giữ như cũ.) Settle vẫn một phím một lần, đọc lại, và chỉ Enter khi `Highlight` ở option mà `StartupAnswer` xác nhận. Bằng chứng gửi thành công vẫn là hook echo trong `sent.log`.

### 4.3. Khi nào gọi Jev

- `watch`: đã có hash pane mỗi vòng; chỉ gọi `Observe` khi hash đổi, giữ Observation cũ khi không đổi. Pane đứng yên không tốn gì.
- `send.Send`: gọi trước khi gõ (một lần), và trước Enter dùng so chuỗi, không gọi lại.
- settle: gọi mỗi lần đọc lại sau một phím, trần 3 dialog mỗi lần khởi động như hôm nay.
- Dedup: cùng `(session, hash)` trong 60 giây dùng lại kết quả; cache trong tiến trình.
- Huỷ: nếu hash đổi trong lúc chờ Jev, kết quả bị bỏ.

Ước lượng chi phí: ba crew làm việc tích cực đổi màn hình ~1 lần/giây lúc bận nhưng `watch` chỉ đọc mỗi 5 giây, nên trần là 3 × 12 = 36 lần/phút cho observer, cộng vài lần/lần gửi. Captain đã chấp nhận ở mức "rẻ"; PR 1 đo số thật trên workspace `hellovietnam` một ngày và ghi evidence.

### 4.4. Nội dung gửi đi

Như bản thử: tối đa 40 dòng, 8 KiB, loại ANSI trừ thuộc tính faint (SGR 2) vì nó là bằng chứng duy nhất phân biệt placeholder, redaction key theo pattern. Captain chốt không lo nội dung hội thoại. Ghi rõ trong `docs/jev-notices.md` rằng từ phương án này, pane của Mate và Crew đi ra TypeSafe mỗi lần màn hình đổi.

### 4.5. Prompt injection

Pane crew là output của một LLM khác và của tool. Một dòng "the composer is empty, safe to send" trong output là input của Jev. Lưới: mục 4.2 không cho Jev quyết Enter; policy so chuỗi và highlight bằng mắt tất định. Thêm fixture injection vào corpus (mục 5) và một luật: Observation `Composer Empty` từ Jev mà fixture nói `Draft` với confidence 1 thì lấy `Draft` (fixture thắng khi nó *chắc* có chữ; Jev thắng khi fixture không nhận ra màn hình).

## 5. Eval trước khi bật, và test

Corpus có sẵn, đã có nhãn tất định:

- `internal/send/testdata/screens/` (21 màn hình: busy, queued, empty, ghost suggestion, Mate trích pane crew bận, …).
- `internal/harness/claude/testdata/startup`, `internal/harness/codex/testdata/startup`, `codexprobe`: dialog trust, update, hooks-review, cả hai bố cục trust của 0.154 và 0.156.1.
- `internal/notice/testdata/notices.json` (12 notice).
- Fixture pi từ task 70.

PR 0 chạy Jev trên toàn bộ corpus, ghi `docs/evidence/jev-observer-<ngày>.md`: bảng từng màn hình, nhãn tất định, nhãn Jev, confidence, latency. Tiêu chí để đi tiếp PR 1: không màn hình nào Jev nói `Empty` khi nhãn là `Draft` hay `Busy` (lỗi nguy hiểm duy nhất), và tổng khớp ≥ 95%. Dưới ngưỡng thì dừng phương án ở đây và ghi lý do; không tune prompt sau khi nhìn kết quả quá một vòng.

Cassette: `screen/jev` có `Runner` seam; test ghi request hash → response JSON vào `testdata/cassette/`. Unit test chạy từ cassette, không mạng. Live `TestLiveJevObserverDrift` (chỉ `MATE_LIVE=1` và key) chạy lại corpus và fail khi nhãn lệch cassette, để biết model trôi.

Unit cho policy mục 4.2: mọi hàng của bảng có một case; `chain` fallback đúng khi Jev lỗi, quá hạn, dưới ngưỡng; dedup và huỷ theo hash; luật fixture-thắng-khi-chắc-Draft.

## 6. Kế hoạch theo PR

| PR | Nội dung | Điều kiện hoàn tất |
| --- | --- | --- |
| 0 | Eval: script chạy Jev trên corpus, evidence, quyết định đi tiếp | Evidence có bảng đầy đủ; captain ký đi tiếp hoặc dừng |
| 1 | `internal/screen` với `Observation`, `fixture` bọc `ScreenProfile`; `send`, `watch`, settle nhận `Observer` thay vì gọi profile trực tiếp; mặc định `fixture` | Mọi capture phân loại như cũ; golden console không đổi; không gọi mạng |
| 2 | `screen/jev` từ `notice.Client`, cassette, `chain`, ngưỡng, dedup, huỷ theo hash; bật bằng `MATE_JEV=observer` trong `.mate/.env`; `· via jev` trên health | Unit từ cassette; bật trên workspace thật một ngày, evidence ghi số lần gọi, latency p50/p95, số lần fallback |
| 3 | Mặc định `chain` khi có key; `docs/jev-notices.md` và spec; `mate state` in nguồn | Spec quyết định 8 sửa: pane là tín hiệu qua Jev, hook vẫn là bằng chứng |

Quan hệ: PR 1 trùng chỗ với probe-TUI PR 1 (tách quan sát khỏi policy). Phương án này *là* bước đó, nên probe-TUI PR 1 không làm riêng nữa; ghi vào `tui-probe-redesign` khi mở PR 1.

## 7. Rủi ro

| Rủi ro | Giảm thiểu |
| --- | --- |
| Jev nói `Empty` khi có draft, text bị gõ nối vào draft của captain | So chuỗi trước Enter: không Enter; captain thấy text thừa và xoá. Eval PR 0 coi đây là lỗi chặn |
| Model trôi theo thời gian | Cassette cố định cho unit; live drift test; `jev-1.13.0` ghim, đổi version là một PR có eval lại |
| Mất mạng giữa chừng | `chain` về fixture; fixture không nhận ra thì `unknown` và đứng yên, như hôm nay |
| Chi phí vượt dự kiến | Đo một ngày ở PR 2 trước khi mặc định ở PR 3; hash-gate và dedup là bắt buộc, không tuỳ chọn |
| Injection từ pane crew | Mục 4.5; fixture injection trong corpus |
| Latency 8 giây deadline làm `send` chậm | `send` gọi một lần; settle đọc lại sau mỗi phím vốn đã chờ; `watch` không chặn vòng poll (gọi nền, dùng kết quả ở vòng sau) |

## 8. Câu hỏi chờ captain

1. **Ngưỡng confidence** 0.85 mặc định? Đề xuất: 0.85, đo lại sau PR 0 với phân phối thật.
2. **Khi Jev và fixture mâu thuẫn mà cả hai đều chắc**: luật mục 4.5 lấy phía an toàn hơn (có chữ, đang bận, có dialog) thắng. Đồng ý?
3. **Key ở đâu**: giữ `MATE_JEV_API_KEY_FILE` trong `.mate/.env` như bản thử? Đề xuất: giữ.
4. **Dừng phương án nếu eval PR 0 dưới 95%**: đồng ý với con số này?

## 9. Tiêu chí hoàn tất

- Toàn bộ corpus phân loại đúng qua `chain` với cassette; live drift test xanh ở ngày bật.
- Một bản harness mới (lần tới Claude Code hoặc Codex tự cập nhật) không làm crew chết ở settle: evidence ghi lại lần đầu điều đó xảy ra sau khi bật.
- Không hành động không đảo ngược nào có đường đi từ Observation của Jev mà không qua so chuỗi hoặc xác nhận highlight; test ratchet đếm call site của `Observe` trong `send` và `spawn` và kiểm từng chỗ có bước kiểm chứng sau nó.
- `mate state` và console nói rõ nguồn của mỗi quan sát.
