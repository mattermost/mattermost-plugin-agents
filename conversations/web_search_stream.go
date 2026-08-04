// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

package conversations

import (
	"github.com/mattermost/mattermost-plugin-agents/v2/llm"
	"github.com/mattermost/mattermost-plugin-agents/v2/mmtools"
)

// decorateStreamWithWebSearchAnnotations wraps stream output with URL citation
// annotations when ctx carries WebSearch results from the current request.
func decorateStreamWithWebSearchAnnotations(stream *llm.TextStreamResult, ctx *llm.Context) *llm.TextStreamResult {
	if webSearchData := mmtools.ConsumeWebSearchContexts(ctx); len(webSearchData) > 0 {
		return mmtools.DecorateStreamWithAnnotations(stream, webSearchData, nil)
	}
	return stream
}
