// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package postgres

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// defaultVectorOpClass is the pgvector opclass used for L2 <-> queries.
// Phase 2 can pass halfvec_l2_ops without changing matching.
const defaultVectorOpClass = "vector_l2_ops"

// hnswMOptionRE finds m in a WITH clause. Tolerates quoted values (m='8').
var hnswMOptionRE = regexp.MustCompile(`(?i)(?:^|[,(]\s*)m\s*=\s*'?(\d+)'?`)

// createVectorIndexSQL builds CREATE INDEX for the HNSW ANN index.
// Always emits WITH (m = N) so the catalog definition is explicit.
func createVectorIndexSQL(m int, opclass string) string {
	if opclass == "" {
		opclass = defaultVectorOpClass
	}
	return fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS %s ON llm_posts_embeddings USING hnsw (embedding %s) WITH (m = %d)",
		vectorIndexName, opclass, m,
	)
}

func (pv *PGVector) createVectorIndexSQL() string {
	return createVectorIndexSQL(pv.hnswM, pv.opclass)
}

// parseHNSWMFromIndexDef extracts HNSW m from pg_get_indexdef output.
func parseHNSWMFromIndexDef(indexDef string) (int, bool) {
	match := hnswMOptionRE.FindStringSubmatch(indexDef)
	if len(match) < 2 {
		return 0, false
	}
	m, err := strconv.Atoi(match[1])
	if err != nil || m <= 0 {
		return 0, false
	}
	return m, true
}

// vectorIndexDefinitionOK is true when the catalog def uses the expected
// HNSW opclass and m. Suffix matching is not enough: WITH (m = N) follows
// the USING clause, so a wrong m would still match a suffix check.
func vectorIndexDefinitionOK(indexDef string, wantM int, opclass string) bool {
	if opclass == "" {
		opclass = defaultVectorOpClass
	}
	using := "USING hnsw (embedding " + opclass + ")"
	if !strings.Contains(indexDef, using) {
		return false
	}
	m, ok := parseHNSWMFromIndexDef(indexDef)
	return ok && m == wantM
}
