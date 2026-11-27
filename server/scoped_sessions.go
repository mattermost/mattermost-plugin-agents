// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"

	"github.com/mattermost/mattermost-plugin-agents/v2/scopedsession"
	"github.com/mattermost/mattermost/server/public/pluginapi"
)

// newScopedSessionMinter returns the minter scoped channel mode runs on, or nil
// when this node's server cannot create scoped sessions or the probe could not
// tell, which keeps the mode off. The server restarts its plugins when it
// restarts, so probing once at activation covers server upgrades.
func newScopedSessionMinter(api scopedsession.SessionAPI, log *pluginapi.LogService) scopedsession.Minter {
	if err := scopedsession.Probe(api); err != nil {
		if errors.Is(err, scopedsession.ErrServerUnsupported) {
			log.Info("Scoped channel mode is unavailable: this Mattermost server cannot create scoped sessions")
		} else {
			log.Warn("Scoped channel mode is unavailable: could not tell whether this Mattermost server can create scoped sessions", "error", err)
		}
		return nil
	}
	return scopedsession.NewPluginAPIMinter(api)
}
