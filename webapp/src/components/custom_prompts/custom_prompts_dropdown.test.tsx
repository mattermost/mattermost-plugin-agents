// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';

import manifest from '@/manifest';
import {CustomPrompt} from '@/types';

import CustomPromptsDropdown from './custom_prompts_dropdown';

// Message ids are injected by babel-plugin-formatjs at build time; under
// ts-jest FormattedMessage has no id, so render the defaultMessage instead.
jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {
        ...actual,
        useIntl: () => intl,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

// The dropdown dispatches a thunk on mount; the store here has no middleware.
jest.mock('@/redux', () => ({
    ShowCustomPromptsModalHandler: 'SHOW_CUSTOM_PROMPTS_MODAL',
    fetchCustomPrompts: () => ({type: 'NOOP'}),
}));

jest.mock('@/client', () => ({
    renderCustomPrompt: jest.fn(),
    createPost: jest.fn(),
}));

jest.mock('@/bots', () => ({
    useBotlist: () => ({
        bots: [{id: 'bot1', username: 'agent', dmChannelID: 'dm-channel'}],
        activeBot: {id: 'bot1', username: 'agent', dmChannelID: 'dm-channel'},
        setActiveBot: jest.fn(),
    }),
}));

jest.mock('@/components/bot_selector', () => ({
    DropdownBotSelector: () => null,
}));

const {renderCustomPrompt, createPost} = jest.requireMock('@/client');

function makePrompt(overrides: Partial<CustomPrompt>): CustomPrompt {
    return {
        id: 'prompt1',
        creator_id: 'user1',
        name: 'Triage',
        description: '',
        template: 'Triage this',
        is_shared: false,
        run_immediately: false,
        created_at: 0,
        updated_at: 0,
        deleted_at: 0,
        ...overrides,
    };
}

function renderDropdown(prompt: CustomPrompt, updateText: jest.Mock, channelId = 'channel1') {
    const state = {
        [`plugins-${manifest.id}`]: {
            customPrompts: [prompt],
            pinnedPromptIds: [],
            showCustomPromptsModal: false,
        },
    };
    const store = createStore(() => state);

    return render(
        <Provider store={store}>
            <CustomPromptsDropdown
                draft={{rootId: ''}}
                getSelectedText={() => ({start: 0, end: 0})}
                updateText={updateText}
                channelId={channelId}
                isRHS={false}
            />
        </Provider>,
    );
}

describe('CustomPromptsDropdown', () => {
    beforeEach(() => {
        jest.clearAllMocks();
        renderCustomPrompt.mockResolvedValue({rendered: 'Triage this'});
        createPost.mockResolvedValue({id: 'post1'});
    });

    it('fills in the draft for a prompt that is not marked run_immediately', async () => {
        const updateText = jest.fn();
        renderDropdown(makePrompt({run_immediately: false}), updateText);

        fireEvent.click(screen.getByText('Triage'));

        await waitFor(() => expect(updateText).toHaveBeenCalledWith('@agent Triage this'));
        expect(createPost).not.toHaveBeenCalled();
    });

    it('posts without touching the draft for a prompt marked run_immediately', async () => {
        const updateText = jest.fn();
        renderDropdown(makePrompt({run_immediately: true}), updateText);

        fireEvent.click(screen.getByText('Triage'));

        await waitFor(() => expect(createPost).toHaveBeenCalledTimes(1));
        expect(createPost).toHaveBeenCalledWith(expect.objectContaining({
            channel_id: 'channel1',
            message: '@agent Triage this',
        }));
        expect(updateText).not.toHaveBeenCalled();
    });

    it('omits the agent mention when run_immediately fires inside the agent DM', async () => {
        const updateText = jest.fn();
        renderDropdown(makePrompt({run_immediately: true}), updateText, 'dm-channel');

        fireEvent.click(screen.getByText('Triage'));

        await waitFor(() => expect(createPost).toHaveBeenCalledTimes(1));
        expect(createPost).toHaveBeenCalledWith(expect.objectContaining({
            channel_id: 'dm-channel',
            message: 'Triage this',
        }));
    });

    it('marks run_immediately prompts in the menu so selecting one is not a surprise', () => {
        const {unmount} = renderDropdown(makePrompt({run_immediately: true}), jest.fn());
        expect(screen.queryByLabelText('Sends without review')).not.toBeNull();
        unmount();

        renderDropdown(makePrompt({run_immediately: false}), jest.fn());
        expect(screen.queryByLabelText('Sends without review')).toBeNull();
    });

    it('surfaces a visible error when an immediate run fails, without touching the draft', async () => {
        const updateText = jest.fn();
        createPost.mockRejectedValue(new Error('boom'));
        jest.spyOn(console, 'error').mockImplementation(() => null);

        renderDropdown(makePrompt({run_immediately: true}), updateText);

        fireEvent.click(screen.getByText('Triage'));

        // A failed send leaves no draft behind, so it has to say so: silence
        // would look exactly like success.
        await waitFor(() => expect(screen.getByRole('alert').textContent).toMatch(/Could not send the prompt/));
        expect(updateText).not.toHaveBeenCalled();
    });

    it('shows no error on a successful immediate run', async () => {
        renderDropdown(makePrompt({run_immediately: true}), jest.fn());

        fireEvent.click(screen.getByText('Triage'));

        await waitFor(() => expect(createPost).toHaveBeenCalledTimes(1));
        expect(screen.queryByRole('alert')).toBeNull();
    });
});
