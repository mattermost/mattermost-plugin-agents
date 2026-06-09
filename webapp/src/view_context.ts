// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {GlobalState} from '@mattermost/types/store';

// Post prop keys that carry the channel/team the user is currently viewing
// in the center panel when posting to an agent from the RHS. Names must stay
// in sync with conversations/viewing_context.go on the backend.
export const VIEWING_CHANNEL_ID_PROP = 'viewing_channel_id';
export const VIEWING_TEAM_ID_PROP = 'viewing_team_id';

export interface ViewingContextProps {
    [VIEWING_CHANNEL_ID_PROP]?: string;
    [VIEWING_TEAM_ID_PROP]?: string;
}

// getViewingContextProps returns the props to attach to a post created against
// an agent bot DM, when the user is actively viewing a real channel in the
// center panel that is different from the bot DM. Returns an empty object in
// any other case (user is in the bot DM as the center channel, no current
// channel, e.g. on global threads/drafts pages, etc.).
//
// targetChannelId is the channel the post is being created in (the bot DM).
export function getViewingContextProps(state: GlobalState, targetChannelId: string): ViewingContextProps {
    if (!targetChannelId) {
        return {};
    }

    const entities = state?.entities;
    const currentChannelId = entities?.channels?.currentChannelId ?? '';
    const currentTeamId = entities?.teams?.currentTeamId ?? '';

    if (!currentChannelId) {
        return {};
    }

    // Skip when the user is already viewing the bot DM as the center channel.
    if (currentChannelId === targetChannelId) {
        return {};
    }

    // Skip when the current "channel" is actually a DM/GM with the bot itself
    // (defensive: should be caught by the equality check above, but the global
    // threads/drafts pages can leave currentChannelId stale).
    const currentChannel = entities?.channels?.channels?.[currentChannelId];
    if (!currentChannel) {
        return {};
    }

    const props: ViewingContextProps = {
        [VIEWING_CHANNEL_ID_PROP]: currentChannelId,
    };
    if (currentTeamId) {
        props[VIEWING_TEAM_ID_PROP] = currentTeamId;
    }
    return props;
}

// isAgentBotDMChannel returns true if the given channelId is one of the
// known agent bot DM channels for any LLMBot in the bots list.
export function isAgentBotDMChannel(channelId: string, bots: Array<{dmChannelID: string}> | null | undefined): boolean {
    if (!channelId || !bots) {
        return false;
    }
    return bots.some((b) => b.dmChannelID === channelId);
}
