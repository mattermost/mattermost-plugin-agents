// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

// NoopMetrics is a no-operation implementation of the Metrics interface for testing.
type NoopMetrics struct {
}

// NewNoopMetrics creates a new instance of NoopMetrics.
func NewNoopMetrics() Metrics {
	return &NoopMetrics{}
}

// GetRegistry returns a new empty registry.
func (m *NoopMetrics) GetRegistry() *prometheus.Registry {
	return prometheus.NewRegistry()
}

// ObserveAPIEndpointDuration is a no-op implementation.
func (m *NoopMetrics) ObserveAPIEndpointDuration(handler, method, statusCode string, elapsed float64) {
	// No-op
}

// IncrementHTTPRequests is a no-op implementation.
func (m *NoopMetrics) IncrementHTTPRequests() {
	// No-op
}

// IncrementHTTPErrors is a no-op implementation.
func (m *NoopMetrics) IncrementHTTPErrors() {
	// No-op
}

// GetMetricsForAIService returns a no-op implementation of LLMetrics.
func (m *NoopMetrics) GetMetricsForAIService(llmName string) *llmMetrics { //nolint:revive
	return &llmMetrics{}
}

// ObserveTokenUsage is a no-op implementation.
func (m *NoopMetrics) ObserveTokenUsage(botName, teamID, userID string, inputTokens, outputTokens int) {
	// No-op
}
