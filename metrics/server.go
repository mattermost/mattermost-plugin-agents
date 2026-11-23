// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package metrics

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type ErrorLoggerWrapper struct {
}

func (el *ErrorLoggerWrapper) Println(v ...any) {
	slog.Warn("metric server error", "error", fmt.Sprint(v...))
}

// NewMetricsHandler creates an HTTP handler to expose metrics.
func NewMetricsHandler(metricsService Metrics) http.Handler {
	return promhttp.HandlerFor(metricsService.GetRegistry(), promhttp.HandlerOpts{
		ErrorLog: &ErrorLoggerWrapper{},
	})
}
