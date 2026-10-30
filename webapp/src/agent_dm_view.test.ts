// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {Channel} from '@mattermost/types/channels';

import {
    agentDMRawView,
    dmOtherUserId,
    isAgentDMChannel,
    isCurrentAgentDMShownAs,
    setAgentDMRawView,
} from './agent_dm_view';
import manifest from './manifest';

const pluginKey = `plugins-${manifest.id}`;

const bots = [{id: 'agent1'}, {id: 'agent2'}];

const makeState = ({botList = bots as unknown[] | null, raw = {} as Record<string, true>, currentChannelId = ''} = {}) => ({
    [pluginKey]: {bots: botList, agentDMRawView: raw},
    entities: {
        users: {currentUserId: 'me'},
        channels: {
            currentChannelId,
            channels: {
                dmAgent: channel({id: 'dmAgent', name: 'agent1__me'}),
                dmHuman: channel({id: 'dmHuman', name: 'me__human'}),
            },
        },
    },
});

function channel(overrides: Partial<Channel>): Channel {
    return {id: 'c', type: 'D', name: 'agent1__me', ...overrides} as Channel;
}

describe('dmOtherUserId', () => {
    test.each([
        ['current user second', 'agent1__me', 'agent1'],
        ['current user first', 'me__agent1', 'agent1'],
        ['self DM', 'me__me', 'me'],
        ['not a member', 'a__b', ''],
        ['not a DM name', 'town-square', ''],
        ['three parts', 'a__me__b', ''],
    ])('%s', (_name, channelName, expected) => {
        expect(dmOtherUserId(channelName, 'me')).toBe(expected);
    });
});

describe('isAgentDMChannel', () => {
    test.each([
        ['agent DM', makeState(), channel({}), true],
        ['agent DM, names in either order', makeState(), channel({name: 'me__agent2'}), true],
        ['DM with a person', makeState(), channel({name: 'me__human'}), false],
        ['DM with an agent the user cannot use', makeState(), channel({name: 'me__agent3'}), false],
        ['self DM', makeState(), channel({name: 'me__me'}), false],
        ['group message', makeState(), channel({type: 'G', name: 'agent1__me'}), false],
        ['public channel', makeState(), channel({type: 'O', name: 'agent1__me'}), false],
        ['bots not loaded yet', makeState({botList: null}), channel({}), false],
        ['switched to plain messages', makeState({raw: {c: true}}), channel({}), false],
        ['another DM switched to plain messages', makeState({raw: {other: true}}), channel({}), true],
        ['no channel', makeState(), null, false],
    ])('%s', (_name, state, ch, expected) => {
        expect(isAgentDMChannel(state as any, ch as Channel)).toBe(expected);
    });

    test('a refreshed bots list is used', () => {
        const ch = channel({name: 'me__agent3'});
        expect(isAgentDMChannel(makeState() as any, ch)).toBe(false);
        expect(isAgentDMChannel(makeState({botList: [...bots, {id: 'agent3'}]}) as any, ch)).toBe(true);
        expect(isAgentDMChannel(makeState() as any, ch)).toBe(false);
    });
});

describe('isCurrentAgentDMShownAs', () => {
    test.each([
        ['agent DM as chat: offers plain messages', makeState({currentChannelId: 'dmAgent'}), false, true],
        ['agent DM as chat: no way back needed', makeState({currentChannelId: 'dmAgent'}), true, false],
        ['agent DM as messages: offers the chat', makeState({currentChannelId: 'dmAgent', raw: {dmAgent: true}}), true, true],
        ['agent DM as messages: already plain', makeState({currentChannelId: 'dmAgent', raw: {dmAgent: true}}), false, false],
        ['DM with a person', makeState({currentChannelId: 'dmHuman'}), false, false],
        ['DM with a person, as messages', makeState({currentChannelId: 'dmHuman'}), true, false],
        ['no current channel', makeState(), false, false],
    ])('%s', (_name, state, raw, expected) => {
        expect(isCurrentAgentDMShownAs(state as any, raw)).toBe(expected);
    });
});

describe('agentDMRawView', () => {
    test('switches one channel to plain messages and back', () => {
        let state = agentDMRawView({}, {type: 'init'});
        expect(state).toEqual({});

        state = agentDMRawView(state, setAgentDMRawView('a', true));
        state = agentDMRawView(state, setAgentDMRawView('b', true));
        expect(state).toEqual({a: true, b: true});

        const same = agentDMRawView(state, setAgentDMRawView('a', true));
        expect(same).toBe(state);

        state = agentDMRawView(state, setAgentDMRawView('a', false));
        expect(state).toEqual({b: true});
        expect(agentDMRawView(state, setAgentDMRawView('a', false))).toBe(state);
    });
});
