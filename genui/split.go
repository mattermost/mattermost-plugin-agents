// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package genui holds the server side of generative UI: locating the OpenUI
// Lang blocks in a reply and validating what a user submits from them.
package genui

import (
	"regexp"
	"strings"
)

var fenceOpen = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})\\s*([^\\s`]*)\\s*$")

var fenceClose = regexp.MustCompile("^(`+|~+)$")

func isUILanguage(lang string) bool {
	switch strings.ToLower(lang) {
	case "openui-lang", "openui":
		return true
	}
	return false
}

func isFenceClose(line, fence string) bool {
	trimmed := strings.TrimSpace(line)
	return len(trimmed) >= len(fence) && trimmed[0] == fence[0] && fenceClose.MatchString(trimmed)
}

// ClosedBlocks returns the source of every terminated OpenUI Lang fence in
// message, in order. It mirrors splitGenerativeUI in the webapp
// (webapp/src/components/generative_ui/split_message.ts), so block indexes
// agree between client and server: fences of other languages are skipped,
// including any UI fence nested inside them, and an unterminated UI fence is
// not a block.
func ClosedBlocks(message string) []string {
	var blocks []string
	var ui []string
	inUI := false
	insideOtherFence := false
	fence := ""

	for line := range strings.SplitSeq(message, "\n") {
		if inUI {
			if isFenceClose(line, fence) {
				blocks = append(blocks, strings.Join(ui, "\n"))
				ui = nil
				inUI = false
			} else {
				ui = append(ui, line)
			}
			continue
		}

		if insideOtherFence {
			if isFenceClose(line, fence) {
				insideOtherFence = false
			}
			continue
		}

		if open := fenceOpen.FindStringSubmatch(line); open != nil {
			fence = open[1]
			if isUILanguage(open[2]) {
				inUI = true
				continue
			}
			insideOtherFence = true
		}
	}

	return blocks
}
