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

    test('renders user, team, channel and post IDs as names, in arrays and scalars', async () => {
        mockGetProfilesByIds.mockResolvedValue([
            {id: 'u1', username: 'alice'},
            {id: 'u2', username: 'bob'},
        ]);
        mockGetTeamsByIds.mockResolvedValue([{id: 't1', name: 'eng', display_name: 'Engineering'}]);
        mockGetChannel.mockResolvedValue({id: 'c1', name: 'town-square', display_name: 'Town Square'});
        mockGetPost.mockResolvedValue({id: 'p1', message: 'the   quarterly\nplan'});

        renderCard({user_ids: ['u1', 'u2'], team_id: 't1', channel_id: 'c1', root_id: 'p1', limit: 5});

        expect(await screen.findByText('alice')).not.toBeNull();
        expect(await screen.findByText('bob')).not.toBeNull();
        expect(await screen.findByText('Engineering')).not.toBeNull();
        expect(await screen.findByText('Town Square')).not.toBeNull();
        expect(await screen.findByText('the quarterly plan')).not.toBeNull();
        expect(screen.queryByText('u1')).toBeNull();
        expect(screen.queryByText('c1')).toBeNull();
        expect(screen.getByText('5')).not.toBeNull();
    });

    test('keeps the raw ID when it cannot be resolved and ignores non-ID keys', async () => {
        renderCard({channel_id: 'gone', message: 'u1'});

        expect(await screen.findByText('gone')).not.toBeNull();
        expect(mockGetChannel).toHaveBeenCalledWith('gone');
        expect(mockGetProfilesByIds).not.toHaveBeenCalled();
        expect(screen.getByText('u1')).not.toBeNull();
    });

    test('resolves a DM channel that has no team', async () => {
        mockGetChannel.mockResolvedValue({id: 'dm1', name: 'u1__u2', display_name: '', team_id: ''});

        renderCard({channel_id: 'dm1'});

        expect(await screen.findByText('u1__u2')).not.toBeNull();
    });

    test('one unresolvable team does not discard the others', async () => {
        mockGetTeamsByIds.mockImplementation(async ([id]: string[]) => {
            if (id === 'gone') {
                throw new Error('404');
            }
            return [{id, name: id, display_name: `Team ${id}`}];
        });

        renderCard({team_ids: ['t1', 'gone', 't2']});

        expect(await screen.findByText('Team t1')).not.toBeNull();
        expect(await screen.findByText('Team t2')).not.toBeNull();
        expect(screen.getByText('gone')).not.toBeNull();
    });

    test('escapes bidi and invisible characters in resolved names', async () => {
        mockGetChannel.mockResolvedValue({id: 'c1', name: 'c1', display_name: 'safe\u202Eevil\u200B'});

        renderCard({channel_id: 'c1'});

        const label = await screen.findByText('safe[U+202E]evil[U+200B]');
        expect(label.getAttribute('title')).toBe('c1');
    });

    test('mixed kinds resolve with one request per kind and no refetch', async () => {
        mockGetProfilesByIds.mockResolvedValue([{id: 'u1', username: 'alice'}, {id: 'u2', username: 'bob'}]);
        mockGetChannel.mockResolvedValue({id: 'c1', name: 'c1', display_name: 'Town Square'});

        const view = renderCard({user_ids: ['u1', 'u2'], channel_id: 'c1'});
        expect(await screen.findByText('alice')).not.toBeNull();
        expect(await screen.findByText('Town Square')).not.toBeNull();

        view.rerender(cardElement({user_ids: ['u1', 'u2'], channel_id: 'c1', limit: 3}));
        expect(await screen.findByText('3')).not.toBeNull();

        expect(mockGetProfilesByIds).toHaveBeenCalledTimes(1);
        expect(mockGetChannel).toHaveBeenCalledTimes(1);
    });

    test('a failed lookup is not retried and does not disturb the others', async () => {
        mockGetProfilesByIds.mockResolvedValue([{id: 'u1', username: 'alice'}]);

        const view = renderCard({user_id: 'u1', channel_id: 'gone'});
        expect(await screen.findByText('alice')).not.toBeNull();
        view.rerender(cardElement({user_id: 'u1', channel_id: 'gone', limit: 3}));
        expect(await screen.findByText('3')).not.toBeNull();

        expect(screen.getByText('gone')).not.toBeNull();
        expect(mockGetChannel).toHaveBeenCalledTimes(1);
    });

    test('keeps names from a lookup that finishes after the arguments change', async () => {
        let resolveOld: (value: unknown) => void = jest.fn();
        mockGetChannel.mockImplementation((id: string) => {
            if (id === 'c1') {
                return new Promise((resolve) => {
                    resolveOld = resolve;
                });
            }
            return Promise.resolve({id, name: id, display_name: `Channel ${id}`});
        });

        const view = renderCard({channel_id: 'c1'});
        view.rerender(cardElement({channel_ids: ['c1', 'c2']}));
        expect(await screen.findByText('Channel c2')).not.toBeNull();

        resolveOld({id: 'c1', name: 'c1', display_name: 'Channel c1'});
        expect(await screen.findByText('Channel c1')).not.toBeNull();
        expect(mockGetChannel).toHaveBeenCalledTimes(2);
    });
});
