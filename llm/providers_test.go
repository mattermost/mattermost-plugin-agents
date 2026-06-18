// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"net/http"
	"testing"
)

func TestGetOpenAICompatibleProvider(t *testing.T) {
	tests := []struct {
		name        string
		serviceType string
		wantFound   bool
	}{
		{
			name:        "scale returns provider",
			serviceType: ServiceTypeScale,
			wantFound:   true,
		},
		{
			name:        "cohere returns provider",
			serviceType: ServiceTypeCohere,
			wantFound:   true,
		},
		{
			name:        "mistral returns provider",
			serviceType: ServiceTypeMistral,
			wantFound:   true,
		},
		{
			name:        "unregistered type returns false",
			serviceType: "nonexistent",
			wantFound:   false,
		},
		{
			name:        "openai is not in compatible registry",
			serviceType: ServiceTypeOpenAI,
			wantFound:   false,
		},
		{
			name:        "anthropic is not in compatible registry",
			serviceType: ServiceTypeAnthropic,
			wantFound:   false,
		},
		{
			name:        "empty string returns false",
			serviceType: "",
			wantFound:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, ok := GetOpenAICompatibleProvider(tc.serviceType)
			if ok != tc.wantFound {
				t.Fatalf("GetOpenAICompatibleProvider(%q) found=%v, want %v", tc.serviceType, ok, tc.wantFound)
			}
		})
	}
}

func TestScaleProviderConfig(t *testing.T) {
	p, ok := GetOpenAICompatibleProvider(ServiceTypeScale)
	if !ok {
		t.Fatal("Scale provider not found in registry")
	}

	if p.DefaultModel != "openai/gpt-4o" {
		t.Errorf("DefaultModel = %q, want %q", p.DefaultModel, "openai/gpt-4o")
	}

	if !p.DisableStreamOptions {
		t.Error("expected DisableStreamOptions to be true for Scale")
	}

	if p.UseMaxTokens {
		t.Error("expected UseMaxTokens to be false for Scale")
	}

	if p.CreateTransport == nil {
		t.Fatal("expected CreateTransport to be non-nil for Scale")
	}
}

func TestScaleTransportFactory(t *testing.T) {
	tests := []struct {
		name              string
		cfg               ServiceConfig
		wantHeaders       map[string]string
		wantRemoveHeaders []string
	}{
		{
			name: "api key only",
			cfg: ServiceConfig{
				APIKey: "test-key",
			},
			wantHeaders: map[string]string{
				"x-api-key": "test-key",
			},
			wantRemoveHeaders: []string{"Authorization"},
		},
		{
			name: "api key with org id",
			cfg: ServiceConfig{
				APIKey: "test-key",
				OrgID:  "org-123",
			},
			wantHeaders: map[string]string{
				"x-api-key":             "test-key",
				"x-selected-account-id": "org-123",
			},
			wantRemoveHeaders: []string{"Authorization"},
		},
		{
			name: "empty org id omits account header",
			cfg: ServiceConfig{
				APIKey: "test-key",
				OrgID:  "",
			},
			wantHeaders: map[string]string{
				"x-api-key": "test-key",
			},
			wantRemoveHeaders: []string{"Authorization"},
		},
	}

	p, ok := GetOpenAICompatibleProvider(ServiceTypeScale)
	if !ok {
		t.Fatal("Scale provider not found in registry")
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			transport := p.CreateTransport(tc.cfg, http.DefaultTransport)

			cat, ok := transport.(*CustomAuthTransport)
			if !ok {
				t.Fatalf("expected *CustomAuthTransport, got %T", transport)
			}

			if len(cat.SetHeaders) != len(tc.wantHeaders) {
				t.Errorf("SetHeaders count = %d, want %d", len(cat.SetHeaders), len(tc.wantHeaders))
			}
			for k, v := range tc.wantHeaders {
				if cat.SetHeaders[k] != v {
					t.Errorf("SetHeaders[%q] = %q, want %q", k, cat.SetHeaders[k], v)
				}
			}

			if len(cat.RemoveHeaders) != len(tc.wantRemoveHeaders) {
				t.Errorf("RemoveHeaders count = %d, want %d", len(cat.RemoveHeaders), len(tc.wantRemoveHeaders))
			}
			for i, h := range tc.wantRemoveHeaders {
				if i < len(cat.RemoveHeaders) && cat.RemoveHeaders[i] != h {
					t.Errorf("RemoveHeaders[%d] = %q, want %q", i, cat.RemoveHeaders[i], h)
				}
			}

			if cat.Base != http.DefaultTransport {
				t.Error("expected Base to be http.DefaultTransport")
			}
		})
	}
}

func TestScaleTransportFactoryNilBase(t *testing.T) {
	p, ok := GetOpenAICompatibleProvider(ServiceTypeScale)
	if !ok {
		t.Fatal("Scale provider not found in registry")
	}

	transport := p.CreateTransport(ServiceConfig{APIKey: "k"}, nil)
	cat, ok := transport.(*CustomAuthTransport)
	if !ok {
		t.Fatalf("expected *CustomAuthTransport, got %T", transport)
	}
	if cat.Base != nil {
		t.Error("expected Base to be nil when nil is passed")
	}
}
