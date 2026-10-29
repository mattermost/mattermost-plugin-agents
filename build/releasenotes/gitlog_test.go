// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractPRNumbers(t *testing.T) {
	for name, tc := range map[string]struct {
		subject string
		want    []int
	}{
		"squash merge":          {"Add experimental ambient auto-reply (#997)", []int{997}},
		"no number":             {"Bump Go version", nil},
		"revert of a PR":        {`Revert "Add thing (#12)" (#13)`, []int{13}},
		"repeated number":       {"Fix (#5) and again (#5)", []int{5}},
		"issue ref is ignored":  {"Fix crash, fixes #55", nil},
		"merge commit":          {"Merge pull request #321 from user/branch", []int{321}},
		"merge and squash form": {"Merge pull request #321 from user/branch (#322)", []int{321}},
		"number inside text":    {"Handle (#7) in the middle of a subject", []int{7}},
		"huge number rejected":  {"Fix (#99999999999999999999999)", nil},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, extractPRNumbers(tc.subject))
		})
	}
}

func TestParseGitLog(t *testing.T) {
	out := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\tAdd feature (#10)\n" +
		"\n" +
		"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\tTidy up\n" +
		"malformed line without a tab\n"
	got := parseGitLog(out)
	require.Len(t, got, 2)
	assert.Equal(t, "Add feature (#10)", got[0].Subject)
	assert.Equal(t, []int{10}, got[0].PRs)
	assert.Equal(t, "aaaaaaaa", got[0].short())
	assert.Empty(t, got[1].PRs)
}

func TestSelectBase(t *testing.T) {
	// Newest first, as `git tag --sort=-v:refname` prints them.
	tags := []string{"v2.12.0-rc1", "v2.11.1", "v2.11.0-rc2", "v2.11.0", "v2.10.0"}
	for name, tc := range map[string]struct {
		current string
		tags    []string
		want    string
	}{
		"full release skips pre-releases":    {"v2.12.0", tags, "v2.11.1"},
		"pre-release uses the newest tag":    {"v2.12.0-rc2", tags, "v2.12.0-rc1"},
		"untagged build uses the newest":     {"", tags, "v2.12.0-rc1"},
		"full release with only rc tags":     {"v2.12.0", []string{"v2.12.0-rc1"}, ""},
		"blank lines are ignored":            {"v2.12.0", []string{"", " v2.11.1 "}, "v2.11.1"},
		"no tags":                            {"v1.0.0", nil, ""},
		"non-semver current acts like a pre": {"latest-master", tags, "v2.12.0-rc1"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, selectBase(tc.tags, tc.current))
		})
	}
}

func TestUniquePRNumbers(t *testing.T) {
	commits := []commit{
		{PRs: []int{3, 1}},
		{},
		{PRs: []int{1, 2}},
	}
	assert.Equal(t, []int{3, 1, 2}, uniquePRNumbers(commits))
}

// gitIn runs git in dir for building a fixture repository.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
}

// TestResolveBaseAndCollectCommits runs the real git commands against a
// fixture history: v1.0.0, v1.1.0-rc1, v1.1.0 and an unreleased tip.
func TestResolveBaseAndCollectCommits(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	commit := func(subject string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "f.txt"), []byte(subject), 0o600))
		gitIn(t, dir, "add", "f.txt")
		gitIn(t, dir, "commit", "-q", "-m", subject)
	}
	commit("first (#1)")
	gitIn(t, dir, "tag", "v1.0.0")
	commit("second (#2)")
	gitIn(t, dir, "tag", "v1.1.0-rc1")
	commit("third (#3)")
	gitIn(t, dir, "tag", "v1.1.0")
	commit("fourth, no pull request")
	t.Chdir(dir)
	t.Setenv("GITHUB_REF_TYPE", "") // CI sets these on tag builds
	t.Setenv("GITHUB_REF_NAME", "")
	ctx := context.Background()

	for name, tc := range map[string]struct {
		head, current, want string
		wantErr             bool
	}{
		"full release uses previous full release": {head: "v1.1.0", current: "v1.1.0", want: "v1.0.0"},
		"pre-release uses previous tag":           {head: "v1.1.0-rc1", current: "v1.1.0-rc1", want: "v1.0.0"},
		"untagged head uses newest tag":           {head: "HEAD", current: "", want: "v1.1.0"},
		"first release has no previous tag":       {head: "v1.0.0", current: "v1.0.0", wantErr: true},
	} {
		t.Run("resolveBase/"+name, func(t *testing.T) {
			base, err := resolveBase(ctx, tc.head, tc.current)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, base)
		})
	}

	commits, err := collectCommits(ctx, "v1.0.0", "v1.1.0")
	require.NoError(t, err)
	require.Len(t, commits, 2)
	assert.Equal(t, "third (#3)", commits[0].Subject)
	assert.Equal(t, []int{3}, commits[0].PRs)
	assert.Equal(t, "second (#2)", commits[1].Subject)

	for name, tc := range map[string]struct{ head, want string }{
		"tagged head":   {head: "v1.1.0", want: "v1.1.0"},
		"untagged head": {head: "HEAD", want: ""},
	} {
		t.Run("detectTag/"+name, func(t *testing.T) {
			assert.Equal(t, tc.want, detectTag(ctx, tc.head))
		})
	}

	t.Setenv("GITHUB_REF_TYPE", "tag")
	t.Setenv("GITHUB_REF_NAME", "v9.9.9")
	assert.Equal(t, "v9.9.9", detectTag(ctx, "HEAD"), "a tag build names its own tag")
}
