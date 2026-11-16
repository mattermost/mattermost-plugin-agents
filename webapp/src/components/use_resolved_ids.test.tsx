// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getChannel, getPost, getProfilesByIds, getTeamsByIds} from '@/client';

import ToolCard from './tool_card';
import {ToolCall, ToolCallStatus} from './tool_types';
import {clearResolvedIdsForTest} from './use_resolved_ids';

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('@/client', () => ({
    getChannel: jest.fn(),
    getPost: jest.fn(),
    getProfilesByIds: jest.fn(),
    getTeamsByIds: jest.fn(),
}));

jest.mock('@/mm_webapp', () => ({
    Client4: {},
}));

const mockGetChannel = getChannel as jest.Mock;
const mockGetPost = getPost as jest.Mock;
const mockGetProfilesByIds = getProfilesByIds as jest.Mock;
const mockGetTeamsByIds = getTeamsByIds as jest.Mock;

const id = (seed: string) => seed.padEnd(26, '0');

function cardElement(args: ToolCall['arguments']) {
    const tool: ToolCall = {
        id: 'tool_1',
        name: 'some_tool',
        description: '',
        status: ToolCallStatus.Pending,
        arguments: args,
    };
    return (
        <IntlProvider locale='en'>
            <ToolCard
                tool={tool}
                isCollapsed={false}
                isProcessing={false}
                onToggleCollapse={jest.fn()}
                canExpand={false}
                showArguments={true}
                showResults={false}
            />
        </IntlProvider>
    );
}

function renderCard(args: ToolCall['arguments']) {
    return render(cardElement(args));
}

