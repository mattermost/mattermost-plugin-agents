// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Channel} from '@mattermost/types/channels';

import type {LLMBot} from './bots';
import manifest from './manifest';

// An agent's DM in the Channels app shows the agent chat view (registerChannelViewComponent)
// instead of the message list. The user can switch one DM back to the plain messages for the rest
// of the session; that choice lives in the plugin's redux slice (not persisted), so the host's
// matcher, which runs on every store update, sees it.

export const AgentDMRawViewSet = 'AI_AGENT_DM_RAW_VIEW_SET';

// Channel ids of the agent DMs shown as plain messages.
export type AgentDMRawViewState = Readonly<Record<string, true>>;

export function agentDMRawView(state: AgentDMRawViewState = {}, action: {type: string; channelId?: string; raw?: boolean}): AgentDMRawViewState {
    if (action.type !== AgentDMRawViewSet || !action.channelId) {
        return state;
    }
    const {channelId} = action;
    if (action.raw) {
        return state[channelId] ? state : {...state, [channelId]: true};
    }
    if (!state[channelId]) {
        return state;
    }
    const next = {...state};
    delete next[channelId];
    return next;
}

export const setAgentDMRawView = (channelId: string, raw: boolean) => ({type: AgentDMRawViewSet, channelId, raw});

// The other member of a DM, from the channel name (`<userId>__<userId>`); '' when the current user is
// not one of them or the name is not a DM name.
export const dmOtherUserId = (channelName: string, currentUserId: string): string => {
    const parts = channelName.split('__');
    if (parts.length !== 2 || !currentUserId) {
        return '';
    }
    if (parts[0] === currentUserId) {
        return parts[1];
    }
    if (parts[1] === currentUserId) {
        return parts[0];
    }
    return '';
};

interface MatcherState {
    entities: {
        users: {currentUserId: string};
        channels?: {currentChannelId?: string; channels?: Record<string, Channel | undefined>};
    };
}

const pluginState = (state: unknown): {bots?: LLMBot[] | null; agentDMRawView?: AgentDMRawViewState} | undefined => (state as Record<string, any> | undefined)?.[`plugins-${manifest.id}`];

// The matcher runs on every store update while a channel is open, so the bot id set is rebuilt only
// when the bots list changes.
let memoBots: LLMBot[] | null = null;
let memoBotIds: ReadonlySet<string> = new Set();
const botIds = (bots: LLMBot[]): ReadonlySet<string> => {
    if (bots !== memoBots) {
        memoBots = bots;
        memoBotIds = new Set(bots.map((b) => b.id));
    }
    return memoBotIds;
};

// The agent (one of the current user's agents) on the other side of this DM, or ''. Not dmChannelID:
// a DM opened after the bots list was fetched is not in it.
export const agentIdForDMChannel = (state: MatcherState, channel: Pick<Channel, 'type' | 'name'> | null | undefined): string => {
    if (!channel || channel.type !== 'D') {
        return '';
    }
    const bots = pluginState(state)?.bots;
    if (!bots) {
        return '';
    }
    const otherId = dmOtherUserId(channel.name, state.entities.users.currentUserId);
    return otherId && botIds(bots).has(otherId) ? otherId : '';
};

const isRawView = (state: unknown, channelId: string) => Boolean(pluginState(state)?.agentDMRawView?.[channelId]);

// registerChannelViewComponent matcher: an agent DM the user has not switched to plain messages.
export const isAgentDMChannel = (state: MatcherState, channel: Channel | undefined): boolean => {
    return Boolean(channel) && !isRawView(state, channel!.id) && agentIdForDMChannel(state, channel) !== '';
};

// Channel header menu: whether the current channel is an agent DM shown as `raw` (plain messages)
// or not, i.e. whether the switch to the other view applies.
export const isCurrentAgentDMShownAs = (state: MatcherState, raw: boolean): boolean => {
    const channelId = state.entities.channels?.currentChannelId ?? '';
    const channel = (channelId && state.entities.channels?.channels?.[channelId]) || null;
    return Boolean(channel) && isRawView(state, channelId) === raw && agentIdForDMChannel(state, channel) !== '';
};
