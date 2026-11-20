// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import AskChannelButton from './ask_channel_button';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

jest.mock('react-redux', () => ({
    useSelector: (selector: (state: unknown) => unknown) => selector({
        entities: {
            channels: {
                currentChannelId: 'chan1',
                channels: {chan1: {display_name: 'Town Square'}},
                myMembers: {chan1: {last_viewed_at: 1}},
            },
            teams: {currentTeamId: 'team1'},
        },
    }),
    useDispatch: () => jest.fn(),
}));

jest.mock('@mattermost/shared/components/tooltip', () => ({
    WithTooltip: ({children, title}: {children: React.ReactNode; title: string}) => (
        <div
            data-testid='ask-channel-tooltip'
            data-title={title}
        >
            {children}
        </div>
    ),
}));

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
}));

jest.mock('@/bots', () => ({
    useBotlist: () => ({
        bots: [{username: 'matty', displayName: 'Matty'}],
        activeBot: {username: 'matty', displayName: 'Matty'},
        setActiveBot: jest.fn(),
    }),
}));

jest.mock('@/client', () => ({
    doChannelAnalysis: jest.fn(),
}));

jest.mock('@/redux_actions', () => ({
    openRHS: jest.fn(),
}));

jest.mock('./channel_summarize_popover', () => ({
    ChannelSummarizePopover: () => <div data-testid='channel-summarize-popover'/>,
}));

describe('AskChannelButton license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    test.each([
        {licensed: true, rendered: true},
        {licensed: false, rendered: false},
    ])('channel summarization licensed=$licensed renders=$rendered', ({licensed, rendered}) => {
        useIsLicensedFor.mockImplementation((capability: string) => capability === 'channel_summarization' && licensed);
        render(
            <IntlProvider locale='en'>
                <AskChannelButton/>
            </IntlProvider>,
        );
        expect(screen.queryByTestId('ask-channel-button') !== null).toBe(rendered);
    });
});

describe('AskChannelButton sizing', () => {
    test('renders the channel-header IconButton at x-small to match host header actions', () => {
        const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};
        useIsLicensedFor.mockImplementation(() => true);
        render(
            <IntlProvider locale='en'>
                <AskChannelButton/>
            </IntlProvider>,
        );

        const button = screen.getByTestId('ask-channel-button');
        expect(button.className).toMatch(/icon-button--size-x-small/);
        expect(button.className).not.toMatch(/icon-button--size-small(?!-)/);
        expect(button.getAttribute('aria-label')).toBe('Ask Agents about this channel');
        expect(button.getAttribute('title')).toBeNull();
        expect(screen.getByTestId('ask-channel-tooltip').getAttribute('data-title')).toBe('Ask Agents about this channel');
    });
});

describe('AskChannelButton toggle', () => {
    test('clicking the button marks it pressed and suppresses the tooltip while open', () => {
        const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};
        useIsLicensedFor.mockImplementation(() => true);
        render(
            <IntlProvider locale='en'>
                <AskChannelButton/>
            </IntlProvider>,
        );

        const button = screen.getByRole('button', {name: 'Ask Agents about this channel'});
        expect(button.getAttribute('aria-pressed')).toBe('false');
        expect(screen.getByTestId('ask-channel-tooltip').getAttribute('data-title')).toBe('Ask Agents about this channel');

        fireEvent.click(button);
        expect(screen.getByRole('button', {name: 'Ask Agents about this channel'}).getAttribute('aria-pressed')).toBe('true');
        expect(screen.getByTestId('ask-channel-tooltip').getAttribute('data-title')).toBe('');
        expect(screen.getByTestId('channel-summarize-popover')).toBeTruthy();

        fireEvent.click(screen.getByRole('button', {name: 'Ask Agents about this channel'}));
        expect(screen.getByRole('button', {name: 'Ask Agents about this channel'}).getAttribute('aria-pressed')).toBe('false');
        expect(screen.getByTestId('ask-channel-tooltip').getAttribute('data-title')).toBe('Ask Agents about this channel');
        expect(screen.queryByTestId('channel-summarize-popover')).toBeNull();
    });

    test('closes the popover on Escape and outside click', () => {
        const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};
        useIsLicensedFor.mockImplementation(() => true);
        render(
            <IntlProvider locale='en'>
                <AskChannelButton/>
            </IntlProvider>,
        );

        const button = screen.getByRole('button', {name: 'Ask Agents about this channel'});
        fireEvent.click(button);
        expect(screen.getByTestId('channel-summarize-popover')).toBeTruthy();

        fireEvent.keyDown(document, {key: 'Escape'});
        expect(screen.queryByTestId('channel-summarize-popover')).toBeNull();

        fireEvent.click(button);
        expect(screen.getByTestId('channel-summarize-popover')).toBeTruthy();

        fireEvent.mouseDown(document.body);
        expect(screen.queryByTestId('channel-summarize-popover')).toBeNull();
    });

    test('keeps the popover open when clicking inside a nested portaled popup or dialog', () => {
        const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};
        useIsLicensedFor.mockImplementation(() => true);
        render(
            <IntlProvider locale='en'>
                <AskChannelButton/>
            </IntlProvider>,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Ask Agents about this channel'}));
        expect(screen.getByTestId('channel-summarize-popover')).toBeTruthy();

        const nestedPopup = document.createElement('div');
        nestedPopup.setAttribute('data-open-popup', '');
        const popupOption = document.createElement('button');
        nestedPopup.appendChild(popupOption);
        document.body.appendChild(nestedPopup);

        try {
            fireEvent.mouseDown(popupOption);
            expect(screen.getByTestId('channel-summarize-popover')).toBeTruthy();
        } finally {
            document.body.removeChild(nestedPopup);
        }
    });

    test('closes on Escape when only a hidden dialog is in the DOM', () => {
        const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};
        useIsLicensedFor.mockImplementation(() => true);
        render(
            <IntlProvider locale='en'>
                <AskChannelButton/>
            </IntlProvider>,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Ask Agents about this channel'}));
        expect(screen.getByTestId('channel-summarize-popover')).toBeTruthy();

        const hiddenDialog = document.createElement('div');
        hiddenDialog.setAttribute('role', 'dialog');
        hiddenDialog.setAttribute('aria-hidden', 'true');
        document.body.appendChild(hiddenDialog);

        try {
            fireEvent.keyDown(document, {key: 'Escape'});
            expect(screen.queryByTestId('channel-summarize-popover')).toBeNull();
        } finally {
            document.body.removeChild(hiddenDialog);
        }
    });
});
