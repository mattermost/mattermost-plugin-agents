// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package httpclienttimeout_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/mattermost/mattermost-plugin-agents/v2/build/govet/httpclienttimeout"
)

func TestAnalyzer(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), httpclienttimeout.Analyzer, "clients")
}
