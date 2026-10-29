// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost/server/public/model"
)

// ErrInvalidAgentReference is returned when a setting that selects an agent
// does not hold a Mattermost bot user ID.
var ErrInvalidAgentReference = errors.New("invalid agent reference")

// ValidateAgentReferences rejects a default or transcript agent reference that
// is not a well-formed bot user ID. Whether the agent exists is not checked:
// a reference to an unavailable agent fails at use instead.
func (c *Config) ValidateAgentReferences() error {
	for _, ref := range []struct{ field, id string }{
		{"defaultBotUserID", c.DefaultBotUserID},
		{"transcriptBotUserID", c.TranscriptBotUserID},
	} {
		if ref.id != "" && !model.IsValidId(ref.id) {
			return fmt.Errorf("%w: %s must be a bot user ID", ErrInvalidAgentReference, ref.field)
		}
	}
	return nil
}

func (c *Config) transcriptAgentConfigured() bool {
	return c.TranscriptBotUserID != "" || c.TranscriptGenerator != ""
}

// ReconcileAgentReferences adapts the agent references of next, a config about
// to replace prev, so each setting has one source of truth. A client that
// predates the ID fields round-trips them unchanged while editing the legacy
// name, so a changed legacy name with an unchanged ID selects the agent by that
// name. Otherwise a set ID makes the legacy name redundant, and it is dropped.
func ReconcileAgentReferences(prev *Config, next Config) Config {
	var from Config
	if prev != nil {
		from = *prev
	}
	next.DefaultBotUserID, next.DefaultBotName = reconcileAgentReference(
		from.DefaultBotUserID, from.DefaultBotName, next.DefaultBotUserID, next.DefaultBotName)
	next.TranscriptBotUserID, next.TranscriptGenerator = reconcileAgentReference(
		from.TranscriptBotUserID, from.TranscriptGenerator, next.TranscriptBotUserID, next.TranscriptGenerator)
	return next
}

func reconcileAgentReference(prevID, prevName, nextID, nextName string) (string, string) {
	if nextName != "" && nextName != prevName && nextID == prevID {
		return "", nextName
	}
	if nextID != "" {
		return nextID, ""
	}
	return nextID, nextName
}

// ClearAgentReferences returns a copy of c with every setting that selects the
// given agent cleared, by bot user ID or by legacy username, and whether any
// was. A legacy name is cleared too, so it cannot later select another agent
// that takes over the username.
func (c *Config) ClearAgentReferences(botUserID, username string) (Config, bool) {
	next := *c
	cleared := false
	clearID := func(id *string) {
		if botUserID != "" && *id == botUserID {
			*id = ""
			cleared = true
		}
	}
	clearName := func(name *string) {
		if username != "" && strings.EqualFold(*name, username) {
			*name = ""
			cleared = true
		}
	}
	clearID(&next.DefaultBotUserID)
	clearID(&next.TranscriptBotUserID)
	clearName(&next.DefaultBotName)
	clearName(&next.TranscriptGenerator)
	return next, cleared
}

// MigrateAgentReferences replaces each legacy username reference that has no
// bot user ID with the bot user ID resolved maps it to. A name missing from
// resolved is left in place. It reports whether anything changed.
func (c *Config) MigrateAgentReferences(resolved map[string]string) (Config, bool) {
	next := *c
	migrated := false
	migrate := func(id, name *string) {
		if *id != "" || *name == "" {
			return
		}
		botUserID, ok := resolved[*name]
		if !ok || botUserID == "" {
			return
		}
		*id = botUserID
		*name = ""
		migrated = true
	}
	migrate(&next.DefaultBotUserID, &next.DefaultBotName)
	migrate(&next.TranscriptBotUserID, &next.TranscriptGenerator)
	return next, migrated
}

// LegacyAgentReferences returns the legacy username references that still
// need a bot user ID.
func (c *Config) LegacyAgentReferences() []string {
	var names []string
	add := func(id, name string) {
		if id == "" && name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	add(c.DefaultBotUserID, c.DefaultBotName)
	add(c.TranscriptBotUserID, c.TranscriptGenerator)
	return names
}
