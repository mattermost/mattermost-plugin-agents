// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
)

var lineBreakReplacer = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")

// ChannelArtifactEntry holds data for formatting one channel library artifact.
type ChannelArtifactEntry struct {
	HeaderLabel string
	Entry       *channelartifacts.Entry
}

// WriteChannelArtifact writes catalog metadata for a channel library artifact.
// These are saved HTML library entries, not ordinary post file attachments.
func WriteChannelArtifact(w *strings.Builder, entry ChannelArtifactEntry) {
	if entry.HeaderLabel != "" {
		writeHeader(w, entry.HeaderLabel)
	}
	e := entry.Entry
	if e == nil {
		return
	}
	fmt.Fprintf(w, "ID: %s\n", e.ID)
	fmt.Fprintf(w, "Title: %s\n", lineBreakReplacer.Replace(e.Title))
	if e.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", lineBreakReplacer.Replace(e.Description))
	}
	fmt.Fprintf(w, "Channel ID: %s\n", e.ChannelID)
	fmt.Fprintf(w, "Version: %d\n", e.Version)
	if e.SHA256 != "" {
		sha := e.SHA256
		if len(sha) > 12 {
			sha = sha[:12]
		}
		fmt.Fprintf(w, "SHA256 prefix: %s\n", sha)
	}
	fmt.Fprintf(w, "Size: %d bytes\n", e.Size)
	if e.SourceAgentDisplayName != "" {
		fmt.Fprintf(w, "Source agent: %s\n", e.SourceAgentDisplayName)
	} else if e.SourceBotUserID != "" {
		fmt.Fprintf(w, "Source bot user ID: %s\n", e.SourceBotUserID)
	}
	if e.SourceFileName != "" {
		fmt.Fprintf(w, "Source file: %s\n", e.SourceFileName)
	}
	if e.SourcePostID != "" {
		fmt.Fprintf(w, "Source post ID: %s\n", e.SourcePostID)
	}
	if e.SourceFileID != "" {
		fmt.Fprintf(w, "Source file ID: %s\n", e.SourceFileID)
	}
	if e.UpdateAt > 0 {
		fmt.Fprintf(w, "Updated: %s\n", time.UnixMilli(e.UpdateAt).UTC().Format(time.RFC3339))
	}
	w.WriteString("\n")
}

// ChannelArtifactList formats a list of channel library artifacts for the LLM.
func ChannelArtifactList(entries []*channelartifacts.Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Found %d channel library artifact(s). These are saved HTML library entries for the channel, not ordinary post file attachments.\n\n", len(entries))
	for i, e := range entries {
		WriteChannelArtifact(&b, ChannelArtifactEntry{
			HeaderLabel: fmt.Sprintf("Artifact %d", i+1),
			Entry:       e,
		})
	}
	return b.String()
}
