// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createContext} from 'react';

export interface AgentChatSurface {

    // True inside the agent chat views (Agents product view, Agents RHS). Agent
    // posts there: show animated dots while a reply has produced nothing yet, reveal streamed text at a
    // steady pace, hug short replies, put the controls under the bubble and
    // offer Copy. Anywhere else (the Channels DM, channel threads in the host
    // RHS) posts keep the plain host behavior.
    isAgentChat: boolean;
}

// Provided by the agent chat views around the host ThreadViewer, whose posts
// render the plugin's LLMBotPost.
export const AgentChatSurfaceContext = createContext<AgentChatSurface>({isAgentChat: false});

export const AGENT_CHAT_SURFACE: AgentChatSurface = {isAgentChat: true};
