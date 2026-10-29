// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testBlogOptions() blogOptions {
	return blogOptions{
		Repo:    "o/r",
		Tag:     "v2.12.0",
		DocsURL: "https://docs.example.com/agents",
		Notes:   sampleNotes,
		Docs: []docFile{
			{Name: "user_guide.md", Content: "# User guide\nAsk the agent."},
			{Name: "admin_guide.md", Content: strings.Repeat("x", maxDocChars+100)},
		},
	}
}

func TestBuildBlogPrompt(t *testing.T) {
	prompt := buildBlogPrompt(testBlogOptions())
	for _, want := range []string{
		"Release: v2.12.0",
		"- https://github.com/o/r/releases/tag/v2.12.0",
		"- https://docs.example.com/agents",
		"- https://github.com/o/r/blob/v2.12.0/docs/user_guide.md",
		"<release-notes>\nSupported Mattermost Server Versions: **12.0.0+**",
		`<documentation file="user_guide.md" url="https://github.com/o/r/blob/v2.12.0/docs/user_guide.md">`,
		"Ask the agent.",
		"[truncated]",
	} {
		assert.Contains(t, prompt, want)
	}
}

func TestValidateBlog(t *testing.T) {
	allowed := docLinks(testBlogOptions())
	good := "# Mattermost Agents 2.12\n\nThis release adds ambient replies.\n\n## Ambient replies\n\nSee the [admin guide](https://github.com/o/r/blob/v2.12.0/docs/admin_guide.md#ambient).\n"

	for name, tc := range map[string]struct {
		in      string
		wantErr string
	}{
		"valid":              {good, ""},
		"bare allowed url":   {"# T\n\nRead https://docs.example.com/agents.\n", ""},
		"empty":              {"", "empty"},
		"no title":           {"Intro\n\n## A\n", "title"},
		"emoji":              {"# T \U0001F680\n", "emoji"},
		"exclamation":        {"# T\n\nIt works!\n", "exclamation"},
		"marketing":          {"# T\n\nA seamless experience.\n", "seamless"},
		"first person":       {"# T\n\nWe built this.\n", "first person"},
		"invented link":      {"# T\n\n[more](https://example.com/blog)\n", "not in the allowed list"},
		"invented bare link": {"# T\n\nSee https://example.com/blog for more.\n", "not in the allowed list"},
		"uppercase scheme":   {"# T\n\nSee HTTPS://example.com/blog for more.\n", "not in the allowed list"},
		"email":              {"# T\n\nWrite to sales@example.com.\n", "email address"},
		"other repo":         {"# T\n\nSee example/repo#12.\n", "another repository"},
		"www host":           {"# T\n\nVisit www.evil.example today.\n", "www. host"},
		"html anchor":        {"# T\n\nSee <a href=\"//evil.example\">this</a>.\n", "raw HTML"},
		"mention":            {"# T\n\nAsk `@copilot` or @copilot.\n", ""},
	} {
		t.Run(name, func(t *testing.T) {
			problems := validateBlog(tc.in, allowed)
			if tc.wantErr == "" {
				assert.Empty(t, problems)
				return
			}
			assert.Contains(t, strings.Join(problems, "\n"), tc.wantErr)
		})
	}
}

func TestGenerateBlogRetriesOnce(t *testing.T) {
	f := &fakeAnthropic{outputs: []string{
		"# T\n\nIt is a powerful release.\n",
		"# Mattermost Agents 2.12\n\nThis release adds ambient replies.\n",
	}}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()

	got, err := generateBlog(t.Context(), newTestLLM(srv.URL), testBlogOptions())
	require.NoError(t, err)
	assert.Equal(t, "# Mattermost Agents 2.12\n\nThis release adds ambient replies.\n", got)
	require.Len(t, f.requests, 2)
	assert.Equal(t, blogSystemPrompt, f.requests[0].System)
	assert.Contains(t, f.requests[1].Messages[2].Content, "powerful")
	assert.Contains(t, f.requests[1].Messages[2].Content, "blog draft")
}

func TestLoadDocsSkipsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "user_guide.md"), []byte("hello"), 0o600))

	docs, err := loadDocs(dir, defaultBlogDocs)
	require.NoError(t, err)
	require.Len(t, docs, 1)
	assert.Equal(t, "user_guide.md", docs[0].Name)
	assert.Equal(t, "hello", docs[0].Content)
}
