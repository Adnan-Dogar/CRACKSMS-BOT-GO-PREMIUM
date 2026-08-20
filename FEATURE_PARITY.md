# Feature parity

This matrix compares the three supplied legacy repositories and the v20/v21 inventory with the modular Go implementation. Marketing counts such as “397 buttons” are represented by shared, tested builders: every generated button receives the same style/custom-emoji support without maintaining hundreds of copied literals.

| Capability | Go vNext implementation |
|---|---|
| Dynamic OTP groups | Persistent per-bot destinations; add/list/enable/remove commands |
| Buttons optional per group | `buttons_enabled`, `/groupbuttons` |
| Public OTP privacy | Per-group `visible`, `masked`, or `hidden`; message and copy controls are redacted together |
| Ten OTP themes | T0–T9 renderers and `/otpguipreview`; global, group, and user selection |
| Premium emojis/button colors | Legacy country/app/UI custom-emoji ID catalogs; HTML-safe `<tg-emoji>` rendering; raw Bot API `icon_custom_emoji_id`, `style`, and `copy_text`; Unicode fallback for older clients |
| OTP extraction | Multilingual and formatted-code rules, plus tenant custom RE2 capture patterns |
| Deduplication | Provider-record-aware keys and PostgreSQL uniqueness |
| Multi-panel support | Token, legacy URL, login/cookie, and WebSocket adapters; encrypted config and supervised workers |
| Speed/reliability | Concurrent panel workers, durable ingest queue, atomic cursor advance, SKIP LOCKED delivery workers, retry/backoff |
| First OTP consumes number | Transactional assignment consumption; later events cannot credit the number again |
| 20-minute re-add | Expiry scheduler returns no-OTP numbers and excludes the previous user for 24 hours |
| Daily rewards | Global and per-user replacement schedules; cumulative milestones and Asia/Karachi logical day reset |
| Earnings/referrals/withdrawals | Immutable ledger, balances, qualification, holds/refunds, admin approval |
| User experience | Styled compact/full dashboards, dynamic inline service/country picker, stats, history pagination, profile, leaderboard, settings, help, FAQ-style guide |
| Force join | Tenant-scoped required chats and styled verification controls |
| Admin dashboard | Full styled inline navigation and guarded actions for inventory, panels, groups, rewards, users/tiers, bots, tutorials, withdrawals, admins, required chats, patterns, broadcasts, settings, and analytics |
| Analytics | Telegram dashboard, health/Prometheus metrics, and Enterprise JSON API |
| Free/Pro/Enterprise | Persistent subscriptions and 2/10/50 panel limits; feature gates |
| Child bots | Encrypted requests, approval/rejection, tier selection, tenant admins/groups/panels/settings, supervised clients and routed deliveries |
| Webhooks | Persistent endpoints/deliveries, HMAC signatures, public-HTTPS SSRF protection, retry/backoff |
| Message scheduling | Persistent user/group/all-user schedules, cancellation, retry, bot routing |
| API access | One-time API keys, SHA-256 storage, scopes, expiry/revocation, 60 requests/minute |
| Tutorials/media | Persistent tenant tutorials and Telegram text/photo/video delivery |
| Broadcasts | Tenant-scoped recipients, HTML/premium emoji support, cancellation on shutdown |
| Country/service detection | Dial-code flag/name detection plus original CrackSMS animated flag and service/app custom-emoji IDs in OTP messages and selector buttons |
| Security/operations | AES-256-GCM credentials, no secrets in repo, non-root/read-only container, PostgreSQL migrations, readiness, graceful shutdown, legacy dry-run importer |

The Python repositories’ webhook, schedule, tier, and child-bot state was partly in-memory or process-folder based. vNext implements those capabilities as durable PostgreSQL records and supervised workers.
