// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"strings"
	"text/template"
	"unicode"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
)

type Prompts struct {
	templates *template.Template
	locales   map[string]*template.Template
}

const PromptExtension = "tmpl"

// EscapePromptContent replaces angle brackets in user-generated content to prevent
// injection of fake XML structural elements into prompt templates.
func EscapePromptContent(s string) string {
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

func promptFuncMap() template.FuncMap {
	return template.FuncMap{
		"escapeContent": EscapePromptContent,
		"formatTime":    format.TimeFromMillis,
	}
}

func NewPrompts(input fs.FS) (*Prompts, error) {
	funcMap := promptFuncMap()
	templates, err := template.New("").Funcs(funcMap).ParseFS(input, "*.tmpl")
	if err != nil {
		return nil, fmt.Errorf("unable to parse prompt templates: %w", err)
	}

	locales, err := parseLocalizedPrompts(input, templates, funcMap)
	if err != nil {
		return nil, err
	}

	return &Prompts{
		templates: templates,
		locales:   locales,
	}, nil
}

func parseLocalizedPrompts(input fs.FS, base *template.Template, funcMap template.FuncMap) (map[string]*template.Template, error) {
	entries, err := fs.ReadDir(input, ".")
	if err != nil {
		return nil, fmt.Errorf("unable to list prompt templates: %w", err)
	}

	locales := make(map[string]*template.Template)
	for _, entry := range entries {
		if !entry.IsDir() || !isPromptLanguageDir(entry.Name()) {
			continue
		}

		cloned, cloneErr := base.Clone()
		if cloneErr != nil {
			return nil, fmt.Errorf("unable to clone prompt templates for %s: %w", entry.Name(), cloneErr)
		}
		cloned.Funcs(funcMap)

		walkErr := fs.WalkDir(input, entry.Name(), func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() || !strings.HasSuffix(p, "."+PromptExtension) {
				return nil
			}
			content, readErr := fs.ReadFile(input, p)
			if readErr != nil {
				return readErr
			}
			_, parseErr := cloned.New(path.Base(p)).Parse(string(content))
			return parseErr
		})
		if walkErr != nil {
			return nil, fmt.Errorf("unable to parse %s prompt templates: %w", entry.Name(), walkErr)
		}

		locales[entry.Name()] = cloned
	}

	return locales, nil
}

func isPromptLanguageDir(name string) bool {
	if len(name) < 2 || len(name) > 3 {
		return false
	}
	for _, r := range name {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// PromptLanguage returns the BCP 47 primary language subtag from the
// requesting user's Mattermost locale (fr from fr_FR). Empty means English defaults.
func PromptLanguage(ctx *Context) string {
	if ctx == nil || ctx.RequestingUser == nil {
		return ""
	}
	loc := strings.ToLower(strings.ReplaceAll(ctx.RequestingUser.Locale, "-", "_"))
	loc = strings.TrimSpace(loc)
	if loc == "" {
		return ""
	}
	lang, _, _ := strings.Cut(loc, "_")
	for _, r := range lang {
		if !unicode.IsLetter(r) {
			return ""
		}
	}
	return lang
}

func withPromptExtension(filename string) string {
	return filename + "." + PromptExtension
}

func (p *Prompts) FormatString(templateCode string, data any) (string, error) {
	tmpl, err := p.templates.Clone()
	if err != nil {
		return "", err
	}

	tmpl.Option("missingkey=zero")

	tmpl, err = tmpl.Parse(templateCode)
	if err != nil {
		return "", err
	}

	out := &strings.Builder{}
	if err := tmpl.Execute(out, data); err != nil {
		return "", fmt.Errorf("unable to execute template: %w", err)
	}
	return strings.TrimSpace(out.String()), nil
}

func (p *Prompts) lookup(templateName string, context *Context) *template.Template {
	name := withPromptExtension(templateName)
	if lang := PromptLanguage(context); lang != "" {
		if localized := p.locales[lang]; localized != nil {
			if tmpl := localized.Lookup(name); tmpl != nil {
				return tmpl
			}
		}
	}
	return p.templates.Lookup(name)
}

func (p *Prompts) Format(templateName string, context *Context) (string, error) {
	tmpl := p.lookup(templateName, context)
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
