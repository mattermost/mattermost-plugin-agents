// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package agentdocs

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type countingLoader struct {
	texts map[string]string
	err   error
	calls [][]string
}

func (l *countingLoader) GetAgentDocumentTexts(ids []string) (map[string]string, error) {
	l.calls = append(l.calls, slices.Clone(ids))
	if l.err != nil {
		return nil, l.err
	}
	out := make(map[string]string)
	for _, id := range ids {
		if text, ok := l.texts[id]; ok {
			out[id] = text
		}
	}
	return out, nil
}

func TestTextCache(t *testing.T) {
	type request struct {
		ids  []string
		want map[string]string
	}
	tests := []struct {
		name      string
		texts     map[string]string
		loadErr   error
		limit     int
		requests  []request
		wantCalls [][]string
		wantErr   bool
	}{
		{
			name:  "loads only uncached documents and skips missing ones",
			texts: map[string]string{"a": "alpha", "b": "beta"},
			limit: 100,
			requests: []request{
				{ids: []string{"a", "missing"}, want: map[string]string{"a": "alpha"}},
				{ids: []string{"a", "b"}, want: map[string]string{"a": "alpha", "b": "beta"}},
			},
			wantCalls: [][]string{{"a", "missing"}, {"b"}},
		},
		{
			name:  "evicts the least recently used text beyond the bound",
			texts: map[string]string{"a": "aaaa", "b": "bbbb", "c": "cccc"},
			limit: 8,
			requests: []request{
				{ids: []string{"a"}, want: map[string]string{"a": "aaaa"}},
				{ids: []string{"b"}, want: map[string]string{"b": "bbbb"}},
				{ids: []string{"a"}, want: map[string]string{"a": "aaaa"}},
				{ids: []string{"c"}, want: map[string]string{"c": "cccc"}},
				{ids: []string{"a", "b", "c"}, want: map[string]string{"a": "aaaa", "b": "bbbb", "c": "cccc"}},
			},
			wantCalls: [][]string{{"a"}, {"b"}, {"c"}, {"b"}},
		},
		{
			name:  "texts larger than the cache are returned but not cached",
			texts: map[string]string{"big": "0123456789"},
			limit: 5,
			requests: []request{
				{ids: []string{"big"}, want: map[string]string{"big": "0123456789"}},
				{ids: []string{"big"}, want: map[string]string{"big": "0123456789"}},
			},
			wantCalls: [][]string{{"big"}, {"big"}},
		},
		{
			name:      "loader errors are returned",
			loadErr:   errors.New("db down"),
			limit:     5,
			requests:  []request{{ids: []string{"a"}}},
			wantCalls: [][]string{{"a"}},
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := &countingLoader{texts: tt.texts, err: tt.loadErr}
			cache := NewTextCache(loader, tt.limit)
			for _, req := range tt.requests {
				texts, err := cache.AgentDocumentTexts(context.Background(), req.ids)
				if tt.wantErr {
					require.Error(t, err)
					continue
				}
				require.NoError(t, err)
				assert.Equal(t, req.want, texts)
			}
			assert.Equal(t, tt.wantCalls, loader.calls)
		})
	}
}
