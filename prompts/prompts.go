// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package prompts

import "embed"

// English defaults live at the prompts/ root. Optional overlays live in
// ISO 639 language subdirectories (for example prompts/fr/*.tmpl) and are
// selected from the requesting user's Mattermost locale, with fallback to English.
//
//go:embed *.tmpl */*.tmpl
var PromptsFolder embed.FS

//go:generate go run generate_prompt_vars.go
