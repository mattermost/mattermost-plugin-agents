// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

import {getAIThreads} from '@/client';
import type {AIThread} from '@/types/ai_thread';

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

    test('empty state lists recent agents only, most recently active first', async () => {
        await openModal();
        await waitFor(() => expect(screen.queryByRole('status')).toBeNull());
        expect(optionNames()).toEqual(['Beta@beta', 'Alpha@alpha', 'Gamma@gamma']);
        expect(screen.getByText('Recent')).toBeTruthy();
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
        expect(selectedName()).toBe('Gamma@gamma');
        fireEvent.keyDown(input, {key: 'ArrowDown'});
        expect(selectedName()).toBe('Beta@beta');
        fireEvent.keyDown(input, {key: 'ArrowDown'});
        expect(selectedName()).toBe('Alpha@alpha');
    });

    test('hover sets the selection', async () => {
        await openModal();
        const options = screen.getAllByRole('option');
        fireEvent.mouseMove(options[2]);
        expect(screen.getAllByRole('option')[2].getAttribute('aria-selected')).toBe('true');
    });

    const selectionCases: Array<{name: string; query: string; pick: string; path: string}> = [
        {name: 'agent with chats opens its most recent chat', query: '', pick: 'Beta@beta', path: buildChatPath('b', 'root_1')},
        {name: 'agent without chats starts a new chat', query: '', pick: 'Alpha@alpha', path: buildChatPath('a')},
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
