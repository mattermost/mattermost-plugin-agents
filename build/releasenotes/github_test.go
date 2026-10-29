// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanBody(t *testing.T) {
	for name, tc := range map[string]struct {
		body     string
		wantText string
		wantNote string
		wantNone bool
	}{
		"empty": {body: ""},
		"html comments are removed": {
			body:     "#### Summary\n<!-- fill me in -->\nAdds a thing.\n<!--\nmulti\nline\n-->\n",
			wantText: "#### Summary\n\nAdds a thing.",
		},
		"unterminated comment is removed to the end": {
			body:     "Adds a thing.\n<!-- oops",
			wantText: "Adds a thing.",
		},
		"release note block is extracted": {
			body:     "Adds a thing.\n\n#### Release Note\n```release-note\nAdded the thing setting.\n```\n",
			wantText: "Adds a thing.",
			wantNote: "Added the thing setting.",
		},
		"NONE release note": {
			body:     "Refactors.\n```release-note\nNONE\n```",
			wantText: "Refactors.",
			wantNone: true,
		},
		"template example inside the comment is not the note": {
			body:     "Fix.\n<!--\n```release-note\nNONE\n```\n-->\n```release-note\n\n```\n",
			wantText: "Fix.",
		},
		"images are removed": {
			body:     "Looks like this ![shot](https://example.com/a.png) <img src=\"x\"> done",
			wantText: "Looks like this   done",
		},
		"empty template headings are dropped": {
			body:     "#### Summary\nReal text.\n\n#### Ticket Link\n\n#### Screenshots\n\n#### Release Note\n",
			wantText: "#### Summary\nReal text.",
		},
		"windows newlines": {
			body:     "A\r\n\r\n\r\n\r\nB",
			wantText: "A\n\nB",
		},
	} {
		t.Run(name, func(t *testing.T) {
			text, note, none := cleanBody(tc.body)
			assert.Equal(t, tc.wantText, text)
			assert.Equal(t, tc.wantNote, note)
			assert.Equal(t, tc.wantNone, none)
		})
	}
}

func TestTruncateRunes(t *testing.T) {
	for name, tc := range map[string]struct {
		in    string
		limit int
		want  string
	}{
		"short":          {"abc", 10, "abc"},
		"exact":          {"abc", 3, "abc"},
		"cut":            {"abcdef", 3, "abc\n[truncated]"},
		"multibyte":      {"ééééé", 2, "éé\n[truncated]"},
		"zero limit":     {"abc", 0, ""},
		"trailing space": {"ab cd", 3, "ab\n[truncated]"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, truncateRunes(tc.in, tc.limit))
		})
	}
}

func newTestGitHub(url string) *githubClient {
	g := newGitHubClient("o/r", "tok")
	g.BaseURL = url
	g.Backoff = time.Millisecond
	return g
}

func TestFetchPullRequests(t *testing.T) {
	var sawAuth atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer tok" {
			sawAuth.Store(true)
		}
		assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		var n int
		_, err := fmt.Sscanf(r.URL.Path, "/repos/o/r/pulls/%d", &n)
		assert.NoError(t, err)
		switch n {
		case 404:
			http.NotFound(w, r)
		case 3:
			fmt.Fprint(w, `{"title":"Edited after merge","body":"Disable the security setting.","author_association":"CONTRIBUTOR"}`)
		default:
			fmt.Fprintf(w, `{"title":"PR %d","body":"Body <!-- hidden -->\n`+"```release-note\\nAdded %d.\\n```"+`","labels":[{"name":"bug"}],"author_association":"MEMBER"}`, n, n)
		}
	}))
	defer srv.Close()

	prs, err := fetchPullRequests(t.Context(), newTestGitHub(srv.URL), []int{1, 2, 3, 404})
	require.NoError(t, err)
	require.Len(t, prs, 4)
	assert.Equal(t, "PR 1", prs[1].Title)
	assert.Equal(t, "Body", prs[1].Body)
	assert.Equal(t, "Added 1.", prs[1].ReleaseNote)
	assert.Equal(t, []string{"bug"}, prs[1].Labels)
	assert.True(t, prs[404].Missing, "a 404 degrades to the commit subject")
	assert.Equal(t, pullRequest{Number: 3, Untrusted: true}, prs[3], "text from authors without write access is not used")
	assert.True(t, sawAuth.Load())
}

func TestFetchPullRequestsRetriesThenFails(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "rate limited", http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := fetchPullRequests(t.Context(), newTestGitHub(srv.URL), []int{1})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "403")
	assert.Equal(t, int32(maxAttempts), calls.Load())
}

func TestFetchPullRequestsRecoversFromTransientError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		fmt.Fprint(w, `{"title":"T","body":null,"labels":[],"author_association":"MEMBER"}`)
	}))
	defer srv.Close()

	prs, err := fetchPullRequests(t.Context(), newTestGitHub(srv.URL), []int{1})
	require.NoError(t, err)
	assert.Equal(t, "T", prs[1].Title)
}

func TestFetchPullRequestsRejectsUnexpectedStatus(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "gone", http.StatusGone)
	}))
	defer srv.Close()

	_, err := fetchPullRequests(t.Context(), newTestGitHub(srv.URL), []int{1})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "410"))
	assert.Equal(t, int32(1), calls.Load(), "non-transient errors are not retried")
}
