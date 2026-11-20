// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    AIThreadTitleSet,
    AIThreadsReceived,
    AIThreadsRequested,
    ProjectAgentsSet,
    ProjectChannelsEnabledSet,
    ProjectMissing,
    ProjectReceived,
    ProjectsReceived,
    aiThreads,
    initialAIThreadsState,
    initialProjectsState,
    pluginReducer,
    projects,
    type ProjectsState,
    rhsTab,
    selectedPostId,
} from './redux';
import {showRHSList} from './redux_actions';
import type {Project} from './types/projects';

describe('rhsTab reducer', () => {
    test.each([
        // eslint-disable-next-line no-undefined -- the reducer's initial state is an undefined argument
        {name: 'starts on the new chat', state: undefined, action: {type: 'NONE'}, expected: 'new'},
        {name: 'shows the chat history', state: 'new' as const, action: {type: 'AI_RHS_SET_TAB', tab: 'threads'}, expected: 'threads'},
        {name: 'shows the new chat', state: 'threads' as const, action: {type: 'AI_RHS_SET_TAB', tab: 'new'}, expected: 'new'},
        {name: 'ignores an unknown tab', state: 'threads' as const, action: {type: 'AI_RHS_SET_TAB', tab: 'bogus'}, expected: 'new'},
        {name: 'unrelated actions keep it', state: 'threads' as const, action: {type: 'SELECT_AI_POST', postId: ''}, expected: 'threads'},
    ])('$name', ({state, action, expected}) => {
        expect(rhsTab(state, action)).toBe(expected);
    });
});

describe('selectedPostId reducer', () => {
    test.each([
        {name: 'selects a thread', action: {type: 'SELECT_AI_POST', postId: 'root2'}, expected: 'root2'},
        {name: 'showing a list view closes the thread', action: showRHSList('threads'), expected: ''},
    ])('$name', ({action, expected}) => {
        expect(selectedPostId('root1', action)).toBe(expected);
    });
});

describe('aiThreads reducer', () => {
    const thread = (id: string, title: string) => ({id, channel_id: 'dm', bot_id: 'b', root_post_id: `root_${id}`, title, turn_count: 1, update_at: 1});
    const apply = (actions: Array<Record<string, unknown>>) => actions.reduce((state, action) => aiThreads(state, action as never), initialAIThreadsState);

    test.each([
        {
            name: 'stores a response',
            actions: [{type: AIThreadsRequested, requestId: 1}, {type: AIThreadsReceived, requestId: 1, threads: [thread('a', 'A')]}],
            expected: ['a:A'],
        },
        {
            name: 'drops a superseded response',
            actions: [{type: AIThreadsRequested, requestId: 1}, {type: AIThreadsRequested, requestId: 2}, {type: AIThreadsReceived, requestId: 1, threads: [thread('a', 'stale')]}],
            expected: null,
        },
        {
            name: 'a title set renames the listed chat',
            actions: [{type: AIThreadsRequested, requestId: 1}, {type: AIThreadsReceived, requestId: 1, threads: [thread('a', 'Old'), thread('b', 'B')]}, {type: AIThreadTitleSet, conversationId: 'a', title: 'New'}],
            expected: ['a:New', 'b:B'],
        },
        {
            name: 'a title set before any list loaded leaves it unloaded',
            actions: [{type: AIThreadTitleSet, conversationId: 'a', title: 'New'}],
            expected: null,
        },
    ])('$name', ({actions, expected}) => {
        const state = apply(actions);
        expect(state.threads?.map((t) => `${t.id}:${t.title}`) ?? null).toEqual(expected);
    });
});

describe('projects reducer', () => {
    const project = (channelId: string, overrides: Record<string, unknown> = {}) => ({
        channel_id: channelId,
        team_id: 'team1',
        state: 'active',
        update_at: 10,
        ...overrides,
    }) as unknown as Project;
    const apply = (actions: Array<Record<string, unknown>>) => actions.reduce<ProjectsState>((state, action) => projects(state, action), initialProjectsState);

    test.each([
        {
            name: 'a pushed update replaces the stored project',
            actions: [{type: ProjectReceived, project: project('c1')}, {type: ProjectReceived, project: project('c1', {state: 'ending', update_at: 11})}],
            expected: ['c1:ending'],
        },
        {
            name: 'a response older than the stored project is dropped',
            actions: [{type: ProjectReceived, project: project('c1', {state: 'ending', update_at: 11})}, {type: ProjectReceived, project: project('c1', {update_at: 5})}],
            expected: ['c1:ending'],
        },
        {
            name: 'a team list replaces that team\'s projects and keeps other teams\'',
            actions: [
                {type: ProjectReceived, project: project('c1')},
                {type: ProjectReceived, project: project('c2', {team_id: 'team2'})},
                {type: ProjectsReceived, teamId: 'team1', projects: [project('c3')]},
            ],
            expected: ['c2:active', 'c3:active'],
        },
        {
            name: 'turning projects off forgets every project',
            actions: [{type: ProjectReceived, project: project('c1')}, {type: ProjectChannelsEnabledSet, projectChannelsEnabled: false}],
            expected: [],
        },
    ])('$name', ({actions, expected}) => {
        const state = apply(actions);
        expect(Object.values(state.byChannelId).map((p) => `${p.channel_id}:${p.state}`).sort()).toEqual(expected);
    });

    test('a project arriving clears the not-a-project mark of its channel', () => {
        const state = apply([{type: ProjectMissing, channelId: 'c1'}, {type: ProjectReceived, project: project('c1')}]);
        expect(state.notProject).toEqual({});
    });

    test('a project is never marked as not a project', () => {
        const state = apply([{type: ProjectReceived, project: project('c1')}, {type: ProjectMissing, channelId: 'c1'}]);
        expect(state.notProject).toEqual({});
    });
});

describe('projectAgents reducer', () => {
    test('is emptied when project channels are turned off', () => {
        const initial = pluginReducer({} as never, {type: 'NONE'});
        const stored = pluginReducer(initial, {type: ProjectAgentsSet, projectAgents: [{id: 'a'}]});
        expect(stored.projectAgents).toEqual([{id: 'a'}]);

        const off = pluginReducer(stored, {type: ProjectChannelsEnabledSet, projectChannelsEnabled: false});

        expect(off.projectAgents).toEqual([]);
    });
});
