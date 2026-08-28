// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
	"github.com/mattermost/mattermost/server/public/model"
)

const sandboxOutputFileAttachmentOperation = "attach_sandbox_output_file"

// AttachSandboxOutputFiles uploads sandbox output files captured this turn
// onto llmCtx for the reply. There is no attach tool: Anthropic reports ids
// only for files left in $OUTPUT_DIR, which is the model's share gesture, and
// those ids are never shown to the model. Failures are skipped so a bad file
// cannot fail the reply. ConsumeSandboxFiles makes a repeat call a no-op.
func AttachSandboxOutputFiles(ctx context.Context, client mmapi.Client, downloader llm.ProviderFileDownloader, llmCtx *llm.Context) {
	files := llmCtx.ConsumeSandboxFiles()
	if len(files) == 0 {
		return
	}
	if client == nil || downloader == nil {
		return
	}

	if err := sandboxAttachmentAllowed(client, llmCtx); err != nil {
		client.LogWarn("Not attaching code execution output files", "reason", err.Error(), "files", len(files))
		return
	}

	cfg := client.GetConfig()
	sizeLimit := createFileContentLimit(cfg)

	for _, fileRef := range files {
		slots := min(llmCtx.ResponseAttachmentSlots(), maxCreatedFilesPerTurn)
		if len(llmCtx.CreatedFilesList()) >= slots {
			client.LogWarn("Dropping code execution output files beyond the response attachment cap",
				"cap", maxCreatedFilesPerTurn)
			return
		}
		if err := attachOneSandboxFile(ctx, client, downloader, llmCtx, fileRef, sizeLimit); err != nil {
			client.LogError("Failed to attach a code execution output file", "error", err.Error(), "provider_file_id", fileRef.ID)
		}
	}
}

// sandboxAttachmentAllowed enforces the same policy as CreateFile. UploadFile
// bypasses api4 per-user checks, so this must stay.
func sandboxAttachmentAllowed(client mmapi.Client, llmCtx *llm.Context) error {
	if llmCtx == nil || llmCtx.Channel == nil || llmCtx.Channel.Id == "" {
		return errors.New("no conversation channel to hold the files")
	}
	if llmCtx.RequestingUser == nil || llmCtx.RequestingUser.Id == "" {
		return errors.New("no requesting user")
	}
	cfg := client.GetConfig()
	if cfg != nil && cfg.FileSettings.EnableFileAttachments != nil && !*cfg.FileSettings.EnableFileAttachments {
		return errors.New("file attachments are disabled by server config")
	}
	if !client.HasPermissionToChannel(llmCtx.RequestingUser.Id, llmCtx.Channel.Id, model.PermissionUploadFile) {
		return errors.New("requesting user lacks upload permission in the channel")
	}
	return nil
}

func attachOneSandboxFile(
	ctx context.Context,
	client mmapi.Client,
	downloader llm.ProviderFileDownloader,
	llmCtx *llm.Context,
	fileRef llm.ProviderFileReference,
	sizeLimit int64,
) error {
	spanAttributes := trace.WithAttributes(
		telemetry.ToolName.String(sandboxOutputFileAttachmentOperation),
		telemetry.ChannelID.String(llmCtx.Channel.Id),
		telemetry.UserID.String(llmCtx.RequestingUser.Id),
	)
	downloadCtx, span := telemetry.Tracer().Start(ctx, "download sandbox output file", spanAttributes)
	defer span.End()
	file, err := downloader.DownloadProviderFile(downloadCtx, fileRef)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "download failed")
		return fmt.Errorf("download failed: %w", err)
	}

	if len(file.Content) == 0 {
		return errors.New("sandbox file is empty")
	}
	if int64(len(file.Content)) > sizeLimit {
		return fmt.Errorf("sandbox file is %d bytes, over the %d-byte limit", len(file.Content), sizeLimit)
	}

	name, err := sandboxFileName(file.Name)
	if err != nil {
		return err
	}

	_, uploadSpan := telemetry.Tracer().Start(ctx, "upload sandbox output file", spanAttributes)
	defer uploadSpan.End()
	info, err := client.UploadFile(bytes.NewReader(file.Content), name, llmCtx.Channel.Id)
	if err != nil {
		uploadSpan.RecordError(err)
		uploadSpan.SetStatus(otelcodes.Error, "upload failed")
		return fmt.Errorf("upload failed: %w", err)
	}

	llmCtx.AddCreatedFile(llm.CreatedFile{ID: info.Id, Name: info.Name})
	return nil
}

// sandboxFileName sanitizes a model-influenced provider name so it cannot escape the upload.
func sandboxFileName(providerName string) (string, error) {
	name := filepath.Base(strings.TrimSpace(providerName))
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) || strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("provider reported an unusable file name")
	}
	if utf8.RuneCountInString(name) > maxCreateFileNameLength {
		return "", fmt.Errorf("provider file name is longer than %d characters", maxCreateFileNameLength)
	}
	return name, nil
}
