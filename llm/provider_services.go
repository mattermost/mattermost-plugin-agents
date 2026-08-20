// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package llm

// ProviderServices carries the provider-side capabilities available for a bot's
// primary provider — things a tool can only do by talking to the LLM provider
// with the agent's credentials, rather than to Mattermost.
//
// It is resolved once, where the concrete provider client is constructed and
// its type is still known, and injected from there (see the bots package).
// Capabilities are deliberately NOT discovered later by type-asserting an
// optional interface on LanguageModel: the model a bot exposes is a decorator
// chain (truncation, token accounting, structured-output fallback), and an
// optional interface implemented only by the innermost client is invisible
// through it — the assertion silently fails and the capability disappears.
//
// Add a field per new provider-side capability, left nil when the resolved
// provider cannot perform it.
type ProviderServices struct {
	// FileDownloader fetches provider-side file content (e.g. output files
	// from a provider code-execution sandbox). nil when the provider has no
	// file-retrieval support the plugin can use.
	FileDownloader ProviderFileDownloader
}

// CanDownloadFiles reports whether provider-side file content can be fetched.
// A nil receiver reports false so callers can treat "no services resolved"
// (test bots, mock providers) the same as "provider cannot do it".
func (s *ProviderServices) CanDownloadFiles() bool {
	return s != nil && s.FileDownloader != nil
}
