// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"errors"
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/mattermost/mattermost/server/public/plugin/plugintest"
	"github.com/mattermost/mattermost/server/public/pluginapi"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestDeriveInternalServerURL(t *testing.T) {
	cfgWith := func(listen, connSec *string) *model.Config {
		return &model.Config{
			ServiceSettings: model.ServiceSettings{
				ListenAddress:      listen,
				ConnectionSecurity: connSec,
			},
		}
	}
	none := model.NewPointer(model.ConnSecurityNone)
	tls := model.NewPointer(model.ConnSecurityTLS)

	tests := []struct {
		name    string
		cfg     *model.Config
		siteURL string
		want    string
	}{
		{
			name:    "nil config falls back to default localhost",
			cfg:     nil,
			siteURL: "https://example.com",
			want:    "http://localhost:8065",
		},
		{
			name:    "nil ListenAddress falls back to default localhost",
			cfg:     cfgWith(nil, none),
			siteURL: "https://example.com",
			want:    "http://localhost:8065",
		},
		{
			name:    "empty ListenAddress falls back to default localhost",
			cfg:     cfgWith(model.NewPointer(""), none),
			siteURL: "https://example.com",
			want:    "http://localhost:8065",
		},
		{
			name:    "nil ConnectionSecurity is treated as plain HTTP",
			cfg:     cfgWith(model.NewPointer(":8065"), nil),
			siteURL: "https://example.com",
			want:    "http://localhost:8065",
		},
		{
			name:    "wildcard IPv4 listen with no TLS",
			cfg:     cfgWith(model.NewPointer(":8065"), none),
			siteURL: "https://example.com",
			want:    "http://localhost:8065",
		},
		{
			name:    "0.0.0.0 listen with no TLS",
			cfg:     cfgWith(model.NewPointer("0.0.0.0:8065"), none),
			siteURL: "https://example.com",
			want:    "http://localhost:8065",
		},
		{
			name:    "IPv6 wildcard listen with no TLS",
			cfg:     cfgWith(model.NewPointer("[::]:8065"), none),
			siteURL: "https://example.com",
			want:    "http://localhost:8065",
		},
		{
			name:    "specific bind address with no TLS",
			cfg:     cfgWith(model.NewPointer("127.0.0.1:8065"), none),
			siteURL: "https://example.com",
			want:    "http://127.0.0.1:8065",
		},
		{
			name:    "TLS on :443 falls back to SiteURL (MM-69180)",
			cfg:     cfgWith(model.NewPointer(":443"), tls),
			siteURL: "https://example.com",
			want:    "https://example.com",
		},
		{
			name:    "TLS without SiteURL uses https on wildcard IPv4 listen",
			cfg:     cfgWith(model.NewPointer(":443"), tls),
			siteURL: "",
			want:    "https://localhost:443",
		},
		{
			name:    "TLS without SiteURL uses https on 0.0.0.0 listen",
			cfg:     cfgWith(model.NewPointer("0.0.0.0:8443"), tls),
			siteURL: "",
			want:    "https://localhost:8443",
		},
		{
			name:    "TLS without SiteURL uses https on IPv6 wildcard listen",
			cfg:     cfgWith(model.NewPointer("[::]:8443"), tls),
			siteURL: "",
			want:    "https://localhost:8443",
		},
		{
			name:    "TLS without SiteURL uses https on hostname:port listen",
			cfg:     cfgWith(model.NewPointer("internal.example.com:8443"), tls),
			siteURL: "",
			want:    "https://internal.example.com:8443",
		},
		{
			name:    "subpath SiteURL keeps the path on wildcard IPv4 listen",
			cfg:     cfgWith(model.NewPointer(":8065"), none),
			siteURL: "https://example.com/chat",
			want:    "http://localhost:8065/chat",
		},
		{
			name:    "subpath SiteURL with trailing slash keeps the path without the slash",
			cfg:     cfgWith(model.NewPointer("127.0.0.1:8065"), none),
			siteURL: "https://example.com/chat/",
			want:    "http://127.0.0.1:8065/chat",
		},
		{
			name:    "subpath SiteURL keeps the path on default localhost",
			cfg:     cfgWith(nil, none),
			siteURL: "https://example.com/team/chat",
			want:    "http://localhost:8065/team/chat",
		},
		{
			name:    "subpath SiteURL keeps encoded characters in the path",
			cfg:     cfgWith(model.NewPointer(":8065"), none),
			siteURL: "https://example.com/chat%3Fteam",
			want:    "http://localhost:8065/chat%3Fteam",
		},
		{
			name:    "TLS with subpath SiteURL falls back to SiteURL",
			cfg:     cfgWith(model.NewPointer(":443"), tls),
			siteURL: "https://example.com/chat",
			want:    "https://example.com/chat",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveInternalServerURLFromConfig(tt.cfg, tt.siteURL)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestSessionTokenResolver(t *testing.T) {
	const (
		knownSessionID = "new-session"
		knownToken     = "known-token"
	)
	replicaMiss := errors.New("We encountered an error finding the session")

	tests := []struct {
		name      string
		sessionID string
		session   *model.Session
		getErr    error
		wantToken string
		wantErr   bool
	}{
		{
			name:      "uses the token the caller holds when the lookup misses a new session",
			sessionID: knownSessionID,
			getErr:    replicaMiss,
			wantToken: knownToken,
		},
		{
			name:      "prefers a successful lookup",
			sessionID: knownSessionID,
			session:   &model.Session{Id: knownSessionID, Token: "fresh-token"},
			wantToken: "fresh-token",
		},
		{
			name:      "never hands the known token to another session",
			sessionID: "other-session",
			getErr:    replicaMiss,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api := &plugintest.API{}
			api.On("LogDebug", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Maybe()
			getSession := func(string) (*model.Session, error) { return tt.session, tt.getErr }

			resolve := newSessionTokenResolver(getSession, pluginapi.NewClient(api, nil).Log, "user", knownSessionID, knownToken)
			token, err := resolve(tt.sessionID)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantToken, token)
		})
	}
}
