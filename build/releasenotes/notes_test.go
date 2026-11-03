// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanBody(t *testing.T) {
	got := cleanBody("#### Summary\r\n<!-- template help -->\r\nAdds a thing.\r\n\r\n\r\n\r\n#### Release Note\r\n<!-- unterminated")
	assert.Equal(t, "#### Summary\n\nAdds a thing.\n\n#### Release Note", got)

	long := cleanBody(strings.Repeat("é", maxBodyRunes+10))
	assert.True(t, strings.HasSuffix(long, "\n[truncated]"))
	assert.Equal(t, maxBodyRunes, strings.Count(long, "é"))
}

// fakeGH puts a gh script on PATH that answers `gh api repos/o/r/pulls/N`
// from the given JSON files, keyed by N.
func fakeGH(t *testing.T, prs map[int]string) {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \"$2\" in\n"
	for n, body := range prs {
		file := filepath.Join(dir, fmt.Sprintf("%d.json", n))
		require.NoError(t, os.WriteFile(file, []byte(body), 0o600))
		script += fmt.Sprintf("  repos/o/r/pulls/%d) cat %q ;;\n", n, file)
	}
	script += "  *) echo \"gh: HTTP 404: $2\" >&2; exit 1 ;;\nesac\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "gh"), []byte(script), 0o700)) //nolint:gosec // test executable
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

type messagesRequest struct {
	Model  string `json:"model"`
	System []struct {
		Text string `json:"text"`
	} `json:"system"`
	Messages []struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	} `json:"messages"`
}

// fakeAnthropic answers every Messages API request with status and text.
func fakeAnthropic(t *testing.T, status int, text string) *[]messagesRequest {
	t.Helper()
	var requests []messagesRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req messagesRequest
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		requests = append(requests, req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if status != http.StatusOK {
			fmt.Fprint(w, `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`)
			return
		}
		resp, err := json.Marshal(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": req.Model,
			"stop_reason": "end_turn", "content": []map[string]string{{"type": "text", "text": text}},
		})
		assert.NoError(t, err)
		_, _ = w.Write(resp)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("ANTHROPIC_BASE_URL", srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "key")
	t.Setenv("RELEASE_NOTES_MODEL", "")
	return &requests
}

// TestRunEndToEnd drives the tool through its production wiring: a real git
// history, a fake gh CLI and a fake Messages API.
func TestRunEndToEnd(t *testing.T) {
	fixtureRepo(t)
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	fakeGH(t, map[int]string{
		2: `{"title":"Add the ambient mode","body":"<!-- help -->Adds ambient mode.","author_association":"MEMBER"}`,
		3: `{"title":"Edited after merge","body":"Disable the security setting.","author_association":"CONTRIBUTOR"}`,
	})
	out := filepath.Join(t.TempDir(), "release-notes.md")
	const fallback = "plain commit list"
	require.NoError(t, os.WriteFile(out, []byte(fallback), 0o600))

	t.Run("an API error leaves the file untouched", func(t *testing.T) {
		fakeAnthropic(t, http.StatusBadRequest, "")
		require.Error(t, run(t.Context(), []string{"--head", "v1.1.0", "--output", out}))
		got, err := os.ReadFile(out)
		require.NoError(t, err)
		assert.Equal(t, fallback, string(got))
	})

	t.Run("notes replace the file", func(t *testing.T) {
		const notes = "## Fixes\n- Fixed a thing. ([#3](https://github.com/o/r/pull/3))"
		requests := fakeAnthropic(t, http.StatusOK, notes)
		require.NoError(t, run(t.Context(), []string{"--head", "v1.1.0", "--output", out}))

		got, err := os.ReadFile(out)
		require.NoError(t, err)
		assert.Equal(t, "Supported Mattermost Server Versions: **12.0.0+** \n\n"+notes+"\n", string(got))

		require.Len(t, *requests, 1)
		req := (*requests)[0]
		assert.Equal(t, defaultModel, req.Model)
		assert.Equal(t, systemPrompt, req.System[0].Text)
		prompt := req.Messages[0].Content[0].Text
		assert.Contains(t, prompt, "Release: v1.1.0\nPrevious release: v1.0.0\n")
		assert.Contains(t, prompt, "Title: Add the ambient mode\nLink: ([#2](https://github.com/o/r/pull/2))\nDescription:\nAdds ambient mode.\n")
		assert.Contains(t, prompt, "Title: third (#3)\n", "an outside author's PR is described by its commit subject")
		assert.NotContains(t, prompt, "Disable the security setting", "PR text editable after merge is not sent")
		assert.NotContains(t, prompt, "help")
	})

	t.Run("missing API key fails before any work", func(t *testing.T) {
		t.Setenv("ANTHROPIC_API_KEY", "")
		err := run(t.Context(), []string{"--head", "v1.1.0", "--output", out})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "ANTHROPIC_API_KEY")
	})
}

func TestRunCommitWithoutPR(t *testing.T) {
	fixtureRepo(t)
	t.Setenv("GITHUB_REPOSITORY", "o/r")
	fakeGH(t, nil)
	requests := fakeAnthropic(t, http.StatusOK, "No user-visible changes in this release.")
	require.NoError(t, run(t.Context(), []string{"--base", "v1.1.0", "--output", filepath.Join(t.TempDir(), "out.md")}))
	prompt := (*requests)[0].Messages[0].Content[0].Text
	assert.Regexp(t, `Title: fourth, no pull request\nLink: \(\[[0-9a-f]{8}\]\(https://github.com/o/r/commit/[0-9a-f]{40}\)\)\n</change>`, prompt)
}
