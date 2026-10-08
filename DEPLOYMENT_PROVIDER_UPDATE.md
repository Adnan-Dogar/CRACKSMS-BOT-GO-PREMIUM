# Provider and group workflow release

Deployed on 2026-09-28. Release: `20260928T102836Z`.

The later import and interface release is documented in [DEPLOYMENT_IMPORT_UPDATE.md](DEPLOYMENT_IMPORT_UPDATE.md).

## Verification

- Full Go suite passed with an isolated PostgreSQL database across 14 packages.
- The full race suite passed across 14 packages, including 161 reported tests/subtests. Final storage/UI refinements passed their additional race checks.
- `go vet ./...` and `git diff --check` passed.
- Migration tests preserve existing assignment IDs, queue cursors, balances, ledger entries and encrypted accounts; the same phone can then be inserted for a second app.
- Provider contracts cover bearer placement, bounded pagination, stable deduplication, shared request budgets, 401/403/429 handling, Socket.IO authentication/heartbeats, CSRF/cookie login and manual challenge renewal.
- Telegram tests cover actor ownership, ephemeral navigation/fallback, group/DM flow separation, command registration, multi-app imports, credential rotation and existing premium broadcasts.
- Production `/healthz` and `/readyz` passed after migration 007. The container runs without restarts or startup warnings/errors.
- Telegram confirms 33 private commands, 33 ephemeral group commands and 72 admin commands. The new daily and provider tools appear in their proper scopes.

## Provider activation

Create a panel through Admin → Panels → Add Panel, then select it → Add Account. Add the scoped AxonSMS ASP token or AugesTel API key there. AugesTel number imports offer a range filter and explicit app/country mapping; AxonSMS publishes SMS retrieval only.

IVAS's supplied bridge hostname did not resolve from this server. The normal IVAS login page returned HTTP 403. The Socket.IO connector and ordinary session-renewal logic pass fixture tests, but live IVAS SMS delivery could not be verified. A current reachable authenticated stream or provider-enabled portal session is required. Accounts that fail authentication or need manual session renewal remain disabled, with diagnostics and reconnect controls.

## Backup and rollback

Pre-migration backup: `legacy-backup/pre-providers-schema7-20260928T102836Z.dump` (144315 bytes, mode 0600). Restoring it into an isolated PostgreSQL database succeeded and preserved schema version 6, all 3 users and existing inventory/ledger counts.

Deployed image: `cracksms-bot-go-premium-bot:providers-schema7-20260928T102836Z`.

Schema-compatible baseline: `cracksms-bot-go-premium-bot:providers-schema7-baseline-20260928T102836Z`. This tested baseline includes migration 007 and service-aware stock/credit logic, before the final table styling and provider-type cursor refinements. It can run against the current database without restoring data.

To select it, add this Compose override and start the bot with `--no-build`:

```yaml
services:
  bot:
    image: cracksms-bot-go-premium-bot:providers-schema7-baseline-20260928T102836Z
```

```bash
docker compose -f docker-compose.yml -f rollback.yml up -d --no-deps --no-build bot
```

The older schema-6 image is retained as `cracksms-bot-go-premium-bot:before-providers-schema6-20260928T102836Z`. Its number queries require the old uniqueness constraint; it must only be used with a coordinated restore of the pre-migration database. Such a restore discards writes made after the backup.
