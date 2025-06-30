// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/openai"
)

// Example of how to use custom headers with the LLM providers
func exampleCustomHeaders() {
	// Example service configuration with custom headers
	serviceConfig := llm.ServiceConfig{
		Name:         "example-openai",
		Type:         "openai_compatible",
		APIKey:       "sk-fake-key-for-example",
		APIURL:       "https://api.example.com/v1",
		DefaultModel: "gpt-3.5-turbo",
		CustomHeaders: map[string]string{
			"X-Organization":   "my-company",
			"X-Request-Source": "mattermost-ai",
			"X-Custom-Auth":    "Bearer additional-token",
		},
	}

	// Convert to OpenAI-specific config
	openaiConfig := config.OpenAIConfigFromServiceConfig(serviceConfig)

	// Create HTTP client
	httpClient := &http.Client{}

	// Create OpenAI client - custom headers will be automatically injected
	openaiClient := openai.NewCompatible(openaiConfig, httpClient)

	// Print configuration for verification
	configJSON, _ := json.MarshalIndent(serviceConfig, "", "  ")
	fmt.Println("Service Config with Custom Headers:")
	fmt.Println(string(configJSON))

	fmt.Printf("\nOpenAI client created successfully with custom headers: %+v\n", openaiClient)
	fmt.Println("All API requests will now include the custom headers automatically!")
}
