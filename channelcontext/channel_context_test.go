// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package channelcontext

import (
	"errors"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeStore struct {
	record        *Record
	getErr        error
	saveErr       error
	deleteErr     error
	saved         *Record
	deleted       string
	saveVersion   int64
	deleteVersion int64
}

func (s *fakeStore) GetChannelContext(string) (*Record, error) {
	if s.getErr != nil || s.record == nil {
		return nil, s.getErr
	}
	clone := *s.record
	clone.FileIDs = append([]string(nil), s.record.FileIDs...)
	return &clone, nil
}

func (s *fakeStore) SaveChannelContext(record *Record, expectedUpdateAt int64) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	clone := *record
	clone.FileIDs = append([]string(nil), record.FileIDs...)
	s.saved = &clone
	s.saveVersion = expectedUpdateAt
	return nil
}

func (s *fakeStore) DeleteChannelContext(channelID string, expectedUpdateAt int64) error {
	if s.deleteErr != nil {
		return s.deleteErr
	}
	s.deleted = channelID
	s.deleteVersion = expectedUpdateAt
	return nil
}

type fakeMMClient struct {
	files    map[string]*model.FileInfo
	errs     map[string]error
	getCalls []string
	warnings int
}

func (m *fakeMMClient) GetFileInfo(fileID string) (*model.FileInfo, error) {
	m.getCalls = append(m.getCalls, fileID)
	return m.files[fileID], m.errs[fileID]
}

func (m *fakeMMClient) LogWarn(string, ...interface{}) {
	m.warnings++
}

func TestSaveChannelContext(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	firstID := model.NewId()
	secondID := model.NewId()
	store := &fakeStore{}
	mm := &fakeMMClient{files: map[string]*model.FileInfo{
		firstID: {
			Id: firstID, ChannelId: channelID, CreatorId: userID,
			Name: "guide.pdf", MimeType: "application/pdf", Size: 1200,
		},
		secondID: {
			Id: secondID, ChannelId: channelID, CreatorId: model.NewId(), PostId: model.NewId(),
			Name: "notes.txt", MimeType: "text/plain", Size: 80,
		},
	}}

	state, err := New(store, mm).Save(channelID, userID, Update{
		CustomInstructions: "Use channel terminology.",
		FileIDs:            []string{secondID, firstID, secondID},
	})
	require.NoError(t, err)

	require.NotNil(t, store.saved)
	assert.Equal(t, []string{secondID, firstID}, store.saved.FileIDs)
	assert.Equal(t, "Use channel terminology.", store.saved.CustomInstructions)
	assert.Equal(t, []KnowledgeFile{
		{ID: secondID, Name: "notes.txt", MimeType: "text/plain", Size: 80},
		{ID: firstID, Name: "guide.pdf", MimeType: "application/pdf", Size: 1200},
	}, state.Files)
}

func TestSaveChannelContextValidation(t *testing.T) {
	channelID := model.NewId()
	userID := model.NewId()
	fileID := model.NewId()

	tests := []struct {
		name      string
		channelID string
		userID    string
		update    Update
		file      *model.FileInfo
		fileErr   error
		want      string
	}{
		{name: "invalid channel", channelID: "bad", userID: userID, want: "invalid channel id"},
		{name: "invalid user", channelID: channelID, userID: "bad", want: "invalid user id"},
		{
			name: "instructions too long", channelID: channelID, userID: userID,
			update: Update{CustomInstructions: strings.Repeat("a", MaxCustomInstructionsRunes+1)},
			want:   "exceeds maximum length",
		},
		{
			name: "too many files", channelID: channelID, userID: userID,
			update: Update{FileIDs: makeIDs(MaxKnowledgeFiles + 1)},
			want:   "cannot contain more than",
		},
		{
			name: "invalid file id", channelID: channelID, userID: userID,
			update: Update{FileIDs: []string{"bad"}},
			want:   "invalid file id",
		},
		{
			name: "missing file", channelID: channelID, userID: userID,
			update: Update{FileIDs: []string{fileID}}, fileErr: errors.New("missing"),
			want: "could not be found",
		},
		{
			name: "deleted file", channelID: channelID, userID: userID,
			update: Update{FileIDs: []string{fileID}},
			file:   &model.FileInfo{Id: fileID, ChannelId: channelID, CreatorId: userID, Name: "a.pdf", DeleteAt: 1},
			want:   "has been deleted",
		},
		{
			name: "wrong channel", channelID: channelID, userID: userID,
			update: Update{FileIDs: []string{fileID}},
			file:   &model.FileInfo{Id: fileID, ChannelId: model.NewId(), CreatorId: userID, Name: "a.pdf"},
			want:   "does not belong",
		},
		{
			name: "unattached file owned by another user", channelID: channelID, userID: userID,
			update: Update{FileIDs: []string{fileID}},
			file:   &model.FileInfo{Id: fileID, ChannelId: channelID, CreatorId: model.NewId(), Name: "a.pdf"},
			want:   "not available",
		},
		{
			name: "unsupported file", channelID: channelID, userID: userID,
			update: Update{FileIDs: []string{fileID}},
			file:   &model.FileInfo{Id: fileID, ChannelId: channelID, CreatorId: userID, Name: "a.zip", MimeType: "application/zip"},
			want:   "unsupported type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &fakeStore{}
			mm := &fakeMMClient{
				files: map[string]*model.FileInfo{fileID: tt.file},
				errs:  map[string]error{fileID: tt.fileErr},
			}
			_, err := New(store, mm).Save(tt.channelID, tt.userID, tt.update)
			require.Error(t, err)
			assert.True(t, IsValidationError(err))
			assert.Contains(t, err.Error(), tt.want)
			assert.Nil(t, store.saved)
			assert.Empty(t, store.deleted)
		})
	}
}

