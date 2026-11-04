// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// agents-govet is the repo's go vet tool: go vet -vettool=bin/agents-govet ./...
package main

import (
	"log"

	"github.com/mattermost/mattermost/tools/mattermost-govet/errorAssertions"
	"github.com/mattermost/mattermost/tools/mattermost-govet/errorVars"
	"github.com/mattermost/mattermost/tools/mattermost-govet/errorVarsName"
	"github.com/mattermost/mattermost/tools/mattermost-govet/license"
	"github.com/mattermost/mattermost/tools/mattermost-govet/mutexLock"
	"github.com/mattermost/mattermost/tools/mattermost-govet/noSelectStar"
	"github.com/mattermost/mattermost/tools/mattermost-govet/sortSlices"
	"github.com/mattermost/mattermost/tools/mattermost-govet/tFatal"
	"golang.org/x/tools/go/analysis/unitchecker"

	"github.com/mattermost/mattermost-plugin-agents/v2/build/govet/httpclienttimeout"
)

func main() {
	if err := license.Analyzer.Flags.Set("year", "2023"); err != nil {
		log.Fatal(err)
	}

	unitchecker.Main(
		errorAssertions.Analyzer,
		errorVars.Analyzer,
		errorVarsName.Analyzer,
		license.Analyzer,
		mutexLock.Analyzer,
		noSelectStar.Analyzer,
		sortSlices.Analyzer,
		tFatal.Analyzer,
		httpclienttimeout.Analyzer,
	)
}
