// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Section headings in the order they must appear.
const (
	sectionBreaking     = "## Breaking changes and upgrade notes"
	sectionFeatures     = "## New features"
	sectionImprovements = "## Improvements"
	sectionFixes        = "## Fixes"
)

var sectionOrder = []string{sectionBreaking, sectionFeatures, sectionImprovements, sectionFixes}

// noChangesSentinel is what the model returns when nothing is user-visible.
const noChangesSentinel = "NO_USER_VISIBLE_CHANGES"

const noChangesText = "No user-visible changes in this release."

var (
	// Emoji shortcodes such as :tada:.
	emojiShortcodeRe = regexp.MustCompile(`(?:^|\s):[a-z0-9_+-]{2,}:(?:\s|$|[.,;])`)

	bannedWordsRe = regexp.MustCompile(`(?i)\b(?:` + strings.Join([]string{
		`exciting`, `excited`, `thrilled`, `delighted`, `powerful`, `seamless`, `seamlessly`,
		`revolutionary`, `revolutionize`, `game-changing`, `game changer`, `groundbreaking`,
		`cutting-edge`, `state-of-the-art`, `best-in-class`, `next-level`, `amazing`, `awesome`,
		`incredible`, `blazing`, `supercharge`, `supercharged`, `effortless`, `effortlessly`,
		`world-class`, `unleash`, `robust`, `delight`,
	}, "|") + `)\b`)

	firstPersonRe = regexp.MustCompile(`(?i)\b(?:we|we're|we've|we'll|our|ours)\b`)

	// A trailing parenthesised group of markdown links.
	trailingLinksRe = regexp.MustCompile(`\(((?:\[[^\]]+\]\([^)\s]+\)(?:, )?)+)\)$`)
	linkRe          = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
	// GitHub also autolinks "www." hosts without a scheme.
	wwwAutolinkRe = regexp.MustCompile(`(?i)\bwww\.[a-z0-9-]+\.[a-z]`)
)

// isEmoji reports whether r is an emoji or an emoji modifier. Plain arrows,
// box drawing and the like are not matched.
func isEmoji(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF: // pictographs, emoticons, transport, flags, supplemental symbols
		return true
	case r >= 0x2600 && r <= 0x27BF: // miscellaneous symbols and dingbats
		return true
	case r >= 0x2B00 && r <= 0x2BFF: // stars, large squares and circles
		return true
	case r >= 0x231A && r <= 0x23FF: // watch, hourglass, media controls
		return true
	case r == 0x2934 || r == 0x2935 || r == 0x3030 || r == 0x303D || r == 0x3297 || r == 0x3299:
		return true
	case r == 0xFE0F || r == 0x200D || r == 0x20E3: // emoji presentation selector, joiner, keycap
		return true
	}
	return false
}

// findEmoji returns the first emoji or emoji shortcode in s.
func findEmoji(s string) (string, bool) {
	for _, r := range s {
		if isEmoji(r) {
			return fmt.Sprintf("U+%04X", r), true
		}
	}
	if m := emojiShortcodeRe.FindString(s); m != "" {
		return strings.TrimSpace(m), true
	}
	return "", false
}

// styleProblems checks the tone rules shared by every generated document:
// no emoji, no exclamation marks and no marketing language.
func styleProblems(s string) []string {
	var problems []string
	if e, ok := findEmoji(s); ok {
		problems = append(problems, fmt.Sprintf("contains an emoji (%s); use none", e))
	}
	if strings.Contains(s, "!") {
		problems = append(problems, "contains an exclamation mark; use none")
	}
	if w := bannedWordsRe.FindString(s); w != "" {
		problems = append(problems, fmt.Sprintf("contains the marketing word %q", w))
	}
	return problems
}

// validateNotes checks model output against the release notes format.
// allowed maps each permitted link URL to its link text. It returns every
// problem found; an empty result means the output is acceptable.
func validateNotes(out string, allowed map[string]string) []string {
	out = strings.TrimSpace(out)
	if out == "" {
		return []string{"output is empty"}
	}
	if out == noChangesSentinel {
		return nil
	}

	problems := styleProblems(out)
	if firstPersonRe.MatchString(out) {
		problems = append(problems, "uses first person (we, our); state the change directly")
	}

	lastSection := -1
	bullets := 0
	sawHeading := false
	closeSection := func(heading string) {
		if sawHeading && bullets == 0 {
			problems = append(problems, fmt.Sprintf("section %q has no items; omit empty sections", heading))
		}
	}
	currentHeading := ""
	for i, line := range strings.Split(out, "\n") {
		n := i + 1
		trimmed := strings.TrimRight(line, " \t")
		switch {
		case trimmed == "":
		case strings.HasPrefix(trimmed, "#"):
			closeSection(currentHeading)
			switch idx := indexOf(sectionOrder, trimmed); {
			case idx < 0:
				problems = append(problems, fmt.Sprintf("line %d: heading %q is not one of the allowed sections", n, trimmed))
			case idx <= lastSection:
				problems = append(problems, fmt.Sprintf("line %d: section %q is out of order or repeated", n, trimmed))
			default:
				lastSection = idx
			}
			currentHeading = trimmed
			sawHeading = true
			bullets = 0
		case !sawHeading:
			problems = append(problems, fmt.Sprintf("line %d: text before the first section heading; no intro text", n))
		case !strings.HasPrefix(trimmed, "- "):
			problems = append(problems, fmt.Sprintf("line %d: every line under a section must be a single-line item starting with \"- \"", n))
		default:
			bullets++
			problems = append(problems, bulletProblems(n, trimmed, allowed)...)
		}
	}
	closeSection(currentHeading)
	if !sawHeading {
		problems = append(problems, "no \"## \" section headings found")
	}
	return problems
}

// bulletProblems checks one "- " line: it must end with links to the
// pull requests or commits it summarizes, and only to ones in the input.
func bulletProblems(lineNo int, line string, allowed map[string]string) []string {
	loc := trailingLinksRe.FindStringSubmatchIndex(line)
	if loc == nil {
		return []string{fmt.Sprintf("line %d: item must end with the link(s) given in the input, in parentheses", lineNo)}
	}
	var problems []string
	desc := strings.TrimSpace(strings.TrimPrefix(line[:loc[0]], "- "))
	if len(desc) < 8 {
		problems = append(problems, fmt.Sprintf("line %d: item has no description", lineNo))
	}
	// The notes are published as the release body, and PR text is an input,
	// so the description may carry no URL that GitHub would render as a link.
	if strings.Contains(desc, "](") || markdownLinkURLRe.MatchString(desc) || wwwAutolinkRe.MatchString(desc) {
		problems = append(problems, fmt.Sprintf("line %d: links and URLs belong only at the end of the item", lineNo))
	}
	for _, m := range linkRe.FindAllStringSubmatch(line[loc[2]:loc[3]], -1) {
		text, url := m[1], m[2]
		want, ok := allowed[url]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf("line %d: link %s is not one of the input changes", lineNo, url))
		case want != text:
			problems = append(problems, fmt.Sprintf("line %d: link text for %s must be %q", lineNo, url, want))
		}
	}
	return problems
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}
