# Schema Migration Review: 000013 — Create Agents_AgentDocuments, add Documents to Agents_UserAgents

> **Context:** Agents can carry reference documents (PDF and text files) whose extracted text
> is added to the agent's system prompt. `Agents_AgentDocuments` is an append-only,
> content-addressed blob store: one row per uploaded or imported document holding the
> original bytes and the extracted text. Rows are never updated or deleted, because agent
> version snapshots keep referring to them (no pruning, like `Agents_AgentVersions`).
> `Agents_UserAgents.Documents` holds the agent's current references (JSON array of
> `{id, name, mimeType, size, sha256, textRunes}`), never content. Growth is bounded by
> human-driven uploads: at most 10 MiB per document, 20 documents and 25 MiB per agent
> configuration.

## Schema Changes
- [x] New table(s): `Agents_AgentDocuments` (`ID VARCHAR(26) PRIMARY KEY`,
  `SHA256 VARCHAR(64) NOT NULL`, `Name VARCHAR(256) NOT NULL`, `MimeType VARCHAR(128) NOT NULL`,
  `Size BIGINT NOT NULL`, `Content BYTEA NOT NULL`, `ExtractedText TEXT NOT NULL`,
  `TextRunes INT NOT NULL`, `CreatedBy VARCHAR(26) NOT NULL DEFAULT ''`, `CreateAt BIGINT NOT NULL`)
- [x] New column(s) on `Agents_UserAgents`: `Documents TEXT NOT NULL DEFAULT '[]'`
- [x] New index(es): `idx_agentdocuments_sha256_createdby` on `Agents_AgentDocuments(SHA256, CreatedBy)`;
  serves the per-uploader de-duplication lookup on upload/import
- [ ] Modified column(s): —
- [ ] Dropped object(s): —

## Safety Analysis

| Check | Status | Notes |
|-------|--------|-------|
| No ALTER COLUMN TYPE | ✅ | Only CREATE TABLE / CREATE INDEX / ADD COLUMN. |
| CREATE INDEX uses CONCURRENTLY | N/A | The index is on the table created in the same migration, so it is empty and unreachable by concurrent writers. CONCURRENTLY would also be incompatible with the transactional execution. |
| DROP INDEX uses CONCURRENTLY | N/A | No DROP INDEX in the up migration. |
| No FOREIGN KEY via ALTER TABLE | ✅ | No FKs. (References from `Agents_UserAgents.Documents` and version snapshots are resolved by ID in Go; consistent with project convention.) |
| No full-table DELETE/UPDATE | ✅ | No DML; the column default supplies `'[]'` for existing rows. |
| morph:nontransactional where needed | N/A | Pure transactional DDL. |
| Down migration exists | ✅ | Drops the column, then the index and the table. |
| Transactional/nontransactional split correct | ✅ | All DDL transactional. |

## Postgres-Specific Notes
- `ADD COLUMN ... NOT NULL DEFAULT '[]'` is metadata-only on PostgreSQL 11+ (constant default → no table rewrite). ✅
- `Content` and `ExtractedText` values are TOASTed (compressed / stored out of line); row reads that
  do not select them (reference resolution) do not read the blob data.

## Backwards Compatibility
- Compatible with previous ESR: Yes (plugin-owned tables; no Mattermost core change).
- Can previous Mattermost version run with new schema: Yes — additive. An older plugin version
  ignores the column and the table; its agent saves leave `Documents` untouched, and its agent
  version snapshots omit `documents`, which the new code treats as no documents.
- Impact if not compatible: N/A.

## Observations
- `Content` holds uploaded files and `ExtractedText` their text, which can be sensitive
  organizational material. API reads (download, text preview) are restricted to users who can
  manage an agent that references the document.
- `Name` is the original file name; the name an agent shows is stored in the agent reference.

## Table Locks & Impact
- Tables affected: `Agents_AgentDocuments` (newly created), `Agents_UserAgents`.
- Lock types acquired:
  - ACCESS EXCLUSIVE on the new table during CREATE TABLE / CREATE INDEX — no other session can
    reference it because it does not yet exist.
  - `ALTER TABLE … ADD COLUMN`: ACCESS EXCLUSIVE on `Agents_UserAgents`. Metadata-only because the
    default is constant, so the lock is held for a negligible amount of work, but it waits for any
    transaction already touching the table.
- Impact to concurrent operations: Negligible once the lock is granted; bounded by lock-wait
  duration if a long-running transaction holds a conflicting lock on `Agents_UserAgents`.

## Zero Downtime
- Possible: Yes.
- Reason: Additive DDL on a new object plus a metadata-only ADD COLUMN on an admin-managed table.

## Large-Dataset Testing Recommendation
- **Recommended: No**
- Reason: Empty new table; `Agents_UserAgents` is admin-configured and small.
- Tables to seed for testing: —

## Test Results

| DB | Table Size | Row Count | Duration | Instance |
|----|-----------|-----------|----------|----------|
| PostgreSQL | | | | |

## SQL Queries
```sql
CREATE TABLE IF NOT EXISTS Agents_AgentDocuments (
    ID VARCHAR(26) PRIMARY KEY,
    SHA256 VARCHAR(64) NOT NULL,
    Name VARCHAR(256) NOT NULL,
    MimeType VARCHAR(128) NOT NULL,
    Size BIGINT NOT NULL,
    Content BYTEA NOT NULL,
    ExtractedText TEXT NOT NULL,
    TextRunes INT NOT NULL,
    CreatedBy VARCHAR(26) NOT NULL DEFAULT '',
    CreateAt BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_agentdocuments_sha256_createdby ON Agents_AgentDocuments(SHA256, CreatedBy);

ALTER TABLE Agents_UserAgents
    ADD COLUMN IF NOT EXISTS Documents TEXT NOT NULL DEFAULT '[]';
```
