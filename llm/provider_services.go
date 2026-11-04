// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

// ProviderServices is resolved from the concrete provider client before it is
// wrapped. Do not recover these capabilities by type-asserting LanguageModel:
// the bot's model is a decorator chain, so the assertion silently fails.
//
// Add a field per capability, left nil when the provider cannot perform it.
type ProviderServices struct {
	FileDownloader ProviderFileDownloader
}

// CanDownloadFiles is false for a nil receiver (unresolved services or mocks).
func (s *ProviderServices) CanDownloadFiles() bool {
	return s != nil && s.FileDownloader != nil
}
