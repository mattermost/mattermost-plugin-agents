// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-agents/llm"
)

const (
	markerScopeTagTrue = "true"
)

// AnnotateMattermostScopeTags copies a schema tree and mirrors `scope:"..."`
// struct tags into property metadata for runtime Mattermost access-scope binding.
func AnnotateMattermostScopeTags[T any](root *jsonschema.Schema) *jsonschema.Schema {
	if root == nil {
		return nil
	}
	out := root.CloneSchemas()
	if out.Properties == nil {
		return out
	}

	var zero T
	structType := reflect.TypeOf(zero)
	if structType.Kind() == reflect.Ptr {
		structType = structType.Elem()
	}
	if structType.Kind() != reflect.Struct {
		return out
	}

	for i := 0; i < structType.NumField(); i++ {
		field := structType.Field(i)
		scopeKind, ok := parseMattermostScopeTag(field)
		if !ok {
			continue
		}
		jsonTag := field.Tag.Get("json")
		if jsonTag == "" || jsonTag == "-" {
			continue
		}
		jsonFieldName := strings.Split(jsonTag, ",")[0]
		if jsonFieldName == "" {
			continue
		}
		prop, ok := out.Properties[jsonFieldName]
		if !ok || prop == nil {
			continue
		}
		p := *prop
		if p.Extra == nil {
			p.Extra = make(map[string]any)
		}
		p.Extra[llm.MattermostScopeSchemaExtraKey] = scopeKind
		out.Properties[jsonFieldName] = &p
	}
	return out
}

func parseMattermostScopeTag(field reflect.StructField) (string, bool) {
	scopeTag := strings.TrimSpace(field.Tag.Get("scope"))
	if scopeTag == "" {
		return "", false
	}

	jsonTag := field.Tag.Get("json")
	jsonFieldName := strings.Split(jsonTag, ",")[0]

	switch scopeTag {
	case llm.MattermostScopeTagTeamID, llm.MattermostScopeTagChannelID:
		return scopeTag, true
	case markerScopeTagTrue:
		scopeKind, ok := inferMattermostScopeKind(jsonFieldName)
		if !ok {
			panic(fmt.Sprintf("unsupported inferred mattermost scope for json field %q", jsonFieldName))
		}
		return scopeKind, true
	default:
		panic(fmt.Sprintf("unsupported mattermost scope tag %q on field %q", scopeTag, field.Name))
	}
}

func inferMattermostScopeKind(jsonFieldName string) (string, bool) {
	switch jsonFieldName {
	case llm.MattermostScopeTagTeamID:
		return llm.MattermostScopeTagTeamID, true
	case llm.MattermostScopeTagChannelID:
		return llm.MattermostScopeTagChannelID, true
	default:
		return "", false
	}
}
