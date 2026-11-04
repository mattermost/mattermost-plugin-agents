// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getChannelById, getPost, getProfilesByIds, getTeamsByIds} from '@/client';

import ToolCard from './tool_card';
import {ToolCall, ToolCallStatus} from './tool_types';
import {clearResolvedIdsForTest} from './use_resolved_ids';

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('@/client', () => ({
    getChannelById: jest.fn(),
    getPost: jest.fn(),
    getProfilesByIds: jest.fn(),
    getTeamsByIds: jest.fn(),
}));

jest.mock('@/mm_webapp', () => ({
    Client4: {},
}));

const mockGetChannelById = getChannelById as jest.Mock;
const mockGetPost = getPost as jest.Mock;
const mockGetProfilesByIds = getProfilesByIds as jest.Mock;
const mockGetTeamsByIds = getTeamsByIds as jest.Mock;

function renderCard(args: ToolCall['arguments']) {
    const tool: ToolCall = {
        id: 'tool_1',
        name: 'some_tool',
        description: '',
        status: ToolCallStatus.Pending,
        arguments: args,
    };
    return render(
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
        </IntlProvider>,
    );
}

describe('tool call ID resolution', () => {
    beforeEach(() => {
        clearResolvedIdsForTest();
        jest.resetAllMocks();
        mockGetProfilesByIds.mockResolvedValue([]);
        mockGetTeamsByIds.mockResolvedValue([]);
        mockGetChannelById.mockRejectedValue(new Error('not found'));
        mockGetPost.mockRejectedValue(new Error('not found'));
    });

    test('renders user, team, channel and post IDs as names, in arrays and scalars', async () => {
        mockGetProfilesByIds.mockResolvedValue([
            {id: 'u1', username: 'alice'},
            {id: 'u2', username: 'bob'},
        ]);
        mockGetTeamsByIds.mockResolvedValue([{id: 't1', name: 'eng', display_name: 'Engineering'}]);
        mockGetChannelById.mockResolvedValue({id: 'c1', name: 'town-square', display_name: 'Town Square'});
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
        expect(mockGetChannelById).toHaveBeenCalledWith('gone');
        expect(mockGetProfilesByIds).not.toHaveBeenCalled();
        expect(screen.getByText('u1')).not.toBeNull();
    });
});
