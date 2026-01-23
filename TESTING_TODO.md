# Embedding Search Test Coverage TODO

This document tracks missing test coverage for the embedding search feature. Tests are organized into stages that can be worked on independently.

---

## Stage 1: Chunking Utility ✅ COMPLETE

**Files:** `chunking/chunker.go`, `chunking/text_splitting.go`
**Existing Tests:** `chunking/chunker_test.go`, `chunking/text_splitting_test.go`
**Scope:** 6 test cases
**Dependencies:** None (standalone utility)

- [x] Whitespace-only content
- [x] Content exactly at chunk size boundary
- [x] Content that produces exactly one chunk (vs non-chunk)
- [x] Unknown/invalid chunking strategy string
- [x] Very large chunk overlap relative to chunk size
- [x] Content with only sentence-ending punctuation

---

## Stage 2: Vector Store (PGVector) ✅ COMPLETE (19/22 - 3 require mocking)

**Files:** `postgres/pgvector.go`
**Existing Tests:** `postgres/pgvector_test.go`
**Scope:** 22 test cases
**Dependencies:** Test database

### Initialization
- [x] `NewPGVector` with zero dimensions (should error)
- [x] `NewPGVector` with negative dimensions (should error)
- [ ] Handling when `CREATE EXTENSION vector` fails *(requires mocking - skipped per CLAUDE.md)*
- [ ] Handling when table creation fails *(requires mocking - skipped per CLAUDE.md)*
- [ ] Handling when index creation fails *(requires mocking - skipped per CLAUDE.md)*

### Store Operation
- [x] Mismatched length between `docs` and `embeddings` arrays *(bug fixed: now returns error instead of panic)*
- [x] Concurrent store operations (race conditions on same post_id)
- [x] Very large documents (content approaching DB limits)
- [x] Unicode/special characters/emoji in content
- [x] Re-indexing post that changed from single to chunked
- [x] Re-indexing post that changed from chunked to single

### Search Operation
- [x] Very large result sets (approaching maxSearchLimit=1000)
- [x] Limit <= 0 uses maxSearchLimit default
- [x] Combined filters: team + channel + time range + min score all at once
- [x] `CreatedBefore` filter by itself
- [x] Both `CreatedAfter` AND `CreatedBefore` together (time range query)
- [x] Zero MinScore value
- [x] Negative MinScore value
- [x] Very high MinScore value (> 1.0)
- [x] Empty embedding vector passed to search
- [x] Malformed embedding vector (wrong dimensions)
- [x] User who is member of zero channels

### Delete Operation
- [x] Delete with empty postIDs slice
- [x] Delete non-existent post IDs (should succeed silently)

---

## Stage 3: Embedding Provider (OpenAI) ✅ COMPLETE

**Files:** `openai/openai.go`
**Existing Tests:** `openai/openai_test.go`
**Scope:** 13 test cases
**Dependencies:** HTTP response mocking

### CreateEmbedding
- [x] API error responses (rate limiting, invalid API key, etc.)
- [x] Empty response from API (`len(resp.Data) == 0`)
- [x] Context cancellation
- [x] Network timeout

### BatchCreateEmbeddings
- [x] API errors during batch call
- [x] Large batch handling (API limits)
- [x] Empty texts array input
- [x] Context cancellation
- [x] Response with mismatched count vs input *(bug fixed: now validates and returns error)*

### Other
- [x] `Dimensions()` method
- [x] NewEmbeddings/NewCompatibleEmbeddings default model + dimensions when unset

---

## Stage 4: Composite Search ✅ COMPLETE

**Files:** `embeddings/composite.go`
**Existing Tests:** `embeddings/composite_test.go`
**Scope:** 10 test cases
**Dependencies:** Interfaces from Stages 2-3 (can be tested with test doubles)

### Store Method
- [x] Empty docs input (returns nil without error)
- [x] Embedding provider failure during `BatchCreateEmbeddings`
- [x] Vector store failure during `Store`
- [x] Partial failures in batch embedding generation *(bug fixed: now validates embedding count)*
- [x] Documents that chunk into zero content (empty after processing)

### Search Method
- [x] Embedding provider failure during `CreateEmbedding`
- [x] Vector store failure during `Search`
- [x] Context cancellation mid-operation

### Delete/Clear Methods
- [x] Vector store failure during `Delete`
- [x] Vector store failure during `Clear`

---

## Stage 5: Search Initialization ✅ COMPLETE

**Files:** `search/embeddings.go`
**Existing Tests:** `search/embeddings_test.go`
**Scope:** 10 test cases
**Dependencies:** Configuration layer

### InitEmbeddingsSearch
- [x] `cfg.Type` empty (search disabled)
- [x] Missing/invalid license (Basics not licensed)
- [x] Invalid dimensions (<= 0)
- [x] Unsupported search type
- [x] Default chunking options applied when `ChunkingOptions.ChunkSize` is 0

