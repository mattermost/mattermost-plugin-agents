// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

//go:build !linux

package agentdocs

// limitWorkerMemory does nothing where address-space limits are unreliable;
// the worker is still killed on timeout.
func limitWorkerMemory() {}
