// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import RHSHeader from './rhs_header';

jest.mock('react-intl', () => ({
    FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    useIntl: () => ({
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    }),
}));

jest.mock('@mattermost/compass-icons/components', () => ({
    ClockOutlineIcon: () => <span data-testid='clock-icon'/>,
    PencilOutlineIcon: () => <span data-testid='pencil-icon'/>,
}));

jest.mock('./context_usage_indicator', () => ({
    __esModule: true,
    default: ({conversationId}: {conversationId: string}) => <span data-testid='context-usage'>{conversationId}</span>,
}));

function renderHeader(currentTab: string, extra: Partial<React.ComponentProps<typeof RHSHeader>> = {}) {
    const setCurrentTab = jest.fn();
    const selectPost = jest.fn();
    render(
        <RHSHeader
            currentTab={currentTab}
            setCurrentTab={setCurrentTab}
            selectPost={selectPost}
            {...extra}
        />,
    );
    return {setCurrentTab, selectPost};
}

describe('RHSHeader', () => {
    test('no longer renders the history button, an agent selector or the tools button (they live in the RHS title)', () => {
        renderHeader('thread');

        expect(screen.queryByTestId('chat-history')).toBeNull();
        expect(screen.queryByTestId('bot-selector-rhs')).toBeNull();
        expect(screen.queryByRole('button', {name: 'Tools'})).toBeNull();
    });

    test('the history view is titled and offers New chat', () => {
        const {setCurrentTab, selectPost} = renderHeader('threads');

        expect(screen.getByText('Chat history')).not.toBeNull();
        fireEvent.click(screen.getByTestId('new-chat'));
        expect(setCurrentTab).toHaveBeenCalledWith('new');
        expect(selectPost).toHaveBeenCalledWith('');
    });

    test('the new-chat tab has no title and no New chat button', () => {
        renderHeader('new');

        expect(screen.queryByText('Chat history')).toBeNull();
        expect(screen.queryByTestId('new-chat')).toBeNull();
    });

    test('an open chat shows the context usage ring and New chat', () => {
        renderHeader('thread', {activeConversationId: 'conv1'});

        expect(screen.getByTestId('context-usage').textContent).toBe('conv1');
        expect(screen.getByTestId('new-chat')).not.toBeNull();
    });
});
