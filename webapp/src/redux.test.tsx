// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {rhsTab} from './redux';

describe('rhsTab reducer', () => {
    test.each([
        // eslint-disable-next-line no-undefined -- the reducer's initial state is an undefined argument
        {name: 'starts on the new chat', state: undefined, action: {type: 'NONE'}, expected: 'new'},
        {name: 'shows the chat history', state: 'new' as const, action: {type: 'AI_RHS_SET_TAB', tab: 'threads'}, expected: 'threads'},
        {name: 'shows the new chat', state: 'threads' as const, action: {type: 'AI_RHS_SET_TAB', tab: 'new'}, expected: 'new'},
        {name: 'ignores an unknown tab', state: 'threads' as const, action: {type: 'AI_RHS_SET_TAB', tab: 'bogus'}, expected: 'new'},
        {name: 'switching agent starts a new chat', state: 'threads' as const, action: {type: 'AI_RHS_NEW_CHAT'}, expected: 'new'},
        {name: 'unrelated actions keep it', state: 'threads' as const, action: {type: 'SELECT_AI_POST', postId: ''}, expected: 'threads'},
    ])('$name', ({state, action, expected}) => {
        expect(rhsTab(state, action)).toBe(expected);
    });
});
