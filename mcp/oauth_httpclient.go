// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"io"
	"net/http"
)

// maxOAuthResponseBytes caps how much of an OAuth HTTP response body we will
// read, matching the 1 MiB limit applied to metadata and token responses. It
// bounds the decompressed size (the transport delivers an already-decompressed
// body when it negotiated the content encoding), so a hostile endpoint cannot
// exhaust memory with a gzip bomb or an endless stream.
const maxOAuthResponseBytes = 1 << 20

// bodyLimitTransport wraps an http.RoundTripper and caps the size of every
// response body it returns. It exists because go-sdk's oauthex.RegisterClient
// (dynamic client registration) reads the response with an unbounded
// io.ReadAll; wrapping the client used for that call restores the same bound
// the plugin applies everywhere else.
type bodyLimitTransport struct {
	base  http.RoundTripper
	limit int64
}

func (t *bodyLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.base
	if base == nil {
		base = http.DefaultTransport
	}
	resp, err := base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &limitedReadCloser{
		reader: io.LimitReader(resp.Body, t.limit),
		closer: resp.Body,
	}
	return resp, nil
}

type limitedReadCloser struct {
	reader io.Reader
	closer io.Closer
}

func (l *limitedReadCloser) Read(p []byte) (int, error) { return l.reader.Read(p) }
func (l *limitedReadCloser) Close() error               { return l.closer.Close() }

// bodyLimitedHTTPClient returns a shallow copy of the manager's HTTP client
// whose responses are size-capped, for use with SDK calls (like dynamic
// client registration) that would otherwise read the body unbounded.
func (m *OAuthManager) bodyLimitedHTTPClient() *http.Client {
	base := http.DefaultClient
	if m.httpClient != nil {
		base = m.httpClient
	}
	clientCopy := *base
	clientCopy.Transport = &bodyLimitTransport{base: base.Transport, limit: maxOAuthResponseBytes}
	return &clientCopy
}

// noRedirectClient returns a shallow copy of base whose CheckRedirect stops
// before following any redirect, surfacing the 3xx response to the caller
// instead. Direct token/revocation endpoint requests carry secrets (the
// authorization code, refresh token, and client secret); those endpoints have
// no legitimate reason to redirect, and following one would replay the secrets
// to the redirect target. Callers treat the returned non-2xx as an error.
func noRedirectClient(base *http.Client) *http.Client {
	clientCopy := http.Client{}
	if base != nil {
		clientCopy = *base
	}
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &clientCopy
}
