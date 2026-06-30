# postgres/AGENTS.md

Scoped instructions for pgvector storage tests. Root rules in `/AGENTS.md` and `/search/AGENTS.md` apply.

## Commands

- Default pgvector tests: `go test -v ./postgres/...`.
- Reuse an existing pgvector instance: `PGVECTOR_TEST_DSN='postgres://user:pass@host:5432/db?sslmode=disable' go test -v ./postgres/...`.

## Gotchas

- Tests start `pgvector/pgvector:pg17` through testcontainers when `PGVECTOR_TEST_DSN` is unset.
- `PGVECTOR_TEST_DSN` must be a URL-style DSN.
- Runtime admin setup belongs in `docs/admin_guide.md`; this package is storage/test plumbing.
