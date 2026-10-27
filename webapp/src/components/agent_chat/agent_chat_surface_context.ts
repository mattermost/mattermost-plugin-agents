// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createContext} from 'react';

export interface AgentChatSurface {

    // The pill composer's send button turns into Stop while a reply generates,
    // so agent posts in this surface leave their own Stop control out.
    composerHandlesStop: boolean;
}

// Provided by the agent chat views (Agents product view, Agents RHS) around
// the host ThreadViewer, whose posts render the plugin's LLMBotPost. Anywhere
// else (the Channels DM, channel threads in the host RHS) the default applies.
export const AgentChatSurfaceContext = createContext<AgentChatSurface>({composerHandlesStop: false});

export const AGENT_CHAT_SURFACE: AgentChatSurface = {composerHandlesStop: true};
