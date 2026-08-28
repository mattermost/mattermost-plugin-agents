// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmtools

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	otelcodes "go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi"
	"github.com/mattermost/mattermost-plugin-agents/v2/telemetry"
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

	if err := uploadPolicyAllowed(client, llmCtx); err != nil {
		client.LogWarn("Not attaching code execution output files", "reason", err.Error(), "files", len(files))
		return
	}

	sizeLimit := createFileContentLimit(client.GetConfig())
	slots := min(llmCtx.ResponseAttachmentSlots(), maxCreatedFilesPerTurn)

	for _, fileRef := range files {
		if len(llmCtx.CreatedFilesList()) >= slots {
			client.LogWarn("Dropping code execution output files beyond the response attachment cap",
				"cap", slots)
			return
		}
		if err := attachOneSandboxFile(ctx, client, downloader, llmCtx, fileRef, sizeLimit); err != nil {
			client.LogError("Failed to attach a code execution output file", "error", err.Error(), "provider_file_id", fileRef.ID)
		}
	}
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
	rejected := func(err error) error {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "sandbox file rejected")
		return err
	}
	file, err := downloader.DownloadProviderFile(downloadCtx, fileRef, sizeLimit)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(otelcodes.Error, "download failed")
		return fmt.Errorf("download failed: %w", err)
	}

	if len(file.Content) == 0 {
		return rejected(errors.New("sandbox file is empty"))
	}
	// The downloader already gates on the metadata size; re-check the actual
	// content in case the provider misreported it.
	if int64(len(file.Content)) > sizeLimit {
		return rejected(fmt.Errorf("sandbox file is %d bytes, over the %d-byte limit", len(file.Content), sizeLimit))
	}

	name, err := validateUploadFileName(file.Name)
	if err != nil {
		return rejected(fmt.Errorf("provider reported an unusable file name: %w", err))
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
