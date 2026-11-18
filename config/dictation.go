// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// DictationServiceTypes are the service types that can be selected as the
// dictation transcription service. Azure joins once its realtime sessions
// are verified over WebRTC.
var DictationServiceTypes = []string{llm.ServiceTypeOpenAI}

// ErrInvalidDictationConfig is wrapped by every ValidateDictation error.
var ErrInvalidDictationConfig = errors.New("invalid dictation configuration")

// ValidateDictation checks that an enabled dictation setting names a
// configured service of a supported type and a model.
func (c *Config) ValidateDictation() error {
	d := c.Dictation
	if !d.Enabled {
		return nil
	}
	if d.ServiceID == "" {
		return fmt.Errorf("%w: select a transcription service", ErrInvalidDictationConfig)
	}
	svc, ok := c.GetServiceByID(d.ServiceID)
	if !ok {
		return fmt.Errorf("%w: transcription service %q is not configured", ErrInvalidDictationConfig, d.ServiceID)
	}
	if !slices.Contains(DictationServiceTypes, svc.Type) {
		return fmt.Errorf("%w: service %q has type %q; dictation supports %s", ErrInvalidDictationConfig, svc.Name, svc.Type, strings.Join(DictationServiceTypes, ", "))
	}
	if strings.TrimSpace(d.Model) == "" {
		return fmt.Errorf("%w: a transcription model is required", ErrInvalidDictationConfig)
	}
	return nil
}
