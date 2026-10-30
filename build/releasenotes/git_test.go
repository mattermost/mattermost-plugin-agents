// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPRNumber(t *testing.T) {
	for subject, want := range map[string]int{
		"Add experimental ambient auto-reply (#997)": 997,
		"Bump Go version":                0,
		`Revert "Add thing (#12)" (#13)`: 13,
		"Fix crash, fixes #55":           0,
	} {
		assert.Equal(t, want, prNumber(subject), subject)
	}
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

// fixtureRepo builds v1.0.0, v1.1.0-rc1, v1.1.0 and an untagged tip in a new
// repository and makes it the working directory.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	gitIn(t, dir, "init", "-q")
	commit := func(subject string) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"min_server_version":"12.0.0","x":"`+subject+`"}`), 0o600))
		gitIn(t, dir, "add", "plugin.json")
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
	return dir
}

func TestReleaseRange(t *testing.T) {
	fixtureRepo(t)
	ctx := t.Context()

	for name, tc := range map[string]struct{ head, tag, want string }{
		"full release skips pre-releases": {head: "v1.1.0", tag: "v1.1.0", want: "v1.0.0"},
		"pre-release uses previous tag":   {head: "v1.1.0-rc1", tag: "v1.1.0-rc1", want: "v1.0.0"},
		"untagged head uses newest tag":   {head: "HEAD", tag: "", want: "v1.1.0"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := previousRelease(ctx, tc.head, tc.tag)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
	_, err := previousRelease(ctx, "v1.0.0", "v1.0.0")
	assert.Error(t, err, "the first release has no previous tag")

	commits, err := commitsBetween(ctx, "v1.0.0", "v1.1.0")
	require.NoError(t, err)
	require.Len(t, commits, 2)
	assert.Equal(t, "third (#3)", commits[0].Subject)
	assert.Equal(t, 3, commits[0].PR)
	assert.Equal(t, 2, commits[1].PR)

	assert.Equal(t, "v1.1.0", releaseTag(ctx, "v1.1.0"))
	assert.Empty(t, releaseTag(ctx, "HEAD"))
	t.Setenv("GITHUB_REF_TYPE", "tag")
	t.Setenv("GITHUB_REF_NAME", "v9.9.9")
	assert.Equal(t, "v9.9.9", releaseTag(ctx, "HEAD"), "a tag build names its own tag")

	v, err := minServerVersion(ctx, "v1.1.0")
	require.NoError(t, err)
	assert.Equal(t, "12.0.0", v)
}
