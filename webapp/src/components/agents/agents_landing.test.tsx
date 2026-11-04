// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {render} from '@testing-library/react';

import manifest from '@/manifest';

import AgentsLanding from './agents_landing';

jest.mock('@/client', () => ({
    getAIBots: jest.fn(() => new Promise(() => null)),
}));

const bot = (id: string, isDefault?: boolean) => ({id, displayName: id, isDefault});

const nav = {replaceWithAll: jest.fn(), replaceWithChat: jest.fn()};

const renderLanding = (bots: Array<ReturnType<typeof bot>> | null, selected = '') => {
    const state = {
        [`plugins-${manifest.id}`]: {bots},
        entities: {
            users: {currentUserId: 'me'},
            preferences: {myPreferences: selected ? {'agents--selected_agent': {value: selected}} : {}},
        },
    };
    return render(
        <Provider store={createStore(() => state)}>
            <AgentsLanding nav={nav}/>
        </Provider>,
    );
};

describe('AgentsLanding', () => {
    beforeEach(() => {
        nav.replaceWithAll.mockClear();
        nav.replaceWithChat.mockClear();
    });

    test.each([
        {name: 'the user\'s selected agent', bots: [bot('a'), bot('b', true), bot('c')], selected: 'c', expected: 'c'},
        {name: 'the default agent when none is selected', bots: [bot('a'), bot('b', true)], selected: '', expected: 'b'},
        {name: 'the default agent when the selected one is gone', bots: [bot('a'), bot('b', true)], selected: 'gone', expected: 'b'},
    ])('opens a new chat with $name, replacing the root', ({bots, selected, expected}) => {
        renderLanding(bots, selected);
        expect(nav.replaceWithChat).toHaveBeenCalledWith(expected);
        expect(nav.replaceWithAll).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'no selected or default agent (never the first one)', bots: [bot('a'), bot('b')]},
        {name: 'no accessible agents', bots: []},
    ])('hands over to All agents: $name', ({bots}) => {
        renderLanding(bots);
        expect(nav.replaceWithAll).toHaveBeenCalledTimes(1);
        expect(nav.replaceWithChat).not.toHaveBeenCalled();
    });

    test('waits for the agent list before choosing', () => {
        renderLanding(null, 'a');
        expect(nav.replaceWithAll).not.toHaveBeenCalled();
        expect(nav.replaceWithChat).not.toHaveBeenCalled();
    });
});
