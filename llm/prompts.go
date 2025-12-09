// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"text/template"
)

type Prompts struct {
	templates *template.Template
}

const PromptExtension = "tmpl"

func NewPrompts(input fs.FS) (*Prompts, error) {
	templates, err := template.ParseFS(input, "*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("unable to parse prompt templates: %w", err)
	}

	return &Prompts{
		templates: templates,
	}, nil
}

func withPromptExtension(filename string) string {
	return filename + "." + PromptExtension
}

func (p *Prompts) FormatString(templateCode string, context *Context) (string, error) {
	template, err := p.templates.Clone()
	if err != nil {
		return "", err
	}

	template, err = template.Parse(templateCode)
	if err != nil {
		return "", err
	}

	out := &strings.Builder{}
	if err := template.Execute(out, context); err != nil {
		return "", fmt.Errorf("unable to execute template: %w", err)
	}
	return strings.TrimSpace(out.String()), nil
}

func (p *Prompts) Format(templateName string, context *Context) (string, error) {
	tmpl := p.templates.Lookup(withPromptExtension(templateName))
	if tmpl == nil {
		return "", errors.New("template not found")
	}

	return p.execute(tmpl, context)
}

func (p *Prompts) execute(template *template.Template, data *Context) (string, error) {
	out := &strings.Builder{}
	if err := template.Execute(out, data); err != nil {
		return "", fmt.Errorf("unable to execute template: %w", err)
	}
	return strings.TrimSpace(out.String()), nil
}
