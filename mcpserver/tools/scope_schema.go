// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/mattermost/mattermost-plugin-agents/llm"
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
		scopeTag := strings.TrimSpace(field.Tag.Get("scope"))
		if scopeTag == "" {
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
		p.Extra[llm.MattermostScopeSchemaExtraKey] = scopeTag
		out.Properties[jsonFieldName] = &p
	}
	return out
}
