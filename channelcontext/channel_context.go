// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package channelcontext manages per-channel AI instructions and knowledge files.
package channelcontext

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/mattermost/mattermost-plugin-agents/v2/format"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost/server/public/model"
)

const (
	MaxCustomInstructionsRunes = llm.MaxCustomInstructionsRunes
	MaxKnowledgeFiles          = 10
)

// Record is the database representation of one channel's context.
type Record struct {
	ChannelID          string
	CustomInstructions string
	FileIDs            []string
	CreateAt           int64
	UpdateAt           int64
}

// Update is the editable portion of a channel context.
type Update struct {
	CustomInstructions string   `json:"customInstructions"`
	FileIDs            []string `json:"fileIDs"`
}

// KnowledgeFile is safe file metadata returned to the webapp.
type KnowledgeFile struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

// State is the resolved channel context returned to the webapp.
type State struct {
	CustomInstructions string          `json:"customInstructions"`
	Files              []KnowledgeFile `json:"files"`
}

// Store persists channel-context records.
type Store interface {
	GetChannelContext(channelID string) (*Record, error)
	SaveChannelContext(record *Record) error
	DeleteChannelContext(channelID string) error
}

// MattermostClient provides the file operations needed by the service.
type MattermostClient interface {
	GetFileInfo(fileID string) (*model.FileInfo, error)
	LogWarn(msg string, keyValuePairs ...interface{})
}

// ValidationError reports an invalid channel-context update.
type ValidationError struct {
	message string
}

func (e *ValidationError) Error() string {
	return e.message
}

// IsValidationError reports whether err is safe to return as a bad request.
func IsValidationError(err error) bool {
	var target *ValidationError
	return errors.As(err, &target)
}

func invalid(format string, args ...any) error {
	return &ValidationError{message: fmt.Sprintf(format, args...)}
}

// Service validates, stores, and resolves channel context.
type Service struct {
	store Store
	mm    MattermostClient
}

// New creates a channel-context service.
func New(store Store, mm MattermostClient) *Service {
	return &Service{store: store, mm: mm}
}

// Get returns the current context and resolvable file metadata for channelID.
func (s *Service) Get(channelID string) (State, error) {
	if !model.IsValidId(channelID) {
		return State{}, invalid("invalid channel id")
	}

	record, err := s.store.GetChannelContext(channelID)
	if err != nil {
		return State{}, fmt.Errorf("failed to load channel context: %w", err)
	}
	if record == nil {
		return State{Files: []KnowledgeFile{}}, nil
	}

	return State{
		CustomInstructions: record.CustomInstructions,
		Files:              s.resolveFiles(record, true),
	}, nil
}

// Save validates and replaces a channel's context.
func (s *Service) Save(channelID, userID string, update Update) (State, error) {
	if !model.IsValidId(channelID) {
		return State{}, invalid("invalid channel id")
	}
	if !model.IsValidId(userID) {
		return State{}, invalid("invalid user id")
	}
	if utf8.RuneCountInString(update.CustomInstructions) > MaxCustomInstructionsRunes {
		return State{}, invalid("customInstructions exceeds maximum length of %d characters", MaxCustomInstructionsRunes)
	}

	fileIDs := uniqueFileIDs(update.FileIDs)
	if len(fileIDs) > MaxKnowledgeFiles {
		return State{}, invalid("fileIDs cannot contain more than %d files", MaxKnowledgeFiles)
	}

	files := make([]KnowledgeFile, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		if !model.IsValidId(fileID) {
			return State{}, invalid("invalid file id %q", fileID)
		}

		info, err := s.mm.GetFileInfo(fileID)
		if err != nil || info == nil {
			return State{}, invalid("file %q could not be found", fileID)
		}
		if info.DeleteAt != 0 {
			return State{}, invalid("file %q has been deleted", fileID)
		}
		if info.ChannelId != channelID {
			return State{}, invalid("file %q does not belong to this channel", fileID)
		}
		if info.PostId == "" && info.CreatorId != userID {
			return State{}, invalid("file %q is not available to this user", fileID)
		}
		if !isSupportedKnowledgeFile(info) {
			return State{}, invalid("file %q has an unsupported type", fileID)
		}

		files = append(files, knowledgeFile(info))
	}

	record := &Record{
		ChannelID:          channelID,
		CustomInstructions: update.CustomInstructions,
		FileIDs:            fileIDs,
	}
	if strings.TrimSpace(record.CustomInstructions) == "" && len(record.FileIDs) == 0 {
		if err := s.store.DeleteChannelContext(channelID); err != nil {
			return State{}, fmt.Errorf("failed to delete channel context: %w", err)
		}
		return State{Files: []KnowledgeFile{}}, nil
	}

	if err := s.store.SaveChannelContext(record); err != nil {
		return State{}, fmt.Errorf("failed to save channel context: %w", err)
	}

	return State{
		CustomInstructions: record.CustomInstructions,
		Files:              files,
	}, nil
}

