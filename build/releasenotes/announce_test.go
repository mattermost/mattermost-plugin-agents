// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const sampleNotes = "Supported Mattermost Server Versions: **12.0.0+** \n\n" +
	"## Breaking changes and upgrade notes\n" +
	"- Renamed the `Old` setting to `New`. ([#5](https://github.com/o/r/pull/5))\n\n" +
	"## New features\n" +
	"- Added experimental ambient auto-reply. ([#1](https://github.com/o/r/pull/1))\n" +
	"- Added agent version history. ([#2](https://github.com/o/r/pull/2))\n\n" +
	"## Improvements\n" +
	"- Sped up search. ([#3](https://github.com/o/r/pull/3))\n\n" +
	"## Fixes\n" +
	"- Fixed a crash. ([#4](https://github.com/o/r/pull/4))\n"

func TestParseNotes(t *testing.T) {
	p := parseNotes(sampleNotes)
	assert.Equal(t, "12.0.0", p.MinServer)
	assert.Len(t, p.Sections[sectionBreaking], 1)
	assert.Len(t, p.Sections[sectionFeatures], 2)
	assert.Len(t, p.Sections[sectionFixes], 1)

	legacy := parseNotes("Supported Mattermost Server Versions: **11.2.0+** \n## Enhancements\n\n## Fixes\n- abc123 Fix (#1)")
	assert.Equal(t, "11.2.0", legacy.MinServer)
	assert.Empty(t, legacy.Sections[sectionFeatures])
}

func TestFormatAnnouncement(t *testing.T) {
	opts := announceOptions{Repo: "o/r", Tag: "v2.12.0", DocsURL: "https://docs.example.com/agents", Notes: sampleNotes}

	for name, tc := range map[string]struct {
		notes    string
		contains []string
		excludes []string
	}{
		"breaking and features only": {
			notes: sampleNotes,
			contains: []string{
				"#### Mattermost Agents v2.12.0 released\n",
				"Requires Mattermost Server 12.0.0+.",
				"**Breaking changes and upgrade notes**\n- Renamed the `Old` setting",
				"**New features**\n- Added experimental ambient auto-reply.",
				"- Added agent version history.",
				"[Release notes and downloads](https://github.com/o/r/releases/tag/v2.12.0)",
				"[Documentation](https://docs.example.com/agents)",
			},
			excludes: []string{"Sped up search", "Fixed a crash", "Improvements", "Fixes"},
		},
		"features only": {
			notes:    "## New features\n- Added a thing. ([#1](u))\n",
			contains: []string{"**New features**\n- Added a thing.", "released"},
			excludes: []string{"Breaking", "Requires Mattermost Server"},
		},
		"nothing notable": {
			notes: "Supported Mattermost Server Versions: **12.0.0+** \n\n## Fixes\n- Fixed a crash. ([#4](u))\n",
			contains: []string{
				"No breaking changes or new features are listed for this release.",
				"[Release notes and downloads](https://github.com/o/r/releases/tag/v2.12.0)",
			},
			excludes: []string{"Fixed a crash"},
		},
		"plain commit-list fallback body": {
			notes:    "Supported Mattermost Server Versions: **12.0.0+** \n## Enhancements\n\n## Fixes\n- abc1234 Add thing (#1)",
			contains: []string{"No breaking changes or new features are listed"},
			excludes: []string{"Add thing"},
		},
		"empty notes": {
			notes:    "",
			contains: []string{"#### Mattermost Agents v2.12.0 released", "No breaking changes or new features"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			o := opts
			o.Notes = tc.notes
			got := formatAnnouncement(o)
			for _, s := range tc.contains {
				assert.Contains(t, got, s)
			}
			for _, s := range tc.excludes {
				assert.NotContains(t, got, s)
			}
		})
	}
}

func TestFormatAnnouncementWithoutDocsURL(t *testing.T) {
	got := formatAnnouncement(announceOptions{Repo: "o/r", Tag: "v1.0.0", Notes: sampleNotes})
	assert.NotContains(t, got, "Documentation")
}

func TestFormatAnnouncementStaysUnderPostLimit(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("## New features\n")
	for i := range 600 {
		fmt.Fprintf(&sb, "- Added feature number %d with a reasonably long description of the change. ([#%d](https://github.com/o/r/pull/%d))\n", i, i, i)
	}
	got := formatAnnouncement(announceOptions{Repo: "o/r", Tag: "v1.0.0", DocsURL: "https://d", Notes: sb.String()})
	assert.Less(t, len(got), 16383)
	assert.Contains(t, got, "more, see the release notes.")
	assert.Contains(t, got, "Added feature number 0 ")
	assert.Contains(t, got, "[Release notes and downloads]")
}

func TestPostWebhook(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&got))
	}))
	defer srv.Close()

	require.NoError(t, postWebhook(t.Context(), newWebhookClient(), srv.URL, "hello"))
	assert.Equal(t, map[string]string{"text": "hello", "username": "Agents Releases"}, got)
}

func TestPostWebhookErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no such hook", http.StatusNotFound)
	}))
	err := postWebhook(t.Context(), newWebhookClient(), srv.URL+"/hooks/secrettoken", "hello")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
	srv.Close()

	// A connection error must not leak the secret hook URL.
	err = postWebhook(t.Context(), newWebhookClient(), srv.URL+"/hooks/secrettoken", "hello")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "secrettoken")
	assert.NotContains(t, err.Error(), srv.URL)
}