### Provider/Store Wiring
- [x] Vector store config JSON unmarshal error
- [x] Embedding provider config JSON unmarshal error
- [x] Unsupported vector store type
- [x] Unsupported embedding provider type
- [x] Mock provider path (`ProviderTypeMock`)

---

## Stage 6: Indexer ✅ COMPLETE

**Files:** `indexer/indexer.go`, `indexer/indexer_job.go`
**Existing Tests:** `indexer/indexer_test.go`
**Scope:** 17 test cases
**Dependencies:** Stages 4-5 interfaces

### Job Execution
- [x] `runReindexJob` background goroutine logic
- [x] Progress updates during reindexing
- [x] Job heartbeat mechanism
- [x] Batch processing logic (page through posts)
- [x] Cutoff timestamp handling (posts created during reindex)
- [x] `StartReindexJob` happy path (status persisted, cursor reset on full reindex)
- [x] `StartReindexJob` KV store error handling (KVGet/KVSet failures)
- [x] `StartCatchUpJob` happy path (cursor seeded, job started)
- [x] `CancelJob` success path + "not running" error

### Concurrent Operations
- [x] Race condition: `StartReindexJob` called from multiple nodes simultaneously *(tested via cluster mutex behavior)*
- [x] Race condition: `CancelJob` called while job is actively processing *(tested via cancellation detection)*
- [x] Stale job detection edge cases

### shouldIndexPost Additional Cases
- [x] Posts in DM channels with bots (should be skipped)
- [x] Bot posts via `s.bots.IsAnyBot()` check
- [x] Different post types (system messages, join/leave, etc.)

### Error Handling
- [x] `IndexPost` when search.Store() returns an error
- [x] `DeletePost` when search.Delete() returns an error

### Health Checks
- [x] `CheckIndexHealth` excludes bot DM channels (bot IDs in channel names)

---

## Stage 7: Search Service ✅ COMPLETE

**Files:** `search/search.go`
**Existing Tests:** `search/search_test.go`
**Scope:** 14 test cases
**Dependencies:** Stages 4, 6 interfaces

### RunSearch Method
- [x] Full flow: DM creation -> search -> LLM call -> streaming response
- [x] Error handling at each stage (DM creation failure, search failure)
- [x] No results scenario
- [x] LLM failure mid-stream *(covered via SearchQuery LLM failure test)*

### SearchQuery Method
- [x] Zero results from search (returns empty response with message)
- [x] LLM failure during ChatCompletionNoStream
- [x] Very large result sets passed to prompt (100 results test)

### Service State
- [x] `Enabled()` false when getSearch is nil or returns nil
- [x] `Search()` returns error when embedding search is not configured

### buildPrompt Edge Cases
- [x] Prompt template rendering errors (nil prompts case)
- [x] Very large results causing prompt to exceed limits

### enrichResults Edge Cases
- [x] Same channel accessed multiple times (caching behavior - no caching, separate API calls)
- [x] Same user accessed multiple times (caching behavior - no caching, separate API calls)
- [ ] Rate limiting from Mattermost API *(not implemented in current code)*

### Bug Fixed
- **`buildPrompt`**: Now validates `s.prompts` is not nil before use, preventing nil pointer dereference

---

## Stage 8: API Layer ✅ COMPLETE

**Files:** `api/api_search.go`
**Existing Tests:** `api/api_search_test.go`
**Scope:** 7 test cases
**Dependencies:** Stage 7 (Search Service)

- [x] `MaxResults` with negative value (defaults to 5)
- [x] `MaxResults` with very large value (capped to 100)
- [x] Malformed JSON request body (10 test cases: invalid JSON, truncated, wrong types, etc.)
- [x] Missing required fields (6 test cases: empty object, missing query, empty query, whitespace-only)
- [x] Missing `Mattermost-User-ID` header (5 test cases including case variations)
- [x] Concurrent search requests handling (10 parallel requests)
- [ ] Rate limiting *(not implemented in current code)*

### Bug Fixed
- **`handleSearchQuery`**: Added missing empty query validation, now returns HTTP 400 instead of HTTP 500

---

## Stage 9: Integration & End-to-End ✅ COMPLETE

**Files:** `integration/integration_test.go`, `search/search_eval_test.go`
**Scope:** 10 test cases
**Dependencies:** All previous stages

