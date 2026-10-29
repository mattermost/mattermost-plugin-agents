// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

const (
	pr1 = "https://github.com/o/r/pull/1"
	pr2 = "https://github.com/o/r/pull/2"
)

var testAllowed = map[string]string{
	pr1: "#1",
	pr2: "#2",
	"https://github.com/o/r/commit/abcdef1234567890": "abcdef12",
}

func TestFindEmoji(t *testing.T) {
	for name, tc := range map[string]struct {
		in   string
		want bool
	}{
		"plain text":         {"Added the `Foo` setting.", false},
		"pictograph":         {"Shipped \U0001F680", true},
		"party popper":       {"\U0001F389 release", true},
		"check mark symbol":  {"Done ✅", true},
		"warning sign":       {"⚠ careful", true},
		"star":               {"⭐ favorite", true},
		"variation selector": {"heart️", true},
		"shortcode":          {"Added a thing :tada:", true},
		"shortcode at start": {":rocket: Added", true},
		"time-like text":     {"Runs every 10:30:15 hours", false},
		"config path":        {"Set Foo:Bar:Baz to true", false},
		"arrows are allowed": {"Moved A → B", false},
		"command key":        {"Press ⌘K", false},
		"accents":            {"café naïve", false},
		"cjk":                {"日本語", false},
	} {
		t.Run(name, func(t *testing.T) {
			_, got := findEmoji(tc.in)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestStyleProblems(t *testing.T) {
	for name, tc := range map[string]struct {
		in      string
		wantAny string
	}{
		"clean":            {"Agents now retry failed tool calls.", ""},
		"exclamation":      {"Fixed a crash!", "exclamation"},
		"marketing word":   {"A powerful new editor.", "powerful"},
		"marketing casing": {"Seamless upgrade path.", "Seamless"},
		"hyphenated":       {"A game-changing update.", "game-changing"},
		"substring is ok":  {"Added a powerless-user check.", ""},
		"emoji":            {"Added a thing \U0001F389", "emoji"},
	} {
		t.Run(name, func(t *testing.T) {
			problems := styleProblems(tc.in)
			if tc.wantAny == "" {
				assert.Empty(t, problems)
				return
			}
			assert.Contains(t, strings.Join(problems, "\n"), tc.wantAny)
		})
	}
}

func TestValidateNotes(t *testing.T) {
	good := "## New features\n" +
		"- Added experimental ambient auto-reply for channels. ([#1](" + pr1 + "))\n" +
		"- Added per-agent version history. ([#1](" + pr1 + "), [#2](" + pr2 + "))\n" +
		"\n" +
		"## Fixes\n" +
		"- Fixed tool approvals being ignored after a quick click. ([abcdef12](https://github.com/o/r/commit/abcdef1234567890))\n"

	for name, tc := range map[string]struct {
		in      string
		wantErr string // substring of a problem; empty means valid
	}{
		"valid":                   {good, ""},
		"valid single section":    {"## Fixes\n- Fixed a crash on startup. ([#1](" + pr1 + "))", ""},
		"no-changes sentinel":     {noChangesSentinel, ""},
		"all four in order":       {"## Breaking changes and upgrade notes\n- Removed the `Old` setting. ([#1](" + pr1 + "))\n## New features\n- Added a thing here. ([#2](" + pr2 + "))\n## Improvements\n- Sped up search a lot. ([#2](" + pr2 + "))\n## Fixes\n- Fixed the bug properly. ([#1](" + pr1 + "))", ""},
		"empty":                   {"  \n", "empty"},
		"no heading":              {"- Added a thing. ([#1](" + pr1 + "))", "before the first section"},
		"intro text":              {"Here are the notes:\n" + good, "before the first section"},
		"unknown heading":         {"## Highlights\n- Added a thing here. ([#1](" + pr1 + "))", "not one of the allowed"},
		"wrong order":             {"## Fixes\n- Fixed a crash on start. ([#1](" + pr1 + "))\n## New features\n- Added a thing here. ([#2](" + pr2 + "))", "out of order"},
		"repeated section":        {"## Fixes\n- Fixed a crash on start. ([#1](" + pr1 + "))\n## Fixes\n- Fixed another. ([#2](" + pr2 + "))", "out of order or repeated"},
		"empty section":           {"## New features\n## Fixes\n- Fixed a crash on start. ([#1](" + pr1 + "))", "has no items"},
		"trailing empty section":  {"## Fixes\n- Fixed a crash on start. ([#1](" + pr1 + "))\n## Improvements\n", "has no items"},
		"paragraph under heading": {"## Fixes\nSome paragraph.\n", "single-line item"},
		"nested bullet":           {"## Fixes\n- Fixed a crash on start. ([#1](" + pr1 + "))\n  - detail", "single-line item"},
		"missing link":            {"## Fixes\n- Fixed a crash on start.", "end with the link"},
		"link not at the end":     {"## Fixes\n- Fixed a crash ([#1](" + pr1 + ")) on start.", "end with the link"},
		"unknown link":            {"## Fixes\n- Fixed a crash on start. ([#9](https://github.com/o/r/pull/9))", "not one of the input"},
		"wrong link text":         {"## Fixes\n- Fixed a crash on start. ([#2](" + pr1 + "))", "must be"},
		"inline link":             {"## Fixes\n- Fixed [a crash](" + pr2 + ") on start. ([#1](" + pr1 + "))", "only at the end"},
		"bare URL":                {"## Breaking changes and upgrade notes\n- Download the fix from https://evil.example/fix before upgrading. ([#1](" + pr1 + "))", "only at the end"},
		"autolink":                {"## Breaking changes and upgrade notes\n- Follow <https://evil.example/fix> before upgrading. ([#1](" + pr1 + "))", "only at the end"},
		"uppercase scheme":        {"## Breaking changes and upgrade notes\n- Install the patch from HTTPS://evil.example/fix first. ([#1](" + pr1 + "))", "only at the end"},
		"html anchor":             {"## Breaking changes and upgrade notes\n- Install <a href=\"//evil.example/fix\">the patch</a> first. ([#1](" + pr1 + "))", "only at the end"},
		"html in code span":       {"## Fixes\n- Fixed rendering of `<a href=\"//x\">` and `https://host` in posts. ([#1](" + pr1 + "))", ""},
		"uppercase autolink":      {"## Breaking changes and upgrade notes\n- Follow <HTTPS://evil.example/fix> first. ([#1](" + pr1 + "))", "only at the end"},
		"email":                   {"## Breaking changes and upgrade notes\n- Email licensing@evil.example before upgrading. ([#1](" + pr1 + "))", "only at the end"},
		"mailto":                  {"## Breaking changes and upgrade notes\n- Contact mailto:licensing before upgrading. ([#1](" + pr1 + "))", "only at the end"},
		"other repo issue":        {"## Breaking changes and upgrade notes\n- Follow the runbook in evilorg/runbook#4 first. ([#1](" + pr1 + "))", "only at the end"},
		"other repo commit":       {"## Breaking changes and upgrade notes\n- Apply evilorg/runbook@abc1234 first. ([#1](" + pr1 + "))", "only at the end"},
		"mention in prose":        {"## Fixes\n- Fixed replies to @copilot in threads. ([#1](" + pr1 + "))", "only at the end"},
		"mention in code span":    {"## Fixes\n- Fixed replies to `@copilot` in threads. ([#1](" + pr1 + "))", ""},
		"bare CR reference link":  {"## Breaking changes and upgrade notes\n- Install the patch [here][patch].\r[patch]: //evil.example/fix\r([#1](" + pr1 + "))", "control characters"},
		"reference link":          {"## Breaking changes and upgrade notes\n- Install the patch [here][patch] first. ([#1](" + pr1 + "))", "only at the end"},
		"issue number":            {"## Breaking changes and upgrade notes\n- Follow the steps in #4 first. ([#1](" + pr1 + "))", "only at the end"},
		"GH reference":            {"## Breaking changes and upgrade notes\n- Follow the steps in GH-4 first. ([#1](" + pr1 + "))", "only at the end"},
		"channel mention":         {"## Breaking changes and upgrade notes\n- Tell @channel to upgrade now. ([#1](" + pr1 + "))", "only at the end"},
		"CRLF is accepted":        {"## Fixes\r\n- Fixed a crash on startup. ([#1](" + pr1 + "))\r\n", ""},
		"www host":                {"## Breaking changes and upgrade notes\n- Follow www.evil.example/fix before upgrading. ([#1](" + pr1 + "))", "only at the end"},
		"no description":          {"## Fixes\n- ([#1](" + pr1 + "))", "no description"},
		"emoji":                   {"## Fixes\n- Fixed a crash on start \U0001F389 ([#1](" + pr1 + "))", "emoji"},
		"exclamation":             {"## Fixes\n- Fixed a crash on start! ([#1](" + pr1 + "))", "exclamation"},
		"marketing":               {"## New features\n- Added a powerful editor to chat. ([#1](" + pr1 + "))", "powerful"},
		"first person":            {"## New features\n- We added an editor to chat. ([#1](" + pr1 + "))", "first person"},
	} {
		t.Run(name, func(t *testing.T) {
			problems := validateNotes(tc.in, testAllowed)
			if tc.wantErr == "" {
				assert.Empty(t, problems)
				return
			}
			assert.Contains(t, strings.Join(problems, "\n"), tc.wantErr)
		})
	}
}

func TestNormalizeNotes(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"plain":           {"  ## Fixes\n- x  \n", "## Fixes\n- x"},
		"fenced markdown": {"```markdown\n## Fixes\n- x\n```", "## Fixes\n- x"},
		"bare fence":      {"```\n## Fixes\n- x\n```\n", "## Fixes\n- x"},
		"inner fence":     {"## Fixes\n- x `y`\n", "## Fixes\n- x `y`"},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, normalizeNotes(tc.in))
		})
	}
}
