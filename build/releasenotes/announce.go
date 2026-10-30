// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	webhookUsername = "Agents Releases"
	// Mattermost rejects posts longer than 16383 characters.
	maxAnnounceChars = 15000
)

var supportedVersionsRe = regexp.MustCompile(`^Supported Mattermost Server Versions:\s*\*\*(.+?)\*\*`)

// parsedNotes is release notes split into the parts the announcement uses.
type parsedNotes struct {
	// MinServer is the version from the "Supported Mattermost Server Versions" line.
	MinServer string
	// Sections maps a "## " heading to its non-empty lines.
	Sections map[string][]string
}

// parseNotes splits release notes at "## " headings. Text before the first
// heading is only inspected for the supported-versions line.
func parseNotes(notes string) parsedNotes {
	p := parsedNotes{Sections: map[string][]string{}}
	current := ""
	for line := range strings.SplitSeq(strings.ReplaceAll(notes, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimRight(line, " \t")
		switch {
		case strings.HasPrefix(trimmed, "## "):
			current = trimmed
		case current == "":
			if m := supportedVersionsRe.FindStringSubmatch(trimmed); m != nil && p.MinServer == "" {
				p.MinServer = strings.TrimSuffix(m[1], "+")
			}
		case trimmed != "":
			p.Sections[current] = append(p.Sections[current], trimmed)
		}
	}
	return p
}

type announceOptions struct {
	Repo    string
	Tag     string
	DocsURL string
	Notes   string
}

// formatAnnouncement builds the Mattermost post for a release. It lists only
// the breaking changes and new features and links to the full notes. It is
// deterministic: no model is involved.
func formatAnnouncement(o announceOptions) string {
	p := parseNotes(o.Notes)
	releaseURL := fmt.Sprintf("https://github.com/%s/releases/tag/%s", o.Repo, o.Tag)
	footer := fmt.Sprintf("[Release notes and downloads](%s)", releaseURL)
	if o.DocsURL != "" {
		footer += fmt.Sprintf(" | [Documentation](%s)", o.DocsURL)
	}

	var head strings.Builder
	fmt.Fprintf(&head, "#### Mattermost Agents %s released\n\n", o.Tag)
	if p.MinServer != "" {
		fmt.Fprintf(&head, "Requires Mattermost Server %s+.\n\n", p.MinServer)
	}

	type block struct {
		title string
		lines []string
	}
	var blocks []block
	for _, h := range []string{sectionFeatures, sectionBreaking} {
		if lines := bulletLines(p.Sections[h]); len(lines) > 0 {
			blocks = append(blocks, block{title: strings.TrimPrefix(h, "## "), lines: lines})
		}
	}

	if len(blocks) == 0 {
		return head.String() +
			"No breaking changes or new features are listed for this release. See the release notes for the full list of changes.\n\n" +
			footer
	}

	// Shorten the lists if the post would exceed the server limit.
	fixed := len(head.String()) + len(footer) + 200
	budget := maxAnnounceChars - fixed
	var body strings.Builder
	for i, b := range blocks {
		if i > 0 {
			body.WriteString("\n")
		}
		fmt.Fprintf(&body, "**%s**\n", b.title)
		kept := 0
		for _, l := range b.lines {
			if body.Len()+len(l)+1 > budget {
				break
			}
			body.WriteString(l + "\n")
			kept++
		}
		if kept < len(b.lines) {
			fmt.Fprintf(&body, "- %d more, see the release notes.\n", len(b.lines)-kept)
		}
	}
	return head.String() + body.String() + "\n" + footer
}

// bulletLines returns the "- " lines of a section.
func bulletLines(lines []string) []string {
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(l, "- ") {
			out = append(out, l)
		}
	}
	return out
}

// postWebhook sends text to a Mattermost incoming webhook.
func postWebhook(ctx context.Context, client *http.Client, webhookURL, text string) error {
	payload, err := json.Marshal(map[string]string{"text": text, "username": webhookUsername})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		// Do not echo the error's URL: it contains the webhook token.
		return fmt.Errorf("posting to webhook failed: %s", redactURLError(err))
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("webhook returned %s", resp.Status)
	}
	return nil
}

// redactURLError drops the URL from a net/http error, which would contain the
// webhook secret.
func redactURLError(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return ue.Err.Error()
	}
	return "request error"
}

func newWebhookClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}
