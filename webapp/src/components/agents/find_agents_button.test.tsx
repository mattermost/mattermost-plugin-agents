// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

import {getAIThreads} from '@/client';
import type {AIThread} from '@/types/ai_thread';

import {MAX_RECENT_CHATS} from './find_agents_search';
import FindAgentsButton from './find_agents_button';
import {buildChatPath} from './use_agents_route';

jest.mock('@/client', () => ({
    getAIThreads: jest.fn(),
    getProfilePictureUrl: () => 'avatar.png',
}));
jest.mock('@/bots', () => ({useBotlist: () => ({bots: mockBots})}));
jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        formatMessage: (descriptor: {id?: string; defaultMessage: string}, values?: Record<string, string | number>) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}, values),
    };
    return {...actual, useIntl: () => intl};
});

const mockBots = [
    {id: 'a', displayName: 'Alpha', username: 'alpha', dmChannelID: 'dm_a', lastIconUpdate: 0},
    {id: 'b', displayName: 'Beta', username: 'beta', dmChannelID: 'dm_b', lastIconUpdate: 0},
    {id: 'c', displayName: 'Gamma', username: 'gamma', dmChannelID: 'dm_c', lastIconUpdate: 0},
];

const thread = (id: string, botId: string, title: string, updateAt: number): AIThread => ({
    id,
    channel_id: `dm_${botId}`,
    bot_id: botId,
    root_post_id: `root_${id}`,
    title,
    turn_count: 1,
    update_at: updateAt,
});

const mockGetThreads = getAIThreads as jest.Mock;
const push = jest.fn();

const openModal = async () => {
    render(<FindAgentsButton/>);
    fireEvent.click(screen.getByRole('button', {name: 'Find agents'}));
    const input = await screen.findByRole('combobox');
    await waitFor(() => expect(document.activeElement).toBe(input));
    return input;
};

const optionNames = () => screen.queryAllByRole('option').map((o) => o.textContent);

