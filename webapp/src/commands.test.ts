// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {doRunSearch} from './client';
import {handleAskChannelCommand} from './commands';

jest.mock('./client', () => ({
    doRunSearch: jest.fn(),
    getChannelInterval: jest.fn(),
}));

jest.mock('./hooks', () => ({
    doSelectPost: jest.fn(),
}));

const mockedDoRunSearch = doRunSearch as jest.MockedFunction<typeof doRunSearch>;

describe('handleAskChannelCommand', () => {
    beforeEach(() => {
        mockedDoRunSearch.mockReset();
    });

    test.each(['', '   ', '\n\t'])('rejects a blank query without starting a search (%j)', async (message) => {
        const result = await handleAskChannelCommand(
            message,
            {channel_id: 'channel-id', team_id: 'team-id', root_id: ''},
            {dispatch: jest.fn()},
            {showRHSPlugin: jest.fn()},
        );

        expect(result).toEqual({
            error: {
                message: 'Please provide a search query after /ask-channel',
            },
        });
        expect(mockedDoRunSearch).not.toHaveBeenCalled();
    });
});
