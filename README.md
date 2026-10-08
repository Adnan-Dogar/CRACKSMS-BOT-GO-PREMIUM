# CrackSMS vNext

Modular Go rewrite of the authorized SMS/OTP management bots. The service uses PostgreSQL as its source of truth, persistently deduplicates provider events, queues Telegram deliveries, credits cumulative daily rewards exactly once, and safely recycles no-OTP numbers.

The behavioral baselines are [CRACKSMSBOTV2](https://github.com/Adnan-Dogar/CRACKSMSBOTV2), [CRACKSMSBOT](https://github.com/Adnan-Dogar/CRACKSMSBOT), and [OTP-BOT-PRO](https://github.com/Adnan-Dogar/OTP-BOT-PRO). vNext preserves their user-facing ideas while separating Telegram, panels, OTP processing, delivery, rewards, storage, scheduling, security, and monitoring into modules.

## Implemented behavior

- Dynamic OTP destination groups with independently configurable buttons.
- Per-group OTP display (`visible`, `masked`, or `hidden`), button mode, and theme override. All group themes mask the destination number, including occurrences in SMS text and Full Message copy buttons. The full code remains available through an enabled copy button; Minimal requires buttons to access the code.
- Ten complete OTP themes (Classic, Minimal, Developer, Electric, Tech, Premium, UltraMinimal, Business, Social, and Deluxe) with animated country/service detection.
- The original CrackSMS animated country-flag, service/app, and UI custom-emoji ID catalogs are ported. Bot-facing emoji are never emitted as plain UI icons: the HTML-safe renderer emits `<tg-emoji emoji-id="…">fallback</tg-emoji>` in bot UI, OTP, broadcast, daily-summary, tutorial, and scheduled HTML messages without nesting existing custom emoji or modifying code blocks.
- Verified UI IDs and clearly marked replacement keys are centralized in `internal/premium/custom_emoji_ids.go`; country/app catalogs remain in `internal/premium/catalog.go`. Unknown emoji also use a valid custom-emoji placeholder rather than remaining plain Unicode.
- Shared premium button builders carry Telegram `primary`, `success`, `danger`, `copy_text`, and `icon_custom_emoji_id` fields across user/admin/theme interfaces.
- Styled compact/full menus, dynamic service/country selection, and permission-gated admin submenus cover panels, groups, inventory, rewards, users/tiers, withdrawals, admins, required chats, patterns, child bots, tutorials, settings, analytics, and broadcasts.
- The first valid deduplicated OTP consumes its assigned application/number entry and counts once for earnings/rewards. The same phone can belong to several applications independently.
- Numbers without an OTP return to the same service/country after 20 minutes.
- A returned number is immediately available to other users but excluded from its previous user for 24 hours.
- Global cumulative daily reward milestones with per-user replacement schedules.
- Normal service/country PKR earnings plus milestone bonuses and an immutable balance ledger.
- Token API, legacy/reseller API, HTTP/HTTPS login/cookie, Live SMS Stream, generic ASP SMS API and IPRN REST API adapters with supervised workers. The Add Panel wizard separates panel metadata from encrypted accounts, tests each connection, and saves failed accounts disabled for later correction. Live SMS Stream offers IVAS Account, Socket.IO URL and an advanced plain WebSocket method; existing account types and cursors remain compatible.
- Persistent Telegram delivery jobs, retry/backoff, rate limiting, and destination health.
- Durable panel-ingest jobs: a successful panel cursor is committed atomically with its events, so restarts cannot lose the gap between polling and OTP processing.
- User accounts, balances, referrals, withdrawals, leaderboard, force-join chats, broadcasts, admins, inventory imports, panel health, and daily summaries. Withdrawals support encrypted saved JazzCash, Easypaisa, Binance, and USDT BEP20 accounts, available-balance checks, confirmation, immediate owner/admin notification, approval, rejection, and atomic refund.
- Direct `.txt`/`.csv` uploads and Upload Numbers share one workflow: upload once, select up to 20 apps, configure PKR/USD earnings, preview and confirm. A single editable selector offers search, pagination, Select Page and Clear. Each app receives independent stock; all selected apps are published atomically by a durable worker. `/imports` provides receipts, retry and cancellation.
- Interface cards use native Telegram tables, animated heading/action icons and explicit button colors, with classic fallback. OTP themes retain distinct layouts; Minimal shows one line with the app icon, flag and masked number, plus a dots-only OTP copy button and community links. Authored broadcast entities remain intact.
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
2. Run the all-in-one installer and enter the BotFather token plus initial Telegram admin ID when prompted:

   ```bash
   chmod +x install.sh
   ./install.sh
   ```

   The installer can provision supported Linux host packages, generates the PostgreSQL, metrics, and AES-256 secrets, writes `.env` with mode `0600`, builds and starts the containers, runs migrations automatically, and waits for database/bot readiness. Existing secrets are preserved; use `./install.sh --reconfigure` to update settings with an automatic `.env` backup.

   For unattended deployment, provide at least `BOT_TOKEN` and `INITIAL_ADMIN_IDS`:

   ```bash
   BOT_TOKEN='BotFather-token' INITIAL_ADMIN_IDS='123456789' \
     ./install.sh --non-interactive
   ```

3. Manual setup remains available: copy `.env.example` to `.env` and set all values. Generate secrets with:

   ```bash
   openssl rand -base64 32   # PANEL_CONFIG_KEY
   openssl rand -hex 32      # METRICS_TOKEN and POSTGRES_PASSWORD
   ```

4. Start PostgreSQL and the bot:

   ```bash
   docker compose up --build -d
   docker compose logs -f bot
   ```

5. Add the first OTP destination from the initial admin account:

   ```text
   /addgroup -1001234567890|buttons|Main OTP Group
   ```

Use `nobuttons` for a text-only destination.

## Telegram menus

Send `/start` to open the compact menu and **More Options** for the full menu. Admins can use `/admin` or **Admin Dashboard**. Management buttons launch guided forms; enter each requested value, review it, and press **Confirm**. **Back** revisits a field; **Cancel**, **Main Menu**, and `/cancel` exit the form. Forms expire after 30 minutes and survive service restarts.

If you see an inventory menu from before this update, select the service again. Current inventory buttons remain valid when catalog ordering changes. Premium icons have a plain fallback when Telegram rejects their formatting.

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

Upload a `.txt` or `.csv` directly in the admin private chat, or open **Admin → Numbers → Upload Numbers**. Select apps, enter `PricePKR|PriceUSD|NumbersPerCycle` or choose defaults, review each app's counts and confirm. The bot auto-detects country per number. **Other App** asks for the app name and numeric Telegram custom emoji ID. Confirmation updates catalog prices for the selected apps/imported countries while preserving existing stock and assignments; provider-page imports keep existing bot prices.

The input is downloaded once and cached with encryption. Drafts expire after 30 minutes of inactivity; queued/failed uploads are retained for up to seven days. Completed jobs discard the input and retain a receipt. Imports survive restarts through leases, and duplicate confirmations create one job. Use `/imports` to review, retry a failed import or cancel preparation. If final publication has already committed, the completed receipt remains available.

The legacy caption remains supported:

```text
/addnumbers WhatsApp|Pakistan|PK|1.00|0|3
```

Files may contain one phone per line, delimited phone lists or CSV headers such as `phone`, `number`, `phone_number` or `msisdn`. When a phone column is present, other columns are ignored. Files are limited to 10 MB. Preview and receipts report invalid entries and duplicates.

**ASP SMS API** and **IPRN REST API** work with compatible providers on custom HTTPS hosts and ports. Enter a root URL to use `/api/sms` or `/api/v1/iprn`, or enter a full custom API path. Review shows the exact endpoints before saving; scoped bearer tokens belong in Add Account. API request budgets are shared by normalized provider origin and credential fingerprint. Login panels accept HTTP or HTTPS and allow same-host HTTPS upgrades; bearer APIs require HTTPS and stream credentials require WSS.

## Custom emoji IDs

All supplied UI custom emoji IDs are centralized in `internal/premium/custom_emoji_ids.go`; `ReplacementEmojiKeys()` now reports no unresolved entries. Exact compound variants such as artist, airplane, envelope, lightning, calendar, inbox, and alert emoji are mapped explicitly instead of falling through to a generic asset. Handlers and menus do not contain duplicated IDs.

Built-in country and service IDs are populated separately in `internal/premium/catalog.go`. A custom service created through **Other App** stores the ID entered by the admin and uses it in Get Number buttons and OTP messages.

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

### Panels, accounts and broadcasts

Admin → Panels → Add Panel saves a name, provider type and credential-free link. Select the saved panel → Add Account to enter an account label and username/email/password or API token. Credentials and unfinished forms are encrypted; password/token messages are deleted after capture. The account is saved disabled and tested in the background; it is enabled after the SMS endpoint is verified. An account keeps its existing ingestion ID and cursor when credentials are edited. Panel link edits apply to new accounts; reconnect an existing account through Edit Credentials.

IVAS accounts prefer a current authenticated Socket.IO URL. The URL is split into a credential-free WSS endpoint and encrypted token/user fields. Engine.IO v4 connection, namespace authentication, heartbeat and actual SMS receipt have distinct diagnostics. Normal portal login preserves cookies/CSRF fields and can renew an expired session when credentials are configured. If the provider presents a challenge or does not expose a stream URL, the account stops and requests manual session renewal through Edit Credentials. No challenge evasion is implemented. Connectivity to a supplied bridge is a separate requirement from the connector's protocol tests.

AxonSMS ASP uses only its scoped API token in `Authorization: Bearer …` with bounded `/api/sms` pagination, UTC date windows and optional service/sender/number/search/range filters. It has no published number-inventory endpoint. AugesTel supports assigned-number imports, message history and earnings/statistics. Its five-requests-per-minute key budget and source-IP budget are shared in PostgreSQL across polling, connection tests, imports, statistics and child bots; lower observed limits and `Retry-After` delays are respected. Provider payouts are stored separately from bot reward rates. Failed AugesTel deliveries never consume stock or earn credits.

Account → Diagnostics & Tools provides reconnect, encrypted credential rotation, service/sender/message/range mappings, an unmapped SMS inbox and replay. Unresolved app names are held instead of assigning their OTP to an arbitrary application. Replay preserves provider timestamps and cannot credit a later assignment. AugesTel imports include a range filter, app/country mapping and preview; existing PKR/USD bot prices remain intact. Backfill checkpoints commit with the durable ingestion queue, with fresh-page polling interleaved during pagination.

`/mynumbers`, `/search`, filtered history and `/notifications` provide personal number tools. Releasing an unconsumed number affects only its app and applies the configured previous-user cooldown. Availability alerts support mute and timezone-aware quiet hours; direct OTP delivery remains immediate. Number uploads can select several applications and preview invalid, repeated and existing entries before confirmation.

Personal group menus use [Telegram ephemeral messages](https://core.telegram.org/bots/api#ephemeral-messages-and-commands), with receiver and topic metadata preserved across navigation and rendering fallback. Native group commands are ephemeral; ordinary commands require the bot to be a group administrator for private replies. Unsupported private rendering offers a DM handoff without publishing personal content. Automatic live OTP sessions, credentials, withdrawals and admin forms open in private chat. Group activity tables refresh on demand and do not replace a user's private live session. Every button has an explicit color; removing unavailable emoji styling preserves that color. Context-specific phone/home/back/chart/settings assets come from the verified V2 catalog.

Migration 007 preserves number IDs, assignments, balances and ledger references while changing stock uniqueness to normalized phone plus application. Roll back using an image built with the service-aware inventory queries. An older image using `ON CONFLICT(normalized_phone)` is incompatible with this schema; using it requires coordinated database restoration from the pre-migration backup, which discards subsequent writes.

Broadcast accepts formatted text, photos and videos. A lossless update adapter retains original custom emoji IDs and UTF-16 entity offsets for both text and captions. Premium emojis survive preview, encrypted drafts, recipient snapshots and queued delivery. Invalid emoji formatting fails before confirmation; it is never silently downgraded to Unicode. It previews the original content, asks for an audience and confirmation, then prepares the recipient snapshot in bounded background batches. Help, Cancel, OTP delivery and other bot actions remain available during preparation and sending. Delivery survives restarts, respects rate-limit retries, and reports sent/failed/skipped/unconfirmed counts. Cancel stops pending recipients; a request already in flight may finish. An interrupted request is marked unconfirmed rather than resent automatically. Recent broadcasts are available from the broadcast screen. `/broadcast message` uses the same preview and confirmation workflow.

Statistics offer Today/7 Days/30 Days/All Time, tenant-scoped event counts and ledger-backed earnings. Inventory is labeled as shared; daily rewards remain account-wide under the existing billing model. Settings persist menu mode, IANA timezone, group/channel/number-bot links, assignment limits and new-group privacy. Child-bot administration includes status filters, pagination and retry controls. Help uses topic screens and action buttons.

UI emoji IDs were checked against Telegram `getCustomEmojiStickers` on 2026-09-27. The metadata fixture and test require every named UI asset to resolve and animate. Unmapped text emoji retain their Unicode appearance; screen rendering falls back to plain emoji if Telegram rejects custom assets. Actual animation appearance still depends on Telegram clients and the bot account’s custom-emoji permissions.

### Live OTP, activity and user tools

`/liveotp` opens your own assignments and recent codes, with copy buttons. Anonymous activity is scoped to the current bot and counts unique accepted events containing an OTP; it does not expose other users' codes, numbers or messages. Credited usage remains separate. Top Apps and Top Countries support 1h/24h/7d/30d windows (24h default). All Countries lists the complete configured and historical catalog, including zero counts, with ten entries per page and app breakdowns. Unknown metadata uses neutral icons.

Live screens edit changed content at most once every five seconds, provide Refresh/Pause/Resume, and pause after ten minutes or when you navigate away. Restarted sessions remain paused until explicitly resumed. Settings offers Auto/Rich/Classic rendering. Rankings and multi-field data use native Telegram rich tables; welcome screens, prompts, Help and tutorials use prose and actions, with independently rendered classic fallbacks. See [Telegram rich messages](https://core.telegram.org/bots/features#rich-messages).

### User backups and child sources

Main administrators can use `/backupusers` or Admin → User Backups to export an encrypted `.csbackup` archive. Large archives arrive in numbered parts. `/restoreusers` accepts every part in any order, shows a preview, then asks for confirmation. The archive includes users, membership, safe preferences and account history across existing bot instances; it excludes panel credentials, API keys and bot tokens. Restore keeps current balances and history, fills missing users and settings, and closes archived active assignments. It never delivers archived OTPs. The deployment must retain the same `PANEL_CONFIG_KEY`, and the destination must have matching bot instances.

Main panel source templates appear in child bots. A child can select one and add its own encrypted account; the main account credentials are never copied. Main OTP sharing starts disabled for each child. A main administrator may enable it from Child Bots. Only new OTPs matching that child's active number and application assignments are copied. Turning sharing off cancels pending shared deliveries.

Number upload has **Add App** in the application selector. Enter a distinct app name and a custom emoji ID, then select more apps or continue to pricing. Each selected app gets its own inventory and emoji profile after successful import.

Admin → Rewards → Limited Reward asks for the daily counted OTP target, the number of qualifying users, then the amount and currency. For example, `1000`, `5`, `2 USD` pays the first five users who each reach 1,000 counted OTPs in a day $2 once. The award is recorded in the USD ledger and balance. A limited reward replaces the current global daily schedule; individual user overrides still take precedence. `/limitedreward 1000|5|2 USD` is the command form.

The exact 37 CrackSMS V2 UI emoji IDs are retained as `v2_` aliases alongside the existing country, app and visually checked Go UI catalogs. Those aliases can be selected without replacing the current semantic button icons.

`/favorites` saves up to 20 app/country selections. `/lastselection` offers your last successful selection for reacquisition. `/alerts` manages up to 10 opt-in availability watches. The worker checks every 30 seconds, respects personal number exclusions, sends on unavailable-to-available transitions with a 30-minute cooldown, and rechecks inventory and membership before delivery. Alerts never reserve numbers. Unsubscribe by removing a watch.

The main and child bots register supported commands and the native command menu at startup. Private administrator menus include only authorized commands and are refreshed on permission changes. Commands needing input open guided workflows.

Set `TELEGRAM_RICH_SCREENS=false`, `TELEGRAM_LIVE_REFRESH=false`, or `TELEGRAM_AVAILABILITY_ALERTS=false` to independently pause the respective capability; all default to enabled. The shared per-bot limiter observes Telegram retry delays and reserves capacity for OTP delivery. Availability notifications use a durable queue; interrupted sends are marked uncertain rather than duplicated.

On 2026-09-28, primary menu assets were inspected visually as well as checked for animation. Get Number uses an actual phone, Home a house, Back an arrow, and application logos stay with their corresponding apps. The V2 emoji catalog supplies the decorative themes and semantic action icons. Physical appearance on each Telegram client still requires client-side review. Previously saved broadcasts with stripped custom emoji IDs must be recreated.
