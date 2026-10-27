// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {act, fireEvent, render, screen} from '@testing-library/react';

import manifest from '@/manifest';
import {PREFERENCE_CATEGORY_AGENTS, PREFERENCE_NAME_SELECTED_AGENT} from '@/bots';

import RHSTitle from './rhs_title';

const mockSavePreferences = jest.fn();
jest.mock('@/client', () => ({
    getProfilePictureUrl: (id: string) => `/users/${id}/image`,
    getAIBots: jest.fn(() => Promise.resolve(null)),
    getUserMCPTools: jest.fn(() => Promise.resolve({servers: []})),
    getUserToolPreferences: jest.fn(() => Promise.resolve({disabled_servers: []})),
    savePreferences: (...args: unknown[]) => mockSavePreferences(...args),
}));

jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children}: {children: React.ReactNode}) => <>{children}</>,
}));

// The popover's own behavior is covered by tool_provider_popover.test.tsx.
jest.mock('./tool_provider_popover', () => ({
    __esModule: true,
    default: () => <button type='button'>{'Tools'}</button>,
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}, values?: Record<string, string>) => (
            defaultMessage.replace(/\{(\w+)\}/g, (_m, k) => values?.[k] ?? '')
        ),
    };
    return {...actual, useIntl: () => intl, FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage};
});

const pluginKey = `plugins-${manifest.id}`;
const preferenceKey = `${PREFERENCE_CATEGORY_AGENTS}--${PREFERENCE_NAME_SELECTED_AGENT}`;

const makeBot = (id: string, displayName: string): {id: string; displayName: string; username: string; lastIconUpdate: number; dmChannelID: string; useServiceAccountAuth?: boolean} => ({
    id,
    displayName,
    username: id,
    lastIconUpdate: 0,
    dmChannelID: `dm-${id}`,
});

// A reducer that applies the preference the title writes, so the title re-renders.
const dispatched: Array<{type: string; postId?: string}> = [];

const makeStore = (bots: ReturnType<typeof makeBot>[] | null, selected: string, rhs: {selectedPostId?: string; rhsTab?: string} = {}) => {
    const initial = {
        [pluginKey]: {bots, ...rhs},
        entities: {
            users: {currentUserId: 'me'},
            preferences: {myPreferences: selected ? {[preferenceKey]: {value: selected}} : {}},
        },
    };
    const reducer = (state: any = initial, action: any) => {
        dispatched.push(action);
        if (action.type === 'RECEIVED_PREFERENCES') {
            const pref = action.data[0];
            return {...state, entities: {...state.entities, preferences: {myPreferences: {[preferenceKey]: {value: pref.value}}}}};
        }
        return state;
    };
    return createStore(reducer);
};

const renderTitle = (bots: ReturnType<typeof makeBot>[] | null, selected: string, rhs: {selectedPostId?: string; rhsTab?: string} = {}) => render(
    <Provider store={makeStore(bots, selected, rhs)}>
        <RHSTitle/>
    </Provider>,
);

const two = [makeBot('a', 'Alpha'), makeBot('b', 'Beta')];

