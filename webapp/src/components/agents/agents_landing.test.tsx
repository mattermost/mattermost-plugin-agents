// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';

import AgentsLanding, {resolveDefaultAgent} from './agents_landing';

const mockUseBotlist = jest.fn();
jest.mock('@/bots', () => ({useBotlist: () => mockUseBotlist()}));

jest.mock('./agent_chat_page', () => ({
    __esModule: true,
    default: ({botId}: {botId: string}) => <div>{`chat ${botId}`}</div>,
}));
const bot = (id: string, isDefault?: boolean) => ({id, displayName: id, isDefault});

const nav = {toAll: jest.fn(), replaceWithAll: jest.fn(), toNewAgent: jest.fn(), closeNewAgent: jest.fn(), toPrompts: jest.fn(), toChat: jest.fn()};

describe('resolveDefaultAgent', () => {
    test.each([
        {name: 'flagged default', bots: [bot('a'), bot('b', true)], expected: 'b'},
        {name: 'no flag: never the first bot', bots: [bot('a'), bot('b')], expected: null},
        {name: 'empty list', bots: [], expected: null},
        {name: 'not loaded', bots: null, expected: null},
    ])('$name', ({bots, expected}) => {
        expect(resolveDefaultAgent(bots as any)?.id ?? null).toBe(expected);
    });
});

describe('AgentsLanding', () => {
    beforeEach(() => {
        nav.replaceWithAll.mockClear();
    });

    test('renders a new chat with the default agent', () => {
        mockUseBotlist.mockReturnValue({bots: [bot('a'), bot('b', true)]});
        render(<AgentsLanding nav={nav}/>);
        expect(screen.getByText('chat b')).toBeTruthy();
        expect(nav.replaceWithAll).not.toHaveBeenCalled();
    });

    test.each([
        {name: 'unset default', bots: [bot('a'), bot('b')]},
        {name: 'no accessible agents', bots: []},
    ])('hands over to All agents: $name', ({bots}) => {
        mockUseBotlist.mockReturnValue({bots});
        render(<AgentsLanding nav={nav}/>);
        expect(nav.replaceWithAll).toHaveBeenCalledTimes(1);
        expect(screen.queryByText(/^chat /)).toBeNull();
    });

    test('waits for the agent list before choosing', () => {
        mockUseBotlist.mockReturnValue({bots: null});
        render(<AgentsLanding nav={nav}/>);
        expect(nav.replaceWithAll).not.toHaveBeenCalled();
        expect(screen.queryByText(/^chat /)).toBeNull();
    });
});
