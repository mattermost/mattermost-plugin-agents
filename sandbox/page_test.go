// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderPage(t *testing.T) {
	tests := []struct {
		name       string
		hostOrigin string
		contain    []string
		notContain []string
	}{
		{
			name:       "plain origin",
			hostOrigin: "https://mm.example.com",
			contain: []string{
				`const ALLOWED_HOST_ORIGIN = "https://mm.example.com";`,
				"sandbox-proxy-ready",
			},
			notContain: []string{"{{"},
		},
		{
			name:       "origin with port",
			hostOrigin: "http://localhost:8065",
			contain: []string{
				`const ALLOWED_HOST_ORIGIN = "http://localhost:8065";`,
			},
		},
		{
			name:       "JS-hostile input escaped",
			hostOrigin: `https://x";alert(1)//`,
			contain: []string{
				`\"`,
				`const ALLOWED_HOST_ORIGIN = "https://x\";alert(1)//";`,
			},
			notContain: []string{
				`const ALLOWED_HOST_ORIGIN = "https://x";`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := RenderPage(tt.hostOrigin)
			require.NoError(t, err)
			s := string(body)
			for _, c := range tt.contain {
				require.Contains(t, s, c)
			}
			for _, c := range tt.notContain {
				require.NotContains(t, s, c)
			}
		})
	}
}

type recordingLogger struct {
	warns []string
}

func (l *recordingLogger) Info(string, ...any)  {}
func (l *recordingLogger) Error(string, ...any) {}
func (l *recordingLogger) Warn(message string, _ ...any) {
	l.warns = append(l.warns, message)
}

func TestServePage(t *testing.T) {
	restrictiveDefault := BuildCSPHeader(nil, testHostOrigin)

	tests := []struct {
		name           string
		target         string
		wantCSPContain string
		wantCSPExact   string
		wantWarn       bool
	}{
		{
			name:         "no csp param",
			target:       "/sandbox.html",
			wantCSPExact: restrictiveDefault,
		},
		{
			name:           "declared csp",
			target:         "/sandbox.html?csp=%7B%22connectDomains%22%3A%5B%22https%3A%2F%2Fapi.example.com%22%5D%7D",
			wantCSPContain: "connect-src 'self' https://api.example.com",
		},
		{
			name:         "malformed csp",
			target:       "/sandbox.html?csp=%7Bnope",
			wantCSPExact: restrictiveDefault,
			wantWarn:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			logger := &recordingLogger{}
			ServePage(rec, req, testHostOrigin, logger)

			require.Equal(t, http.StatusOK, rec.Code)
			require.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
			require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
			require.Equal(t, "no-referrer", rec.Header().Get("Referrer-Policy"))
			require.Equal(t, "no-cache, no-store, must-revalidate", rec.Header().Get("Cache-Control"))

			csp := rec.Header().Get("Content-Security-Policy")
			if tt.wantCSPExact != "" {
				require.Equal(t, tt.wantCSPExact, csp)
			}
			if tt.wantCSPContain != "" {
				require.Contains(t, csp, tt.wantCSPContain)
			}
			if tt.wantWarn {
				require.NotEmpty(t, logger.warns)
				require.True(t, strings.Contains(logger.warns[0], "malformed csp"))
			} else {
				require.Empty(t, logger.warns)
			}
		})
	}
}
