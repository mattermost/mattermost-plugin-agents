// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseFlags(t *testing.T) {
	boolPtr := func(v bool) *bool { return &v }

	tests := []struct {
		name    string
		args    []string
		want    cliConfig
		wantErr bool
	}{
		{
			name: "defaults",
			args: nil,
			want: cliConfig{
				transport:    "stdio",
				httpPort:     8080,
				httpBindAddr: "127.0.0.1",
			},
		},
		{
			name: "short flags",
			args: []string{"-s", "https://mm.example.com", "-t", "pat-123", "-d", "-l", "/tmp/mcp.log"},
			want: cliConfig{
				mmServerURL:  "https://mm.example.com",
				token:        "pat-123",
				debug:        true,
				logFile:      "/tmp/mcp.log",
				transport:    "stdio",
				httpPort:     8080,
				httpBindAddr: "127.0.0.1",
			},
		},
		{
			name: "attached and clustered short flags",
			args: []string{"-shttps://mm.example.com", "-tpat-123", "-dl", "/tmp/mcp.log"},
			want: cliConfig{
				mmServerURL:  "https://mm.example.com",
				token:        "pat-123",
				debug:        true,
				logFile:      "/tmp/mcp.log",
				transport:    "stdio",
				httpPort:     8080,
				httpBindAddr: "127.0.0.1",
			},
		},
		{
			name: "http long flags with explicit false track-ai-generated",
			args: []string{
				"--server-url", "https://mm.example.com",
				"--internal-server-url", "http://127.0.0.1:8065",
				"--transport", "http",
				"--http-port", "9090",
				"--http-bind-addr", "0.0.0.0",
				"--site-url", "https://mcp.example.com",
				"--stateless",
				"--dev",
				"--track-ai-generated=false",
			},
			want: cliConfig{
				mmServerURL:         "https://mm.example.com",
				mmInternalServerURL: "http://127.0.0.1:8065",
				transport:           "http",
				httpPort:            9090,
				httpBindAddr:        "0.0.0.0",
				siteURL:             "https://mcp.example.com",
				stateless:           true,
				devMode:             true,
				trackAIGenerated:    boolPtr(false),
			},
		},
		{
			name: "track-ai-generated explicit true",
			args: []string{"--track-ai-generated"},
			want: cliConfig{
				transport:        "stdio",
				httpPort:         8080,
				httpBindAddr:     "127.0.0.1",
				trackAIGenerated: boolPtr(true),
			},
		},
		{
			name: "version flag",
			args: []string{"--version"},
			want: cliConfig{
				transport:    "stdio",
				httpPort:     8080,
				httpBindAddr: "127.0.0.1",
				showVersion:  true,
			},
		},
		{
			name:    "unknown flag errors",
			args:    []string{"--does-not-exist"},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got, err := parseFlags(tc.args, &stderr)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
