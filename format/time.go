// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package format

import "time"

// UnixMillisUTC formats a Mattermost millisecond timestamp as UTC RFC3339.
func UnixMillisUTC(ms int64) string {
	return time.Unix(ms/1000, (ms%1000)*int64(time.Millisecond)).UTC().Format(time.RFC3339)
}
