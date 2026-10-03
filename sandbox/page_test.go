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
		mode       PageMode
		contain    []string
		notContain []string
	}{
		{
			name:       "external mode keeps strict self-test",
			hostOrigin: "https://mm.example.com",
			mode:       PageModeExternal,
			contain: []string{
				`const ALLOWED_HOST_ORIGIN = "https://mm.example.com";`,
				`const REQUIRE_ORIGIN_ISOLATION = true;`,
				"sandbox-proxy-ready",
				"Sandbox is NOT isolated",
			},
			notContain: []string{"{{"},
		},
		{
			name:       "same-origin mode skips self-test",
			hostOrigin: "http://localhost:8065",
			mode:       PageModeSameOrigin,
			contain: []string{
				`const ALLOWED_HOST_ORIGIN = "http://localhost:8065";`,
				`const REQUIRE_ORIGIN_ISOLATION = false;`,
				"same-origin fallback mode: origin isolation self-test skipped",
				"sandbox-proxy-ready",
			},
		},
		{
			name:       "JS-hostile input escaped",
			hostOrigin: `https://x";alert(1)//`,
			mode:       PageModeExternal,
			contain: []string{
				`\"`,
				`const ALLOWED_HOST_ORIGIN = "https://x\";alert(1)//";`,
			},
			notContain: []string{
				`const ALLOWED_HOST_ORIGIN = "https://x";`,
			},
		},
		{
			name:       "permission allow uses semicolon join",
			hostOrigin: "https://mm.example.com",
			mode:       PageModeExternal,
			contain: []string{
				`join('; ')`,
				"OWN_ORIGIN",
				"rejected guest message from",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := RenderPage(tt.hostOrigin, tt.mode)
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

func TestRenderPagePermissionAllowContract(t *testing.T) {
	body, err := RenderPage(testHostOrigin, PageModeExternal)
	require.NoError(t, err)
	s := string(body)
	// Contract: multi-permission join must be "; " not space.
	require.Contains(t, s, `join('; ')`)
	require.NotContains(t, s, `join(' ')`)
	require.Contains(t, s, "OWN_ORIGIN")
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
	tests := []struct {
		name            string
		target          string
		mode            PageMode
		wantCSPExact    string
		wantCSPContain  string
		wantWarn        bool
		wantBodyContain string
		wantBodyAbsent  string
	}{
		{
			name:            "no csp param external",
			target:          "/sandbox.html",
			mode:            PageModeExternal,
			wantCSPExact:    specOmittedCSPDefault,
			wantBodyContain: "REQUIRE_ORIGIN_ISOLATION = true",
		},
		{
			name:            "same-origin mode body",
			target:          "/sandbox.html",
			mode:            PageModeSameOrigin,
			wantCSPExact:    specOmittedCSPDefault,
			wantBodyContain: "REQUIRE_ORIGIN_ISOLATION = false",
		},
		{
			name:           "declared csp",
			target:         "/sandbox.html?csp=%7B%22connectDomains%22%3A%5B%22https%3A%2F%2Fapi.example.com%22%5D%7D",
			mode:           PageModeExternal,
			wantCSPContain: "connect-src 'self' https://api.example.com",
		},
		{
			name:         "malformed csp",
			target:       "/sandbox.html?csp=%7Bnope",
			mode:         PageModeExternal,
			wantCSPExact: specOmittedCSPDefault,
			wantWarn:     true,
		},
		{
			name:         "invalid source fails closed",
			target:       "/sandbox.html?csp=" + "%7B%22connectDomains%22%3A%5B%22data%3A%22%5D%7D",
			mode:         PageModeExternal,
			wantCSPExact: specOmittedCSPDefault,
			wantWarn:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tt.target, nil)
			logger := &recordingLogger{}
			ServePage(rec, req, testHostOrigin, tt.mode, logger)

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
			body := rec.Body.String()
			if tt.wantBodyContain != "" {
				require.Contains(t, body, tt.wantBodyContain)
			}
			if tt.wantBodyAbsent != "" {
				require.NotContains(t, body, tt.wantBodyAbsent)
			}
			if tt.wantWarn {
				require.NotEmpty(t, logger.warns)
			} else {
				require.Empty(t, logger.warns)
			}
			_ = strings.Contains
		})
	}
}
