// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from './manifest';

// The chat each agent DM view has open (its root post id), so the sidebar's nested chats can
// highlight it. The view owns the selection and publishes it here; nothing reads it back from the
// URL, which the host rewrites on its own (a permalink's id is dropped a few seconds after opening).

export const AgentDMOpenChatSet = 'AI_AGENT_DM_OPEN_CHAT_SET';

// Root post id by DM channel id; a channel with a new chat open has no entry.
export type AgentDMOpenChatState = Readonly<Record<string, string>>;

export function agentDMOpenChat(state: AgentDMOpenChatState = {}, action: {type: string; channelId?: string; rootId?: string}): AgentDMOpenChatState {
    if (action.type !== AgentDMOpenChatSet || !action.channelId) {
        return state;
    }
    const {channelId, rootId} = action;
    if (rootId) {
        return state[channelId] === rootId ? state : {...state, [channelId]: rootId};
    }
    if (!(channelId in state)) {
        return state;
    }
    const next = {...state};
    delete next[channelId];
    return next;
}

export const setAgentDMOpenChat = (channelId: string, rootId: string) => ({type: AgentDMOpenChatSet, channelId, rootId});

export const openChatRootId = (state: unknown, channelId: string): string => {
    const slice = (state as Record<string, {agentDMOpenChat?: AgentDMOpenChatState} | undefined> | undefined)?.[`plugins-${manifest.id}`]?.agentDMOpenChat;
    return slice?.[channelId] ?? '';
};
