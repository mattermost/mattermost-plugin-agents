// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost/server/public/model"
)

// Upload policy failures shared by every tool-driven upload path. Callers
// that need distinct user-facing messages match with errors.Is.
var (
	errUploadNoChannel    = errors.New("no conversation channel to hold the file")
	errUploadNoUser       = errors.New("no requesting user")
	errUploadDisabled     = errors.New("file attachments are disabled by server config")
	errUploadNoPermission = errors.New("requesting user lacks upload permission in the channel")

	errUploadFileNameInvalid = errors.New("file name is not a plain file name")
	errUploadFileNameTooLong = fmt.Errorf("file name is longer than %d characters", maxCreateFileNameLength)
)

// uploadPolicyAllowed enforces the server attachment policy and the requesting
// user's upload permission. UploadFile goes through the admin-level plugin
// API, which bypasses the per-user checks of the api4 upload endpoint, so
// every tool upload path (CreateFile, sandbox attachment) must pass this
// first. A nil EnableFileAttachments means enabled (server default). A nil
// RequestingUser is a bug in the calling flow, so fail closed rather than
// skip the permission check.
func uploadPolicyAllowed(client mmapi.Client, llmCtx *llm.Context) error {
	if llmCtx == nil || llmCtx.Channel == nil || llmCtx.Channel.Id == "" {
		return errUploadNoChannel
	}
	if llmCtx.RequestingUser == nil || llmCtx.RequestingUser.Id == "" {
		return errUploadNoUser
	}
	cfg := client.GetConfig()
	if cfg != nil && cfg.FileSettings.EnableFileAttachments != nil && !*cfg.FileSettings.EnableFileAttachments {
		return errUploadDisabled
	}
	if !client.HasPermissionToChannel(llmCtx.RequestingUser.Id, llmCtx.Channel.Id, model.PermissionUploadFile) {
		return errUploadNoPermission
	}
	return nil
}

// validateUploadFileName sanitizes an LLM- or provider-influenced file name so
// it cannot escape the upload, returning the cleaned base name.
func validateUploadFileName(raw string) (string, error) {
	name := filepath.Base(strings.TrimSpace(raw))
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return "", errUploadFileNameInvalid
	}
	if utf8.RuneCountInString(name) > maxCreateFileNameLength {
		return "", errUploadFileNameTooLong
	}
	return name, nil
}
