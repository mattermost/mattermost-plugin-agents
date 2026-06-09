// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    getViewingContextProps,
    isAgentBotDMChannel,
    VIEWING_CHANNEL_ID_PROP,
    VIEWING_TEAM_ID_PROP,
} from './view_context';

function stateWith(currentChannelId: string, currentTeamId: string, channels: Record<string, {id: string}> = {}): any {
    return {
        entities: {
            channels: {
                currentChannelId,
                channels: {
                    ...channels,
                    ...(currentChannelId ? {[currentChannelId]: {id: currentChannelId}} : {}),
                },
            },
            teams: {
                currentTeamId,
            },
        },
    };
}

describe('getViewingContextProps', () => {
    it('returns empty when targetChannelId is missing', () => {
        const state = stateWith('center-ch', 'team1');
        expect(getViewingContextProps(state, '')).toEqual({});
    });

    it('returns empty when there is no current channel (global threads/drafts)', () => {
        const state = stateWith('', 'team1');
        expect(getViewingContextProps(state, 'bot-dm')).toEqual({});
    });

    it('returns empty when the user is already viewing the bot DM', () => {
        const state = stateWith('bot-dm', 'team1');
        expect(getViewingContextProps(state, 'bot-dm')).toEqual({});
    });

    it('returns empty when the redux current channel is not loaded', () => {
        const state: any = {
            entities: {
                channels: {currentChannelId: 'stale-ch', channels: {}},
                teams: {currentTeamId: 'team1'},
            },
        };
        expect(getViewingContextProps(state, 'bot-dm')).toEqual({});
    });

    it('attaches channel + team when viewing a different channel', () => {
        const state = stateWith('center-ch', 'team1');
        expect(getViewingContextProps(state, 'bot-dm')).toEqual({
            [VIEWING_CHANNEL_ID_PROP]: 'center-ch',
            [VIEWING_TEAM_ID_PROP]: 'team1',
        });
    });

    it('omits the team prop when no current team is set', () => {
        const state = stateWith('center-ch', '');
        expect(getViewingContextProps(state, 'bot-dm')).toEqual({
            [VIEWING_CHANNEL_ID_PROP]: 'center-ch',
        });
    });

    it('handles entirely missing entities tree gracefully', () => {
        expect(getViewingContextProps({} as any, 'bot-dm')).toEqual({});
    });
});

describe('isAgentBotDMChannel', () => {
    it('returns false when no bots are present', () => {
        expect(isAgentBotDMChannel('bot-dm', null)).toBe(false);
        expect(isAgentBotDMChannel('bot-dm', [])).toBe(false);
    });

    it('returns false when channelId is empty', () => {
        expect(isAgentBotDMChannel('', [{dmChannelID: 'bot-dm'}])).toBe(false);
    });

    it('returns true when any bot matches the dm channel', () => {
        const bots = [{dmChannelID: 'other'}, {dmChannelID: 'bot-dm'}];
        expect(isAgentBotDMChannel('bot-dm', bots)).toBe(true);
    });

    it('returns false when no bot matches', () => {
        const bots = [{dmChannelID: 'other'}];
        expect(isAgentBotDMChannel('bot-dm', bots)).toBe(false);
    });
});
