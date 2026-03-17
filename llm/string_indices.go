// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import "unicode/utf16"

// UTF16CodeUnitCount returns the length of a string in JavaScript-compatible
// UTF-16 code units. The frontend applies annotation indices with JS string
// slicing, so locally computed offsets must use this unit.
func UTF16CodeUnitCount(s string) int {
	count := 0
	for _, r := range s {
		n := utf16.RuneLen(r)
		if n < 0 {
			count++
			continue
		}
		count += n
	}
	return count
}
