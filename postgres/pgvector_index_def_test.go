// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package postgres

import (
	"encoding/json"
	"testing"

	"github.com/mattermost/mattermost-plugin-agents/v2/embeddings"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateVectorIndexSQL(t *testing.T) {
	tests := []struct {
		name string
		m    int
		want []string
	}{
		{
			name: "default m is emitted explicitly",
			m:    embeddings.DefaultHNSWM,
			want: []string{
				"CREATE INDEX IF NOT EXISTS " + vectorIndexName,
				"USING hnsw (embedding vector_l2_ops)",
				"WITH (m = 8)",
			},
		},
		{
			name: "configured m is emitted explicitly",
			m:    16,
			want: []string{"WITH (m = 16)", "vector_l2_ops"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := createVectorIndexSQL(tt.m)
			for _, fragment := range tt.want {
				assert.Contains(t, got, fragment)
			}
		})
	}
}

func TestParseHNSWMFromIndexDef(t *testing.T) {
	tests := []struct {
		name   string
		def    string
		wantM  int
		wantOK bool
	}{
		{
			name:   "quoted m in WITH clause",
			def:    "CREATE INDEX llm_posts_embeddings_embedding_idx ON public.llm_posts_embeddings USING hnsw (embedding vector_l2_ops) WITH (m='16', ef_construction='64')",
			wantM:  16,
			wantOK: true,
		},
		{
			name:   "unquoted m with spaces",
			def:    "CREATE INDEX llm_posts_embeddings_embedding_idx ON llm_posts_embeddings USING hnsw (embedding vector_l2_ops) WITH (m = 8)",
			wantM:  8,
			wantOK: true,
		},
		{
			name:   "m after other options",
			def:    "CREATE INDEX idx ON t USING hnsw (embedding vector_l2_ops) WITH (ef_construction='64', m='8')",
			wantM:  8,
			wantOK: true,
		},
		{
			name:   "missing WITH clause",
			def:    "CREATE INDEX idx ON t USING hnsw (embedding vector_l2_ops)",
			wantOK: false,
		},
		{
			name:   "WITH without m",
			def:    "CREATE INDEX idx ON t USING hnsw (embedding vector_l2_ops) WITH (ef_construction='64')",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseHNSWMFromIndexDef(tt.def)
			assert.Equal(t, tt.wantOK, ok)
			if tt.wantOK {
				assert.Equal(t, tt.wantM, got)
			}
		})
	}
}

func TestVectorIndexDefinitionOK(t *testing.T) {
	l2m8 := "CREATE INDEX llm_posts_embeddings_embedding_idx ON public.llm_posts_embeddings USING hnsw (embedding vector_l2_ops) WITH (m='8', ef_construction='64')"
	l2m16 := "CREATE INDEX llm_posts_embeddings_embedding_idx ON public.llm_posts_embeddings USING hnsw (embedding vector_l2_ops) WITH (m='16', ef_construction='64')"
	cosine := "CREATE INDEX llm_posts_embeddings_embedding_idx ON llm_posts_embeddings USING hnsw (embedding vector_cosine_ops) WITH (m='8')"
	btree := "CREATE INDEX llm_posts_embeddings_embedding_idx ON llm_posts_embeddings USING btree (post_id)"
	partial := "CREATE INDEX llm_posts_embeddings_embedding_idx ON public.llm_posts_embeddings USING hnsw (embedding vector_l2_ops) WITH (m='8', ef_construction='64') WHERE (NOT is_chunk)"
	expr := "CREATE INDEX llm_posts_embeddings_embedding_idx ON llm_posts_embeddings USING hnsw ((embedding) vector_l2_ops) WITH (m='8')"

	tests := []struct {
		name  string
		def   string
		wantM int
		want  bool
	}{
		{name: "matching full-table L2 index", def: l2m8, wantM: 8, want: true},
		{name: "quoted m=16 vs configured 8", def: l2m16, wantM: 8, want: false},
		{name: "wrong opclass", def: cosine, wantM: 8, want: false},
		{name: "btree occupying the name", def: btree, wantM: 8, want: false},
		{name: "partial index with matching m is rejected", def: partial, wantM: 8, want: false},
		{name: "expression index is rejected", def: expr, wantM: 8, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, vectorIndexDefinitionOK(tt.def, tt.wantM))
		})
	}
}

func TestPGVectorConfigHNSWMNotUnmarshaledFromParameters(t *testing.T) {
	var cfg PGVectorConfig
	err := json.Unmarshal([]byte(`{"dimensions": 768, "hnswM": 16, "HNSWM": 32}`), &cfg)
	require.NoError(t, err)
	assert.Equal(t, 768, cfg.Dimensions)
	assert.Equal(t, 0, cfg.HNSWM, "HNSWM must not be taken from vectorStore.parameters")
}
