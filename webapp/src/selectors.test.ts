// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from './manifest';
import {getCurrentChannelProject, getCustomPrompts, getPinnedPromptIds, getProject, isAgentBotUserId} from './selectors';

type SelectorState = Parameters<typeof getCustomPrompts>[0];

const makeState = (pluginState?: Record<string, unknown>) => ({
    [`plugins-${manifest.id}`]: pluginState,
}) as unknown as SelectorState;

describe('array selectors return stable references while unloaded', () => {
    const selectors = [
        {name: 'getCustomPrompts', select: getCustomPrompts},
        {name: 'getPinnedPromptIds', select: getPinnedPromptIds},
    ];

    const states = [
        {desc: 'null plugin state values', state: makeState({customPrompts: null, pinnedPromptIds: null})},
        {desc: 'missing plugin state', state: makeState()},
    ];

    describe.each(selectors)('$name', ({select}) => {
        it.each(states)('returns the same empty array for $desc', ({state}) => {
            const first = select(state);
            const second = select(state);

            expect(first).toEqual([]);
            expect(second).toBe(first);
        });
    });

    it('returns the stored arrays once loaded', () => {
        const customPrompts = [{id: 'p1'}];
        const pinnedPromptIds = ['p1'];
        const state = makeState({customPrompts, pinnedPromptIds});

        expect(getCustomPrompts(state)).toBe(customPrompts);
        expect(getPinnedPromptIds(state)).toBe(pinnedPromptIds);
    });
});

describe('project selectors', () => {
    const project = {channel_id: 'chan1', state: 'active'};
    const stateOf = (plugin: Record<string, unknown>) => ({
        entities: {channels: {currentChannelId: 'chan1'}},
        'plugins-mattermost-ai': plugin,
    }) as never;

    test.each([
        {name: 'returns the project while project channels are on', plugin: {projectChannelsEnabled: true, projects: {byChannelId: {chan1: project}}}, expected: project},
        {name: 'hides every project while project channels are off', plugin: {projectChannelsEnabled: false, projects: {byChannelId: {chan1: project}}}, expected: null},
        {name: 'knows nothing before the slice loads', plugin: {}, expected: null},
    ])('getProject $name', ({plugin, expected}) => {
        expect(getProject(stateOf(plugin), 'chan1') ?? null).toEqual(expected);
        expect(getCurrentChannelProject(stateOf(plugin)) ?? null).toEqual(expected);
    });

    test('project agents are trusted as agent authors but regular lookups stay separate', () => {
        const state = stateOf({bots: [{id: 'bot'}], projectAgents: [{id: 'hidden'}]});

        expect(isAgentBotUserId(state, 'bot')).toBe(true);
        expect(isAgentBotUserId(state, 'hidden')).toBe(true);
        expect(isAgentBotUserId(state, 'user')).toBe(false);
        expect(isAgentBotUserId(stateOf({}), 'hidden')).toBe(false);
    });
});
