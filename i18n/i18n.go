// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package i18n

import (
	"embed"
	"fmt"

	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

//go:embed *.json
var i18nFiles embed.FS

type TranslationFunc func(translationId string, defaultMessage string, params ...any) string

type Bundle = i18n.Bundle

func Init() *Bundle {
	bundle := i18n.NewBundle(language.English)
	_, _ = bundle.LoadMessageFileFS(i18nFiles, "i18n/es.json")

	return bundle
}

func LocalizerFunc(bundle *Bundle, lang string) TranslationFunc {
	localizer := i18n.NewLocalizer(bundle, lang)

	return func(translationId string, defaultMessage string, params ...any) string {
		message := localizer.MustLocalize(&i18n.LocalizeConfig{
			DefaultMessage: &i18n.Message{
				ID:    translationId,
				Other: defaultMessage,
			},
		})
		if len(params) > 0 {
			return fmt.Sprintf(message, params...)
		}
		return message
	}
}
