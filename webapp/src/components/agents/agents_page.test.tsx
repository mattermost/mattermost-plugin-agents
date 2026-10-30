// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, render, screen} from '@testing-library/react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';

import manifest from '@/manifest';

import AgentsPage, {AGENTS_ROUTE} from './agents_page';

jest.mock('@/client', () => ({
    getAIBots: jest.fn(() => new Promise(() => null)),
}));

// The sidebar has its own tests; here it only navigates.
jest.mock('./product_sidebar', () => ({
    __esModule: true,
    default: ({onSelectAll, onSelectPrompts, onSelectSettings}: {onSelectAll: () => void; onSelectPrompts: () => void; onSelectSettings: () => void}) => (
        <nav>
            <button
                type='button'
                onClick={onSelectSettings}
            >{'Settings'}</button>
            <button
                type='button'
                onClick={onSelectAll}
            >{'All agents'}</button>
            <button
                type='button'
                onClick={onSelectPrompts}
            >{'Custom Prompts'}</button>
        </nav>
    ),
}));

// The host mounts root components only outside product routes; the page mounts the Custom Prompts modal.
jest.mock('@/components/custom_prompts/custom_prompts_management', () => ({__esModule: true, default: () => <div data-testid='custom-prompts-modal-root'/>}));
jest.mock('@/components/custom_prompts/custom_prompts_page', () => ({__esModule: true, default: () => <div>{'prompts view'}</div>}));
jest.mock('./agents_list', () => ({__esModule: true, default: ({openCreate}: {openCreate: boolean}) => <div>{openCreate ? 'create open' : 'agents list'}</div>}));
jest.mock('./settings/agents_settings_page', () => ({__esModule: true, default: ({section}: {section?: string}) => <div>{`settings ${section ?? 'default'}`}</div>}));
jest.mock('./agent_chat_page', () => ({__esModule: true, default: ({botId, rootId}: {botId: string; rootId?: string}) => <div>{`chat ${botId} ${rootId ?? 'new'}`}</div>}));

const makeHistory = (initial: string) => {
    const listeners = new Set<() => void>();
    const history = {
        location: {pathname: initial},
        push: jest.fn((path: string) => {
            history.location = {pathname: path};
            listeners.forEach((l) => l());
        }),
        replace: jest.fn((path: string) => {
            history.location = {pathname: path};
            listeners.forEach((l) => l());
        }),
        listen: (l: () => void) => {
            listeners.add(l);
            return () => listeners.delete(l);
        },
    };
    return history;
};

const bot = (id: string, isDefault = false) => ({id, displayName: id, isDefault});

const renderPage = (path: string, bots: Array<ReturnType<typeof bot>> | null = [], selected = '') => {
    const history = makeHistory(path);
    window.WebappUtils = {browserHistory: history} as any;
    const state = {
        [`plugins-${manifest.id}`]: {bots},
        entities: {
            users: {currentUserId: 'me'},
            preferences: {myPreferences: selected ? {'agents--selected_agent': {value: selected}} : {}},
        },
    };
    render(
        <Provider store={createStore(() => state)}>
            <AgentsPage/>
        </Provider>,
    );
    return history;
};

describe('AgentsPage', () => {
    afterEach(() => {
        delete window.WebappUtils;
    });

    test('mounts the Custom Prompts modal', () => {
        renderPage(`${AGENTS_ROUTE}/all`);
        expect(screen.getByTestId('custom-prompts-modal-root')).toBeTruthy();
    });

    test('each route shows its view, and the sidebar navigates between them', () => {
        const history = renderPage(`${AGENTS_ROUTE}/all`);
        expect(screen.getByText('agents list')).toBeTruthy();

        act(() => screen.getByRole('button', {name: 'Custom Prompts'}).click());
        expect(history.push).toHaveBeenCalledWith(`${AGENTS_ROUTE}/prompts`);
        expect(screen.getByText('prompts view')).toBeTruthy();

        act(() => screen.getByRole('button', {name: 'All agents'}).click());
        expect(history.push).toHaveBeenCalledWith(`${AGENTS_ROUTE}/all`);
        expect(screen.getByText('agents list')).toBeTruthy();

        act(() => history.push(`${AGENTS_ROUTE}/chat/bot1/root1`));
        expect(screen.getByText('chat bot1 root1')).toBeTruthy();

        act(() => history.push(`${AGENTS_ROUTE}/all/new`));
        expect(screen.getByText('create open')).toBeTruthy();

        act(() => screen.getByRole('button', {name: 'Settings'}).click());
        expect(history.push).toHaveBeenCalledWith(`${AGENTS_ROUTE}/settings`);
        expect(screen.getByText('settings default')).toBeTruthy();

        act(() => history.push(`${AGENTS_ROUTE}/settings/mcp`));
        expect(screen.getByText('settings mcp')).toBeTruthy();
    });

    test.each([
        {name: 'the selected agent', bots: [bot('a'), bot('b', true)], selected: 'a', expected: `${AGENTS_ROUTE}/chat/a`, view: 'chat a new'},
        {name: 'the default agent', bots: [bot('a'), bot('b', true)], selected: '', expected: `${AGENTS_ROUTE}/chat/b`, view: 'chat b new'},
        {name: 'All agents without either', bots: [bot('a'), bot('b')], selected: '', expected: `${AGENTS_ROUTE}/all`, view: 'agents list'},
    ])('the product root is replaced by $name', ({bots, selected, expected, view}) => {
        const history = renderPage(AGENTS_ROUTE, bots, selected);
        expect(history.replace).toHaveBeenCalledWith(expected);
        expect(history.push).not.toHaveBeenCalled();
        expect(screen.getByText(view)).toBeTruthy();
    });
});
