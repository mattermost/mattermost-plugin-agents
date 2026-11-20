# Schema Migration Review: 000019 — Create Agents_ChannelArtifacts and Agents_ChannelArtifactVersions

> **Context:** New plugin tables for the channel artifact library. `Agents_ChannelArtifacts`
> holds one row per saved artifact (title, head version, audit fields).
> `Agents_ChannelArtifactVersions` holds one row per version: references to the source
> post and file plus the SHA-256 and size recorded at save time. The library never copies
> content; the HTML is read from the source file through the plugin file API, so source
> deletion and data retention govern it. Both tables are empty on creation and bounded by
> the per-channel artifact cap and the versions-to-keep setting.

## Schema Changes
- [x] New table(s): `Agents_ChannelArtifacts` (`ID VARCHAR(26) PRIMARY KEY`,
  `ChannelID`, `Title VARCHAR(64)`, `HeadVersion INT`, `CreatedBy`, `CreateAt`,
  `UpdatedBy`, `UpdateAt`); `Agents_ChannelArtifactVersions` (`ArtifactID`, `Version INT`,
  `PostID`, `FileID`, `SHA256 CHAR(64)`, `Size BIGINT`, `SourceBotUserID`, `CreatedBy`,
  `CreateAt`; primary key `(ArtifactID, Version)`)
- [ ] New column(s): —
- [x] New index(es): `idx_agents_channelartifacts_channel` on `(ChannelID, CreateAt)`
  (channel list, per-channel cap count); the versions primary key serves version lookups
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | Only CREATE TABLE / CREATE INDEX. |
| CREATE INDEX uses CONCURRENTLY | N/A | Index is created on a table created in the same transaction. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX. |
| No FOREIGN KEY via ALTER TABLE | ✅ | No FKs; `ArtifactID`, `PostID`, `FileID`, `ChannelID` reference Mattermost or plugin rows logically, consistent with project convention. |
| No full-table DELETE/UPDATE | ✅ | No DML. |
| morph:nontransactional where needed | N/A | Pure transactional DDL. |
| Down migration exists | ✅ | Drops both tables. |
| Transactional/nontransactional split correct | ✅ | One transaction. |

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned tables; no Mattermost core change).
- Can previous Mattermost version run with new schema: Yes — additive.
- Impact if not compatible: N/A.

## Observations
- Version rows may outlive their source post or file (no FK to Mattermost tables). That is
  intended: reads and list queries check the source's existence and report the version as
  unavailable instead of deleting it.
- Rows of permanently deleted channels are removed by the data-retention sweep.

## Table Locks & Impact
- Tables affected: both newly created.
- Lock types acquired: ACCESS EXCLUSIVE on the new tables during CREATE; nothing else can
  reference them yet.
- Impact to concurrent operations: None.

## Zero Downtime
- Possible: Yes.
- Reason: Pure additive DDL on new objects.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: Empty new tables; row counts are bounded by the per-channel cap and versions to keep.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
CREATE TABLE IF NOT EXISTS Agents_ChannelArtifacts (
    ID VARCHAR(26) PRIMARY KEY,
    ChannelID VARCHAR(26) NOT NULL,
    Title VARCHAR(64) NOT NULL,
    HeadVersion INT NOT NULL,
    CreatedBy VARCHAR(26) NOT NULL,
    CreateAt BIGINT NOT NULL,
    UpdatedBy VARCHAR(26) NOT NULL,
    UpdateAt BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agents_channelartifacts_channel ON Agents_ChannelArtifacts(ChannelID, CreateAt);
CREATE TABLE IF NOT EXISTS Agents_ChannelArtifactVersions (
    ArtifactID VARCHAR(26) NOT NULL,
    Version INT NOT NULL,
    PostID VARCHAR(26) NOT NULL,
    FileID VARCHAR(26) NOT NULL,
    SHA256 CHAR(64) NOT NULL,
    Size BIGINT NOT NULL,
    SourceBotUserID VARCHAR(26) NOT NULL,
    CreatedBy VARCHAR(26) NOT NULL,
    CreateAt BIGINT NOT NULL,
    PRIMARY KEY (ArtifactID, Version)
);
```
