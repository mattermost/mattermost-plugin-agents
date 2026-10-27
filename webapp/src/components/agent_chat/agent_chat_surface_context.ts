// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createContext} from 'react';

export interface AgentChatSurface {

    // The pill composer's send button turns into Stop while a reply generates,
    // so agent posts in this surface leave their own Stop control out.
    composerHandlesStop: boolean;

    // Agent posts show animated dots while a reply has produced nothing yet.
    // Only the agent chat views opt in. To widen it to other surfaces (the
    // Channels DM, host RHS), set this true in the default context value below.
    showThinkingIndicator: boolean;

    // Agent posts reveal streamed text at a steady pace, fading it in, instead
    // of in chunk-sized jumps. Only the agent chat views opt in; widening it
    // is a one-line change to the default context value below.
    smoothStreaming: boolean;

    // Short agent replies hug their content instead of spanning the column.
    // Only the agent chat views opt in; widening it is a one-line change to
    // the default context value below (the host bubble rule also needs the
    // surface's host styles).
    hugShortBubbles: boolean;
}

// Provided by the agent chat views (Agents product view, Agents RHS) around
// the host ThreadViewer, whose posts render the plugin's LLMBotPost. Anywhere
// else (the Channels DM, channel threads in the host RHS) the default applies.
export const AgentChatSurfaceContext = createContext<AgentChatSurface>({
    composerHandlesStop: false,
    showThinkingIndicator: false,
    smoothStreaming: false,
    hugShortBubbles: false,
});

export const AGENT_CHAT_SURFACE: AgentChatSurface = {
    composerHandlesStop: true,
    showThinkingIndicator: true,
    smoothStreaming: true,
    hugShortBubbles: true,
};
