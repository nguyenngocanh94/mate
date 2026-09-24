# Task

## Captain's words
Thêm nút "Mua ngay" lên landing page ESP32, bấm vào thì mở trang thanh toán.

Earlier, when commissioning the ESP32 research (2026-09-17):
tôi muốn bạn tìm hiểu cho tôi mạch esp32 sau đó báo cáo lại, ta sẽ làm landing page cho nó

## What we already know
- The Mate has not read this repository and has no record of its contents: PROJECT.md is empty and no earlier task changed code here.
- No earlier task built an ESP32 landing page; the backlog holds only research tasks. Whether one exists in the repo is unknown.
- A research report with a landing-page content outline exists: /Users/erics/work-mate/.mate/projects/shop/crews/esp32research/bao-cao-esp32-devkit.md, section "4) Khung nội dung sẵn cho landing page". Read it only if Open decision 1 is answered "build it".
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
