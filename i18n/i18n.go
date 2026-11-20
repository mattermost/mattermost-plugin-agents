// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
)

//go:embed *.json
var i18nFiles embed.FS

type TranslationFunc func(translationId string, defaultMessage string, params ...any) string

type Bundle struct {
	messages map[string]map[string]string
}

type translationEntry struct {
	ID          string `json:"id"`
	Translation string `json:"translation"`
}

func Init() *Bundle {
	b := &Bundle{
		messages: make(map[string]map[string]string),
	}

	entries, err := fs.ReadDir(i18nFiles, ".")
	if err != nil {
		return b
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := i18nFiles.ReadFile(entry.Name())
		if err != nil {
			continue
		}
		var items []translationEntry
		if err := json.Unmarshal(data, &items); err != nil {
			continue
		}
		locale := normalizeLocale(strings.TrimSuffix(entry.Name(), ".json"))
		catalog := make(map[string]string, len(items))
		for _, item := range items {
			if item.ID != "" && item.Translation != "" {
				catalog[item.ID] = item.Translation
			}
		}
		b.messages[locale] = catalog
	}

	return b
}

func LocalizerFunc(bundle *Bundle, lang string) TranslationFunc {
	catalog := bundle.lookupCatalog(lang)

	return func(translationId string, defaultMessage string, params ...any) string {
		message := defaultMessage
		if translated, ok := catalog[translationId]; ok {
			message = translated
		}
		if len(params) > 0 {
			return fmt.Sprintf(message, params...)
		}
		return message
	}
}

func (b *Bundle) lookupCatalog(lang string) map[string]string {
	if b == nil || lang == "" {
		return nil
	}
	normalized := normalizeLocale(lang)
	if catalog, ok := b.messages[normalized]; ok {
		return catalog
	}
	if base, _, ok := strings.Cut(normalized, "-"); ok {
		if catalog, ok := b.messages[base]; ok {
			return catalog
		}
	}
	return nil
}

func normalizeLocale(lang string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(lang), "_", "-"))
}
