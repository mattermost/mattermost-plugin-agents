// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestServePageNetworkCSPSmuggling(t *testing.T) {
	server, err := NewServer(":0", testHostOrigin, noopLogger{})
	require.NoError(t, err)
	defer func() { _ = server.Shutdown() }()

	go func() { _ = server.Run() }()

	base := "http://" + server.Addr()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, getErr := http.Get(base + "/sandbox.html")
		if getErr == nil {
			resp.Body.Close()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	client := &http.Client{Timeout: 2 * time.Second}

	tests := []struct {
		name       string
		cspJSON    string
		wantStatus int
		wantCSP    string // exact or ""
		wantSubstr string
	}{
		{
			name:       "nul in connectDomains fails closed",
			cspJSON:    "{\"connectDomains\":[\"https://evil.com\\u0000.example.com\"]}",
			wantStatus: http.StatusOK,
			wantCSP:    specOmittedCSPDefault,
		},
		{
			name:       "vertical tab fails closed",
			cspJSON:    "{\"connectDomains\":[\"https://evil.com\\u000b.example.com\"]}",
			wantStatus: http.StatusOK,
			wantCSP:    specOmittedCSPDefault,
		},
		{
			name:       "crlf fails closed",
			cspJSON:    "{\"connectDomains\":[\"https://evil.com\\r\\n script-src *\"]}",
			wantStatus: http.StatusOK,
			wantCSP:    specOmittedCSPDefault,
		},
		{
			name:       "unicode nbsp fails closed",
			cspJSON:    "{\"connectDomains\":[\"https://evil.com\\u00a0.example.com\"]}",
			wantStatus: http.StatusOK,
			wantCSP:    specOmittedCSPDefault,
		},
		{
			name:       "star rejected",
			cspJSON:    `{"connectDomains":["*"]}`,
			wantStatus: http.StatusOK,
			wantCSP:    specOmittedCSPDefault,
		},
		{
			name:       "data scheme rejected",
			cspJSON:    `{"resourceDomains":["data:"]}`,
			wantStatus: http.StatusOK,
			wantCSP:    specOmittedCSPDefault,
		},
		{
			name:       "valid wildcard accepted",
			cspJSON:    `{"resourceDomains":["https://*.cloudflare.com"]}`,
			wantStatus: http.StatusOK,
			wantSubstr: "https://*.cloudflare.com",
		},
		{
			name:       "valid connect accepted",
			cspJSON:    `{"connectDomains":["https://api.example.com"]}`,
			wantStatus: http.StatusOK,
			wantSubstr: "connect-src 'self' https://api.example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := base + "/sandbox.html?csp=" + url.QueryEscape(tt.cspJSON)
			resp, err := client.Get(u)
			require.NoError(t, err, "response must be a well-formed HTTP message")
			defer resp.Body.Close()
			require.Equal(t, tt.wantStatus, resp.StatusCode)
			_, _ = io.Copy(io.Discard, resp.Body)

			csp := resp.Header.Get("Content-Security-Policy")
			require.NotEmpty(t, csp)
			if tt.wantCSP != "" {
				require.Equal(t, tt.wantCSP, csp)
			}
			if tt.wantSubstr != "" {
				require.Contains(t, csp, tt.wantSubstr)
			}
		})
	}
}
