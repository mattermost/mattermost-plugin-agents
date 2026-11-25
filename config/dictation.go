// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package config

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
)

// DictationServiceTypes are the service types that can be selected as the
// dictation transcription service. Azure joins once its realtime sessions
// are verified over WebRTC.
var DictationServiceTypes = []string{llm.ServiceTypeOpenAI}

var (
	// ErrDictationDisabled is returned by DictationService when dictation is off.
	ErrDictationDisabled = errors.New("dictation is disabled")
	// ErrInvalidDictationConfig is wrapped by every other DictationService error.
	ErrInvalidDictationConfig = errors.New("invalid dictation configuration")
)

// DictationService returns the service dictation mints sessions with. Save
// validation and every runtime request use it, so a setting that saves is
// one that works.
func (c *Config) DictationService() (llm.ServiceConfig, error) {
	d := c.Dictation
	if !d.Enabled {
		return llm.ServiceConfig{}, ErrDictationDisabled
	}
	if d.ServiceID == "" {
		return llm.ServiceConfig{}, fmt.Errorf("%w: select a transcription service", ErrInvalidDictationConfig)
	}
	svc, ok := c.GetServiceByID(d.ServiceID)
	if !ok {
		return llm.ServiceConfig{}, fmt.Errorf("%w: transcription service %q is not configured", ErrInvalidDictationConfig, d.ServiceID)
	}
	if !slices.Contains(DictationServiceTypes, svc.Type) {
		return llm.ServiceConfig{}, fmt.Errorf("%w: service %q has type %q; dictation supports %s", ErrInvalidDictationConfig, svc.Name, svc.Type, strings.Join(DictationServiceTypes, ", "))
	}
	if svc.APIKey == "" {
		return llm.ServiceConfig{}, fmt.Errorf("%w: service %q has no API key", ErrInvalidDictationConfig, svc.Name)
	}
	// The server sends the service key to this URL and derives the sideband
	// WebSocket URL from it.
	if svc.APIURL != "" {
		u, err := url.Parse(svc.APIURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.Opaque != "" ||
			u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return llm.ServiceConfig{}, fmt.Errorf("%w: service %q needs an absolute http or https API URL with only a host and path", ErrInvalidDictationConfig, svc.Name)
		}
	}
	if strings.TrimSpace(d.Model) == "" {
		return llm.ServiceConfig{}, fmt.Errorf("%w: a transcription model is required", ErrInvalidDictationConfig)
	}
	return svc, nil
}

// ValidateDictation checks that an enabled dictation setting can be used.
func (c *Config) ValidateDictation() error {
	if _, err := c.DictationService(); err != nil && !errors.Is(err, ErrDictationDisabled) {
		return err
	}
	return nil
}
