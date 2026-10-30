// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor, within} from '@testing-library/react';
import {IntlProvider} from 'react-intl';
import {Provider} from 'react-redux';
import {createStore} from 'redux';

import ProductSidebar from './product_sidebar';
import SidebarAddMenu from './sidebar_add_menu';
import {AGENTS_ROUTE, NEW_AGENT_PATH, buildChatPath} from './use_agents_route';

let mockBots: Array<{id: string; displayName: string; lastIconUpdate: number}> | null = [];
jest.mock('@/bots', () => ({
    useBotlist: () => ({bots: mockBots}),
}));
jest.mock('@/client', () => ({
    getProfilePictureUrl: (id: string) => `/api/v4/users/${id}/image`,
    getAIThreads: jest.fn().mockResolvedValue([]),
    getMySettingsAccess: jest.fn().mockResolvedValue('none'),
}));
jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children}: {children: React.ReactNode}) => <>{children}</>,
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
    };
    return {...actual, useIntl: () => intl};
});

const bot = (id: string, displayName: string) => ({id, displayName, lastIconUpdate: 0});

const makeState = (permissions: string[]) => ({
    entities: {
        users: {currentUserId: 'u1', profiles: {u1: {roles: 'system_user'}}},
        roles: {roles: {system_user: {permissions}}},
    },
});

const makeHistory = (initial: string) => {
    const history = {
        location: {pathname: initial},
        push: jest.fn(),
        listen: () => () => null,
    };
    return history;
};

const renderMenu = (permissions: string[] = ['manage_own_agent']) => {
    const history = makeHistory(AGENTS_ROUTE);
    window.WebappUtils = {browserHistory: history} as any;
    render(
        <IntlProvider
            locale='en'
            onError={() => null}
        >
            <Provider store={createStore(() => makeState(permissions))}>
                <SidebarAddMenu/>
            </Provider>
        </IntlProvider>,
    );
    return history;
};

// By test id: while the menu is open its focus manager hides the rest of the page.
const trigger = () => screen.getByTestId('sidebar-add-menu-button');

// The focus manager moves focus onto the first item shortly after opening.
const openMenu = async () => {
    fireEvent.click(trigger());
    const menu = screen.getByRole('menu', {name: 'Add agent or chat'});
    await waitFor(() => expect(menu.contains(document.activeElement)).toBe(true));
    return menu;
};
const newChatRow = () => screen.getByRole('menuitem', {name: /New chat/});
const openSubmenu = () => {
    fireEvent.click(newChatRow());
    return screen.getByRole('menu', {name: 'Choose an agent'});
};

