# Import and interface release

Deployed on 2026-09-29. Release: `20260929T104139Z`.

## Changes

- Corrected 64-bit Telegram creator/audit parameter handling that caused the reported number import to fail.
- Direct TXT/CSV uploads and Upload Numbers use cached encrypted input, one editable app selector, search, pagination, Select Page/Clear, pricing and per-app preview. Up to 20 apps receive independent inventory.
- Migration 008 adds durable import jobs. COPY staging and one transaction publish all app inventories, profiles and catalog changes together. Confirming twice creates one job. Queued/interrupted jobs recover through leases; failed jobs offer retry and cancellation through `/imports`.
- Navigation, cancellation and lease renewal remain responsive during preparation. Cancelling or replacing a lease prevents stock publication. Existing assignments and consumed entries are preserved.
- HTTP/HTTPS login panels, generic ASP SMS API/IPRN REST API names, custom HTTPS domains/ports/API paths and provider-scoped request budgets. Endpoint review uses the adapter's actual URL selection.
- One Live SMS Stream category: IVAS Account, Socket.IO Stream URL and advanced plain WebSocket. Existing kinds, encrypted accounts and cursors remain compatible.
- Native table cards, animated semantic icons and explicit colors for interface buttons, including Home/Back/Cancel. Help/tutorials retain rich actions and classic fallback.
- The ten OTP themes, delivery formatter and theme-preview bodies remain unchanged. Authored premium broadcast entities retain their existing rendering path.

## Verification

- `go test ./...`, `go test -race ./...`, `go vet ./...` and `git diff --check` passed.
- PostgreSQL tests published 10,000 phones across two apps (20,000 entries) and 20 apps (200,000 entries). The latter publication took approximately 15 seconds in the isolated test database.
- An injected second-app failure rolled back inventory, profiles and catalog pricing; retry used the same saved input successfully.
- Concurrent cancellation during publication committed zero stock. Lease renewal and leaving the progress screen remained responsive.
- Telegram fixtures cover direct/captioned uploads, one download, empty selection, stale selector/cancel buttons, search/pagination/selection limits, duplicate confirmation, permission checks, worker restart and editable receipts.
- Provider fixtures cover generic custom endpoints on the wire, bearer placement, origin/token budget isolation, HTTP login redirects, existing pagination/backoff and Socket.IO handshake/heartbeat behavior.
- Migration preservation tests include 008 and retain account IDs, cursors, assignments, balances, preferences and ledger entries.
- The pre-release production dump restored successfully into an isolated PostgreSQL database. Migration 008 also applied to that restored copy, preserving all five users and the existing stock/ledger counts.
- Production applied migration 008 and reports HTTP 200 on `/healthz` and `/readyz`. The non-root, read-only bot container has zero restarts; startup logs contain zero warnings/errors.
- Live Telegram command lookups confirm 33 private commands, 33 ephemeral group commands and 73 admin commands, including `/imports` in the admin scope. No test messages were sent to real users.
- SHA-256 checks confirm the OTP theme definitions/tests and delivery formatter match their pre-release contents.

## Backup and rollback

Backup: `legacy-backup/pre-imports-schema8-20260929T104139Z.dump` (157605 bytes; mode 0600).

Release image: `cracksms-bot-go-premium-bot:imports-schema8-20260929T104139Z`.

Previous image: `cracksms-bot-go-premium-bot:before-imports-schema7-20260929T104139Z`.

Migration 008 is additive. The previous schema-7 binary can run with the new table present and retains the service-aware inventory model. Its UI and workers do not process new durable imports; those records remain for the updated release to resume. A binary rollback does not require restoring the database.

To roll back the binary, use this Compose override:

```yaml
services:
  bot:
    image: cracksms-bot-go-premium-bot:before-imports-schema7-20260929T104139Z
```

```bash
docker compose -f docker-compose.yml -f rollback.yml up -d --no-deps --no-build bot
```

## Provider activation

Use Admin → Panels → Add Panel, then Add Account. ASP/IPRN integrations need a scoped token from the compatible provider. Login panels accept HTTP or HTTPS; APIs use HTTPS and stream credentials use WSS.

Live IVAS SMS delivery still requires a reachable authenticated provider stream or an approved portal session. The earlier supplied bridge was unreachable from this server and the portal returned a challenge; this release does not establish a live IVAS delivery result. Protocol/session behavior is covered by fixtures.

Drafts expire after 30 minutes of inactivity. Failed/queued encrypted input is retained for up to seven days; successful jobs discard it and retain their receipts. Actual Telegram desktop/mobile appearance depends on client support for native tables, premium icons and button colors; classic fallback remains available.
