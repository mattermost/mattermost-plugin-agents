# Schema Migration Review: 000012 — Create Agents_ChannelInstructions and Agents_ChannelContextPosts

> **Context:** Two new plugin tables storing member-authored context for agents working in a
> channel. `Agents_ChannelInstructions` holds one row per channel that has instructions (no
> instructions is represented by row absence). `Agents_ChannelContextPosts` holds the posts
> pinned to a channel's agent context, capped by the application at a small number per
> channel. These pins are deliberately separate from Mattermost's own `Posts.IsPinned`, so
> pinning a post for agents never changes the channel's pinned messages and vice versa. Both
> tables are empty on creation and grow only through channel-manager action. Both are read by
> primary key (`ChannelID`, or the `(ChannelID, PostID)` prefix) when an agent request is built
> for a channel; there is no in-memory cache.

## Schema Changes
- [x] New table(s): `Agents_ChannelInstructions` (`ChannelID VARCHAR(26) PRIMARY KEY`,
  `Instructions TEXT NOT NULL`, `UpdatedBy VARCHAR(26) NOT NULL`, `UpdateAt BIGINT NOT NULL`);
  `Agents_ChannelContextPosts` (`ChannelID VARCHAR(26) NOT NULL`, `PostID VARCHAR(26) NOT NULL`,
  `PinnedBy VARCHAR(26) NOT NULL`, `PinnedAt BIGINT NOT NULL`, `PRIMARY KEY (ChannelID, PostID)`)
- [ ] New column(s): —
- [ ] New index(es): — (only the implicit primary-key indexes; per-channel lookups use the
  `ChannelID` prefix of the composite key)
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | Only CREATE TABLE. |
| CREATE INDEX uses CONCURRENTLY | N/A | No explicit indexes; PK indexes are created atomically with the tables. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | No FKs. (`ChannelID`, `PostID`, `UpdatedBy`, and `PinnedBy` logically reference core tables, but are not enforced — consistent with project convention.) |
| No full-table DELETE/UPDATE | ✅ | No DML. |
| morph:nontransactional where needed | N/A | Pure transactional DDL. |
| Down migration exists | ✅ | Drops both tables with `IF EXISTS`. |
| Transactional/nontransactional split correct | ✅ | Two transactional CREATE TABLE statements. |

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned tables; no Mattermost core change).
- Can previous Mattermost version run with new schema: Yes — additive; older plugin code
  never references the tables.
- Impact if not compatible: N/A.

## Observations
- Rows can outlive their channel or post (no FK). Pins whose post was deleted or moved are
  pruned by the application the next time the channel's pins are read, and are skipped when
  building agent context, so orphaned rows are inert.
- `Instructions` is length-limited by the application (8,000 characters), not by the column type.

## Table Locks & Impact
- Tables affected: `Agents_ChannelInstructions`, `Agents_ChannelContextPosts` (newly created).
- Lock types acquired: ACCESS EXCLUSIVE on each new table during CREATE TABLE — no other
  session can reference them because they do not yet exist.
- Impact to concurrent operations: None.

## Zero Downtime
- Possible: Yes.
- Reason: Pure additive DDL on new objects.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: Empty new tables; row counts bounded by the number of channels using the feature
  (and, for pins, a small per-channel cap).
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
CREATE TABLE IF NOT EXISTS Agents_ChannelInstructions (
    ChannelID VARCHAR(26) PRIMARY KEY,
    Instructions TEXT NOT NULL,
    UpdatedBy VARCHAR(26) NOT NULL,
    UpdateAt BIGINT NOT NULL
);

CREATE TABLE IF NOT EXISTS Agents_ChannelContextPosts (
    ChannelID VARCHAR(26) NOT NULL,
    PostID VARCHAR(26) NOT NULL,
    PinnedBy VARCHAR(26) NOT NULL,
    PinnedAt BIGINT NOT NULL,
    PRIMARY KEY (ChannelID, PostID)
);
```