describe('RHSTitle', () => {
    beforeEach(() => {
        mockSavePreferences.mockReset();
        mockSavePreferences.mockResolvedValue(true);
        dispatched.length = 0;
    });

    const rhsActions = () => dispatched.filter((a) => a.type === 'SELECT_AI_POST' || a.type === 'AI_RHS_NEW_CHAT');

    test.each([
        ['bots still loading', null],
        ['no agents', []],
    ])('falls back to the generic title with %s', (_name, bots) => {
        renderTitle(bots, '');

        expect(screen.getByText('Agents')).not.toBeNull();
        expect(screen.queryByRole('button', {name: /choose agent/})).toBeNull();
    });

    test('a single agent is a plain title without a menu', () => {
        renderTitle([makeBot('a', 'Alpha')], '');

        expect(screen.getByText('Alpha')).not.toBeNull();
        expect(screen.queryByRole('button', {name: /choose agent/})).toBeNull();
        expect(screen.queryByText('Agents')).toBeNull();
    });

    test('with several agents shows the active agent as a labelled menu button', () => {
        renderTitle(two, 'b');

        const button = screen.getByRole('button', {name: 'Beta, choose agent'});
        expect(button.textContent).toBe('Beta');
        expect(button.getAttribute('aria-haspopup')).toBe('menu');
        expect(button.getAttribute('aria-expanded')).toBe('false');
        expect(document.querySelector('h1,h2,h3')).toBeNull();
    });

    test('lists the agents, and choosing one saves it and updates the title', () => {
        renderTitle(two, 'a');

        fireEvent.click(screen.getByRole('button', {name: 'Alpha, choose agent'}));
        expect(screen.getByTestId('bot-selector-rhs').getAttribute('aria-expanded')).toBe('true');
        expect(screen.getByRole('menu')).not.toBeNull();
        expect(screen.getAllByRole('menuitem').map((i) => i.textContent)).toEqual(['Alpha', 'Beta']);

        fireEvent.click(screen.getByRole('menuitem', {name: 'Beta'}));

        expect(mockSavePreferences).toHaveBeenCalledWith('me', [expect.objectContaining({name: PREFERENCE_NAME_SELECTED_AGENT, value: 'b'})]);
        expect(screen.getByTestId('bot-selector-rhs').textContent).toBe('Beta');
        expect(screen.getByTestId('bot-selector-rhs').getAttribute('aria-expanded')).toBe('false');
    });

    test('switching to another agent behaves like starting a new chat with it', () => {
        renderTitle(two, 'a');

        fireEvent.click(screen.getByRole('button', {name: 'Alpha, choose agent'}));
        fireEvent.click(screen.getByRole('menuitem', {name: 'Beta'}));

        expect(mockSavePreferences).toHaveBeenCalledTimes(1);
        expect(rhsActions()).toEqual([
            {type: 'SELECT_AI_POST', postId: ''},
            {type: 'AI_RHS_NEW_CHAT'},
        ]);
    });

    test('choosing the current agent changes nothing and just closes the menu', () => {
        renderTitle(two, 'a');

        fireEvent.click(screen.getByRole('button', {name: 'Alpha, choose agent'}));
        fireEvent.click(screen.getByRole('menuitem', {name: 'Alpha'}));

        expect(mockSavePreferences).not.toHaveBeenCalled();
        expect(rhsActions()).toEqual([]);
        expect(screen.getByTestId('bot-selector-rhs').getAttribute('aria-expanded')).toBe('false');
    });

    test('opens from the keyboard and closes with Escape', async () => {
        renderTitle(two, 'a');
        const button = screen.getByRole('button', {name: 'Alpha, choose agent'});

        // A native button turns Enter/Space into a click; the click is the keyboard path.
        button.focus();
        expect(document.activeElement).toBe(button);
        fireEvent.click(button);
        expect(button.getAttribute('aria-expanded')).toBe('true');

        await act(async () => {
            fireEvent.keyDown(document.activeElement ?? document.body, {key: 'Escape'});
        });
        expect(button.getAttribute('aria-expanded')).toBe('false');
    });
    describe('Back and tools', () => {
        const backButton = () => screen.queryByRole('button', {name: 'Back to chat history'});
        const toolsButton = () => screen.queryByRole('button', {name: 'Tools'});

        test.each([
            {name: 'new chat', rhs: {}, back: true, tools: true},
            {name: 'an open thread', rhs: {selectedPostId: 'root1'}, back: true, tools: false},
            {name: 'the chat history', rhs: {rhsTab: 'threads'}, back: false, tools: false},
        ])('on $name: back=$back, tools=$tools', ({rhs, back, tools}) => {
            renderTitle(two, 'a', rhs);

            expect(backButton() !== null).toBe(back);
            expect(toolsButton() !== null).toBe(tools);
        });

        test('the chat history has the generic Agents title: no agent menu, no Back, no tools', () => {
            renderTitle(two, 'a', {rhsTab: 'threads'});

            expect(screen.getByText('Agents')).not.toBeNull();
            expect(screen.queryByRole('button', {name: /choose agent/})).toBeNull();
            expect(screen.queryByText('Alpha')).toBeNull();
        });

        test('Back leaves the open chat and shows the chat history', () => {
            renderTitle(two, 'a', {selectedPostId: 'root1'});

            fireEvent.click(backButton()!);

            expect(dispatched.filter((a) => a.type === 'SELECT_AI_POST' || a.type === 'AI_RHS_SET_TAB')).toEqual([
                {type: 'SELECT_AI_POST', postId: ''},
                {type: 'AI_RHS_SET_TAB', tab: 'threads'},
            ]);
        });

        test('Back is also there while the agents are loading or there is a single agent', () => {
            const {unmount} = renderTitle(null, '');
            expect(backButton()).not.toBeNull();
            unmount();

            renderTitle([makeBot('a', 'Alpha')], '');
            expect(backButton()).not.toBeNull();
            expect(toolsButton()).not.toBeNull();
        });

        test('no tools button for a service account agent', () => {
            renderTitle([{...makeBot('a', 'Alpha'), useServiceAccountAuth: true}, makeBot('b', 'Beta')], 'a');

            expect(toolsButton()).toBeNull();
        });
    });
});
