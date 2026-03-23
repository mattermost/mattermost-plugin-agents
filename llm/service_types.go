// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"fmt"
	"slices"

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
	ServiceTypeScale            = "scale"
)

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
