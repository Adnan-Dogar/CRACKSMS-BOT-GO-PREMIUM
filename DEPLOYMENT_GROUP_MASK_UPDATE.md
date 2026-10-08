# Group OTP number masking

Deployed on 2026-10-01. Image: `cracksms-bot-go-premium-bot:group-number-mask-20261001T121445Z`.

All ten OTP themes show a masked destination number in group posts. Short numbers are masked as `+••••`. Themes that include the original SMS text also redact the same number there, including common spacing and punctuation variants. A group's **Full Message** copy button uses the redacted text; private user messages and their copy buttons retain the full number.

The full Go suite, focused group-theme cases, `go vet ./...` and `git diff --check` passed. `/healthz` and `/readyz` returned HTTP 200 after deployment. The bot container reports zero restarts, and its binary SHA-256 matches the release build: `10e11c6255b1bbc2709124cedea43534c42f143f998db98ecd971e35e4bed81d`. No database migration was needed.

Previous image: `cracksms-bot-go-premium-bot:group-number-mask-20261001T121254Z`.