// GetPromptContext returns channel context formatted for prompt rendering.
func (s *Service) GetPromptContext(channelID string) (llm.ChannelContext, error) {
	if !model.IsValidId(channelID) {
		return llm.ChannelContext{}, invalid("invalid channel id")
	}

	record, err := s.store.GetChannelContext(channelID)
	if err != nil {
		return llm.ChannelContext{}, fmt.Errorf("failed to load channel context: %w", err)
	}
	if record == nil {
		return llm.ChannelContext{}, nil
	}

	var descriptors strings.Builder
	validIndex := 0
	for _, fileID := range record.FileIDs {
		info, fileErr := s.mm.GetFileInfo(fileID)
		if fileErr != nil || info == nil || info.DeleteAt != 0 || info.ChannelId != channelID || !isSupportedKnowledgeFile(info) {
			s.mm.LogWarn("Skipping unavailable channel knowledge file", "channel_id", channelID, "file_id", fileID, "error", fileErr)
			continue
		}
		validIndex++
		format.WriteFileDescriptor(&descriptors, format.FileDescriptorEntry{
			Number:   validIndex,
			FileInfo: info,
		})
	}

	return llm.ChannelContext{
		CustomInstructions: record.CustomInstructions,
		KnowledgeFiles:     strings.TrimSpace(descriptors.String()),
	}, nil
}

func (s *Service) resolveFiles(record *Record, logUnavailable bool) []KnowledgeFile {
	files := make([]KnowledgeFile, 0, len(record.FileIDs))
	for _, fileID := range record.FileIDs {
		info, err := s.mm.GetFileInfo(fileID)
		if err != nil || info == nil || info.DeleteAt != 0 || info.ChannelId != record.ChannelID || !isSupportedKnowledgeFile(info) {
			if logUnavailable {
				s.mm.LogWarn("Skipping unavailable channel knowledge file", "channel_id", record.ChannelID, "file_id", fileID, "error", err)
			}
			continue
		}
		files = append(files, knowledgeFile(info))
	}
	return files
}

func uniqueFileIDs(fileIDs []string) []string {
	if len(fileIDs) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(fileIDs))
	unique := make([]string, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		if _, exists := seen[fileID]; exists {
			continue
		}
		seen[fileID] = struct{}{}
		unique = append(unique, fileID)
	}
	return unique
}

func knowledgeFile(info *model.FileInfo) KnowledgeFile {
	return KnowledgeFile{
		ID:       info.Id,
		Name:     info.Name,
		MimeType: info.MimeType,
		Size:     info.Size,
	}
}

func isSupportedKnowledgeFile(info *model.FileInfo) bool {
	mimeType := strings.ToLower(strings.TrimSpace(strings.Split(info.MimeType, ";")[0]))
	if strings.HasPrefix(mimeType, "text/") {
		return true
	}

	switch mimeType {
	case "application/pdf",
		"application/msword",
		"application/vnd.ms-excel",
		"application/vnd.ms-powerpoint",
		"application/rtf",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return true
	}

	switch strings.ToLower(path.Ext(info.Name)) {
	case ".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".txt", ".md", ".csv", ".rtf":
		return true
	default:
		return false
	}
}
