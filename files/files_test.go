// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package files

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/mmapi/mocks"
	"github.com/mattermost/mattermost/server/public/model"
)

func TestGetContent(t *testing.T) {
	userID := model.NewId()
	channelID := model.NewId()
	fileID := model.NewId()

	// longText is comfortably larger than MaxReadRunes so cap behavior is exercised.
	longText := strings.Repeat("a", MaxReadRunes+500)

	tests := []struct {
		name      string
		fileID    string
		offset    int
		limit     int
		setup     func(m *mocks.MockClient)
		expectErr error
		assert    func(t *testing.T, c Content)
	}{
		{
			name:      "invalid file id is rejected before any lookup",
			fileID:    "too-short",
			setup:     func(_ *mocks.MockClient) {},
			expectErr: nil, // non-nil generic error; checked below
		},
		{
			name:   "missing channel is forbidden without a permission check",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, ChannelId: ""}, nil)
			},
			expectErr: ErrForbidden,
		},
		{
			name:   "no channel permission is forbidden",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{Id: fileID, ChannelId: channelID}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(false)
			},
			expectErr: ErrForbidden,
		},
		{
			name:   "extracted content returned whole",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, Name: "report.pdf",
					MimeType: "application/pdf", Content: "  hello world  ",
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
			},
			assert: func(t *testing.T, c Content) {
				assert.True(t, c.HasText)
				assert.Equal(t, "hello world", c.Text) // trimmed
				assert.Equal(t, 11, c.TotalRunes)
				assert.Equal(t, 11, c.Returned)
				assert.False(t, c.HasMore)
				assert.Equal(t, "report.pdf", c.Name)
			},
		},
		{
			name:   "ranged read reports more remaining",
			fileID: fileID,
			offset: 0,
			limit:  4,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, MimeType: "application/pdf", Content: "abcdefghij",
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
			},
			assert: func(t *testing.T, c Content) {
				assert.Equal(t, "abcd", c.Text)
				assert.Equal(t, 0, c.Offset)
				assert.Equal(t, 4, c.Returned)
				assert.Equal(t, 10, c.TotalRunes)
				assert.True(t, c.HasMore)
			},
		},
		{
			name:   "offset past content tail returns empty with no more",
			fileID: fileID,
			offset: 8,
			limit:  100,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, MimeType: "application/pdf", Content: "abcdefghij",
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
			},
			assert: func(t *testing.T, c Content) {
				assert.Equal(t, "ij", c.Text)
				assert.Equal(t, 8, c.Offset)
				assert.Equal(t, 2, c.Returned)
				assert.False(t, c.HasMore)
			},
		},
		{
			name:   "limit is capped at MaxReadRunes",
			fileID: fileID,
			offset: 0,
			limit:  1_000_000,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, MimeType: "application/pdf", Content: longText,
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
			},
			assert: func(t *testing.T, c Content) {
				assert.Equal(t, MaxReadRunes, c.Returned)
				assert.True(t, c.HasMore)
				assert.Equal(t, len(longText), c.TotalRunes)
			},
		},
		{
			name:   "text file without extracted content reads raw bytes",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, MimeType: "text/plain", Content: "",
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
				m.EXPECT().GetFile(fileID).Return(io.NopCloser(strings.NewReader("raw file bytes")), nil)
			},
			assert: func(t *testing.T, c Content) {
				assert.True(t, c.HasText)
				assert.Equal(t, "raw file bytes", c.Text)
			},
		},
		{
			name:   "binary without extracted content has no text",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, MimeType: "application/zip", Content: "",
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
			},
			assert: func(t *testing.T, c Content) {
				assert.False(t, c.HasText)
				assert.Empty(t, c.Text)
			},
		},
		{
			name:   "rune offsets do not split multibyte characters",
			fileID: fileID,
			offset: 0,
			limit:  5,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, MimeType: "application/pdf", Content: "héllo wörld 日本語",
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
			},
			assert: func(t *testing.T, c Content) {
				assert.Equal(t, "héllo", c.Text)
				assert.Equal(t, 15, c.TotalRunes)
				assert.True(t, c.HasMore)
			},
		},
		{
			name:   "non-zero offset is rune-indexed not byte-indexed",
			fileID: fileID,
			offset: 7,
			limit:  4,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, MimeType: "application/pdf", Content: "héllo wörld 日本語",
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
			},
			assert: func(t *testing.T, c Content) {
				// Runes [7,11) of "héllo wörld 日本語" are "örld". A byte-based
				// slice would start inside the multibyte 'ö' and differ.
				assert.Equal(t, "örld", c.Text)
				assert.Equal(t, 7, c.Offset)
				assert.Equal(t, 4, c.Returned)
				assert.True(t, c.HasMore)
			},
		},
		{
			name:   "explicit limit landing exactly on the end reports no more",
			fileID: fileID,
			offset: 0,
			limit:  10,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, MimeType: "application/pdf", Content: "abcdefghij",
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
			},
			assert: func(t *testing.T, c Content) {
				assert.Equal(t, "abcdefghij", c.Text)
				assert.Equal(t, 10, c.Returned)
				assert.False(t, c.HasMore, "an explicit limit that consumes the last rune must not report more")
			},
		},
		{
			name:   "negative offset is clamped to zero",
			fileID: fileID,
			offset: -5,
			limit:  4,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, MimeType: "application/pdf", Content: "abcdefghij",
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
			},
			assert: func(t *testing.T, c Content) {
				assert.Equal(t, 0, c.Offset)
				assert.Equal(t, "abcd", c.Text)
				assert.Equal(t, 4, c.Returned)
			},
		},
		{
			name:   "whitespace-only extracted content falls back to raw bytes for text files",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, MimeType: "text/plain", Content: "   \n",
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
				m.EXPECT().GetFile(fileID).Return(io.NopCloser(strings.NewReader("real bytes")), nil)
			},
			assert: func(t *testing.T, c Content) {
				assert.True(t, c.HasText)
				assert.Equal(t, "real bytes", c.Text)
			},
		},
		{
			name:   "whitespace-only extracted content on a binary has no text and no download",
			fileID: fileID,
			setup: func(m *mocks.MockClient) {
				m.EXPECT().GetFileInfo(fileID).Return(&model.FileInfo{
					Id: fileID, ChannelId: channelID, MimeType: "application/zip", Content: "   ",
				}, nil)
				m.EXPECT().HasPermissionToChannel(userID, channelID, model.PermissionReadChannel).Return(true)
				// No GetFile expectation: a binary with whitespace-only extracted
				// content must not trigger a download.
			},
			assert: func(t *testing.T, c Content) {
				assert.False(t, c.HasText)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mocks.NewMockClient(t)
			tt.setup(m)
			svc := New(m)

			c, err := svc.GetContent(context.Background(), userID, tt.fileID, tt.offset, tt.limit)

			switch {
			case tt.name == "invalid file id is rejected before any lookup":
				require.Error(t, err)
			case tt.expectErr != nil:
				require.ErrorIs(t, err, tt.expectErr)
			default:
				require.NoError(t, err)
				tt.assert(t, c)
			}
		})
	}
}
