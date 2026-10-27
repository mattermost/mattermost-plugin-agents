// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost/server/public/model"
)

// MaxSkillDescriptionRunes caps a skill description, including an agent's
// description override, as it is listed to the model.
const MaxSkillDescriptionRunes = 1024

// SkillsConfig is an agent's skill settings. Built-in skills are always
// listed unless Disabled; user skills must be opted into.
type SkillsConfig struct {
	// Disabled turns off every skill for the agent, built-in skills included.
	Disabled bool `json:"disabled"`
	// AllowPersonal lists each requester's own skills in their DMs with the agent.
	AllowPersonal bool `json:"allowPersonal"`
	// EnabledSharedIDs are the shared skills the agent lists.
	EnabledSharedIDs []string `json:"enabledSharedIDs"`
	// DescriptionOverrides replaces a skill's description, by skill ID, for this agent.
	DescriptionOverrides map[string]string `json:"descriptionOverrides"`
}

func (s SkillsConfig) Validate() error {
	for _, id := range s.EnabledSharedIDs {
		if !model.IsValidId(id) {
			return errors.New("skills.enabledSharedIDs must contain skill IDs")
		}
	}
	for id, description := range s.DescriptionOverrides {
		if !model.IsValidId(id) {
			return errors.New("skills.descriptionOverrides must be keyed by skill ID")
		}
		if utf8.RuneCountInString(description) > MaxSkillDescriptionRunes {
			return fmt.Errorf("skill description overrides must be at most %d characters", MaxSkillDescriptionRunes)
		}
	}
	return nil
}

// SkillInfo is one entry of the skill catalog shown to the model.
type SkillInfo struct {
	ID          string
	Name        string
	Description string
}

// FormatSkillCatalog renders skills one per line, the same way in the system
// prompt and in refresh_skills results.
func FormatSkillCatalog(skills []SkillInfo) string {
	var b strings.Builder
	for i, skill := range skills {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "- id: %s | %s: %s", skill.ID, skill.Name, skill.Description)
	}
	return b.String()
}

// SkillCatalog renders the context's skill catalog for prompt templates.
func (c *Context) SkillCatalog() string {
	if c == nil {
		return ""
	}
	return FormatSkillCatalog(c.Skills)
}
