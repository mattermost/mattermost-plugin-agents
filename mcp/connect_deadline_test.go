// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeCloser stands in for a session established by a connection sequence.
type fakeCloser struct {
	closed bool
}

func (f *fakeCloser) Close() error {
	f.closed = true
	return nil
}

// connectWithDeadline must bound a connection sequence without shortening the
// session that sequence produced: the deadline can only win while the sequence
// is still in flight.
func TestConnectWithDeadline(t *testing.T) {
	testCases := []struct {
		name    string
		budget  time.Duration
		connect func(ctx context.Context, session *fakeCloser) (*fakeCloser, error)
		// keptOpen means the session is returned and its context stays usable.
		keptOpen     bool
		expectErr    string
		expectClosed bool
	}{
		{
			name:     "an established session outlives the deadline",
			budget:   time.Minute,
			connect:  func(_ context.Context, session *fakeCloser) (*fakeCloser, error) { return session, nil },
			keptOpen: true,
		},
		{
			name:   "a session established after the deadline is closed and reported as a timeout",
			budget: 20 * time.Millisecond,
			connect: func(ctx context.Context, session *fakeCloser) (*fakeCloser, error) {
				// The sequence only finishes once the deadline canceled it.
				<-ctx.Done()
				return session, nil
			},
			expectErr:    "timed out connecting to MCP server Jira after 20ms",
			expectClosed: true,
		},
		{
			name:      "a failed sequence reports its own error",
			budget:    time.Minute,
			connect:   func(context.Context, *fakeCloser) (*fakeCloser, error) { return nil, errors.New("connection refused") },
			expectErr: "connection refused",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			session := &fakeCloser{}
			var connectCtx context.Context

			got, err := connectWithDeadline(context.Background(), tc.budget, "MCP server Jira",
				func(ctx context.Context) (*fakeCloser, error) {
					connectCtx = ctx
					return tc.connect(ctx, session)
				})

			if tc.expectErr != "" {
				require.EqualError(t, err, tc.expectErr)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.Same(t, session, got)
			}
			require.Equal(t, tc.expectClosed, session.closed)

			if tc.keptOpen {
				require.NoError(t, connectCtx.Err(), "the session context must stay usable")
			} else {
				require.Error(t, connectCtx.Err(), "an abandoned sequence must release its context")
			}
		})
	}
}
