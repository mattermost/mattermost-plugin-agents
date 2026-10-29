// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// commit is one entry of the release range.
type commit struct {
	SHA     string
	Subject string
	// PRs are the pull request numbers referenced by the subject, in order.
	PRs []int
}

// short returns the abbreviated hash used in commit-only links.
func (c commit) short() string {
	if len(c.SHA) > 8 {
		return c.SHA[:8]
	}
	return c.SHA
}

var (
	// Squash merges end the subject with "(#1234)".
	prNumberRe = regexp.MustCompile(`\(#(\d+)\)`)
	// Merge commits created by the GitHub UI.
	mergePRRe = regexp.MustCompile(`^Merge pull request #(\d+)\b`)

	fullReleaseRe = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
)

// extractPRNumbers returns the distinct pull request numbers referenced by a
// commit subject, in order of appearance.
func extractPRNumbers(subject string) []int {
	var out []int
	seen := map[int]bool{}
	add := func(s string) {
		n, err := strconv.Atoi(s)
		if err != nil || n <= 0 || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	if m := mergePRRe.FindStringSubmatch(subject); m != nil {
		add(m[1])
	}
	for _, m := range prNumberRe.FindAllStringSubmatch(subject, -1) {
		add(m[1])
	}
	return out
}

// parseGitLog parses the output of `git log --pretty=format:%H%x09%s`.
func parseGitLog(out string) []commit {
	var commits []commit
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sha, subject, ok := strings.Cut(line, "\t")
		if !ok || sha == "" {
			continue
		}
		subject = strings.TrimSpace(subject)
		commits = append(commits, commit{SHA: sha, Subject: subject, PRs: extractPRNumbers(subject)})
	}
	return commits
}

// selectBase picks the previous release from the tags that are strict
// ancestors of the head, newest first. It mirrors release-notes.sh from
// mattermost/actions plugin-ci/build: full releases (vX.Y.Z) are compared with
// the previous full release; anything else with the previous tag of any kind.
func selectBase(ancestorTags []string, currentTag string) string {
	full := fullReleaseRe.MatchString(currentTag)
	for _, tag := range ancestorTags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if full && !fullReleaseRe.MatchString(tag) {
			continue
		}
		return tag
	}
	return ""
}

func runGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // arguments are refs supplied by the operator or CI
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// detectTag returns the tag being released: the tag ref of a tag build, or a
// tag pointing exactly at head. It returns "" when there is none.
func detectTag(ctx context.Context, head string) string {
	if os.Getenv("GITHUB_REF_TYPE") == "tag" && os.Getenv("GITHUB_REF_NAME") != "" {
		return os.Getenv("GITHUB_REF_NAME")
	}
	out, err := runGit(ctx, "describe", "--tags", "--exact-match", head)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// resolveBase finds the previous release for head using git.
func resolveBase(ctx context.Context, head, currentTag string) (string, error) {
	// versionsort.suffix makes vX.Y.Z-rcN sort below vX.Y.Z.
	out, err := runGit(ctx, "-c", "versionsort.suffix=-rc", "tag", "-l", "v*",
		"--merged", head, "--no-contains", head, "--sort=-v:refname")
	if err != nil {
		return "", err
	}
	base := selectBase(strings.Split(out, "\n"), currentTag)
	if base == "" {
		return "", fmt.Errorf("no previous release tag found before %s; pass --base", head)
	}
	return base, nil
}

func collectCommits(ctx context.Context, base, head string) ([]commit, error) {
	out, err := runGit(ctx, "log", "--pretty=format:%H%x09%s", "--no-color", "--no-decorate", base+".."+head)
	if err != nil {
		return nil, err
	}
	return parseGitLog(out), nil
}

// uniquePRNumbers returns every distinct PR number referenced by commits.
func uniquePRNumbers(commits []commit) []int {
	var out []int
	seen := map[int]bool{}
	for _, c := range commits {
		for _, n := range c.PRs {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}
