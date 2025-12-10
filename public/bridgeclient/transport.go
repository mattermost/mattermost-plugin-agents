// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package bridgeclient

import (
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/pkg/errors"
)

// pluginAPIRoundTripper wraps the Mattermost plugin API for HTTP requests
type pluginAPIRoundTripper struct {
	api PluginAPI
}

func (p *pluginAPIRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	resp := p.api.PluginHTTP(req)
	if resp == nil {
		return nil, errors.Errorf("failed to make interplugin request")
	}
	return resp, nil
}

// appAPIRoundTripper wraps the Mattermost app layer API for HTTP requests
type appAPIRoundTripper struct {
	api    AppAPI
	userID string
}

func removeFirstPath(r *http.Request) {
	path := r.URL.Path

	// Find the position of the second slash (first slash after the leading one)
	secondSlash := strings.Index(path[1:], "/")

	if secondSlash == -1 {
		// No second slash found, set to just "/"
		r.URL.Path = "/"
		return
	}

	// Update the path to everything from the second slash onwards
	r.URL.Path = path[1+secondSlash:]
}

func (a *appAPIRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Create a response recorder to capture the response
	recorder := httptest.NewRecorder()

	removeFirstPath(req)

	// Make the inter-plugin request from the server to the AI plugin
	a.api.ServeInternalPluginRequest(a.userID, recorder, req, mattermostServerID, aiPluginID)

	// Convert the recorder to an http.Response
	return recorder.Result(), nil
}
