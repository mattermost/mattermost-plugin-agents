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

// AttachSandboxOutputFiles materializes the files a provider code-execution
// sandbox captured this turn as Mattermost files, recording them on llmCtx so
// the response flow attaches them to the bot's reply.
//
// There is no tool for this and no model-supplied file id. A provider captures
// exactly the files the sandbox command left in its output directory
// ($OUTPUT_DIR for Anthropic) and reports their ids in the execution result;
// copying a file there is already the model's "share this" gesture, and the ids
// themselves are never visible to the model. So the ids come only from observed
// server-tool activity, which also means nothing the model says can name a file
// outside this turn's sandbox output.
//
// Files are consumed from llmCtx, making repeat calls for one turn a no-op.
// Individual failures are logged and skipped: a file that cannot be downloaded
// or uploaded must not fail the reply it was going to ride along with.
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
		// Recheck each iteration: every successful attach consumes a slot.
		slots := min(llmCtx.ResponseAttachmentSlots(), maxCreatedFilesPerTurn)
		if len(llmCtx.CreatedFilesList()) >= slots {
			client.LogWarn("Dropping code execution output files beyond the response attachment cap",
				"cap", maxCreatedFilesPerTurn)
			return
		}
		if err := attachOneSandboxFile(ctx, client, downloader, llmCtx, fileRef, sizeLimit); err != nil {
			// The provider file id is a request-scoped handle, not content.
			client.LogError("Failed to attach a code execution output file", "error", err.Error(), "provider_file_id", fileRef.ID)
		}
	}
}

// sandboxAttachmentAllowed reports why the turn's sandbox files cannot be
// attached, or nil when they can. UploadFile bypasses the api4 per-user checks,
// so server attachment policy and the requesting user's channel permission are
// enforced here — the same policy CreateFile applies.
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
	file, err := downloader.DownloadProviderFile(downloadCtx, fileRef)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "download failed")
		span.End()
		return fmt.Errorf("download failed: %w", err)
	}
	span.End()

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
	info, err := client.UploadFile(bytes.NewReader(file.Content), name, llmCtx.Channel.Id)
	if err != nil {
		uploadSpan.RecordError(err)
		uploadSpan.SetStatus(otelcodes.Error, "upload failed")
		uploadSpan.End()
		return fmt.Errorf("upload failed: %w", err)
	}
	uploadSpan.End()

	llmCtx.AddCreatedFile(llm.CreatedFile{ID: info.Id, Name: info.Name})
	return nil
}

// sandboxFileName sanitizes the provider-reported name. The sandbox command
// that wrote the file is model-authored, so the name is model-influenced input
// and must not be able to escape the upload's own naming.
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