describe('SidebarAddMenu', () => {
    beforeEach(() => {
        mockBots = [bot('a', 'Alpha'), bot('b', 'Beta')];
    });

    afterEach(() => {
        delete window.WebappUtils;
    });

    test('the plus button is labelled, announces its menu and opens and closes it', async () => {
        renderMenu();
        expect(trigger().getAttribute('aria-haspopup')).toBe('menu');
        expect(trigger().getAttribute('aria-expanded')).toBe('false');
        expect(screen.queryByRole('menu')).toBeNull();

        await openMenu();
        expect(trigger().getAttribute('aria-expanded')).toBe('true');

        fireEvent.click(trigger());
        await waitFor(() => expect(trigger().getAttribute('aria-expanded')).toBe('false'));
    });

    test('ArrowDown on the button opens the menu', () => {
        renderMenu();
        fireEvent.keyDown(trigger(), {key: 'ArrowDown'});
        expect(screen.getByRole('menu', {name: 'Add agent or chat'})).toBeTruthy();
    });

    test('Escape closes the menu and returns focus to the button', async () => {
        renderMenu();
        const menu = await openMenu();
        fireEvent.keyDown(menu, {key: 'Escape'});
        await waitFor(() => expect(trigger().getAttribute('aria-expanded')).toBe('false'));
        await waitFor(() => expect(document.activeElement).toBe(trigger()));
    });

    const permissionCases = [
        {name: 'manage_own_agent', permissions: ['manage_own_agent'], shown: true},
        {name: 'manage_system', permissions: ['manage_system'], shown: true},
        {name: 'no agent permission', permissions: ['manage_others_agent'], shown: false},
        {name: 'no permissions at all', permissions: [], shown: false},
    ];
    test.each(permissionCases)('Create new agent with $name: shown=$shown', async ({permissions, shown}) => {
        renderMenu(permissions);
        await openMenu();
        expect(screen.queryByRole('menuitem', {name: 'Create new agent'}) !== null).toBe(shown);

        // New chat comes first; Create new agent, when allowed, is second.
        expect(screen.getAllByRole('menuitem').map((el) => el.textContent)).toEqual(shown ? ['New chat', 'Create new agent'] : ['New chat']);
    });

    test('Create new agent navigates to the create route and closes the menu', async () => {
        const history = renderMenu();
        await openMenu();
        fireEvent.click(screen.getByRole('menuitem', {name: 'Create new agent'}));
        expect(history.push).toHaveBeenCalledWith(NEW_AGENT_PATH);
        await waitFor(() => expect(trigger().getAttribute('aria-expanded')).toBe('false'));
    });

    test('New chat lists the agents in bot order and starts a new chat with the pick', async () => {
        const history = renderMenu();
        await openMenu();
        const submenu = openSubmenu();
        expect(within(submenu).getAllByRole('menuitem').map((el) => el.textContent)).toEqual(['Alpha', 'Beta']);

        fireEvent.click(within(submenu).getByRole('menuitem', {name: 'Beta'}));
        expect(history.push).toHaveBeenCalledWith(buildChatPath('b'));
        await waitFor(() => expect(screen.queryByRole('menu', {name: 'Choose an agent'})).toBeNull());
        await waitFor(() => expect(trigger().getAttribute('aria-expanded')).toBe('false'));
    });

    const stateCases = [
        {name: 'loading', bots: null, text: 'Loading agents…'},
        {name: 'empty', bots: [], text: 'No agents available'},
    ];
    test.each(stateCases)('submenu $name state is explained, never silent', async ({bots, text}) => {
        mockBots = bots;
        renderMenu();
        await openMenu();
        const submenu = openSubmenu();
        expect(within(submenu).getByText(text)).toBeTruthy();
        if (bots) {
            expect((within(submenu).getByRole('menuitem', {name: text}) as HTMLButtonElement).disabled).toBe(true);
        }
    });

    test('ArrowRight opens the submenu and focuses its first agent; ArrowLeft and Escape close only the submenu', async () => {
        renderMenu();
        await openMenu();
        newChatRow().focus();
        fireEvent.keyDown(newChatRow(), {key: 'ArrowRight'});
        const submenu = screen.getByRole('menu', {name: 'Choose an agent'});
        await waitFor(() => expect(document.activeElement).toBe(within(submenu).getByRole('menuitem', {name: 'Alpha'})));

        fireEvent.keyDown(document.activeElement as HTMLElement, {key: 'ArrowDown'});
        expect(document.activeElement).toBe(within(submenu).getByRole('menuitem', {name: 'Beta'}));

        fireEvent.keyDown(document.activeElement as HTMLElement, {key: 'ArrowLeft'});
        await waitFor(() => expect(screen.queryByRole('menu', {name: 'Choose an agent'})).toBeNull());
        expect(screen.getByRole('menu', {name: 'Add agent or chat'})).toBeTruthy();
        expect(document.activeElement).toBe(newChatRow());

        fireEvent.keyDown(newChatRow(), {key: 'ArrowRight'});
        fireEvent.keyDown(screen.getByRole('menu', {name: 'Choose an agent'}), {key: 'Escape'});
        await waitFor(() => expect(screen.queryByRole('menu', {name: 'Choose an agent'})).toBeNull());
        expect(screen.getByRole('menu', {name: 'Add agent or chat'})).toBeTruthy();
    });
});

describe('ProductSidebar plus menu', () => {
    afterEach(() => {
        delete window.WebappUtils;
    });

    const sidebar = () => {
        const body = (
            <ProductSidebar
                activeView='all'
                onSelectAll={jest.fn()}
                onSelectPrompts={jest.fn()}
                onSelectSettings={jest.fn()}
            />
        );
        render(
            <IntlProvider
                locale='en'
                onError={() => null}
            >
                <Provider store={createStore(() => makeState([]))}>{body}</Provider>
            </IntlProvider>,
        );
    };

    test('sits in the Agents header row', () => {
        mockBots = [];
        sidebar();
        const heading = screen.getByRole('heading', {name: 'Agents'});
        expect(heading.parentElement?.contains(trigger())).toBe(true);
    });
});
