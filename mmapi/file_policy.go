// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package mmapi

import (
	"io"

	"github.com/mattermost/mattermost/server/public/model"
)

// WithFilePolicy returns a Client whose GetFile and GetFileInfo calls are
// gated by the requesting session's download-file policy. Other methods
// delegate unchanged. An empty or denied session fails closed.
func WithFilePolicy(mm Client, sessionID string) Client {
	if mm == nil {
		return nil
	}
	if p, ok := mm.(*policyClient); ok {
		mm = p.Client
	}
	return &policyClient{Client: mm, sessionID: sessionID}
}

type policyClient struct {
	Client
	sessionID string
}

func (p *policyClient) GetFileInfo(fileID string) (*model.FileInfo, error) {
	if err := checkFileDownloadPermission(p.Client, p.sessionID, fileID); err != nil {
		return nil, err
	}
	return p.Client.GetFileInfo(fileID)
}

func (p *policyClient) GetFile(fileID string) (io.ReadCloser, error) {
	if err := checkFileDownloadPermission(p.Client, p.sessionID, fileID); err != nil {
		return nil, err
	}
	return p.Client.GetFile(fileID)
}