### Integration Tests (always run, use mock embeddings)
- [x] Basic index and search mechanics (`TestBasicIndexAndSearchMechanics`)
- [x] Re-indexing existing data with dimension mismatch handling (`TestReindexWithDimensionMismatch`)
- [x] Performance under concurrent load (50 concurrent indexes + 50 concurrent searches) (`TestConcurrentIndexingAndSearching`)
- [x] Real-time indexing during active posting (`TestRealTimeIndexingDuringActivePosting`)
- [x] Index consistency after server restart/reconnection (`TestIndexConsistencyAfterReconnection`)
- [x] Multi-channel permission isolation (`TestMultipleChannelPermissionIsolation`)
- [x] Delete and reindex behavior (`TestDeleteAndReindex`)
- [x] Chunking behavior for long posts (`TestChunkingBehavior`)

### Eval Tests (require `GOEVALS=1` and `OPENAI_API_KEY`, use real embeddings)
- [x] Semantic search relevance with real OpenAI embeddings (`TestSemanticSearchRelevance`)
- [x] Different queries return topically appropriate results (`TestSemanticSearchDifferentQueries`)
- [x] Filters work correctly with semantic search (`TestSemanticSearchWithFilters`)

**Note:** Integration tests use `MockEmbeddingProvider` to test system mechanics. Eval tests use real OpenAI embeddings to verify semantic search quality. Run evals with: `GOEVALS=1 OPENAI_API_KEY=... go test -v ./search/... -run Eval`

---

## Stage 10: Resilience & Security ✅ COMPLETE

**Files:** `embeddings/resilience_security_test.go`
**Scope:** 10 test cases
**Dependencies:** All previous stages (cross-cutting concerns)

### Error Recovery / Resilience
- [x] Embedding API unavailable during indexing (`TestEmbeddingProviderFailureDuringIndex`)
- [x] Index consistency after reconnection (`TestIndexConsistencyAfterReconnection`)
- [x] Real-time indexing during active posting (`TestRealTimeIndexingDuringActivePosting`)
- [x] Graceful degradation when search is unavailable (`TestGracefulDegradationWhenSearchUnavailable`)

### Security / Edge Cases
- [x] SQL injection via search options - TeamID, ChannelID, UserID fields (`TestSQLInjectionProtection`)
- [x] User access to posts in channels they were removed from (`TestUserRemovedFromChannel`)
- [x] User access to posts in private channels they left (`TestUserLeftPrivateChannel`)
- [x] Search across archived channels (`TestSearchAcrossArchivedChannels`)
- [x] Special characters in content (`TestSearchWithSpecialCharactersInContent`)
- [x] Null byte handling in content (`TestNullByteInContentIsRejected`)

**Findings:** All security tests passed - SQL queries use parameterized inputs via Squirrel query builder.
Permission filtering via ChannelMembers join correctly isolates data. Archived channels (DeleteAt != 0) are excluded.

---

## Summary

| Stage | Component | Files | Test Cases | Status |
|-------|-----------|-------|------------|--------|
| 1 | Chunking | `chunking/` | 6 | ✅ Complete |
| 2 | PGVector | `postgres/` | 22 | ✅ 19/22 (3 need mocking) |
| 3 | OpenAI Provider | `openai/` | 13 | ✅ Complete |
| 4 | Composite Search | `embeddings/` | 10 | ✅ Complete |
| 5 | Search Init | `search/embeddings.go` | 10 | ✅ Complete |
| 6 | Indexer | `indexer/` | 17 | ✅ Complete |
| 7 | Search Service | `search/search.go` | 14 | ✅ 13/14 (1 not implemented) |
| 8 | API Layer | `api/api_search.go` | 7 | ✅ 6/7 (1 not implemented) |
| 9 | Integration/E2E | `integration/`, `search/` | 11 | ✅ Complete (8 integration + 3 eval) |
| 10 | Resilience/Security | `embeddings/` | 10 | ✅ Complete |

**Total:** 120 test cases
**Completed:** 115 test cases (96%)

### Bugs Fixed During Testing
1. **PGVector `Store`**: Now validates `len(docs) == len(embeddings)` and returns error instead of panicking
2. **OpenAI `BatchCreateEmbeddings`**: Now validates response count matches input count
3. **Composite Search `Store`**: Now validates embedding count matches document count before storing
4. **Search Service `buildPrompt`**: Now validates prompts are not nil, preventing nil pointer dereference
5. **API `handleSearchQuery`**: Added missing empty query validation, now returns HTTP 400 instead of HTTP 500

### Security Analysis (Stage 10)
- SQL injection protection verified: All queries use parameterized inputs via Squirrel query builder
- Permission isolation verified: ChannelMembers join correctly filters data by user access
- Archived channel handling verified: Channels with DeleteAt != 0 are excluded from search results
- Special character handling verified: Unicode, SQL metacharacters, and HTML are safely stored and searched

### Recommended Order

1. **Parallel Group A** (no dependencies): Stages 1, 2, 3, 5 ✅ COMPLETE
2. **Parallel Group B** (depends on A): Stages 4, 6, 7, 8 ✅ COMPLETE
3. **Sequential** (depends on all): Stages 9, 10 ✅ COMPLETE
