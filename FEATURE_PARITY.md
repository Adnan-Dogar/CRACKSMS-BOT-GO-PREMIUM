# Feature parity

This matrix compares the three supplied legacy repositories and the v20/v21 inventory with the modular Go implementation. Marketing counts such as “397 buttons” are represented by shared, tested builders: every generated button receives the same style/custom-emoji support without maintaining hundreds of copied literals.

| Capability | Go vNext implementation |
|---|---|
| Dynamic OTP groups | Persistent per-bot destinations; add/list/enable/remove commands |
| Buttons optional per group | `buttons_enabled`, `/groupbuttons` |
| Public OTP privacy | Per-group `visible`, `masked`, or `hidden`; message and copy controls are redacted together |
| Ten OTP themes | T0–T9 renderers and `/otpguipreview`; global, group, and user selection |
| Premium emojis/button colors | Centralized legacy country/app/UI custom-emoji ID catalogs; all bot-facing icons pass through HTML-safe `<tg-emoji>` rendering; unknown/missing icons use a named custom-ID placeholder; raw Bot API `icon_custom_emoji_id`, `style`, and `copy_text` |
| OTP extraction | Multilingual and formatted-code rules, plus tenant custom RE2 capture patterns |
| Deduplication | Provider-record-aware keys and PostgreSQL uniqueness |
| Multi-panel support | Login, CR API, reseller/legacy API, WebSocket, Socket.IO/IVAS, AxonSMS ASP and AugesTel; encrypted accounts, diagnostics, queued tests/reconnect, service mappings and shared provider request budgets |
| Speed/reliability | Concurrent panel workers, durable ingest queue, atomic cursor advance, SKIP LOCKED delivery workers, retry/backoff |
| First OTP consumes number | Application-specific stock and consumption; valid delivered OTPs match service and original assignment time; concurrent replays cannot credit twice |
| 20-minute re-add | Expiry scheduler returns no-OTP numbers and excludes the previous user for 24 hours |
| Daily rewards | Global and per-user replacement schedules; cumulative milestones and Asia/Karachi logical day reset |
| Earnings/referrals/withdrawals | PKR/USDT earnings and immutable ledger; encrypted JazzCash/Easypaisa/Binance/BEP20 accounts; balance-aware request wizard, owner notification, holds/refunds, and admin approval |
| User experience | Styled compact/full dashboards, dynamic inline service/country picker, stats, history pagination, profile, leaderboard, settings, help, FAQ-style guide |
| Force join | Tenant-scoped required chats and styled verification controls |
| Admin dashboard | Full styled inline navigation and guarded actions for durable multi-app inventory uploads, panels, groups, rewards, users/tiers, bots, tutorials, withdrawals, admins, required chats, patterns, broadcasts, settings, and analytics |
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

## Telegram interaction repair

Polling explicitly subscribes to messages and callback queries for main and child bots, replacing a saved Telegram filter that excluded inline buttons. `/admin` and the Admin Dashboard button share a permission-filtered dashboard. Navigation edits the current message and falls back to a replacement if it was deleted or cannot be edited.

Groups, rewards, admins, required chats, tutorials, broadcasts, tier expiry, OTP patterns, child-bot requests, webhooks, schedules, and API-key management now use persistent guided forms with validation, review, Back, and Cancel. Existing commands remain supported. Inventory buttons use stable service/country identifiers; old positional buttons request reselection. Confirmed forms are claimed once before execution, and secrets are encrypted and omitted from review screens.

Tests exercise Telegram request rendering, menu routing, membership and permission gates, restart recovery, expiry, and concurrent form claims with a fake Telegram API and an isolated PostgreSQL database. Live Telegram client appearance still depends on the client's support for premium styling.

## Expanded GUI repair (2026-09-27)

- Panel metadata is separate from encrypted account connections; migrated IDs/cursors are retained.
- IVAS supports authenticated Socket.IO and normal portal session renewal; provider challenges require manual renewal and are never bypassed. Live delivery depends on an accessible authenticated provider stream.
- Durable text/photo/video broadcasts include preview, audience selection, confirmation, progress, cancellation and recent jobs.
- Stats period filters, persistent settings, child-bot filters/pagination/retry and topic help are implemented.
- Named UI emoji assets resolve against a Telegram metadata fixture; unmapped glyphs retain Unicode fallbacks.
- PostgreSQL integration tests cover the forms, credential encryption, pending access, recipient snapshots, interrupted sends and ledger statistics. Visual validation on actual desktop/mobile Telegram clients is still manual.

