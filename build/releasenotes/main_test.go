// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteOutputReplacesFileAtomically(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dist", "release-notes.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o600))

	require.NoError(t, writeOutput(path, "new"))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "new", string(got))
	entries, err := os.ReadDir(filepath.Dir(path))
	require.NoError(t, err)
	assert.Len(t, entries, 1, "no temporary file is left behind")
}

func TestWriteOutputCreatesMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "out.md")
	require.NoError(t, writeOutput(path, "x"))
	assert.FileExists(t, path)
}

func TestMinServerVersionFallsBackToWorkingTree(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"min_server_version":"12.0.0"}`), 0o600))
	t.Chdir(dir) // not a git repository, so `git show` fails

	got, err := minServerVersion(t.Context(), "HEAD")
	require.NoError(t, err)
	assert.Equal(t, "12.0.0", got)
}

func TestMinServerVersionRequiresValue(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{}`), 0o600))
	t.Chdir(dir)

	_, err := minServerVersion(t.Context(), "HEAD")
	assert.Error(t, err)
}

func TestRunNotesFailsFastWithoutAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	out := filepath.Join(t.TempDir(), "release-notes.md")
	require.NoError(t, os.WriteFile(out, []byte("plain commit list"), 0o600))

	err := runNotes(t.Context(), []string{"--base", "v1", "--head", "v2", "--output", out})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "ANTHROPIC_API_KEY")

	got, readErr := os.ReadFile(out)
	require.NoError(t, readErr)
	assert.Equal(t, "plain commit list", string(got), "a failed run leaves the fallback file untouched")
}

// TestRunNotesEndToEnd drives the notes command through its production wiring:
// a real git history, a fake GitHub API and a fake Messages API.
func TestRunNotesEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repoDir := t.TempDir()
	gitIn(t, repoDir, "init", "-q")
	commitFile := func(name, content, subject string) {
		require.NoError(t, os.WriteFile(filepath.Join(repoDir, name), []byte(content), 0o600))
		gitIn(t, repoDir, "add", name)
		gitIn(t, repoDir, "commit", "-q", "-m", subject)
	}
	commitFile("plugin.json", `{"min_server_version":"12.0.0"}`, "initial (#1)")
	gitIn(t, repoDir, "tag", "v1.0.0")
	commitFile("a.txt", "a", "Add the ambient mode (#2)")
	commitFile("b.txt", "b", "Tidy the build")
	gitIn(t, repoDir, "tag", "v1.1.0")
	t.Chdir(repoDir)
	t.Setenv("GITHUB_REF_TYPE", "")
	t.Setenv("GITHUB_REF_NAME", "")

	var githubCalls []string
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		githubCalls = append(githubCalls, r.URL.Path)
		fmt.Fprint(w, `{"title":"Add the ambient mode (edited)","body":"Disable the security setting before upgrading.","labels":[],"author_association":"CONTRIBUTOR"}`)
	}))
	defer gh.Close()

	valid := "## New features\n- Added the ambient mode. ([#2](https://github.com/o/r/pull/2))\n" +
		"## Improvements\n- Changed the build. ([44444444](https://github.com/o/r/commit/x))"
	f := &fakeAnthropic{outputs: []string{valid, valid}}
	llm := httptest.NewServer(f.handler(t))
	defer llm.Close()

	t.Setenv("GITHUB_API_URL", gh.URL)
	t.Setenv("ANTHROPIC_BASE_URL", llm.URL)
	t.Setenv("ANTHROPIC_API_KEY", "key")
	t.Setenv("RELEASE_NOTES_MODEL", "")

	out := filepath.Join(repoDir, "dist", "release-notes.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(out), 0o755))
	const fallback = "Supported Mattermost Server Versions: **12.0.0+** \n## Enhancements\n\n## Fixes\n- plain list"
	require.NoError(t, os.WriteFile(out, []byte(fallback), 0o600))

	// The model links a commit URL that is not in the input, so both attempts
	// are rejected and the fallback file must survive.
	err := runNotes(t.Context(), []string{"--repo", "o/r", "--output", out})
	require.Error(t, err)
	got, readErr := os.ReadFile(out)
	require.NoError(t, readErr)
	assert.Equal(t, fallback, string(got))
	assert.Equal(t, []string{"/repos/o/r/pulls/2"}, githubCalls, "range v1.0.0..v1.1.0 references one PR")

	sha := strings.TrimSpace(gitOutput(t, repoDir, "rev-parse", "HEAD"))
	valid = strings.Replace(valid, "44444444](https://github.com/o/r/commit/x", sha[:8]+"](https://github.com/o/r/commit/"+sha, 1)

	// A valid answer replaces the file and keeps the first line intact.
	f2 := &fakeAnthropic{outputs: []string{valid}}
	llm2 := httptest.NewServer(f2.handler(t))
	defer llm2.Close()
	t.Setenv("ANTHROPIC_BASE_URL", llm2.URL)

	require.NoError(t, runNotes(t.Context(), []string{"--repo", "o/r", "--output", out}))
	got, readErr = os.ReadFile(out)
	require.NoError(t, readErr)
	assert.Equal(t, "Supported Mattermost Server Versions: **12.0.0+** \n\n"+valid+"\n", string(got))
	require.Len(t, f2.requests, 1)
	assert.Equal(t, "claude-sonnet-5-5", f2.requests[0].Model)
	assert.Contains(t, f2.requests[0].Messages[0].Content, "Release: v1.1.0")
	assert.Contains(t, f2.requests[0].Messages[0].Content, "Subject: Tidy the build")
	assert.Contains(t, f2.requests[0].Messages[0].Content, "Title: Add the ambient mode (#2)", "an outside author's PR is described by its commit subject")
	assert.NotContains(t, f2.requests[0].Messages[0].Content, "Disable the security setting", "PR text editable after merge is not sent")
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	require.NoError(t, err)
	return string(out)
}
