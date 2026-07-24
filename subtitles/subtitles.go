// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package subtitles

import (
	"bufio"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/asticode/go-astisub"
)

type Subtitles struct {
	storage *astisub.Subtitles
}

func readZoomChat(chat io.Reader) (*astisub.Subtitles, error) {
	storage := astisub.NewSubtitles()

	scanner := bufio.NewScanner(chat)
	for scanner.Scan() {
		line := scanner.Text()
		// Zoom chat lines are "HH:MM:SS <text>". Continuation/short lines lack a timestamp.
		if len(line) < 9 {
			continue
		}
		text := line[9:]
		item := &astisub.Item{}
		startAt, err := time.Parse("15:04:05", line[:8])
		if err != nil {
			return nil, err
		}
		zeroTime, err := time.Parse("15:04:05", "00:00:00")
		if err != nil {
			return nil, err
		}
		item.StartAt = startAt.Sub(zeroTime)
		item.EndAt = startAt.Add(5 * time.Second).Sub(zeroTime)
		item.Lines = append(item.Lines, astisub.Line{Items: []astisub.LineItem{{Text: text}}})
		storage.Items = append(storage.Items, item)
	}
	return storage, nil
}

func NewSubtitlesFromZoomChat(chat io.Reader) (*Subtitles, error) {
	storage, err := readZoomChat(chat)
	if err != nil {
		return nil, err
	}
	return &Subtitles{storage: storage}, nil
}

func NewSubtitlesFromVTT(webvtt io.Reader) (*Subtitles, error) {
	storage, err := astisub.ReadFromWebVTT(webvtt)
	if err != nil {
		return nil, err
	}
	return &Subtitles{storage: storage}, nil
}

func (s *Subtitles) WebVTT() io.Reader {
	reader, writer := io.Pipe()
	go func() {
		if err := s.storage.WriteToWebVTT(writer); err != nil {
			writer.CloseWithError(err)
			return
		}
		writer.Close()
	}()
	return reader
}

func (s *Subtitles) FormatForLLM() string {
	var result strings.Builder
	for _, item := range s.storage.Items {
		// Timestamps
		result.WriteString(formatDurationForLLM(item.StartAt))
		result.WriteString(" to ")
		result.WriteString(formatDurationForLLM(item.EndAt))
		result.WriteString(" - ")

		// Words
		result.WriteString(item.String())
		result.WriteString("\n")
	}

	return strings.TrimSpace(result.String())
}

func (s *Subtitles) FormatTextOnly() string {
	var result strings.Builder
	for _, item := range s.storage.Items {
		result.WriteString(item.String())
		result.WriteString(" ")
	}

	return strings.TrimSpace(result.String())
}

func (s *Subtitles) FormatVTT() string {
	var result strings.Builder
	if err := s.storage.WriteToWebVTT(&result); err != nil {
		return fmt.Sprintf("Error formatting VTT: %v", err)
	}
	return result.String()
}

func (s *Subtitles) IsEmpty() bool {
	return s.storage.IsEmpty()
}

func formatDurationForLLM(dur time.Duration) string {
	dur = dur.Round(time.Second)
	hours := dur / time.Hour
	minutes := (dur - hours*time.Hour) / time.Minute
	seconds := (dur - hours*time.Hour - minutes*time.Minute) / time.Second

	if hours == 0 {
		return fmt.Sprintf("%02d:%02d", int(minutes), int(seconds))
	}

	return fmt.Sprintf("%02d:%02d:%02d", int(hours), int(minutes), int(seconds))
}
