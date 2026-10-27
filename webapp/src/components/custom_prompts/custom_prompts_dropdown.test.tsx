// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

import CustomPromptsDropdown from './custom_prompts_dropdown';

const mockDispatch = jest.fn();
const mockRenderCustomPrompt = jest.fn();
const mockPrompts = [{id: 'prompt-id', name: 'Summary'}];

jest.mock('react-redux', () => ({
    useSelector: () => mockPrompts,
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
}));

jest.mock('@/components/bot_selector', () => ({
    DropdownBotSelector: () => null,
}));

describe('CustomPromptsDropdown', () => {
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
