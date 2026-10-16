// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {getChannelInterval} from './client';
import {handleSummarizeChannelCommand} from './commands';
import manifest from './manifest';

jest.mock('./client', () => ({
    doRunSearch: jest.fn(),
    getChannelInterval: jest.fn(),
}));

jest.mock('./hooks', () => ({
    doSelectPost: jest.fn(),
}));

const mockedGetChannelInterval = getChannelInterval as jest.MockedFunction<typeof getChannelInterval>;

const CHANNEL_ID = 'c7f2m9xq4v1b8n3k6t5w0hzjd2';
const BOT_ID = 'b7m2xq9d4wn1c6zr3t8kv5jhya';
const OTHER_BOT_ID = 'o9k3wd5x1m7zq2r8vb4t6njhyc';

const args = {channel_id: CHANNEL_ID, team_id: 'team', root_id: ''};

const bots = [
    {id: OTHER_BOT_ID, username: 'other'},
    {id: BOT_ID, username: 'matty'},
];

function makeStore() {
    return {
        dispatch: jest.fn(),
        getState: () => ({['plugins-' + manifest.id]: {bots}}),
    };
}

describe('handleSummarizeChannelCommand', () => {
    beforeEach(() => {
        mockedGetChannelInterval.mockReset();
        mockedGetChannelInterval.mockResolvedValue({postid: 'post', channelid: 'channel'});
    });

    test.each([
        {name: '--bot with a username', message: '--bot matty', botId: BOT_ID},
        {name: '--bot with an @-prefixed username', message: '--bot @matty --period 3d', botId: BOT_ID},
        {name: 'no --bot option (server default)', message: '--period 3d', botId: ''},
    ])('maps $name to the agent bot user ID', async ({message, botId}) => {
        const result = await handleSummarizeChannelCommand(message, args, makeStore(), {showRHSPlugin: {type: 'show'}});

        expect(result).toEqual({});
        expect(mockedGetChannelInterval).toHaveBeenCalledTimes(1);
        expect(mockedGetChannelInterval.mock.calls[0][0]).toBe(CHANNEL_ID);
        expect(mockedGetChannelInterval.mock.calls[0][5]).toBe(botId);
    });

    test('reports an error and sends no request when no agent has the typed name', async () => {
        const result = await handleSummarizeChannelCommand('--bot nobody', args, makeStore(), {showRHSPlugin: {type: 'show'}});

        expect(result).toEqual({error: {message: expect.stringContaining('nobody')}});
        expect(mockedGetChannelInterval).not.toHaveBeenCalled();
    });
});
