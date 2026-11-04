// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type commit struct {
	SHA     string
	Subject string
	// PR is the pull request that landed the commit, or 0.
	PR int
}

var (
	// Squash merges end the subject with "(#1234)".
	prNumberRe    = regexp.MustCompile(`\(#(\d+)\)\s*$`)
	fullReleaseRe = regexp.MustCompile(`^v\d+\.\d+\.\d+$`)
)

func prNumber(subject string) int {
	m := prNumberRe.FindStringSubmatch(subject)
	if m == nil {
		return 0
	}
	n, _ := strconv.Atoi(m[1])
	return n
}

// runCmd runs a command and returns its stdout.
func runCmd(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			err = fmt.Errorf("%w: %s", err, bytes.TrimSpace(ee.Stderr))
		}
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return string(out), nil
}

// releaseTag returns the tag being released: the ref of a tag build, or a tag
// pointing at head. It returns "" when there is none.
func releaseTag(ctx context.Context, head string) string {
	if os.Getenv("GITHUB_REF_TYPE") == "tag" {
		return os.Getenv("GITHUB_REF_NAME")
	}
	out, _ := runCmd(ctx, "git", "describe", "--tags", "--exact-match", head)
	return strings.TrimSpace(out)
}

// previousRelease finds the tag to compare head with, like release-notes.sh in
// mattermost/actions plugin-ci/build: the previous full release for a full
// release (vX.Y.Z), otherwise the previous tag of any kind.
func previousRelease(ctx context.Context, head, tag string) (string, error) {
	args := []string{"describe", "--tags", "--abbrev=0", "--match", "v*"}
	if fullReleaseRe.MatchString(tag) {
		args = append(args, "--exclude", "*-*")
	}
	out, err := runCmd(ctx, "git", append(args, head+"^")...)
	if err != nil {
		return "", fmt.Errorf("no previous release found, pass --base: %w", err)
	}
	return strings.TrimSpace(out), nil
}

func commitsBetween(ctx context.Context, base, head string) ([]commit, error) {
	out, err := runCmd(ctx, "git", "log", "--format=%H%x09%s", base+".."+head)
	if err != nil {
		return nil, err
	}
	var commits []commit
	for line := range strings.Lines(out) {
		sha, subject, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if ok {
			commits = append(commits, commit{SHA: sha, Subject: subject, PR: prNumber(subject)})
		}
	}
	if len(commits) == 0 {
		return nil, fmt.Errorf("no commits in %s..%s", base, head)
	}
	return commits, nil
}

// minServerVersion reads min_server_version from plugin.json at head.
func minServerVersion(ctx context.Context, head string) (string, error) {
	out, err := runCmd(ctx, "git", "show", head+":plugin.json")
	if err != nil {
		return "", err
	}
	var manifest struct {
		MinServerVersion string `json:"min_server_version"`
	}
	if err := json.Unmarshal([]byte(out), &manifest); err != nil {
		return "", fmt.Errorf("parsing plugin.json: %w", err)
	}
	if manifest.MinServerVersion == "" {
		return "", errors.New("plugin.json has no min_server_version")
	}
	return manifest.MinServerVersion, nil
}
