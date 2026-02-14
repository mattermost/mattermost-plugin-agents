// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRequestFailedError(t *testing.T) {
	testCases := []struct {
		name          string
		statusCode    int
		responseBody  string
		expectedError string
	}{
		{
			name:          "json error field is used",
			statusCode:    403,
			responseBody:  `{"error":"missing permission"}`,
			expectedError: "request failed with status 403: missing permission",
		},
		{
			name:          "json without error field falls back to body",
			statusCode:    400,
			responseBody:  `{"message":"invalid payload"}`,
			expectedError: `request failed with status 400: {"message":"invalid payload"}`,
		},
		{
			name:          "plain text body falls back to raw message",
			statusCode:    502,
			responseBody:  "upstream timeout",
			expectedError: "request failed with status 502: upstream timeout",
		},
		{
			name:          "empty body omits suffix",
			statusCode:    500,
			responseBody:  "",
			expectedError: "request failed with status 500",
		},
		{
			name:          "whitespace body omits suffix",
			statusCode:    500,
			responseBody:  "   \n\t",
			expectedError: "request failed with status 500",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := requestFailedError(tc.statusCode, []byte(tc.responseBody))
			require.EqualError(t, err, tc.expectedError)
		})
	}
}
