// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"fmt"
	"slices"
	"strings"
	"unicode"

	"github.com/maximhq/bifrost/core/schemas"
)

const (
	ServiceTypeOpenAI           = "openai"
	ServiceTypeOpenAICompatible = "openaicompatible"
	ServiceTypeAzure            = "azure"
	ServiceTypeAnthropic        = "anthropic"
	ServiceTypeCohere           = "cohere"
	ServiceTypeBedrock          = "bedrock"
	ServiceTypeMistral          = "mistral"
)

// ServiceTypeInfo describes a supported service type for the admin UI.
type ServiceTypeInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

func standardServiceTypes() []string {
	types := make([]string, 0, len(schemas.StandardProviders))
	for _, provider := range schemas.StandardProviders {
		types = append(types, string(provider))
	}

	return types
}

// SupportedServiceTypes returns the set of configured service types accepted by the plugin.
func SupportedServiceTypes() []string {
	types := append([]string{ServiceTypeOpenAICompatible}, standardServiceTypes()...)
	slices.Sort(types)
	return types
}

// SupportedServiceTypeInfos returns supported service types with display names for the admin UI.
func SupportedServiceTypeInfos() []ServiceTypeInfo {
	types := SupportedServiceTypes()
	infos := make([]ServiceTypeInfo, 0, len(types))
	for _, serviceType := range types {
		infos = append(infos, ServiceTypeInfo{
			ID:          serviceType,
			DisplayName: serviceTypeDisplayName(serviceType),
		})
	}
	return infos
}

// IsSupportedServiceType returns true if the service type is supported by Bifrost or is a plugin alias.
func IsSupportedServiceType(serviceType string) bool {
	if serviceType == ServiceTypeOpenAICompatible {
		return true
	}

	return slices.Contains(standardServiceTypes(), serviceType)
}

// ModelProviderForServiceType resolves a service type into a Bifrost model provider.
func ModelProviderForServiceType(serviceType string) (schemas.ModelProvider, error) {
	if serviceType == ServiceTypeOpenAICompatible {
		return schemas.OpenAI, nil
	}

	if !IsSupportedServiceType(serviceType) {
		return "", fmt.Errorf("unsupported service type: %s", serviceType)
	}

	return schemas.ModelProvider(serviceType), nil
}

func serviceTypeDisplayName(serviceType string) string {
	switch serviceType {
	case ServiceTypeOpenAI:
		return "OpenAI"
	case ServiceTypeOpenAICompatible:
		return "OpenAI Compatible"
	case ServiceTypeAzure:
		return "Azure"
	case ServiceTypeAnthropic:
		return "Anthropic"
	case ServiceTypeBedrock:
		return "AWS Bedrock"
	case ServiceTypeCohere:
		return "Cohere"
	case ServiceTypeMistral:
		return "Mistral"
	case string(schemas.Cerebras):
		return "Cerebras"
	case string(schemas.Elevenlabs):
		return "ElevenLabs"
	case string(schemas.Gemini):
		return "Gemini"
	case string(schemas.Groq):
		return "Groq"
	case string(schemas.HuggingFace):
		return "Hugging Face"
	case string(schemas.Nebius):
		return "Nebius"
	case string(schemas.Ollama):
		return "Ollama"
	case string(schemas.OpenRouter):
		return "OpenRouter"
	case string(schemas.Parasail):
		return "Parasail"
	case string(schemas.Perplexity):
		return "Perplexity"
	case string(schemas.Replicate):
		return "Replicate"
	case string(schemas.Runway):
		return "Runway"
	case string(schemas.SGL):
		return "SGL"
	case string(schemas.VLLM):
		return "vLLM"
	case string(schemas.Vertex):
		return "Vertex"
	case string(schemas.XAI):
		return "xAI"
	default:
		return humanizeServiceType(serviceType)
	}
}

func humanizeServiceType(serviceType string) string {
	if serviceType == "" {
		return ""
	}

	parts := strings.FieldsFunc(serviceType, func(r rune) bool {
		return r == '-' || r == '_'
	})
	for i, part := range parts {
		runes := []rune(part)
		if len(runes) == 0 {
			continue
		}
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}

	return strings.Join(parts, " ")
}
