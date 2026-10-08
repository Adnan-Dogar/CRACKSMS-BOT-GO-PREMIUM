# Minimal OTP layout correction

Deployed on 2026-10-01. Image: `cracksms-bot-go-premium-bot:otp-minimal-20261001T115049Z`.

Minimal (T1) now renders only the app icon, country flag and masked number in a single message line. It does not put the country name, OTP label, OTP dots or message text in the body. The first button displays only heavy dots and uses the user-supplied animated 👈 custom emoji ID `6319056439096644016`; its Telegram `copy_text` action contains the original OTP. Group/Channel and Number Bot rows remain as configured. Minimal requires buttons to access an OTP; disabling buttons no longer prints the code in the body.

Focused theme, premium, delivery and Telegram tests, `go vet ./...` and `git diff --check` passed. `/healthz` and `/readyz` returned HTTP 200 after deployment; the bot container reports zero restarts. The running binary SHA-256 matches the tested build: `0058db665288f42b5e21a41dd300b6ed09449124546f36da19a1024d2b1b3349`.

Previous image: `cracksms-bot-go-premium-bot:otp-theme-20261001T015703Z`. No database migration was needed.