describe('FindAgentsButton and modal', () => {
    beforeEach(() => {
        push.mockReset();
        window.WebappUtils = {browserHistory: {location: {pathname: '/plug/mattermost-ai/agents'}, push}} as any;
        mockGetThreads.mockReset();
        mockGetThreads.mockResolvedValue([
            thread('1', 'b', 'Quarterly plan', 20),
            thread('2', 'b', '', 10),
        ]);
    });

    afterEach(() => {
        delete window.WebappUtils;
    });

    test('empty state lists recent chats only, newest first', async () => {
        await openModal();
        await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
        expect(optionNames()).toEqual(['Quarterly planBeta', 'New chatBeta']);
        expect(screen.getByText('Recent chats')).toBeTruthy();
        expect(screen.queryByText('Recent agents')).toBeNull();
    });

    test('empty state caps recent chats and spans agents', async () => {
        mockGetThreads.mockResolvedValue([
            ...Array.from({length: MAX_RECENT_CHATS + 2}, (_, i) => thread(`n${i}`, 'a', `Chat ${i}`, 100 + i)),
            thread('c1', 'c', 'Gamma chat', 500),
            {...thread('x1', 'a', 'Wrong channel', 900), channel_id: 'other'},
        ]);
        await openModal();
        await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
        const names = optionNames();
        expect(names).toHaveLength(MAX_RECENT_CHATS);
        expect(names[0]).toBe('Gamma chatGamma');
        expect(names[1]).toBe(`Chat ${MAX_RECENT_CHATS + 1}Alpha`);
        expect(names).not.toContain('Wrong channelAlpha');
    });

    test('without chats the empty state lists recent agents in list order', async () => {
        mockGetThreads.mockResolvedValue([]);
        await openModal();
        await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
        expect(optionNames()).toEqual(['Alpha@alpha', 'Beta@beta', 'Gamma@gamma']);
        expect(screen.getByText('Recent agents')).toBeTruthy();
        expect(screen.queryByText('Recent chats')).toBeNull();
    });

    test('chats of an agent without a DM channel are left out', async () => {
        mockGetThreads.mockResolvedValue([thread('g1', 'ghost', 'Ghost chat', 99)]);
        await openModal();
        await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
        expect(optionNames()).toEqual(['Alpha@alpha', 'Beta@beta', 'Gamma@gamma']);
    });

    test('shows no agents while threads load, then the chats', async () => {
        let resolve: (v: AIThread[]) => void = () => null;
        mockGetThreads.mockReturnValue(new Promise<AIThread[]>((r) => {
            resolve = r;
        }));
        await openModal();
        expect(screen.queryAllByRole('option')).toHaveLength(0);
        expect(screen.queryByText('Recent agents')).toBeNull();
        resolve([thread('1', 'b', 'Quarterly plan', 20)]);
        await waitFor(() => expect(optionNames()).toEqual(['Quarterly planBeta']));
    });

    test('a failed fetch shows the error and falls back to recent agents', async () => {
        mockGetThreads.mockRejectedValue(new Error('boom'));
        await openModal();
        await screen.findByRole('alert');
        expect(optionNames()).toEqual(['Alpha@alpha', 'Beta@beta', 'Gamma@gamma']);
        expect(screen.getByText('Recent agents')).toBeTruthy();
    });

    test('combobox ARIA wires the active descendant to the selected option', async () => {
        const input = await openModal();
        const options = screen.getAllByRole('option');
        expect(options[0].getAttribute('aria-selected')).toBe('true');
        expect(input.getAttribute('aria-activedescendant')).toBe(options[0].id);
        expect(input.getAttribute('aria-controls')).toBe(screen.getByRole('listbox').id);
    });

    test('typing shows agents and chats sections', async () => {
        const input = await openModal();
        await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
        fireEvent.change(input, {target: {value: 'plan'}});
        expect(optionNames()).toEqual(['Quarterly planBeta']);
        fireEvent.change(input, {target: {value: 'new'}});
        expect(optionNames()).toEqual(['New chatBeta']);
        fireEvent.change(input, {target: {value: 'bet'}});
        expect(optionNames()).toEqual(['Beta@beta']);
    });

    test('shows no results for an unmatched query', async () => {
        const input = await openModal();
        await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
        fireEvent.change(input, {target: {value: 'zzz'}});
        expect(screen.getByText('No results for "zzz"')).toBeTruthy();
        expect(screen.queryAllByRole('option')).toHaveLength(0);
    });

    test('shows a loading status until threads arrive', async () => {
        let resolve: (v: AIThread[]) => void = () => null;
        mockGetThreads.mockReturnValue(new Promise<AIThread[]>((r) => {
            resolve = r;
        }));
        await openModal();
        expect(screen.getByRole('status').textContent).toBe('Loading…');
        resolve([]);
        await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
    });

    test('shows a visible error with retry instead of an empty list', async () => {
        mockGetThreads.mockRejectedValueOnce(new Error('boom'));
        const input = await openModal();
        const alert = await screen.findByRole('alert');
        expect(alert.textContent).toContain('Couldn\'t load your chats');
        fireEvent.change(input, {target: {value: 'zzz'}});
        expect(screen.queryByText(/No results/)).toBeNull();
        fireEvent.click(screen.getByRole('button', {name: 'Try again'}));
        await waitFor(() => expect(screen.queryByRole('alert')).toBeNull());
        expect(mockGetThreads).toHaveBeenCalledTimes(2);
    });

    test('keyboard: arrows wrap and move the selection', async () => {
        const input = await openModal();
        await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
        const selectedName = () => screen.getAllByRole('option').find((o) => o.getAttribute('aria-selected') === 'true')?.textContent;
        fireEvent.keyDown(input, {key: 'ArrowUp'});
        expect(selectedName()).toBe('New chatBeta');
        fireEvent.keyDown(input, {key: 'ArrowDown'});
        expect(selectedName()).toBe('Quarterly planBeta');
        fireEvent.keyDown(input, {key: 'ArrowDown'});
        expect(selectedName()).toBe('New chatBeta');
        fireEvent.keyDown(input, {key: 'Enter'});
        expect(push).toHaveBeenCalledWith(buildChatPath('b', 'root_2'));
    });

    test('hover sets the selection', async () => {
        await openModal();
        const options = screen.getAllByRole('option');
        fireEvent.mouseMove(options[1]);
        expect(screen.getAllByRole('option')[1].getAttribute('aria-selected')).toBe('true');
    });

    const selectionCases: Array<{name: string; query: string; pick: string; path: string}> = [
        {name: 'recent chat opens that chat', query: '', pick: 'New chatBeta', path: buildChatPath('b', 'root_2')},
        {name: 'agent with chats opens its most recent chat', query: 'bet', pick: 'Beta@beta', path: buildChatPath('b', 'root_1')},
        {name: 'agent without chats starts a new chat', query: 'alp', pick: 'Alpha@alpha', path: buildChatPath('a')},
        {name: 'chat opens that chat', query: 'new', pick: 'New chatBeta', path: buildChatPath('b', 'root_2')},
    ];

    test.each(selectionCases)('click: $name', async ({query, pick, path}) => {
        const input = await openModal();
        await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
        if (query) {
            fireEvent.change(input, {target: {value: query}});
        }
        fireEvent.click(screen.getAllByRole('option').find((o) => o.textContent === pick) as HTMLElement);
        expect(push).toHaveBeenCalledWith(path);
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    });

    test('Enter selects the highlighted row', async () => {
        const input = await openModal();
        await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
        fireEvent.keyDown(input, {key: 'Enter'});
        expect(push).toHaveBeenCalledWith(buildChatPath('b', 'root_1'));
    });

    test('Escape closes and focus returns to the button', async () => {
        const input = await openModal();
        fireEvent.keyDown(input, {key: 'Escape'});
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
        expect(document.activeElement).toBe(screen.getByRole('button', {name: 'Find agents'}));
        expect(push).not.toHaveBeenCalled();
    });
});