func TestSaveEmptyChannelContextDeletesRecord(t *testing.T) {
	channelID := model.NewId()
	store := &fakeStore{}

	state, err := New(store, &fakeMMClient{}).Save(channelID, model.NewId(), Update{
		CustomInstructions: "  ",
	})
	require.NoError(t, err)
	assert.Equal(t, channelID, store.deleted)
	assert.Empty(t, state.CustomInstructions)
	assert.Empty(t, state.Files)
}

func TestSavePreservesExistingFileUploadedByAnotherManager(t *testing.T) {
	channelID := model.NewId()
	fileID := model.NewId()
	store := &fakeStore{record: &Record{ChannelID: channelID, FileIDs: []string{fileID}, UpdateAt: 42}}
	mm := &fakeMMClient{files: map[string]*model.FileInfo{
		fileID: {
			Id: fileID, ChannelId: channelID, CreatorId: model.NewId(),
			Name: "shared.pdf", MimeType: "application/pdf",
		},
	}}

	state, err := New(store, mm).Save(channelID, model.NewId(), Update{
		CustomInstructions: "Updated by another manager.",
		FileIDs:            []string{fileID},
		Version:            42,
	})

	require.NoError(t, err)
	require.NotNil(t, store.saved)
	assert.Equal(t, []string{fileID}, store.saved.FileIDs)
	assert.Equal(t, int64(42), store.saveVersion)
	assert.Equal(t, []KnowledgeFile{{
		ID: fileID, Name: "shared.pdf", MimeType: "application/pdf",
	}}, state.Files)
}

func TestGetAndPromptContextSkipUnavailableFiles(t *testing.T) {
	channelID := model.NewId()
	validID := model.NewId()
	staleID := model.NewId()
	store := &fakeStore{record: &Record{
		ChannelID:          channelID,
		CustomInstructions: "Prefer the project glossary.",
		FileIDs:            []string{staleID, validID},
	}}
	mm := &fakeMMClient{
		files: map[string]*model.FileInfo{
			validID: {
				Id: validID, ChannelId: channelID, Name: "glossary.docx",
				MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
				Size:     42, Content: "SECRET EXTRACTED TEXT",
			},
		},
		errs: map[string]error{staleID: errors.New("gone")},
	}
	service := New(store, mm)

	state, err := service.Get(channelID)
	require.NoError(t, err)
	assert.Equal(t, "Prefer the project glossary.", state.CustomInstructions)
	assert.Equal(t, []KnowledgeFile{{
		ID: validID, Name: "glossary.docx",
		MimeType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document", Size: 42,
	}}, state.Files)

	promptContext, err := service.GetPromptContext(channelID)
	require.NoError(t, err)
	assert.Equal(t, "Prefer the project glossary.", promptContext.CustomInstructions)
	assert.Contains(t, promptContext.KnowledgeFiles, "glossary.docx")
	assert.Contains(t, promptContext.KnowledgeFiles, validID)
	assert.NotContains(t, promptContext.KnowledgeFiles, "SECRET EXTRACTED TEXT")
	assert.Equal(t, 2, mm.warnings)
}

func TestSaveRejectsStaleVersion(t *testing.T) {
	channelID := model.NewId()
	store := &fakeStore{record: &Record{
		ChannelID: channelID,
		UpdateAt:  2,
	}}

	_, err := New(store, &fakeMMClient{}).Save(channelID, model.NewId(), Update{
		CustomInstructions: "stale edit",
		Version:            1,
	})

	require.ErrorIs(t, err, ErrConflict)
	assert.Nil(t, store.saved)
}

func TestChannelContextStoreErrors(t *testing.T) {
	channelID := model.NewId()
	storeErr := errors.New("database unavailable")
	tests := []struct {
		name  string
		store *fakeStore
		run   func(*Service) error
		want  string
	}{
		{
			name: "get", store: &fakeStore{getErr: storeErr},
			run: func(service *Service) error {
				_, err := service.Get(channelID)
				return err
			},
			want: "failed to load",
		},
		{
			name: "save", store: &fakeStore{saveErr: storeErr},
			run: func(service *Service) error {
				_, err := service.Save(channelID, model.NewId(), Update{CustomInstructions: "context"})
				return err
			},
			want: "failed to save",
		},
		{
			name: "delete", store: &fakeStore{deleteErr: storeErr},
			run: func(service *Service) error {
				_, err := service.Save(channelID, model.NewId(), Update{})
				return err
			},
			want: "failed to delete",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run(New(tt.store, &fakeMMClient{}))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
			assert.ErrorIs(t, err, storeErr)
		})
	}
}

func makeIDs(count int) []string {
	ids := make([]string, count)
	for i := range ids {
		ids[i] = model.NewId()
	}
	return ids
}
