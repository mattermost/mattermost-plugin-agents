// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package accesscontrol

// Outcome mirrors model.AccessDecisionOutcome (contract §1.3). Plugin-local
// for now: the pinned server/public predates the server-side type. WS-D swaps
// the string values' source to model constants when the pin is bumped; the
// values themselves are wire-frozen.
type Outcome string

const (
	OutcomeAllow       Outcome = "allow"
	OutcomeDeny        Outcome = "deny"
	OutcomeNoPolicy    Outcome = "no_policy"
	OutcomeUnavailable Outcome = "unavailable"
)
