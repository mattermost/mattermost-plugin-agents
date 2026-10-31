// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// secretParameterMarkers match keys of free-form upstream parameters
// (embedding provider and vector store settings) that hold credentials.
var secretParameterMarkers = []string{"key", "secret", "token", "password", "credential"}

// WithoutSecrets returns a deep copy of c with every credential blanked:
// service API keys and cloud credentials, MCP server headers and OAuth client
// secrets, web search API keys, embedding provider keys, and OTLP export
// headers. Header names are kept so viewers can see which headers are set.
// Every URL loses its user info and query values, where admins sometimes put
// credentials.
func (c *Config) WithoutSecrets() Config {
	out := *c.Clone()

	for i := range out.Services {
		redactService(&out.Services[i])
	}
	for i := range out.Bots {
		if out.Bots[i].Service != nil {
			redactService(out.Bots[i].Service)
		}
	}

	for i := range out.MCP.Servers {
		server := &out.MCP.Servers[i]
		server.BaseURL = RedactURL(server.BaseURL)
		server.Headers = blankValues(server.Headers)
		server.ServiceAccountHeaders = blankValues(server.ServiceAccountHeaders)
		server.ClientSecret = ""
	}

	out.WebSearch.Google.APIKey = ""
	out.WebSearch.Google.APIURL = RedactURL(out.WebSearch.Google.APIURL)
	out.WebSearch.Brave.APIKey = ""
	out.WebSearch.Brave.APIURL = RedactURL(out.WebSearch.Brave.APIURL)
	out.WebSearch.SearXNG.BaseURL = RedactURL(out.WebSearch.SearXNG.BaseURL)

	out.EmbeddingSearchConfig.EmbeddingProvider.Parameters = redactParameters(out.EmbeddingSearchConfig.EmbeddingProvider.Parameters)
	out.EmbeddingSearchConfig.VectorStore.Parameters = redactParameters(out.EmbeddingSearchConfig.VectorStore.Parameters)
	out.EmbeddingSearchConfig.Parameters = redactParameters(out.EmbeddingSearchConfig.Parameters)

	out.OpenTelemetryHeaders = ""
	out.OpenTelemetryEndpoint = RedactURL(out.OpenTelemetryEndpoint)

	return out
}

func redactService(svc *llm.ServiceConfig) {
	svc.APIKey = ""
	svc.APIURL = RedactURL(svc.APIURL)
	svc.AWSAccessKeyID = ""
	svc.AWSSecretAccessKey = ""
	svc.VertexAuthCredentials = ""
}

func blankValues(headers map[string]string) map[string]string {
	if headers == nil {
		return nil
	}
	out := make(map[string]string, len(headers))
	for name := range headers {
		out[name] = ""
	}
	return out
}

// redactParameters blanks credential-looking keys in a parameters object,
// nested objects included. Parameters that are not a JSON object are dropped
// entirely: what they hold cannot be checked.
func redactParameters(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return raw
	}
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil
	}
	redactParameterMap(params)
	redacted, err := json.Marshal(params)
	if err != nil {
		return nil
	}
	return redacted
}

func redactParameterMap(params map[string]any) {
	for name, value := range params {
		if isSecretParameter(name) {
			params[name] = ""
			continue
		}
		params[name] = redactParameterValue(name, value)
	}
}

// redactParameterValue redacts URLs among string values and walks nested
// objects and arrays.
func redactParameterValue(name string, value any) any {
	switch v := value.(type) {
	case string:
		if strings.Contains(strings.ToLower(name), "url") {
			return RedactURL(v)
		}
		return v
	case map[string]any:
		redactParameterMap(v)
		return v
	case []any:
		for i := range v {
			v[i] = redactParameterValue(name, v[i])
		}
		return v
	default:
		return v
	}
}

func isSecretParameter(name string) bool {
	lower := strings.ToLower(name)
	for _, marker := range secretParameterMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// RedactURL removes the user info and blanks the query values of a URL, the
// places a credential can hide in one. A value that has user info or a query
// but cannot be parsed is dropped.
func RedactURL(raw string) string {
	if !strings.ContainsAny(raw, "@?#") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	// Without a scheme, "user:pass@host" parses as an opaque URL with the
	// credentials in Opaque instead of User.
	if strings.Contains(u.Opaque, "@") {
		return ""
	}
	u.User = nil
	u.Fragment = ""
	u.RawFragment = ""
	if u.RawQuery != "" {
		query := u.Query()
		for key := range query {
			query[key] = []string{""}
		}
		u.RawQuery = query.Encode()
	}
	return u.String()
}