describe('Find agents keyboard shortcut', () => {
    const setPlatform = (platform: string) => jest.spyOn(window.navigator, 'platform', 'get').mockReturnValue(platform);

    beforeEach(() => {
        mockGetThreads.mockResolvedValue([]);
        window.WebappUtils = {browserHistory: {location: {pathname: '/plug/mattermost-ai/agents'}, push}} as any;
    });

    afterEach(() => {
        jest.restoreAllMocks();
    });

    const cases: Array<{name: string; platform: string; init: KeyboardEventInit; opens: boolean; hint: string}> = [
        {name: 'Cmd+K on Mac', platform: 'MacIntel', init: {key: 'k', metaKey: true}, opens: true, hint: '\u2318K'},
        {name: 'Ctrl+K on Mac is left alone', platform: 'MacIntel', init: {key: 'k', ctrlKey: true}, opens: false, hint: '\u2318K'},
        {name: 'Ctrl+K on Windows', platform: 'Win32', init: {key: 'k', ctrlKey: true}, opens: true, hint: 'Ctrl+K'},
        {name: 'Ctrl+K on Linux', platform: 'Linux x86_64', init: {key: 'K', code: 'KeyK', ctrlKey: true}, opens: true, hint: 'Ctrl+K'},
        {name: 'Win+K on Windows is left alone', platform: 'Win32', init: {key: 'k', metaKey: true}, opens: false, hint: 'Ctrl+K'},
        {name: 'Ctrl+Shift+K does not open', platform: 'Win32', init: {key: 'K', ctrlKey: true, shiftKey: true}, opens: false, hint: 'Ctrl+K'},
        {name: 'Ctrl+Alt+K does not open', platform: 'Win32', init: {key: 'k', ctrlKey: true, altKey: true}, opens: false, hint: 'Ctrl+K'},
        {name: 'plain K does not open', platform: 'Win32', init: {key: 'k'}, opens: false, hint: 'Ctrl+K'},
    ];

    test.each(cases)('$name', async ({platform, init, opens, hint}) => {
        setPlatform(platform);
        render(<FindAgentsButton/>);

        expect(screen.getByRole('button', {name: 'Find agents'}).textContent).toContain(hint);

        const event = new KeyboardEvent('keydown', {...init, bubbles: true, cancelable: true});
        document.dispatchEvent(event);

        if (opens) {
            expect(await screen.findByRole('combobox')).not.toBeNull();
            expect(event.defaultPrevented).toBe(true);
        } else {
            expect(screen.queryByRole('combobox')).toBeNull();
            expect(event.defaultPrevented).toBe(false);
        }
    });

    test('the shortcut toggles the modal closed and returns focus to the button', async () => {
        setPlatform('Win32');
        render(<FindAgentsButton/>);

        fireEvent.keyDown(document, {key: 'k', ctrlKey: true});
        await screen.findByRole('combobox');
        fireEvent.keyDown(document, {key: 'k', ctrlKey: true});

        await waitFor(() => expect(screen.queryByRole('combobox')).toBeNull());
    });

    test('advertises the shortcut to assistive technology and stops listening on unmount', () => {
        setPlatform('MacIntel');
        const {unmount} = render(<FindAgentsButton/>);
        expect(screen.getByRole('button', {name: 'Find agents'}).getAttribute('aria-keyshortcuts')).toBe('Meta+K');

        unmount();
        const event = new KeyboardEvent('keydown', {key: 'k', metaKey: true, bubbles: true, cancelable: true});
        document.dispatchEvent(event);

        expect(event.defaultPrevented).toBe(false);
        expect(screen.queryByRole('combobox')).toBeNull();
    });
});