describe('tool call ID resolution', () => {
    beforeEach(() => {
        clearResolvedIdsForTest();
        jest.resetAllMocks();
        mockGetProfilesByIds.mockResolvedValue([]);
        mockGetTeamsByIds.mockResolvedValue([]);
        mockGetChannel.mockRejectedValue(new Error('not found'));
        mockGetPost.mockRejectedValue(new Error('not found'));
    });

    test('keeps every raw ID visible and adds names as secondary hints', async () => {
        mockGetProfilesByIds.mockResolvedValue([
            {id: id('u1'), username: 'alice'},
            {id: id('u2'), username: 'bob'},
        ]);
        mockGetTeamsByIds.mockImplementation(async ([teamId]: string[]) => [{id: teamId, name: 'eng', display_name: 'Engineering'}]);
        mockGetChannel.mockResolvedValue({id: id('c1'), name: 'town-square', display_name: 'Town Square', team_id: id('t1')});
        mockGetPost.mockResolvedValue({id: id('p1'), message: 'the   quarterly\nplan'});

        renderCard({user_ids: [id('u1'), id('u2')], team_id: id('t1'), channel_id: id('c1'), root_id: id('p1'), limit: 5});

        expect(await screen.findByText('@alice')).not.toBeNull();
        expect(await screen.findByText('@bob')).not.toBeNull();
        expect(await screen.findByText('Engineering · eng')).not.toBeNull();
        expect(await screen.findByText('Town Square · ~town-square · eng')).not.toBeNull();
        expect(await screen.findByText('the quarterly plan')).not.toBeNull();
        for (const raw of ['u1', 'u2', 't1', 'c1', 'p1']) {
            expect(screen.getAllByText(id(raw)).length).toBeGreaterThan(0);
        }
        expect(screen.getByText('5')).not.toBeNull();
    });

    test('never sends a malformed or path-like ID to the server', async () => {
        renderCard({
            channel_id: '../teams/' + id('t1'),
            team_ids: ['short', id('t2').toUpperCase()],
            user_id: id('u1') + '/x',
            post_id: 'p1?x=1',
        });

        expect(await screen.findByText('../teams/' + id('t1'))).not.toBeNull();
        await Promise.resolve();
        expect(mockGetChannel).not.toHaveBeenCalled();
        expect(mockGetTeamsByIds).not.toHaveBeenCalled();
        expect(mockGetProfilesByIds).not.toHaveBeenCalled();
        expect(mockGetPost).not.toHaveBeenCalled();
    });

    test('ignores a response whose ID does not match the request', async () => {
        mockGetChannel.mockResolvedValue({id: id('other'), name: 'secret-ops', display_name: 'Town Square', team_id: ''});

        renderCard({channel_id: id('c1')});

        expect(await screen.findByText(id('c1'))).not.toBeNull();
        await Promise.resolve();
        expect(screen.queryByText(/secret-ops/)).toBeNull();
    });

    test('keeps the raw ID when it cannot be resolved and ignores non-ID keys', async () => {
        renderCard({channel_id: id('gone'), message: id('u1')});

        expect(await screen.findByText(id('gone'))).not.toBeNull();
        expect(mockGetChannel).toHaveBeenCalledWith(id('gone'));
        expect(mockGetProfilesByIds).not.toHaveBeenCalled();
    });

    test('resolves a DM channel that has no team', async () => {
        mockGetChannel.mockResolvedValue({id: id('dm1'), name: 'u1__u2', display_name: '', team_id: ''});

        renderCard({channel_id: id('dm1')});

        expect(await screen.findByText('~u1__u2')).not.toBeNull();
        expect(mockGetTeamsByIds).not.toHaveBeenCalled();
    });

    test('one unresolvable team does not discard the others', async () => {
        mockGetTeamsByIds.mockImplementation(async ([teamId]: string[]) => {
            if (teamId === id('gone')) {
                throw new Error('404');
            }
            return [{id: teamId, name: 'n', display_name: `Team ${teamId.slice(0, 2)}`}];
        });

        renderCard({team_ids: [id('t1'), id('gone'), id('t2')]});

        expect(await screen.findByText('Team t1 · n')).not.toBeNull();
        expect(await screen.findByText('Team t2 · n')).not.toBeNull();
        expect(screen.getByText(id('gone'))).not.toBeNull();
    });

    test('a spoofed multi-line display name stays one escaped hint beside the raw ID', async () => {
        mockGetChannel.mockResolvedValue({
            id: id('c1'),
            name: 'secret-ops',
            display_name: 'Town Square\n~fake \u202Eevil\u200B',
            team_id: '',
        });

        renderCard({channel_id: id('c1')});

        const hint = await screen.findByText('Town Square ~fake [U+202E]evil[U+200B] · ~secret-ops');
        expect(hint.textContent).not.toContain('\n');
        expect(screen.getByText(id('c1'))).not.toBeNull();
    });

    test('shares one team lookup across channels and falls back to the team ID', async () => {
        mockGetChannel.mockImplementation(async (channelId: string) => ({id: channelId, name: channelId.slice(0, 2), display_name: '', team_id: id('t1')}));

        renderCard({channel_ids: [id('c1'), id('c2')]});

        expect(await screen.findByText(`~c1 · ${id('t1')}`)).not.toBeNull();
        expect(await screen.findByText(`~c2 · ${id('t1')}`)).not.toBeNull();
        expect(mockGetTeamsByIds).toHaveBeenCalledTimes(1);
    });

    test('mixed kinds resolve with one request per kind and no refetch', async () => {
        mockGetProfilesByIds.mockResolvedValue([{id: id('u1'), username: 'alice'}, {id: id('u2'), username: 'bob'}]);
        mockGetChannel.mockResolvedValue({id: id('c1'), name: 'town-square', display_name: '', team_id: ''});

        const view = renderCard({user_ids: [id('u1'), id('u2')], channel_id: id('c1')});
        expect(await screen.findByText('@alice')).not.toBeNull();
        expect(await screen.findByText('~town-square')).not.toBeNull();

        view.rerender(cardElement({user_ids: [id('u1'), id('u2')], channel_id: id('c1'), limit: 3}));
        expect(await screen.findByText('3')).not.toBeNull();

        expect(mockGetProfilesByIds).toHaveBeenCalledTimes(1);
        expect(mockGetChannel).toHaveBeenCalledTimes(1);
    });

    test('keeps hints from a lookup that finishes after the arguments change', async () => {
        let resolveOld: (value: unknown) => void = jest.fn();
        mockGetChannel.mockImplementation((channelId: string) => {
            if (channelId === id('c1')) {
                return new Promise((resolve) => {
                    resolveOld = resolve;
                });
            }
            return Promise.resolve({id: channelId, name: 'second', display_name: '', team_id: ''});
        });

        const view = renderCard({channel_id: id('c1')});
        view.rerender(cardElement({channel_ids: [id('c1'), id('c2')]}));
        expect(await screen.findByText('~second')).not.toBeNull();

        resolveOld({id: id('c1'), name: 'first', display_name: '', team_id: ''});
        expect(await screen.findByText('~first')).not.toBeNull();
        expect(mockGetChannel).toHaveBeenCalledTimes(2);
    });

    test('keeps each pill and its hint together in one item of the pill row', async () => {
        mockGetProfilesByIds.mockResolvedValue([{id: id('u1'), username: 'alice'}, {id: id('u2'), username: 'bob'}]);

        renderCard({user_ids: [id('u1'), id('u2')]});

        const hint = await screen.findByText('@alice');
        const pill = screen.getByText(id('u1'));
        expect(hint.parentElement).toBe(pill.parentElement);
        expect(pill.parentElement?.parentElement).toBe(screen.getByText(id('u2')).parentElement?.parentElement);
    });

    test('looks up at most a bounded number of IDs per kind and leaves the rest raw', async () => {
        const many = Array.from({length: 30}, (_, i) => id(`c${String(i).padStart(2, 'a')}`));
        mockGetChannel.mockImplementation(async (channelId: string) => ({id: channelId, name: 'n', display_name: '', team_id: ''}));

        renderCard({channel_ids: many});

        expect(await screen.findAllByText('~n')).toHaveLength(20);
        expect(mockGetChannel).toHaveBeenCalledTimes(20);
        expect(screen.getByText(many[29])).not.toBeNull();
    });
});
