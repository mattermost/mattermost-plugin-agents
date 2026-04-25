// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package scope

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/mattermost/mattermost-plugin-agents/llm"
)

// testArgs mirrors the shape of a post-writing tool's args. Used both for
// schema generation and as the unmarshaled target inside the resolver.
type testArgs struct {
	ChannelID          string `json:"channel_id"`
	ChannelDisplayName string `json:"channel_display_name"`
	TeamDisplayName    string `json:"team_display_name"`
	Message            string `json:"message"`
}

// makeTool constructs a Tool whose resolver records whatever args it saw.
// The recording pointer is returned alongside the tool so tests can inspect it.
func makeTool(t *testing.T, name string) (llm.Tool, *testArgs) {
	t.Helper()
	seen := &testArgs{}
	schema := llm.NewJSONSchemaFromStruct[testArgs]()
	tool := llm.Tool{
		Name:        name,
		Description: "test tool",
		Schema:      schema,
		Resolver: func(_ *llm.Context, argsGetter llm.ToolArgumentGetter) (string, error) {
			var decoded testArgs
			if err := argsGetter(&decoded); err != nil {
				return "", err
			}
			*seen = decoded
			return "ok", nil
		},
	}
	return tool, seen
}

func TestApplyToolScope_DropsDisallowed(t *testing.T) {
	source := llm.NewToolStore(nil, false)
	keepTool, _ := makeTool(t, "create_post")
	dropTool, _ := makeTool(t, "delete_post")
	source.AddTools([]llm.Tool{keepTool, dropTool})

	scoped := ApplyToolScope(source, []string{"create_post"}, nil, "CHAN", nil)

	if got := scoped.GetTool("create_post"); got == nil {
		t.Fatalf("expected create_post to be retained")
	}
	if got := scoped.GetTool("delete_post"); got != nil {
		t.Fatalf("expected delete_post to be dropped, got %v", got)
	}
}

func TestApplyToolScope_EmptyAllowlistProducesEmptyStore(t *testing.T) {
	source := llm.NewToolStore(nil, false)
	tool, _ := makeTool(t, "create_post")
	source.AddTools([]llm.Tool{tool})

	scoped := ApplyToolScope(source, nil, nil, "CHAN", nil)

	if got := len(scoped.GetTools()); got != 0 {
		t.Fatalf("expected 0 tools, got %d", got)
	}
}

func TestApplyToolScope_BindsTargetChannelSentinel(t *testing.T) {
	source := llm.NewToolStore(nil, false)
	tool, seen := makeTool(t, "create_post")
	source.AddTools([]llm.Tool{tool})

	scoped := ApplyToolScope(
		source,
		[]string{"create_post"},
		map[string]map[string]interface{}{
			"create_post": {"channel_id": llm.BoundParamTargetChannelSentinel},
		},
		"TARGET_CHAN_ID",
		nil,
	)

	// Resolve with LLM-supplied args that omit channel_id (it should be
	// schema-stripped; the bound param is the authoritative source).
	got, err := scoped.ResolveTool("create_post",
		func(args any) error { return json.Unmarshal([]byte(`{"message":"hi"}`), args) },
		nil,
	)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got != "ok" {
		t.Fatalf("unexpected resolver return: %q", got)
	}
	if seen.ChannelID != "TARGET_CHAN_ID" {
		t.Fatalf("channel_id was not bound; seen=%+v", *seen)
	}
	if seen.Message != "hi" {
		t.Fatalf("message was not passed through; seen=%+v", *seen)
	}
}

func TestApplyToolScope_BindsTargetChannelContext(t *testing.T) {
	source := llm.NewToolStore(nil, false)
	tool, seen := makeTool(t, "create_post")
	source.AddTools([]llm.Tool{tool})

	scoped := ApplyToolScope(
		source,
		[]string{"create_post"},
		map[string]map[string]interface{}{
			"create_post": {
				"channel_id":           llm.BoundParamTargetChannelSentinel,
				"channel_display_name": "Town Square",
				"team_display_name":    "Dev Team",
			},
		},
		"TARGET_CHAN_ID",
		nil,
	)

	_, err := scoped.ResolveTool("create_post",
		func(args any) error { return json.Unmarshal([]byte(`{"message":"hi"}`), args) },
		nil,
	)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if seen.ChannelID != "TARGET_CHAN_ID" {
		t.Fatalf("channel_id=%q, want TARGET_CHAN_ID", seen.ChannelID)
	}
	if seen.ChannelDisplayName != "Town Square" {
		t.Fatalf("channel_display_name=%q, want Town Square", seen.ChannelDisplayName)
	}
	if seen.TeamDisplayName != "Dev Team" {
		t.Fatalf("team_display_name=%q, want Dev Team", seen.TeamDisplayName)
	}
}

