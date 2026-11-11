// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	promptsDir := "."
	entries, err := os.ReadDir(promptsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading prompts directory: %v\n", err)
		os.Exit(1)
	}

	var output bytes.Buffer
	output.WriteString("// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.\n// See LICENSE.txt for license information.\n\npackage prompts\n\n// Automatically generated convenience vars for the filenames in prompts/\nconst (\n")

	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".tmpl" {
			baseName := strings.TrimSuffix(entry.Name(), ".tmpl")
			varName := "Prompt" + toCamelCase(baseName)
			fmt.Fprintf(&output, "\t%s = %q\n", varName, baseName)
		}
	}

	output.WriteString(")\n")

	// Format the output using gofmt
	formattedOutput, err := format.Source(output.Bytes())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting output: %v\n", err)
		os.Exit(1)
	}

	// Write the formatted output to the file
	err = os.WriteFile("prompts_vars.go", formattedOutput, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error writing output file: %v\n", err)
		os.Exit(1)
	}
}

// initialisms are kept all caps so generated names satisfy Go naming lint.
var initialisms = map[string]bool{"ui": true}

func toCamelCase(s string) string {
	words := strings.FieldsFunc(s, func(r rune) bool {
		return r == '_' || r == '-'
	})
	for i, word := range words {
		if initialisms[strings.ToLower(word)] {
			words[i] = strings.ToUpper(word)
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + strings.ToLower(word[1:])
	}
	return strings.Join(words, "")
}
