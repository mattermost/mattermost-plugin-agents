// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package audit

import (
	"bytes"
	"encoding/json"
	"sort"
)

// ChangedJSONKeys returns the sorted top-level JSON keys whose values differ
// between prev and next. It exists for audit enrichment of objects that carry
// secrets or user content: the record may say which fields changed but never
// what they changed to. Keys present on only one side are reported as changed;
// a nil prev (or next) marshals to null and behaves as an empty object, so
// every key of the other side is reported.
func ChangedJSONKeys(prev, next any) []string {
	toMap := func(v any) map[string]json.RawMessage {
		raw, err := json.Marshal(v)
		if err != nil {
			return nil
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil
		}
		return m
	}

	prevMap := toMap(prev)
	nextMap := toMap(next)

	changed := make([]string, 0, len(nextMap))
	for key, nextVal := range nextMap {
		if prevVal, ok := prevMap[key]; !ok || !bytes.Equal(prevVal, nextVal) {
			changed = append(changed, key)
		}
	}
	for key := range prevMap {
		if _, ok := nextMap[key]; !ok {
			changed = append(changed, key)
		}
	}
	sort.Strings(changed)
	return changed
}
