// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package sandbox

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"net/http"
	"text/template"
)

//go:embed sandbox.html
var sandboxHTMLTemplate string

// pageTemplate is parsed once. Substitutions are server-controlled (never
// from the request): the JSON-encoded allowed embedder origin and whether
// the origin-isolation self-test is required.
var pageTemplate = template.Must(template.New("sandbox").Parse(sandboxHTMLTemplate))

// PageMode selects sandbox page behavior baked in at serve time.
type PageMode int

const (
	// PageModeExternal keeps the strict window.top isolation self-test
	// (standalone listener on a different origin).
	PageModeExternal PageMode = iota
	// PageModeSameOrigin replaces the isolation self-test with a
	// console.warn — required because the AppFrame iframe is same-origin
	// with allow-same-origin, so window.top is reachable by design.
	PageModeSameOrigin
)

type pageData struct {
	HostOriginJS           string
	RequireOriginIsolation bool
}

// RenderPage returns sandbox.html with the allowed embedder origin and
// isolation mode baked in.
func RenderPage(hostOrigin string, mode PageMode) ([]byte, error) {
	encoded, err := json.Marshal(hostOrigin)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := pageTemplate.Execute(&buf, pageData{
		HostOriginJS:           string(encoded),
		RequireOriginIsolation: mode == PageModeExternal,
	}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ServePage writes the sandbox page for hostOrigin with security headers
// derived from the request's ?csp= parameter. Method and path checks are
// the caller's responsibility. logger may be nil. mode is server-selected
// (never request-selectable).
func ServePage(w http.ResponseWriter, r *http.Request, hostOrigin string, mode PageMode, logger Logger) {
	raw := r.URL.Query().Get("csp")
	csp, ok := parseCSPParam(raw)
	if !ok {
		if logger != nil {
			logger.Warn("MCP Apps sandbox: malformed csp query parameter, applying restrictive default", "remote_addr", r.RemoteAddr)
		}
		csp = nil
	} else if csp != nil {
		canonical, err := canonicalizeCSP(csp)
		if err != nil {
			if logger != nil {
				logger.Warn("MCP Apps sandbox: invalid csp source list, applying restrictive default", "remote_addr", r.RemoteAddr, "error", err)
			}
			csp = nil
		} else {
			csp = canonical
		}
	}

	w.Header().Set("Content-Security-Policy", BuildCSPHeader(csp, hostOrigin))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	body, err := RenderPage(hostOrigin, mode)
	if err != nil {
		if logger != nil {
			logger.Error("MCP Apps sandbox: failed to render page", "error", err)
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(body)
}
