// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package main

import (
	"testing"

	"github.com/mattermost/mattermost/server/public/model"
	"github.com/stretchr/testify/require"
)

func TestDeriveInternalServerURL(t *testing.T) {
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
			name: "empty listen address falls back to default localhost",
			cfg: &model.Config{
				ServiceSettings: model.ServiceSettings{
					ListenAddress:      model.NewPointer(""),
					ConnectionSecurity: model.NewPointer(""),
					SiteURL:            model.NewPointer("https://example.com"),
				},
			},
			siteURL: "https://example.com",
			want:    "http://localhost:8065",
		},
		{
			name: "wildcard IPv4 listen with no TLS",
			cfg: &model.Config{
				ServiceSettings: model.ServiceSettings{
					ListenAddress:      model.NewPointer(":8065"),
					ConnectionSecurity: model.NewPointer(""),
					SiteURL:            model.NewPointer("https://example.com"),
				},
			},
			siteURL: "https://example.com",
			want:    "http://localhost:8065",
		},
		{
			name: "0.0.0.0 listen with no TLS",
			cfg: &model.Config{
				ServiceSettings: model.ServiceSettings{
					ListenAddress:      model.NewPointer("0.0.0.0:8065"),
					ConnectionSecurity: model.NewPointer(""),
					SiteURL:            model.NewPointer("https://example.com"),
				},
			},
			siteURL: "https://example.com",
			want:    "http://localhost:8065",
		},
		{
			name: "IPv6 wildcard listen with no TLS",
			cfg: &model.Config{
				ServiceSettings: model.ServiceSettings{
					ListenAddress:      model.NewPointer("[::]:8065"),
					ConnectionSecurity: model.NewPointer(""),
					SiteURL:            model.NewPointer("https://example.com"),
				},
			},
			siteURL: "https://example.com",
			want:    "http://localhost:8065",
		},
		{
			name: "specific bind address with no TLS",
			cfg: &model.Config{
				ServiceSettings: model.ServiceSettings{
					ListenAddress:      model.NewPointer("127.0.0.1:8065"),
					ConnectionSecurity: model.NewPointer(""),
					SiteURL:            model.NewPointer("https://example.com"),
				},
			},
			siteURL: "https://example.com",
			want:    "http://127.0.0.1:8065",
		},
		{
			name: "TLS on :443 falls back to SiteURL (MM-69180)",
			cfg: &model.Config{
				ServiceSettings: model.ServiceSettings{
					ListenAddress:      model.NewPointer(":443"),
					ConnectionSecurity: model.NewPointer(model.ConnSecurityTLS),
					SiteURL:            model.NewPointer("https://example.com"),
				},
			},
			siteURL: "https://example.com",
			want:    "https://example.com",
		},
		{
			name: "TLS on alternate port falls back to SiteURL",
			cfg: &model.Config{
				ServiceSettings: model.ServiceSettings{
					ListenAddress:      model.NewPointer(":8443"),
					ConnectionSecurity: model.NewPointer(model.ConnSecurityTLS),
					SiteURL:            model.NewPointer("https://example.com:8443"),
				},
			},
			siteURL: "https://example.com:8443",
			want:    "https://example.com:8443",
		},
		{
			name: "TLS without SiteURL still uses TLS scheme on listen address",
			cfg: &model.Config{
				ServiceSettings: model.ServiceSettings{
					ListenAddress:      model.NewPointer(":443"),
					ConnectionSecurity: model.NewPointer(model.ConnSecurityTLS),
					SiteURL:            model.NewPointer(""),
				},
			},
			siteURL: "",
			want:    "https://localhost:443",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveInternalServerURLFromConfig(tt.cfg, tt.siteURL)
			require.Equal(t, tt.want, got)
		})
	}
}