### Telegram UI and activity update — 2026-09-28

- Premium broadcast entities are retained from raw updates through preview and delivery, including media captions and UTF-16 offsets.
- Semantic animated action icons, consistent styles, rich Help articles and ranking tables with classic fallback.
- Permission-scoped command menus for main and child bots; guided command entry points.
- Private live OTP assignments/history, anonymous bot-specific app/country counts, complete zero-count catalog and app breakdowns.
- Favorites, repeat selection, opt-in availability watches, persistent notifications and shared OTP-priority Telegram rate limiting.

### Provider and group workflow update — 2026-09-28

- Explicit colors for every button, including Home/Back and emoji fallback, with visually checked contextual V2 assets.
- Receiver-specific ephemeral group commands and menus, DM handoffs for sensitive workflows, and manual anonymous activity tables.
- Same-phone inventory across apps, service-aware OTP consumption/history, release confirmation and configured cooldowns.
- My Numbers, catalog search, filtered history, muted availability alerts and timezone-aware quiet hours.
- Multi-app upload previews; AugesTel range-filtered number imports preserve bot PKR/USD prices.
- Bearer-only ASP/AugesTel connectors, delivered-only AugesTel ingestion, stable record deduplication and resumable bounded backfill.
- Distributed key/IP request budgets, provider errors/backoff, reconnect/rotation diagnostics, service mappings and unresolved-message replay.
- Migration preservation, provider HTTP/Socket.IO contracts, concurrent rewards, ownership, private group rendering and admin workflow regression tests.

### Import and interface update — 2026-09-29

- Fixed PostgreSQL parameter inference for 64-bit Telegram creator/audit IDs, including the reported 10,000-number import failure.
- Direct and guided TXT/CSV uploads share encrypted cached input, app search/pagination, one editable selector, pricing, preview, durable confirmation, receipts, retry and cancellation through `/imports`.
- PostgreSQL COPY and one publication transaction handle 10,000 phones across 20 apps without partial inventory, profile or catalog writes. Existing assignments and app-specific consumption remain intact.
- Leases recover queued/interrupted work after restart; navigation/cancellation remain responsive during publication, and a revoked lease prevents commit.
- HTTP/HTTPS login panels; generic ASP SMS API/IPRN REST API names, custom hosts/ports/paths and provider-scoped budgets; one Live SMS Stream category with IVAS, Socket.IO and advanced plain WebSocket connection methods.
- Shared native table cards and animated semantic icons with colored Home/Back/Cancel actions. Help and tutorials retain rich actions and classic fallback. The ten existing OTP themes, delivery formatters and theme previews are preserved.

### Connection, backup and child-feed update — 2026-09-30

- Login panels inspect their actual form fields, cookies and redirects, renew expired sessions and verify an SMS list before enabling ingestion. A manual sign-in challenge remains a provider access requirement.
- CR and reseller requests use their respective date parameters, bounded connection tests and validated response envelopes.
- Main-admin encrypted multipart user backups provide upload, preview and idempotent restore. Current balances and history stay current; recovered active assignments are closed and old OTPs are not delivered.
- Broadcast audience preparation runs in bounded background batches, with interactive bot actions and OTP delivery prioritized while bulk sending continues.
- Upload Numbers can add several custom apps with separate emoji IDs directly in the app selector.
- Child bots see main source templates but use separate credentials. Main OTP sharing is off by default and requires main-admin activation for new events matching a child's active number and app assignments.
- Data displays retain rich tables. Welcome messages, prompts, Help and tutorials use readable prose; OTP theme bodies are unchanged.
- Imported the complete public CrackSMS V2 UI ID map as optional `v2_` aliases while retaining the existing app/country IDs and curated Go button icons.
- Added a three-step limited daily reward form: OTP target, number of users, then PKR/USD amount. A row-locked award rule limits concurrent winners and credits each qualifying user once.
