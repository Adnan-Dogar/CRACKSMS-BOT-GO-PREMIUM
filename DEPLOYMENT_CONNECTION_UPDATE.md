# Connection, backup and rewards release

Deployed on 2026-09-30. Release image: `cracksms-bot-go-premium-bot:connections-schema10-20260930T103345Z`.

The release adds login form and SMS endpoint discovery, separate CR and reseller request formats, encrypted multipart user backup and restore, responsive background broadcast preparation, multiple custom apps during number import, isolated child panel accounts, main OTP sharing controls, selective data tables, the complete CrackSMS V2 UI ID aliases, and a limited daily PKR/USD reward wizard. OTP theme bodies remain unchanged.

PostgreSQL migrations 009 and 010 are installed. A production dump was restored into an isolated PostgreSQL database and upgraded successfully before deployment. Its five users and one panel were preserved. The live database reports schema 10, five users, one panel and unchanged inventory, OTP and ledger counts. `/healthz` and `/readyz` both returned HTTP 200; the bot container is running with zero restarts. Telegram reports 76 administrator commands, including `/limitedreward`, `/backupusers` and `/restoreusers`.

The full PostgreSQL-backed Go suite, focused race tests for reward/broadcast/backup workflows, `go vet ./...`, and `git diff --check` passed. Tests cover concurrent winners for limited USD rewards, archive compatibility across the new reward columns, backup document delivery, upload preview/restore, child account isolation and OTP sharing, responsive broadcast cancellation, login renewal and legacy API requests. The Docker release build also completed its built-in Go test and binary build steps. A final statically linked bot binary with the archive compatibility fix was placed over that image; its SHA-256 matched the release image binary.

Production dump: `legacy-backup/pre-connections-schema8-20260930T103345Z.dump` (mode 0600; SHA-256 `a709b66b9a35e941c67525ae2ca6817a55cbfd27a6d81a821514c252190f51eb`). Previous binary image: `cracksms-bot-go-premium-bot:before-connections-schema8-20260930T103345Z`. Migrations are additive to the earlier binary; reverting the image alone keeps current data but pauses the new workers and menus.

The saved login account at `smshadi.net` still presents a numeric `capt` challenge on its public sign-in form. The new connector correctly marks it as requiring manual provider access rather than claiming the SMS connection is healthy. No OTP delivery from that login account was established by this release. An approved provider API, allowlisting or operator-managed session is required for live ingestion from that account.
