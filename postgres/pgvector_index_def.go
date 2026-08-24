// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package postgres

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// expectedVectorIndexUsing is the exact HNSW clause we create. Partial
// (WHERE) and expression indexes must not match.
const expectedVectorIndexUsing = "USING hnsw (embedding vector_l2_ops)"

// hnswMOptionRE finds m in a WITH clause. Tolerates quoted values (m='8').
var hnswMOptionRE = regexp.MustCompile(`(?i)(?:^|[,(]\s*)m\s*=\s*'?(\d+)'?`)

var vectorIndexWhereRE = regexp.MustCompile(`(?i)\sWHERE\s`)

// createVectorIndexSQL builds CREATE INDEX for the HNSW ANN index.
// Always emits WITH (m = N) so the catalog definition is explicit.
func createVectorIndexSQL(m int) string {
	return fmt.Sprintf(
		"CREATE INDEX IF NOT EXISTS %s ON llm_posts_embeddings %s WITH (m = %d)",
		vectorIndexName, expectedVectorIndexUsing, m,
	)
}

func (pv *PGVector) createVectorIndexSQL() string {
	return createVectorIndexSQL(pv.hnswM)
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

// vectorIndexDefinitionOK is true when the catalog def is a full-table HNSW
// index on embedding with vector_l2_ops and the expected m. Partial indexes
// and expression indexes cannot serve all searches.
func vectorIndexDefinitionOK(indexDef string, wantM int) bool {
	if !strings.Contains(indexDef, "llm_posts_embeddings") {
		return false
	}
	if strings.Contains(indexDef, "USING hnsw ((") {
		return false
	}
	usingIdx := strings.Index(indexDef, expectedVectorIndexUsing)
	if usingIdx < 0 {
		return false
	}
	if vectorIndexWhereRE.MatchString(indexDef[usingIdx:]) {
		return false
	}
	m, ok := parseHNSWMFromIndexDef(indexDef)
	return ok && m == wantM
}
