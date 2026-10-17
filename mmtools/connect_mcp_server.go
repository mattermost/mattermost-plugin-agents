// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mcp"
)

const connectMCPServerDescription = "Ask the requesting user to connect (sign in to) an MCP server they have not connected yet, so its tools become available. " +
	"Call this when the request needs an integration listed in the `server` parameter instead of saying the capability does not exist. " +
	"Only call it when the request actually needs that integration. " +
	"The user is shown a Connect card and may connect or skip; the tool result tells you which. " +
	"Never claim to have performed an action with a server that is not connected."

// ConnectMCPServerArgs is the LLM-visible input schema for the connect tool.
type ConnectMCPServerArgs struct {
	Server string `json:"server" jsonschema_description:"Name of the MCP server to connect, exactly as listed."`
}

// NewConnectMCPServerTool returns the connect tool for this turn's unconnected
// servers. Like AskUserQuestion it is answered through the tool-approval flow
// (Conversations.HandleToolCall), never executed server-side.
func NewConnectMCPServerTool(servers []llm.UnconnectedMCPServer) llm.Tool {
	names := make([]string, 0, len(servers))
	for _, server := range servers {
		names = append(names, server.Name)
	}

	schema := llm.NewJSONSchemaFromStruct[ConnectMCPServerArgs]()
	if prop := schema.Properties["server"]; prop != nil && len(names) > 0 {
		prop.Enum = make([]any, 0, len(names))
		for _, name := range names {
			prop.Enum = append(prop.Enum, name)
		}
		prop.Description = connectMCPServerParamDescription(prop.Description, servers)
	}

	return llm.Tool{
		Name:            llm.ConnectMCPServerToolName,
		Description:     connectMCPServerDescription,
		Schema:          schema,
		UserInteraction: llm.UserInteractionConnect,
		ValidateArguments: func(input json.RawMessage) error {
			args, err := parseConnectMCPServerArgs(input)
			if err != nil {
				return err
			}
			for _, name := range names {
				if args.Server == name {
					return nil
				}
			}
			return fmt.Errorf("server %q is not an unconnected MCP server; use one of: %s", args.Server, strings.Join(names, ", "))
		},
		Resolver: func(_ context.Context, _ *llm.Context, _ llm.ToolArgumentGetter) (string, error) {
			return "", errors.New("ConnectMCPServer must be answered by the user and cannot be executed directly")
		},
	}
}

// connectMCPServerParamDescription lists the user's unconnected servers in the
// schema, which only the model sees. The tool description must stay static:
// it is copied onto tool calls, which are persisted and shown to other
// channel members.
func connectMCPServerParamDescription(base string, servers []llm.UnconnectedMCPServer) string {
	var b strings.Builder
	b.WriteString(base)
	b.WriteString(" Servers the user has not connected:")
	for _, server := range servers {
		b.WriteString("\n- ")
		b.WriteString(server.PromptLine())
	}
	return b.String()
}

// resolveConnectMCPServerAccepted computes the tool result for an accepted
// connect call from the catalog rebuilt after the user's click. The server
// counts as connected only when tools from env.ConnectServerOrigin are
// actually available there; merely dropping off the unconnected list (e.g. an
// admin disabled it) is not enough.
func resolveConnectMCPServerAccepted(input json.RawMessage, env UserInteractionEnv) (UserInteractionResult, error) {
	args, err := parseConnectMCPServerArgs(input)
	if err != nil {
		return UserInteractionResult{}, err
	}
	llmContext := env.Context
	if llmContext == nil {
		return UserInteractionResult{}, errors.New("connect result requires the rebuilt tool context")
	}

	if _, stillUnconnected := llmContext.UnconnectedMCPServerByName(args.Server); stillUnconnected {
		return UserInteractionResult{
			Content: fmt.Sprintf("%s is still not connected: the user did not finish connecting it. "+
				"Do not perform actions that need %s. Tell the user they can connect %s and ask again.",
				args.Server, args.Server, args.Server),
			IsError: true,
		}, nil
	}

	if env.ConnectServerOrigin == "" || llmContext.Tools == nil || !llmContext.Tools.HasMCPToolsFromOrigin(env.ConnectServerOrigin) {
		return UserInteractionResult{
			Content: fmt.Sprintf("%s is not available: none of its tools can be used in this conversation. "+
				"Do not perform actions that need %s. Tell the user %s is unavailable here.",
				args.Server, args.Server, args.Server),
			IsError: true,
		}, nil
	}

	content := fmt.Sprintf("User connected %s. Its tools are now available.", args.Server)
	if llmContext.ToolCatalog.MCPDynamicToolLoading && llmContext.IsToolCallable(mcp.SearchToolsName) {
		content += fmt.Sprintf(" Use %s to find them and %s to load one before calling it.", mcp.SearchToolsName, mcp.LoadToolName)
	}
	return UserInteractionResult{Content: content}, nil
}

// connectMCPServerSkippedResult is the tool result for a skipped connect call.
func connectMCPServerSkippedResult(input json.RawMessage) string {
	args, err := parseConnectMCPServerArgs(input)
	if err != nil {
		return "User declined to connect the MCP server"
	}
	return fmt.Sprintf("User declined to connect %s", args.Server)
}

// ConnectMCPServerName returns the server named by a connect call's arguments.
func ConnectMCPServerName(input json.RawMessage) (string, bool) {
	args, err := parseConnectMCPServerArgs(input)
	if err != nil {
		return "", false
	}
	return args.Server, true
}

func parseConnectMCPServerArgs(input json.RawMessage) (ConnectMCPServerArgs, error) {
	var args ConnectMCPServerArgs
	if err := json.Unmarshal(input, &args); err != nil {
		return args, fmt.Errorf("failed to parse connect arguments: %w", err)
	}
	if strings.TrimSpace(args.Server) == "" {
		return args, errors.New("server must not be empty")
	}
	return args, nil
}
