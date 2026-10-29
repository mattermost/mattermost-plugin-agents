// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getChannelInterval} from './client';
import {handleSummarizeChannelCommand} from './commands';

jest.mock('./client', () => ({
    doRunSearch: jest.fn(),
    getChannelInterval: jest.fn(),
}));

jest.mock('./hooks', () => ({
    doSelectPost: jest.fn(),
}));

const mockedGetChannelInterval = getChannelInterval as jest.MockedFunction<typeof getChannelInterval>;

const CHANNEL_ID = 'c7f2m9xq4v1b8n3k6t5w0hzjd2';

const args = {channel_id: CHANNEL_ID, team_id: 'team', root_id: ''};

function makeStore() {
    return {
        dispatch: jest.fn(),
        getState: () => ({}),
    };
}

describe('handleSummarizeChannelCommand', () => {
    beforeEach(() => {
        mockedGetChannelInterval.mockReset();
        mockedGetChannelInterval.mockResolvedValue({postid: 'post', channelid: 'channel'});
    });

    test.each([
        {name: '--bot with a username', message: '--bot matty', botUsername: 'matty'},
        {name: '--bot with an @-prefixed username', message: '--bot @matty --period 3d', botUsername: 'matty'},
        {name: 'no --bot option (server default)', message: '--period 3d', botUsername: ''},
        {name: 'a bare @ (rejected by the server, not the default agent)', message: '--bot @', botUsername: '@'},
    ])('passes $name to the server to resolve', async ({message, botUsername}) => {
        const result = await handleSummarizeChannelCommand(message, args, makeStore(), {showRHSPlugin: {type: 'show'}});

        expect(result).toEqual({});
        expect(mockedGetChannelInterval).toHaveBeenCalledTimes(1);
        expect(mockedGetChannelInterval.mock.calls[0][0]).toBe(CHANNEL_ID);
        expect(mockedGetChannelInterval.mock.calls[0][5]).toBe('');
        expect(mockedGetChannelInterval.mock.calls[0][6]).toBe(botUsername);
    });

    test('reports the server error when no agent has the typed name', async () => {
        mockedGetChannelInterval.mockRejectedValue(new Error('agent not available: nobody'));
        const result = await handleSummarizeChannelCommand('--bot nobody', args, makeStore(), {showRHSPlugin: {type: 'show'}});

        expect(result).toEqual({error: {message: expect.stringContaining('agent not available: nobody')}});
    });
});
