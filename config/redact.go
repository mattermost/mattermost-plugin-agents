// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"encoding/json"
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
		server.Headers = blankValues(server.Headers)
		server.ServiceAccountHeaders = blankValues(server.ServiceAccountHeaders)
		server.ClientSecret = ""
	}

	out.WebSearch.Google.APIKey = ""
	out.WebSearch.Brave.APIKey = ""

	out.EmbeddingSearchConfig.EmbeddingProvider.Parameters = redactParameters(out.EmbeddingSearchConfig.EmbeddingProvider.Parameters)
	out.EmbeddingSearchConfig.VectorStore.Parameters = redactParameters(out.EmbeddingSearchConfig.VectorStore.Parameters)
	out.EmbeddingSearchConfig.Parameters = redactParameters(out.EmbeddingSearchConfig.Parameters)

	out.OpenTelemetryHeaders = ""

	return out
}

func redactService(svc *llm.ServiceConfig) {
	svc.APIKey = ""
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
		if nested, ok := value.(map[string]any); ok {
			redactParameterMap(nested)
		}
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
