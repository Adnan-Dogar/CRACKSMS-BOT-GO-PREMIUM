# OTP theme release

The Minimal layout described below was superseded by [the 2026-10-01 correction](DEPLOYMENT_MINIMAL_CORRECTION.md).

Deployed on 2026-10-01. Image: `cracksms-bot-go-premium-bot:otp-theme-20261001T015703Z`.

All ten OTP themes omit panel names. Classic no longer uses the snow border. Minimal shows an app emoji, flag, masked number, country label and masked OTP; its copy button carries the original code. Its community row uses per-bot Group and Channel links, followed by a separate Number Bot row. Links without configured URLs are omitted. For groups with buttons disabled, Minimal displays the OTP in the body. Other themes keep their layouts and offer a copy button for masked/hidden destinations when buttons are enabled. Admin privacy labels explain that masking affects display, not the copy action.

An optional `GROUP_URL` default and per-bot `group_url` setting were added without a database migration. No group URL was supplied for production, so administrators must set **OTP Group Link** to display that button.

The full PostgreSQL-backed Go suite, `go vet ./...` and `git diff --check` passed. `/healthz` and `/readyz` returned HTTP 200 after deployment; the container reports zero restarts. The release binary SHA-256 is `6c3fc42d1cf82443d750ab4add8fddf3eeb7b5b3d751418ca25d9123ac4b4d48` and matches the running container.

Backup: `legacy-backup/pre-otp-theme-20261001T015703Z.dump` (SHA-256 `65af4cc720772574866b553a5d01215aa9c5c1f0829bb269486f6dcf45071dda`). Previous image: `cracksms-bot-go-premium-bot:before-otp-theme-20261001T015703Z`.
