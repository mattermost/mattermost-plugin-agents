# chunking/AGENTS.md

Scoped pointer for text chunking helpers. Root rules in `/AGENTS.md` still apply.

- Embedding chunking guidance lives in `/search/AGENTS.md`.
- Meeting transcript chunking guidance lives in `/meetings/AGENTS.md`.
- `ChunkText` is for vector indexing; `SplitPlaintextOnSentences` is for meeting summaries.
- Tests: `go test -v ./chunking/...`