func TestApplyToolScope_BoundCreatePostDescriptionSkipsDiscovery(t *testing.T) {
	source := llm.NewToolStore(nil, false)
	tool, _ := makeTool(t, "create_post")
	tool.Description = "MUST first call get_channel_info before create_post"
	source.AddTools([]llm.Tool{tool})

	scoped := ApplyToolScope(
		source,
		[]string{"create_post"},
		map[string]map[string]interface{}{"create_post": {"channel_id": llm.BoundParamTargetChannelSentinel}},
		"TARGET_CHAN",
		nil,
	)

	scopedTool := scoped.GetTool("create_post")
	if scopedTool == nil {
		t.Fatal("expected scoped tool to exist")
	}
	if scopedTool.Description == tool.Description {
		t.Fatal("create_post description should be replaced for scoped runs")
	}
	if strings.Contains(scopedTool.Description, "get_channel_info") {
		t.Fatalf("scoped description should not mention get_channel_info: %q", scopedTool.Description)
	}
}

func TestApplyToolScope_BoundParamStripsChannelFromSchema(t *testing.T) {
	source := llm.NewToolStore(nil, false)
	tool, _ := makeTool(t, "create_post")
	source.AddTools([]llm.Tool{tool})

	scoped := ApplyToolScope(
		source,
		[]string{"create_post"},
		map[string]map[string]interface{}{
			"create_post": {"channel_id": llm.BoundParamTargetChannelSentinel},
		},
		"TARGET_CHAN",
		nil,
	)

	scopedTool := scoped.GetTool("create_post")
	if scopedTool == nil {
		t.Fatal("expected scoped tool to exist")
	}
	schema, ok := scopedTool.Schema.(*jsonschema.Schema)
	if !ok {
		t.Fatalf("expected jsonschema.Schema, got %T", scopedTool.Schema)
	}
	if _, present := schema.Properties["channel_id"]; present {
		t.Fatal("channel_id should have been stripped from the visible schema")
	}
	if _, present := schema.Properties["message"]; !present {
		t.Fatal("message should remain visible in schema")
	}
}

// capturingLogger is a tiny Logger that accumulates messages so AssertBoundParams tests can check output.
type capturingLogger struct {
	errors []string
	warns  []string
}

func (c *capturingLogger) Error(msg string, _ ...any) { c.errors = append(c.errors, msg) }
func (c *capturingLogger) Warn(msg string, _ ...any)  { c.warns = append(c.warns, msg) }
func (c *capturingLogger) Info(_ string, _ ...any)    {}

func TestAssertBoundParams_FlagsMissingProperty(t *testing.T) {
	source := llm.NewToolStore(nil, false)
	tool, _ := makeTool(t, "create_post")
	source.AddTools([]llm.Tool{tool})

	subs := []llm.AgentSubscription{{
		ID:          "sub1",
		BoundParams: map[string]map[string]interface{}{"create_post": {"nonexistent_field": "x"}},
	}}

	log := &capturingLogger{}
	AssertBoundParams(source, subs, nil, log)
	if len(log.errors) == 0 {
		t.Fatal("expected an error for missing schema property")
	}
}

func TestAssertBoundParams_FlagsMissingTool(t *testing.T) {
	source := llm.NewToolStore(nil, false)
	scheds := []llm.AgentSchedule{{
		ID:          "sched1",
		BoundParams: map[string]map[string]interface{}{"nonexistent_tool": {"foo": "bar"}},
	}}

	log := &capturingLogger{}
	AssertBoundParams(source, nil, scheds, log)
	if len(log.errors) == 0 {
		t.Fatal("expected an error for missing tool")
	}
}
