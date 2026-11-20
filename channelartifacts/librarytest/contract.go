// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Package librarytest is the contract every channelartifacts.Library backend
// has to pass. A backend runs it from its own test with a harness that wires a
// fresh library; the HTTP API, agent tools and MCP tools rely only on what is
// asserted here.
package librarytest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mattermost/mattermost-plugin-agents/v2/channelartifacts"
	"github.com/mattermost/mattermost/server/public/model"
)

// Harness is one fresh library and the people who use it.
type Harness struct {
	Library channelartifacts.Library

	// Channel is where Writer, Reader and Unattended act; OtherChannel is a
	// second channel Writer may use.
	Channel      string
	OtherChannel string

	// Writer may read and change the channel's library and has a session.
	Writer channelartifacts.Actor
	// Unattended is Writer's user acting as an agent without a session.
	Unattended channelartifacts.Actor
	// Reader may read the channel but not change its library.
	Reader channelartifacts.Actor
	// Outsider cannot read the channel.
	Outsider channelartifacts.Actor

	// MaxSize and MaxPerChannel are the limits the library was built with.
	MaxSize       int64
	MaxPerChannel int
}

var counter atomic.Int64

func testContext() context.Context { return context.Background() }

func uniqueBody(format channelartifacts.Format) []byte {
	n := counter.Add(1)
	if format == channelartifacts.FormatMarkdown {
		return []byte(fmt.Sprintf("# Document %d\n\nBody.\n", n))
	}
	return []byte(fmt.Sprintf("<html><body>document %d</body></html>", n))
}

func write(h *Harness, actor channelartifacts.Actor, channel, title string, format channelartifacts.Format, body []byte) (*channelartifacts.Entry, error) {
	return h.Library.Write(testContext(), actor, channelartifacts.WriteRequest{
		ChannelID: channel, Title: title, Format: format,
		Body: body,
	})
}

