// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mattermost/mattermost-plugin-ai/anthropic"
	"github.com/mattermost/mattermost-plugin-ai/config"
	"github.com/mattermost/mattermost-plugin-ai/llm"
	"github.com/mattermost/mattermost-plugin-ai/openai"
)

func main() {
	demoMultipleInstances()
}

// Demonstrates how to create multiple model instances with different custom headers
func demoMultipleInstances() {
	fmt.Println("=== Multiple Model Instances with Custom Headers Example ===")

	// HTTP client (in real usage, this would be passed from the plugin)
	httpClient := &http.Client{}

	// 1. Production OpenAI instance with production headers
	prodService := llm.ServiceConfig{
		Name:         "openai-production",
		Type:         "openai",
		APIKey:       "sk-prod-xxx",
		DefaultModel: "gpt-4",
		CustomHeaders: map[string]string{
			"X-Environment":    "production",
			"X-Cost-Center":    "marketing",
			"X-Request-Source": "mattermost-prod",
			"X-Priority":       "high",
		},
	}
	prodOpenAIConfig := config.OpenAIConfigFromServiceConfig(prodService)
	prodOpenAI := openai.New(prodOpenAIConfig, httpClient)

	// 2. Development OpenAI instance with development headers
	devService := llm.ServiceConfig{
		Name:         "openai-development",
		Type:         "openai",
		APIKey:       "sk-dev-xxx",
		DefaultModel: "gpt-3.5-turbo",
		CustomHeaders: map[string]string{
			"X-Environment":    "development",
			"X-Cost-Center":    "engineering",
			"X-Request-Source": "mattermost-dev",
			"X-Debug":          "true",
			"X-Priority":       "low",
		},
	}
	devOpenAIConfig := config.OpenAIConfigFromServiceConfig(devService)
	devOpenAI := openai.New(devOpenAIConfig, httpClient)

	// 3. Proxy-routed OpenAI instance with proxy headers
	proxyService := llm.ServiceConfig{
		Name:         "openai-proxy",
		Type:         "openai_compatible",
		APIKey:       "sk-proxy-xxx",
		APIURL:       "https://ai-proxy.company.com/v1",
		DefaultModel: "gpt-4",
		CustomHeaders: map[string]string{
			"X-Proxy-Auth":     "Bearer company-proxy-token",
			"X-Department":     "ai-ops",
			"X-Request-Source": "mattermost-proxy",
			"X-Billing-Code":   "AIOPS-2024",
		},
	}
	proxyOpenAIConfig := config.OpenAIConfigFromServiceConfig(proxyService)
	proxyOpenAI := openai.NewCompatible(proxyOpenAIConfig, httpClient)

	// 4. Anthropic instance with different headers
	anthropicService := llm.ServiceConfig{
		Name:         "anthropic-production",
		Type:         "anthropic",
		APIKey:       "sk-ant-xxx",
		DefaultModel: "claude-3-5-sonnet-20241022",
		CustomHeaders: map[string]string{
			"X-Environment":    "production",
			"X-Provider":       "anthropic",
			"X-Request-Source": "mattermost-claude",
			"X-Cost-Center":    "research",
		},
	}
	anthropicClient := anthropic.New(anthropicService, httpClient)

	// Display the configurations
	fmt.Println("1. Production OpenAI Instance:")
	displayServiceConfig(prodService)
	fmt.Printf("   Client created: %T\n\n", prodOpenAI)

	fmt.Println("2. Development OpenAI Instance:")
	displayServiceConfig(devService)
	fmt.Printf("   Client created: %T\n\n", devOpenAI)

	fmt.Println("3. Proxy-routed OpenAI Instance:")
	displayServiceConfig(proxyService)
	fmt.Printf("   Client created: %T\n\n", proxyOpenAI)

	fmt.Println("4. Anthropic Instance:")
	displayServiceConfig(anthropicService)
	fmt.Printf("   Client created: %T\n\n", anthropicClient)

	fmt.Println("=== Key Benefits ===")
	fmt.Println("✓ Each model instance has independent custom headers")
	fmt.Println("✓ Same provider type (OpenAI) can have different headers per instance")
	fmt.Println("✓ Headers are automatically injected into all API requests")
	fmt.Println("✓ Supports multiple providers (OpenAI, Anthropic, etc.)")
	fmt.Println("✓ Perfect for environment separation, cost tracking, and proxy routing")
}

func displayServiceConfig(service llm.ServiceConfig) {
	fmt.Printf("   Name: %s\n", service.Name)
	fmt.Printf("   Type: %s\n", service.Type)
	fmt.Printf("   Model: %s\n", service.DefaultModel)
	if service.APIURL != "" {
		fmt.Printf("   API URL: %s\n", service.APIURL)
	}
	fmt.Printf("   Custom Headers:\n")
	headersJSON, _ := json.MarshalIndent(service.CustomHeaders, "     ", "  ")
	fmt.Printf("     %s\n", string(headersJSON))
}
