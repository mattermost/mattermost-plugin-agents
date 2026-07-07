// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/files"
	"github.com/mattermost/mattermost/server/public/model"
)

type fakeFileContentService struct {
	content files.Content
	err     error

	gotUserID string
	gotFileID string
	gotOffset int
	gotLimit  int
}

func (f *fakeFileContentService) GetContent(_ context.Context, userID, fileID string, offset, limit int) (files.Content, error) {
	f.gotUserID = userID
	f.gotFileID = fileID
	f.gotOffset = offset
	f.gotLimit = limit
	return f.content, f.err
}

func TestToolReadFile(t *testing.T) {
	validID := model.NewId()

	tests := []struct {
		name            string
		fileID          string
		service         FileContentService
		wantErr         bool
		wantResult      string   // exact match when set
		wantContains    []string // substring matches
		wantErrContains string   // substring match against err on error paths
	}{
		{
			name:            "invalid file id",
			fileID:          "too-short",
			service:         &fakeFileContentService{},
			wantErr:         true,
			wantErrContains: "file_id must be a valid ID",
		},
		{
			name:       "service unavailable",
			fileID:     validID,
			service:    nil,
			wantResult: "file reading is not available",
		},
		{
			name:       "forbidden",
			fileID:     validID,
			service:    &fakeFileContentService{err: files.ErrForbidden},
			wantResult: "you do not have permission to read this file",
		},
		{
			name:   "success with paging instructions",
			fileID: validID,
			service: &fakeFileContentService{content: files.Content{
				Name: "report.pdf", MimeType: "application/pdf",
				TotalRunes: 100, Offset: 0, Returned: 6, HasMore: true, HasText: true,
				Text: "abcdef",
			}},
			wantResult: "File: report.pdf (application/pdf)\n" +
				"Showing characters 0-6 of 100. More content remains; call read_file again with offset=6 to continue.\n\n" +
				"abcdef",
		},
		{
			name:   "success without more content omits the paging hint",
			fileID: validID,
			service: &fakeFileContentService{content: files.Content{
				Name: "report.pdf", MimeType: "application/pdf",
				TotalRunes: 6, Offset: 0, Returned: 6, HasMore: false, HasText: true,
				Text: "abcdef",
			}},
			wantResult: "File: report.pdf (application/pdf)\nShowing characters 0-6 of 6.\n\nabcdef",
		},
		{
			name:   "success without a mime type omits the parenthetical",
			fileID: validID,
			service: &fakeFileContentService{content: files.Content{
				Name: "notes", MimeType: "", TotalRunes: 2, Offset: 0, Returned: 2, HasText: true, Text: "hi",
			}},
			wantResult: "File: notes\nShowing characters 0-2 of 2.\n\nhi",
		},
		{
			name:   "no extractable text",
			fileID: validID,
			service: &fakeFileContentService{content: files.Content{
				Name: "archive.zip", MimeType: "application/zip", HasText: false,
			}},
			wantResult: `File "archive.zip" (application/zip) has no extractable text content and cannot be read as text.`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &MattermostToolProvider{fileContentService: tt.service}
			ctx := &MCPToolContext{Ctx: context.Background(), UserID: model.NewId()}

			result, err := p.toolReadFile(ctx, ReadFileArgs{FileID: tt.fileID})

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tt.wantErrContains != "" {
				assert.Contains(t, err.Error(), tt.wantErrContains)
			}
			if tt.wantResult != "" {
				assert.Equal(t, tt.wantResult, result)
			}
			for _, sub := range tt.wantContains {
				assert.Contains(t, result, sub)
			}
		})
	}
}

// TestToolReadFilePassesRequestingUser pins the permission-relevant contract:
// the read flows the authenticated user's ID and the requested range through to
// the content service, which is what enforces channel access.
func TestToolReadFilePassesRequestingUser(t *testing.T) {
	fake := &fakeFileContentService{content: files.Content{Name: "a.txt", HasText: true, Text: "hi"}}
	p := &MattermostToolProvider{fileContentService: fake}

	userID := model.NewId()
	fileID := model.NewId()
	ctx := &MCPToolContext{Ctx: context.Background(), UserID: userID}

	_, err := p.toolReadFile(ctx, ReadFileArgs{FileID: fileID, Offset: 12, Limit: 34})
	require.NoError(t, err)

	assert.Equal(t, userID, fake.gotUserID)
	assert.Equal(t, fileID, fake.gotFileID)
	assert.Equal(t, 12, fake.gotOffset)
	assert.Equal(t, 34, fake.gotLimit)
}