// Run executes the contract.
func Run(t *testing.T, newHarness func(t *testing.T) *Harness) {
	t.Helper()

	t.Run("a written document reads back in its format", func(t *testing.T) {
		for _, format := range []channelartifacts.Format{channelartifacts.FormatHTML, channelartifacts.FormatMarkdown} {
			t.Run(string(format), func(t *testing.T) {
				h := newHarness(t)
				body := uniqueBody(format)
				entry, err := write(h, h.Unattended, h.Channel, "Doc", format, body)
				require.NoError(t, err, "agents write without a session")
				assert.Equal(t, format, entry.Format)
				assert.Equal(t, 1, entry.Version)
				assert.Equal(t, "Doc", entry.Title)
				assert.Equal(t, h.Channel, entry.ChannelID)
				assert.True(t, entry.Available)

				got, err := h.Library.Get(testContext(), h.Writer, entry.ID)
				require.NoError(t, err)
				assert.Equal(t, entry.ID, got.ID)

				content, err := h.Library.Read(testContext(), h.Writer, entry.ID, channelartifacts.ReadOptions{})
				require.NoError(t, err)
				assert.Equal(t, string(body), string(content.Body))
				assert.Equal(t, 1, content.Entry.Version)
				assert.Equal(t, format, content.Entry.Format)

				list, err := h.Library.List(testContext(), h.Reader, h.Channel)
				require.NoError(t, err)
				require.Len(t, list.Entries, 1)
				assert.Equal(t, entry.ID, list.Entries[0].ID)
				assert.False(t, list.CanAdd || list.CanEdit || list.CanDelete, "a reader can change nothing")
				assert.Equal(t, h.MaxPerChannel, list.MaxPerChannel)

				writerList, err := h.Library.List(testContext(), h.Writer, h.Channel)
				require.NoError(t, err)
				assert.True(t, writerList.CanAdd && writerList.CanEdit && writerList.CanDelete)
			})
		}
	})

	t.Run("a new version keeps the old ones readable", func(t *testing.T) {
		h := newHarness(t)
		first := uniqueBody(channelartifacts.FormatMarkdown)
		entry, err := write(h, h.Writer, h.Channel, "Charter", channelartifacts.FormatMarkdown, first)
		require.NoError(t, err)

		second := uniqueBody(channelartifacts.FormatMarkdown)
		updated, err := h.Library.Write(testContext(), h.Unattended, channelartifacts.WriteRequest{
			ChannelID: h.Channel, ArtifactID: entry.ID,
			Body: second,
		})
		require.NoError(t, err)
		assert.Equal(t, entry.ID, updated.ID)
		assert.Equal(t, 2, updated.Version)
		assert.Equal(t, "Charter", updated.Title, "a version without a title keeps the title")
		assert.Equal(t, channelartifacts.FormatMarkdown, updated.Format)

		versions, err := h.Library.Versions(testContext(), h.Reader, entry.ID)
		require.NoError(t, err)
		require.Len(t, versions, 2)
		assert.Equal(t, []int{2, 1}, []int{versions[0].Version, versions[1].Version}, "newest first")
		assert.True(t, versions[0].Head)
		assert.False(t, versions[1].Head)

		current, err := h.Library.Read(testContext(), h.Writer, entry.ID, channelartifacts.ReadOptions{})
		require.NoError(t, err)
		assert.Equal(t, string(second), string(current.Body))

		old, err := h.Library.Read(testContext(), h.Writer, entry.ID, channelartifacts.ReadOptions{Version: 1})
		require.NoError(t, err)
		assert.Equal(t, string(first), string(old.Body))
		assert.Equal(t, 1, old.Entry.Version, "metadata beside a version is that version's")
		assert.Equal(t, 1, old.Version.Version)

		_, err = h.Library.Read(testContext(), h.Writer, entry.ID, channelartifacts.ReadOptions{Version: 9})
		assert.ErrorIs(t, err, channelartifacts.ErrNotFound)
		_, err = h.Library.Read(testContext(), h.Writer, entry.ID, channelartifacts.ReadOptions{Version: -1})
		assert.ErrorIs(t, err, channelartifacts.ErrInvalidInput)
	})

	t.Run("an artifact keeps its format", func(t *testing.T) {
		h := newHarness(t)
		entry, err := write(h, h.Writer, h.Channel, "Charter", channelartifacts.FormatMarkdown, uniqueBody(channelartifacts.FormatMarkdown))
		require.NoError(t, err)
		_, err = h.Library.Write(testContext(), h.Writer, channelartifacts.WriteRequest{
			ChannelID: h.Channel, ArtifactID: entry.ID, Format: channelartifacts.FormatHTML,
			Body: uniqueBody(channelartifacts.FormatHTML),
		})
		assert.ErrorIs(t, err, channelartifacts.ErrInvalidInput)
	})

	t.Run("identical content is refused with the artifact that has it", func(t *testing.T) {
		h := newHarness(t)
		body := uniqueBody(channelartifacts.FormatHTML)
		entry, err := write(h, h.Writer, h.Channel, "One", channelartifacts.FormatHTML, body)
		require.NoError(t, err)

		_, err = write(h, h.Writer, h.Channel, "Two", channelartifacts.FormatHTML, body)
		require.ErrorIs(t, err, channelartifacts.ErrDuplicate)
		dup, ok := errors.AsType[*channelartifacts.DuplicateError](err)
		require.True(t, ok)
		assert.Equal(t, entry.ID, dup.ArtifactID)
	})

	t.Run("who may do what", func(t *testing.T) {
		h := newHarness(t)
		entry, err := write(h, h.Writer, h.Channel, "Doc", channelartifacts.FormatHTML, uniqueBody(channelartifacts.FormatHTML))
		require.NoError(t, err)

		ctx := testContext()
		fresh := func() channelartifacts.WriteRequest {
			return channelartifacts.WriteRequest{
				ChannelID: h.Channel, Title: "x",
				Body: uniqueBody(channelartifacts.FormatHTML),
			}
		}
		tests := []struct {
			name string
			call func() error
			want error
		}{
			{"reader cannot write", func() error { _, e := h.Library.Write(ctx, h.Reader, fresh()); return e }, channelartifacts.ErrForbidden},
			{"reader cannot remove", func() error { _, e := h.Library.Remove(ctx, h.Reader, entry.ID); return e }, channelartifacts.ErrForbidden},
			{"outsider cannot write", func() error { _, e := h.Library.Write(ctx, h.Outsider, fresh()); return e }, channelartifacts.ErrNotFound},
			{"outsider cannot list", func() error { _, e := h.Library.List(ctx, h.Outsider, h.Channel); return e }, channelartifacts.ErrNotFound},
			{"outsider cannot get", func() error { _, e := h.Library.Get(ctx, h.Outsider, entry.ID); return e }, channelartifacts.ErrNotFound},
			{"outsider cannot read", func() error {
				_, e := h.Library.Read(ctx, h.Outsider, entry.ID, channelartifacts.ReadOptions{})
				return e
			}, channelartifacts.ErrNotFound},
			{"outsider cannot read metadata", func() error {
				_, e := h.Library.Read(ctx, h.Outsider, entry.ID, channelartifacts.ReadOptions{MetadataOnly: true})
				return e
			}, channelartifacts.ErrNotFound},
			{"outsider cannot list versions", func() error { _, e := h.Library.Versions(ctx, h.Outsider, entry.ID); return e }, channelartifacts.ErrNotFound},
			{"outsider cannot remove", func() error { _, e := h.Library.Remove(ctx, h.Outsider, entry.ID); return e }, channelartifacts.ErrNotFound},
			{"nobody writes as a user who is not an agent", func() error {
				_, e := h.Library.Write(ctx, channelartifacts.Actor{UserID: h.Writer.UserID, SessionID: h.Writer.SessionID}, fresh())
				return e
			}, channelartifacts.ErrForbidden},
			{"an artifact cannot be replaced from another channel", func() error {
				req := fresh()
				req.ChannelID, req.ArtifactID = h.OtherChannel, entry.ID
				_, e := h.Library.Write(ctx, h.Writer, req)
				return e
			}, channelartifacts.ErrNotFound},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				require.ErrorIs(t, tt.call(), tt.want)
			})
		}

		list, err := h.Library.List(ctx, h.Writer, h.Channel)
		require.NoError(t, err)
		assert.Len(t, list.Entries, 1, "refused calls change nothing")
	})

	t.Run("reading content needs a session", func(t *testing.T) {
		h := newHarness(t)
		entry, err := write(h, h.Unattended, h.Channel, "Doc", channelartifacts.FormatHTML, uniqueBody(channelartifacts.FormatHTML))
		require.NoError(t, err)

		_, err = h.Library.Read(testContext(), h.Unattended, entry.ID, channelartifacts.ReadOptions{})
		assert.ErrorIs(t, err, channelartifacts.ErrSessionRequired)

		meta, err := h.Library.Read(testContext(), h.Unattended, entry.ID, channelartifacts.ReadOptions{MetadataOnly: true})
		require.NoError(t, err)
		assert.Empty(t, meta.Body)
		assert.Equal(t, entry.SHA256, meta.Entry.SHA256)

		_, err = h.Library.Write(testContext(), h.Unattended, channelartifacts.WriteRequest{
			ChannelID: h.Channel, PostID: model.NewId(), FileID: model.NewId(),
		})
		assert.ErrorIs(t, err, channelartifacts.ErrSessionRequired, "a file reference is read under a session's file policy")
	})

	t.Run("invalid writes", func(t *testing.T) {
		h := newHarness(t)
		tests := []struct {
			name string
			req  channelartifacts.WriteRequest
			want error
		}{
			{"no source", channelartifacts.WriteRequest{ChannelID: h.Channel, Title: "x"}, channelartifacts.ErrInvalidInput},
			{"a body and a file", channelartifacts.WriteRequest{
				ChannelID: h.Channel, Title: "x", FileID: model.NewId(), Body: uniqueBody(channelartifacts.FormatHTML),
			}, channelartifacts.ErrInvalidInput},
			{"a new artifact without a title", channelartifacts.WriteRequest{
				ChannelID: h.Channel, Body: uniqueBody(channelartifacts.FormatHTML),
			}, channelartifacts.ErrInvalidInput},
			{"a title that is too long", channelartifacts.WriteRequest{
				ChannelID: h.Channel, Title: strings.Repeat("t", channelartifacts.MaxTitleRunes+1), Body: uniqueBody(channelartifacts.FormatHTML),
			}, channelartifacts.ErrInvalidInput},
			{"an unknown format", channelartifacts.WriteRequest{
				ChannelID: h.Channel, Title: "x", Format: "pdf", Body: uniqueBody(channelartifacts.FormatHTML),
			}, channelartifacts.ErrInvalidInput},
			{"a channel id that is not an id", channelartifacts.WriteRequest{
				ChannelID: "nope", Title: "x", Body: uniqueBody(channelartifacts.FormatHTML),
			}, channelartifacts.ErrInvalidInput},
			{"content that is not text", channelartifacts.WriteRequest{
				ChannelID: h.Channel, Title: "x", Body: []byte{0xff, 0xfe, 0xfd},
			}, channelartifacts.ErrInvalidInput},
			{"content over the size limit", channelartifacts.WriteRequest{
				ChannelID: h.Channel, Title: "x", Body: []byte(strings.Repeat("a", int(h.MaxSize)+1)),
			}, channelartifacts.ErrTooLarge},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_, err := h.Library.Write(testContext(), h.Writer, tt.req)
				require.ErrorIs(t, err, tt.want)
			})
		}
		list, err := h.Library.List(testContext(), h.Writer, h.Channel)
		require.NoError(t, err)
		assert.Empty(t, list.Entries)
	})

	t.Run("a channel's library is bounded", func(t *testing.T) {
		h := newHarness(t)
		for i := range h.MaxPerChannel {
			_, err := write(h, h.Writer, h.Channel, fmt.Sprintf("Doc %d", i), channelartifacts.FormatHTML, uniqueBody(channelartifacts.FormatHTML))
			require.NoError(t, err)
		}
		_, err := write(h, h.Writer, h.Channel, "One too many", channelartifacts.FormatHTML, uniqueBody(channelartifacts.FormatHTML))
		assert.ErrorIs(t, err, channelartifacts.ErrLimitReached)

		_, err = write(h, h.Writer, h.OtherChannel, "Elsewhere", channelartifacts.FormatHTML, uniqueBody(channelartifacts.FormatHTML))
		assert.NoError(t, err, "the limit is per channel")
	})

	t.Run("removing deletes the artifact and its versions", func(t *testing.T) {
		h := newHarness(t)
		entry, err := write(h, h.Writer, h.Channel, "Doc", channelartifacts.FormatHTML, uniqueBody(channelartifacts.FormatHTML))
		require.NoError(t, err)
		_, err = h.Library.Write(testContext(), h.Writer, channelartifacts.WriteRequest{
			ChannelID: h.Channel, ArtifactID: entry.ID,
			Body: uniqueBody(channelartifacts.FormatHTML),
		})
		require.NoError(t, err)

		removed, err := h.Library.Remove(testContext(), h.Unattended, entry.ID)
		require.NoError(t, err)
		assert.Equal(t, entry.ID, removed.ID)
		assert.Equal(t, h.Channel, removed.ChannelID)

		_, err = h.Library.Get(testContext(), h.Writer, entry.ID)
		assert.ErrorIs(t, err, channelartifacts.ErrNotFound)
		_, err = h.Library.Versions(testContext(), h.Writer, entry.ID)
		assert.ErrorIs(t, err, channelartifacts.ErrNotFound)
		_, err = h.Library.Remove(testContext(), h.Writer, entry.ID)
		assert.ErrorIs(t, err, channelartifacts.ErrNotFound)
		list, err := h.Library.List(testContext(), h.Writer, h.Channel)
		require.NoError(t, err)
		assert.Empty(t, list.Entries)
	})

	t.Run("ids are validated", func(t *testing.T) {
		h := newHarness(t)
		_, err := h.Library.Get(testContext(), h.Writer, "nope")
		assert.ErrorIs(t, err, channelartifacts.ErrInvalidInput)
		_, err = h.Library.List(testContext(), h.Writer, "nope")
		assert.ErrorIs(t, err, channelartifacts.ErrInvalidInput)
		_, err = h.Library.Get(testContext(), h.Writer, model.NewId())
		assert.ErrorIs(t, err, channelartifacts.ErrNotFound)
	})
}
