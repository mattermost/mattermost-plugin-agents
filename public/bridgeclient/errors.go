// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"encoding/json"
	"fmt"
	"strings"
)

func requestFailedError(statusCode int, responseBody []byte) error {
	var errResp ErrorResponse
	if err := json.Unmarshal(responseBody, &errResp); err == nil {
		errMessage := strings.TrimSpace(errResp.Error)
		if errMessage != "" {
			return fmt.Errorf("request failed with status %d: %s", statusCode, errMessage)
		}
	}

	bodyText := strings.TrimSpace(string(responseBody))
	if bodyText == "" {
		return fmt.Errorf("request failed with status %d", statusCode)
	}

	return fmt.Errorf("request failed with status %d: %s", statusCode, bodyText)
}
