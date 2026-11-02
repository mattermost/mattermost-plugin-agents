// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

import CustomPromptsDropdown from './custom_prompts_dropdown';

const mockDispatch = jest.fn();
const mockRenderCustomPrompt = jest.fn();
let mockPluginState: {customPrompts: Array<{id: string; name: string}>; pinnedPromptIds: string[] | null} = {customPrompts: [], pinnedPromptIds: null};

jest.mock('react-redux', () => ({
    useSelector: (selector: (state: unknown) => unknown) => selector({[`plugins-${jest.requireActual('@/manifest').default.id}`]: mockPluginState}),
    useDispatch: () => mockDispatch,
}));

jest.mock('react-intl', () => {
    const ReactActual = jest.requireActual<typeof import('react')>('react');
    return {
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => ReactActual.createElement(ReactActual.Fragment, null, defaultMessage),
    };
});

jest.mock('@/bots', () => ({
    useBotlist: () => ({bots: [], activeBot: null, setActiveBot: jest.fn()}),
}));

jest.mock('@/client', () => ({
    renderCustomPrompt: (...args: unknown[]) => mockRenderCustomPrompt(...args),
}));

jest.mock('@/redux', () => ({
    fetchCustomPrompts: () => ({type: 'FETCH_CUSTOM_PROMPTS'}),
    fetchPinnedPromptIds: () => ({type: 'FETCH_PINNED_PROMPT_IDS'}),
}));

jest.mock('@/components/bot_selector', () => ({
    DropdownBotSelector: () => null,
}));

const renderDropdown = (updateText = jest.fn()) => render(
    <CustomPromptsDropdown
        draft={{}}
        getSelectedText={() => ({start: 0, end: 0})}
        updateText={updateText}
        channelId='channel-id'
        isRHS={false}
    />,
);

describe('CustomPromptsDropdown', () => {
    beforeEach(() => {
        mockPluginState = {customPrompts: [{id: 'prompt-id', name: 'Summary'}], pinnedPromptIds: null};
    });

    test.each([
        {name: 'pinned prompts come first, in their list order', pinned: ['c', 'a'], expected: ['Alpha', 'Gamma', 'Beta']},
        {name: 'without pins the list order is kept', pinned: [], expected: ['Alpha', 'Beta', 'Gamma']},
        {name: 'before the pins load the list order is kept', pinned: null, expected: ['Alpha', 'Beta', 'Gamma']},
    ])('$name', ({pinned, expected}) => {
        mockPluginState = {customPrompts: [{id: 'a', name: 'Alpha'}, {id: 'b', name: 'Beta'}, {id: 'c', name: 'Gamma'}], pinnedPromptIds: pinned};
        renderDropdown();

        const names = screen.getAllByRole('menuitem').map((item) => item.textContent).filter((text) => text !== 'Manage prompts');
        expect(names).toEqual(expected);
    });

    test('menu items are focusable and insert the rendered prompt', async () => {
        mockRenderCustomPrompt.mockResolvedValue({rendered: 'Summarize this channel'});
        const updateText = jest.fn();
        render(
            <CustomPromptsDropdown
                draft={{}}
                getSelectedText={() => ({start: 0, end: 0})}
                updateText={updateText}
                channelId='channel-id'
                isRHS={false}
            />,
        );

        const item = screen.getByRole('menuitem', {name: 'Summary'});
        item.focus();
        expect(document.activeElement).toBe(item);

        fireEvent.click(item);
        await waitFor(() => expect(updateText).toHaveBeenCalledWith('Summarize this channel'));
    });
});
