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

// pageTemplate is parsed once; the only substitution is the JSON-encoded
// allowed embedder origin (P1 — tamper-proof: the value comes from server
// config, never from the request).
var pageTemplate = template.Must(template.New("sandbox").Parse(sandboxHTMLTemplate))

type pageData struct {
	HostOriginJS string
}

// RenderPage returns sandbox.html with the allowed embedder origin baked in.
func RenderPage(hostOrigin string) ([]byte, error) {
	encoded, err := json.Marshal(hostOrigin)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := pageTemplate.Execute(&buf, pageData{HostOriginJS: string(encoded)}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ServePage writes the sandbox page for hostOrigin with security headers
// derived from the request's ?csp= parameter. Method and path checks are
// the caller's responsibility. logger may be nil.
func ServePage(w http.ResponseWriter, r *http.Request, hostOrigin string, logger Logger) {
	raw := r.URL.Query().Get("csp")
	csp, ok := parseCSPParam(raw)
	if !ok {
		if logger != nil {
			logger.Warn("MCP Apps sandbox: malformed csp query parameter, applying restrictive default", "remote_addr", r.RemoteAddr)
		}
		csp = nil
	}

	w.Header().Set("Content-Security-Policy", BuildCSPHeader(csp, hostOrigin))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")

	body, err := RenderPage(hostOrigin)
	if err != nil {
		if logger != nil {
			logger.Error("MCP Apps sandbox: failed to render page", "error", err)
		}
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(body)
}
