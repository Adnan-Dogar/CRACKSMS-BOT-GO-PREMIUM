# CrackSMS vNext

Modular Go rewrite of the authorized SMS/OTP management bots. The service uses PostgreSQL as its source of truth, persistently deduplicates provider events, queues Telegram deliveries, credits cumulative daily rewards exactly once, and safely recycles no-OTP numbers.

The behavioral baselines are [CRACKSMSBOTV2](https://github.com/Adnan-Dogar/CRACKSMSBOTV2), [CRACKSMSBOT](https://github.com/Adnan-Dogar/CRACKSMSBOT), and [OTP-BOT-PRO](https://github.com/Adnan-Dogar/OTP-BOT-PRO). vNext preserves their user-facing ideas while separating Telegram, panels, OTP processing, delivery, rewards, storage, scheduling, security, and monitoring into modules.

## Implemented behavior

- Dynamic OTP destination groups with independently configurable buttons.
- Per-group OTP privacy (`visible`, `masked`, or `hidden`), button mode, and theme override. Visible mode keeps the earlier full-code behavior; hidden mode redacts the code from both the body and buttons.
- Ten complete OTP themes (Classic, Minimal, Developer, Electric, Tech, Premium, UltraMinimal, Business, Social, and Deluxe) with animated country/service detection.
- The original CrackSMS animated country-flag, service/app, and UI custom-emoji ID catalogs are ported with Unicode fallbacks.
- An HTML-safe renderer emits `<tg-emoji emoji-id="…">fallback</tg-emoji>` in bot UI, OTP, broadcast, daily-summary, tutorial, and scheduled HTML messages without nesting existing custom emoji or modifying code blocks.
- Shared premium button builders carry Telegram `primary`, `success`, `danger`, `copy_text`, and `icon_custom_emoji_id` fields across user/admin/theme interfaces.
- Styled compact/full menus, dynamic service/country selection, and permission-gated admin submenus cover panels, groups, inventory, rewards, users/tiers, withdrawals, admins, required chats, patterns, child bots, tutorials, settings, analytics, and broadcasts.
- The first deduplicated OTP for an assigned number consumes that number and counts once for earnings/rewards.
- Numbers without an OTP return to the same service/country after 20 minutes.
- A returned number is immediately available to other users but excluded from its previous user for 24 hours.
- Global cumulative daily reward milestones with per-user replacement schedules.
- Normal service/country PKR earnings plus milestone bonuses and an immutable balance ledger.
- Token API, legacy API, login/cookie, and WebSocket panel adapters with supervised workers.
- Persistent Telegram delivery jobs, retry/backoff, rate limiting, and destination health.
- Durable panel-ingest jobs: a successful panel cursor is committed atomically with its events, so restarts cannot lose the gap between polling and OTP processing.
- User accounts, balances, referrals, withdrawals, leaderboard, force-join chats, broadcasts, admins, inventory imports, panel health, and daily summaries.
- User statistics/history, compact and full dashboards, settings, tutorials/media, scoped admin permissions, and request throttling.
- Free/Pro/Enterprise subscriptions, panel limits (2/10/50), persistent signed webhooks, message scheduling, and rate-limited Enterprise API access.
- Encrypted child-bot requests, admin approval/tier selection, isolated bot/group/panel/admin configuration, runtime supervision, and automatic restart reconciliation.
- Built-in multilingual/formatted OTP extraction plus safe Enterprise custom RE2 patterns.
- Encrypted panel configuration using AES-256-GCM.
- Telegram premium custom-emoji IDs from all supplied baselines plus Bot API button styles (`primary`, `success`, and `danger`). Older Telegram clients may show normal fallback emoji/colors, while current clients receive the enhanced markup.
- Protected health/readiness/metrics endpoints and graceful shutdown.

The old repositories remain untouched and are used only as migration/behavioral references.

## First deployment

1. Rotate every token or credential that appeared in the legacy public repositories.
2. Copy `.env.example` to `.env` and set all values. Generate secrets with:

   ```bash
   openssl rand -base64 32   # PANEL_CONFIG_KEY
   openssl rand -hex 32      # METRICS_TOKEN and POSTGRES_PASSWORD
   ```

3. Start PostgreSQL and the bot:

   ```bash
   docker compose up --build -d
   docker compose logs -f bot
   ```

4. Add the first OTP destination from the initial admin account:

   ```text
   /addgroup -1001234567890|buttons|Main OTP Group
   ```

Use `nobuttons` for a text-only destination.

## Important admin commands

```text
/addgroup chat_id|buttons|title
/groups
/groupbuttons chat_id|on/off
/groupprivacy chat_id|visible/masked/hidden
/grouptheme chat_id|0-9/default
/groupenable chat_id|on/off
/removegroup chat_id

/setrewards global|30=30,100=30,200=50
/setrewards user_id|30=30,100=50
/rewards
/clearreward user_id

/addpanel kind|name|poll_duration|config_json
/panels
/paneltoggle id|on/off

/settheme 0-9
/settier user_id|free/pro/enterprise|YYYY-MM-DD
/patternadd name|regex_with_capture_group
/patterns
/patternremove id

/tutorialadd title|description|body
/tutorialremove id

/botinstances
/botapprove id|free/pro/enterprise
/botreject id|reason
/bottoggle id|on/off

/addrequired chat_id|title|https://t.me/link
/required
/removerequired chat_id
```

Users can access premium integrations with:

```text
/theme 0-9
/otpguipreview
/webhook add|https://example.com/otp|otp.received
/schedule add|2026-08-20 15:30|user:123/group:-100/all|message
/apikey create|integration-name
/createbot Bot Name|BotFatherToken
```

Child-bot token messages are deleted immediately, tokens are AES-256-GCM encrypted, and duplicate tokens are rejected by hash. Webhook secrets and API keys are displayed only once.

Panel configuration messages are deleted after processing and their JSON is encrypted before storage. Supported examples:

```text
/addpanel token_api|Provider A|2s|{"url":"https://provider/api","token":"...","api_type":"old","records":200}
/addpanel legacy_api|Provider B|2s|{"sms_url":"https://provider/sms"}
/addpanel login|Provider C|2s|{"base_url":"https://provider/ints","username":"...","password":"..."}
/addpanel websocket|IVAS|1s|{"url":"wss://provider/socket","token":"..."}
```

To import numbers, upload a plain `.txt` document with this caption:

```text
/addnumbers WhatsApp|Pakistan|PK|1.00|0|3
```

Each line or whitespace-separated item in the file is treated as one phone number. Duplicates and invalid values are skipped.

## Graceful legacy migration

The importer defaults to dry-run and never imports active holds or legacy OTP dedup history.

```bash
docker compose run --rm \
  -v /absolute/legacy/path:/legacy:ro \
  --entrypoint /usr/local/bin/cracksms-importer bot \
  --data /legacy/data.json --referral-db /legacy/referrals.db
```

Review the printed counts. Stop the legacy bot, back up PostgreSQL, then repeat with `--apply`. The importer makes a permission-restricted copy of the legacy inputs before writing.

Never run the legacy and vNext services with the same Telegram token at the same time.

## License

Released under the [MIT License](LICENSE).

## Monitoring and backups

- `GET /healthz` is a liveness check.
- `GET /readyz` checks PostgreSQL readiness.
- `GET /metrics` requires `Authorization: Bearer $METRICS_TOKEN`.
- Enterprise API keys authorize `GET /api/v1/me`, `/api/v1/analytics`, and `/api/v1/otp/history` (60 requests/minute per key).
- Webhooks require public HTTPS destinations, reject private/link-local addresses, sign payloads with HMAC-SHA256, and retry persistently.
- Back up PostgreSQL daily with `pg_dump`; test restoration before cutover.
- The container listens only on `127.0.0.1:8080` by default.
