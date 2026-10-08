# Login panel compatibility release

Deployed on 2026-10-01. Release image: `cracksms-bot-go-premium-bot:loginfix-20261001T012044Z`.

The `/ints` login adapter now uses the browser-style User-Agent expected by this panel, answers a single visible arithmetic question on the sign-in form, and recognizes an authenticated dashboard redirect even when that dashboard contains a separate password form. It rejects a returned login form even if the page navigation mentions the dashboard. The SMS request uses the same User-Agent. Other challenge types still require operator action.

The supplied account was checked against the live panel without saving its credentials to the repository or production database. The final adapter completed sign-in and fetched the SMS response. The full PostgreSQL-backed Go suite and the focused tests after the final guard passed. After deployment, `/healthz` and `/readyz` both returned HTTP 200; the bot container is running with zero restarts. Its binary SHA-256 matches the release build: `ea5a5288b60eee8a7f71f56c37d5b430406617d43d098308ae3d397e89ad33a8`.

Production dump: `legacy-backup/pre-loginfix-20261001T012044Z.dump` (mode 0600; SHA-256 `e14b084bb91fd2e88dca19ab645ac6e64405986e90455bdd97740395b7b67b39`). Previous image: `cracksms-bot-go-premium-bot:before-loginfix-20261001T012044Z`. There are no database migrations in this release.
