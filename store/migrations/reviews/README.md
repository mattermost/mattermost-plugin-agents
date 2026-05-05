# Agents Plugin — Release Migration Review (000001 → 000007)

This release introduces the agents-plugin schema from scratch. All seven migrations target plugin-owned tables under the `Agents_*` and `LLM_*` prefixes; none touch core Mattermost tables.

**Migration runner:** morph postgres driver, configured in `store/migrate.go`:
- Tracking table: `Agents_DB_Migrations`
- Cluster mutex (Mattermost API) plus morph's internal PG advisory lock
- 300-second statement timeout

## Per-migration reviews

| # | Description | Severity | Detail |
|---|-------------|----------|--------|
| 000001 | Create `Agents_System` | ✅ Clean | [000001_create_system_table.md](000001_create_system_table.md) |
| 000002 | Create `LLM_PostMeta` + drop legacy `LLM_Threads` FK | ✅ Clean (down doesn't restore the FK — acceptable) | [000002_create_post_meta_table.md](000002_create_post_meta_table.md) |
| 000003 | Create `Agents_ConfigHistory` | ✅ Clean | [000003_create_config_history_table.md](000003_create_config_history_table.md) |
| 000004 | Create `LLM_CustomPrompts` + `LLM_CustomPromptPins` | ✅ Clean | [000004_create_custom_prompts_tables.md](000004_create_custom_prompts_tables.md) |
| 000005 | Create `Agents_UserAgents` | ✅ Clean | [000005_create_user_agents_table.md](000005_create_user_agents_table.md) |
| 000006 | Add 8 columns to `Agents_UserAgents` + UPDATE | ⚠️ Redundant UPDATE | [000006_user_agent_bot_fields.md](000006_user_agent_bot_fields.md) |
| 000007 | Create `LLM_Conversations` / `LLM_Turns`, drop `LLM_PostMeta` | ⚠️ Title-backfill UPDATE is structurally a no-op; existing titles are silently dropped | [000007_create_conversations_table.md](000007_create_conversations_table.md) |

## Cross-cutting observations

### Lock and locking risk
None of these migrations risks blocking production traffic. All new tables are created from scratch in the same transaction as their indexes; the only existing-table operations are:
- `ALTER TABLE … DROP CONSTRAINT` on `LLM_Threads` (000002) — brief metadata-only ACCESS EXCLUSIVE.
- `ALTER TABLE … ADD COLUMN` (eight columns, single statement) on `Agents_UserAgents` (000006) — metadata-only on PG11+ because all defaults are constants.
- `UPDATE` on `Agents_UserAgents` (000006) — bounded by admin-managed row count.
- `DROP TABLE LLM_PostMeta` (000007).

No `ALTER COLUMN TYPE`, no `CONCURRENTLY` (and none required), no foreign-key adds, no full-table DML against any large table.

### morph:nontransactional
None of the seven migrations needs `-- morph:nontransactional`, and none uses it. Each file runs cleanly inside a transaction. The morph engine in `store/migrate.go` is the standard postgres driver with no special configuration needed for these files.

### Backwards compatibility
ESR compatibility is not directly relevant — these are plugin-owned tables, not Mattermost core schema. The relevant compatibility surface is *plugin version vs. plugin schema*. After migration 7, older plugin binaries that still reference `LLM_PostMeta` will fail; the plugin's minimum-version metadata should reflect that.

### Down migrations
All seven `.down.sql` files exist and are reasonable. Two are partial:
- 000002 down does not restore the dropped legacy FK (acceptable — re-adding a FK on rollback would itself be unsafe and the FK was undesired).
- 000007 down recreates `LLM_PostMeta` empty; the original rows are gone (consistent with the up migration's actual behavior, but see the title-backfill bug below).

## Issues worth addressing before release

1. **000007 title-backfill bug** (functional, not safety). The UPDATE that purports to copy `Title` from `LLM_PostMeta` into `LLM_Conversations` runs against a freshly-created (empty) `LLM_Conversations`, so it matches zero rows. `DROP TABLE LLM_PostMeta` then discards the source. Existing installs with stored titles will silently lose them. See `000007_create_conversations_table.md` → "Backfill bug" for three suggested fixes (delete the UPDATE and document, do the backfill in a Go migration step, or split the DROP into a later release).
2. **000006 redundant UPDATE.** The UPDATE writes the same values that the new column defaults already set. Harmless on a small admin-managed table but worth removing or commenting if there's a reason it must stay.
3. **No FK enforcement** on logical references (`LLM_Turns.ConversationID` → `LLM_Conversations.ID`, `LLM_CustomPromptPins.PromptID` → `LLM_CustomPrompts.ID`). Consistent with project convention but means orphan cleanup is application-layer; verify the relevant delete paths cover this.

## Large-dataset testing
Not recommended for any of these migrations in isolation. All affected tables are either brand-new or admin-managed and bounded. The one functional test that *would* be valuable is upgrading a fixture install that has rows in `LLM_PostMeta` and confirming whether titles survive — that's the open item from issue (1) above.
