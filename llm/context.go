// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"fmt"
	"strings"
	"time"

	"github.com/mattermost/mattermost/server/public/model"
)

// Context represents the data necessary to build the context of the LLM.
// For consumers none of the fields can be assumed to be present.
type Context struct {
	// Server
	Time        string
	ServerName  string
	CompanyName string

	// Location
	Team    *model.Team
	Channel *model.Channel
	Thread  []Post // Normalized posts that already have been formatted. nil if not in a thread or a root post

	// User that is making the request
	RequestingUser *model.User

	// Session information for authentication
	SessionID string
	// SessionResolver provides secure access to session token when needed
	SessionResolver func() (string, error)

	// Bot Specific
	BotName            string
	BotUsername        string
	BotModel           string
	CustomInstructions string

	Tools      *ToolStore
	Parameters map[string]interface{}
}

// ContextOption defines a function that configures a Context
type ContextOption func(*Context)

// NewContext creates a new Context with the given options
func NewContext(opts ...ContextOption) *Context {
	c := &Context{
		Time: time.Now().UTC().Format(time.RFC1123),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

func (c Context) String() string {
	var result strings.Builder
	result.WriteString(fmt.Sprintf("Time: %v\nServerName: %v\nCompanyName: %v", c.Time, c.ServerName, c.CompanyName))
	if c.RequestingUser != nil {
		result.WriteString(fmt.Sprintf("\nRequestingUser: %v", c.RequestingUser.Username))
	}
	if c.SessionID != "" {
		result.WriteString(fmt.Sprintf("\nSessionID: %v", c.SessionID))
	}
	if c.Channel != nil {
		result.WriteString(fmt.Sprintf("\nChannel: %v", c.Channel.Name))
	}
	if c.Team != nil {
		result.WriteString(fmt.Sprintf("\nTeam: %v", c.Team.Name))
	}

	result.WriteString("\n--- Parameters ---\n")
	for key := range c.Parameters {
		result.WriteString(fmt.Sprintf(" %v", key))
	}

	if c.Tools != nil {
		result.WriteString("\n--- Tools ---\n")
		for _, tool := range c.Tools.GetTools() {
			result.WriteString(tool.Name)
			result.WriteString(" ")
		}
	}

	return result.String()
}
