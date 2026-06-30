---
description: Runtime license gating for enterprise features (no build tags).
tags: [enterprise, license, gating]
---

# enterprise/AGENTS.md

Runtime license gating via `LicenseChecker`. **No build tags** — this package is always compiled; it checks the Mattermost server license at runtime (development mode bypasses via `pluginapi.IsE20/E10LicensedOrDevelopment`). Don't assume compile-time separation.

- `IsMultiLLMLicensed()` gates more than one config bot (DB-backed user agents bypass this cap); `IsBasicsLicensed()` gates API handlers (thread/channel analysis) and embeddings init. Both currently require E20+. Errors with `ErrNotLicensed`.
- Licensed under the Mattermost Source Available License (`enterprise/LICENSE`), not Apache-2. **Excluded from license-header automation** (`scripts/fix_license_headers.sh` / `make copyright`) — don't add the standard header here.
