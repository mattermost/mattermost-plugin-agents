// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolResultText renders a tool result as text for the model, one content
// item per line. Text, including embedded text resources, is kept as is;
// binary content the model cannot receive is replaced by a placeholder naming
// its type, so the model knows the tool returned something. Structured content
// is rendered as JSON when the result carries no text.
func toolResultText(result *mcp.CallToolResult) string {
	var b strings.Builder
	hasText := false
	writeLine := func(line string) {
		b.WriteString(line)
		b.WriteByte('\n')
	}

	for _, content := range result.Content {
		switch c := content.(type) {
		case *mcp.TextContent:
			writeLine(c.Text)
			hasText = hasText || c.Text != ""
		case *mcp.ImageContent:
			writeLine(omittedContent("image", c.MIMEType))
		case *mcp.AudioContent:
			writeLine(omittedContent("audio", c.MIMEType))
		case *mcp.ResourceLink:
			writeLine(resourceLink(c))
		case *mcp.EmbeddedResource:
			if c.Resource == nil {
				continue
			}
			if c.Resource.Text != "" {
				writeLine(c.Resource.Text)
				hasText = true
			} else if len(c.Resource.Blob) > 0 {
				writeLine(omittedContent("resource", c.Resource.MIMEType))
			}
		}
	}

	if !hasText && result.StructuredContent != nil {
		if structured, err := json.Marshal(result.StructuredContent); err == nil {
			writeLine(string(structured))
		}
	}

	return b.String()
}

func omittedContent(kind, mimeType string) string {
	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" {
		return fmt.Sprintf("[%s content omitted]", kind)
	}
	return fmt.Sprintf("[%s content omitted: %s]", kind, mimeType)
}

func resourceLink(link *mcp.ResourceLink) string {
	label := link.Title
	if label == "" {
		label = link.Name
	}
	if label == "" {
		return fmt.Sprintf("[resource: %s]", link.URI)
	}
	return fmt.Sprintf("[resource: %s (%s)]", link.URI, label)
}
