# embeddings/AGENTS.md

Scoped pointer for embedding interfaces and composite search storage. Root rules in `/AGENTS.md` still apply.

- Full semantic search/RAG guidance lives in `/search/AGENTS.md`.
- This package owns embedding provider/store interfaces and vector document types.
- Keep permission, chunking, and indexer assumptions aligned with the RAG pipeline.
- Tests: `go test -v ./embeddings/...`
