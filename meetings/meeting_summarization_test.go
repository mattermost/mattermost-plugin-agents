// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package meetings

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/accesscontrol"
	"github.com/mattermost/mattermost-plugin-agents/v2/bots"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise"
	"github.com/mattermost/mattermost-plugin-agents/v2/enterprise/enterprisetest"
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmapi/mocks"
)

const (
	createTranscriptionFileID = "recording-file"
	createTranscriptionText   = "Noted the meeting."
	// createTranscriptionFormats is the input format list passed to ffmpeg.
	createTranscriptionFormats = "mov,matroska,ogg,mp3,wav,flac,aac"
	createTranscriptionVTT     = "WEBVTT\n\n1\n00:00:00.000 --> 00:00:02.000\nNoted the meeting.\n"
)

// transcriptionBotsConfig resolves the transcript bot by user id.
type transcriptionBotsConfig struct {
	userID string
}

func (c transcriptionBotsConfig) GetBots() []llm.BotConfig { return nil }
func (c transcriptionBotsConfig) GetServiceByID(string) (llm.ServiceConfig, bool) {
	return llm.ServiceConfig{}, false
}
func (c transcriptionBotsConfig) GetServices() []llm.ServiceConfig { return nil }
func (c transcriptionBotsConfig) GetDefaultBotName() string        { return "" }
func (c transcriptionBotsConfig) EnableTokenUsageLogging() bool    { return false }
func (c transcriptionBotsConfig) EnableTokenUsageLogToPlugin() bool {
	return false
}
func (c transcriptionBotsConfig) EnableTokenUsageLogToFile() bool { return false }
func (c transcriptionBotsConfig) GetTranscriptGenerator() string  { return "" }
func (c transcriptionBotsConfig) GetTranscriptBotUserID() string  { return c.userID }

func allowPluginLogs(api *plugintest.API) {
	for _, method := range []string{"LogDebug", "LogInfo", "LogWarn", "LogError"} {
		for arity := 1; arity <= 8; arity++ {
			args := make([]any, arity)
			for i := range args {
				args[i] = mock.Anything
			}
			api.On(method, args...).Return().Maybe()
		}
	}
}

// ffmpegArgvRecorder returns a path that records the arguments of the process
// createTranscription starts, and the executable path to assign to ffmpegPath.
// The process reads its stdin to completion, records those arguments, writes a
// short stdout, and exits 0.
func ffmpegArgvRecorder(t *testing.T) (argvPath, ffmpegPath string) {
	t.Helper()
	dir := t.TempDir()
	argvPath = filepath.Join(dir, "argv")
	ffmpegPath = filepath.Join(dir, "ffmpeg")
	quoted := "'" + strings.ReplaceAll(argvPath, "'", `'\''`) + "'"
	script := "#!/bin/sh\ncat >/dev/null || exit 1\nprintf '%s\\n' \"$@\" > " + quoted + "\nprintf 'audio'\nexit 0\n"
	require.NoError(t, os.WriteFile(ffmpegPath, []byte(script), 0o755))
	return argvPath, ffmpegPath
}

func readFFmpegArgs(t *testing.T, argvPath string) []string {
	t.Helper()
	data, err := os.ReadFile(argvPath)
	require.NoError(t, err, "ffmpeg arguments were recorded")
	text := strings.TrimRight(string(data), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func expectedCreateTranscriptionArgs(recompress bool) []string {
	args := []string{
		"-protocol_whitelist", "pipe",
		"-format_whitelist", createTranscriptionFormats,
		"-i", "pipe:0",
	}
	if recompress {
		args = append(args, "-ac", "1", "-map", "0:a:0", "-b:a", "32k", "-ar", "16000")
	}
	return append(args, "-f", "mp3", "pipe:1")
}

func newRecordingClient(t *testing.T, size int64, body string) *mocks.MockClient {
	t.Helper()
	mm := mocks.NewMockClient(t)
	mm.On("GetFileInfo", createTranscriptionFileID).Return(&model.FileInfo{Size: size}, nil).Once()
	mm.On("GetFile", createTranscriptionFileID).Return(io.NopCloser(strings.NewReader(body)), nil).Once()
	return mm
}

// newCreateTranscriptionService builds a service whose transcription call reaches
// the configured executable and returns the fixed subtitle text.
func newCreateTranscriptionService(t *testing.T, ffmpegPath, apiURL string) *Service {
	t.Helper()
	mockAPI := &plugintest.API{}
	allowPluginLogs(mockAPI)
	client := pluginapi.NewClient(mockAPI, nil)
	cfg := transcriptionBotsConfig{userID: "transcriber-bot-user"}
	mmBots := bots.New(
		mockAPI,
		client,
		enterprisetest.CheckerAt(enterprise.LevelEnterprise),
		cfg,
		nil,
		accesscontrol.New(accesscontrol.PassthroughClient{}, nil, accesscontrol.NoMCPServerIDs, nil),
		&http.Client{},
		nil,
	)
	mmBots.SetBotsForTesting([]*bots.Bot{
		bots.NewBot(
			llm.BotConfig{Name: "transcriber"},
			llm.ServiceConfig{
				Type:   llm.ServiceTypeOpenAI,
				APIKey: "test-key",
				APIURL: apiURL,
			},
			&model.Bot{UserId: cfg.userID, Username: "transcriber"},
			nil,
		),
	})
	return &Service{
		bots:       mmBots,
		ffmpegPath: ffmpegPath,
	}
}

func transcriptionServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, createTranscriptionVTT)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestCreateTranscriptionInputOptions checks the arguments passed when reading
// a recording. Each file size includes the input options, with the protocol
// option and its value before -i, and keeps that branch's output arguments.
// The same arguments are used for a zero-length file, an empty reader, and
// bytes that are not an audio header.
func TestCreateTranscriptionInputOptions(t *testing.T) {
	srv := transcriptionServer(t)
	cases := []struct {
		name string
		size int64
		body string
	}{
		{name: "size at limit", size: WhisperAPILimit, body: "recording"},
		{name: "size over limit", size: WhisperAPILimit + 1, body: "recording"},
		{name: "size zero", size: 0, body: "recording"},
		{name: "empty reader", size: WhisperAPILimit + 1, body: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argvPath, ffmpegPath := ffmpegArgvRecorder(t)
			s := newCreateTranscriptionService(t, ffmpegPath, srv.URL)
			// The argument check stands on its own if transcription returns an error.
			_, _ = s.createTranscription(newRecordingClient(t, tc.size, tc.body), createTranscriptionFileID)
			got := readFFmpegArgs(t, argvPath)
			require.Equal(t, expectedCreateTranscriptionArgs(tc.size > WhisperAPILimit), got)
		})
	}
}

// TestCreateTranscriptionReturnsResult checks that ffmpeg is started and the
// transcription result is returned for both file sizes.
func TestCreateTranscriptionReturnsResult(t *testing.T) {
	srv := transcriptionServer(t)
	cases := []struct {
		name string
		size int64
	}{
		{name: "size at limit", size: WhisperAPILimit},
		{name: "size over limit", size: WhisperAPILimit + 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argvPath, ffmpegPath := ffmpegArgvRecorder(t)
			s := newCreateTranscriptionService(t, ffmpegPath, srv.URL)
			got, err := s.createTranscription(newRecordingClient(t, tc.size, "recording"), createTranscriptionFileID)
			require.NoError(t, err)
			require.NotNil(t, got)
			require.Equal(t, createTranscriptionText, got.FormatTextOnly())
			require.NotEmpty(t, readFFmpegArgs(t, argvPath))
		})
	}
}
