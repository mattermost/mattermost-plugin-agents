# indexer/AGENTS.md

Scoped pointer for semantic-search indexing. Root rules in `/AGENTS.md` still apply.

- Full RAG guidance lives in `/search/AGENTS.md`.
- Keep post eligibility rules aligned between `shouldIndexPost` and index health SQL.
- Reindex jobs use cluster mutex, KV CAS, heartbeat reclaim, and cutoff timestamps.
- Tests: `go test -v ./indexer/...`
