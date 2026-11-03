// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import manifest from '@/manifest';

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

const stateIn = (currentChannelId: string, agentDMRawView: Record<string, true> = {}) => ({
    [`plugins-${manifest.id}`]: {bots: [{id: 'agent1'}], agentDMRawView},
    entities: {
        users: {currentUserId: 'me'},
        channels: {
            currentChannelId,
            channels: {
                chan1: {id: 'chan1', type: 'O', name: 'town-square', display_name: 'Town Square'},
                dmAgent: {id: 'dmAgent', type: 'D', name: 'agent1__me', display_name: 'agent1'},
                dmHuman: {id: 'dmHuman', type: 'D', name: 'human__me', display_name: 'human'},
            },
            myMembers: {[currentChannelId]: {last_viewed_at: 1}},
        },
        teams: {currentTeamId: 'team1'},
    },
});

let mockState: unknown = stateIn('chan1');

jest.mock('react-redux', () => ({
    useSelector: (selector: (state: unknown) => unknown) => selector(mockState),
    useDispatch: () => jest.fn(),
}));

jest.mock('react-bootstrap', () => ({
    Overlay: () => null,
}), {virtual: true});

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

        fireEvent.click(screen.getByRole('button', {name: 'Ask Agents about this channel'}));
        expect(screen.getByRole('button', {name: 'Ask Agents about this channel'}).getAttribute('aria-pressed')).toBe('false');
        expect(screen.getByTestId('ask-channel-tooltip').getAttribute('data-title')).toBe('Ask Agents about this channel');
    });
});

describe('AskChannelButton in DMs', () => {
    beforeEach(() => {
        const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};
        useIsLicensedFor.mockImplementation(() => true);
    });
    afterEach(() => {
        mockState = stateIn('chan1');
    });

    test.each([
        {name: 'a channel', state: stateIn('chan1'), rendered: true},
        {name: 'a DM with a person', state: stateIn('dmHuman'), rendered: true},
        {name: 'an agent DM shown as the agent chat', state: stateIn('dmAgent'), rendered: false},
        {name: 'an agent DM shown as plain messages', state: stateIn('dmAgent', {dmAgent: true}), rendered: false},
    ])('in $name renders=$rendered', ({state, rendered}) => {
        mockState = state;
        render(
            <IntlProvider locale='en'>
                <AskChannelButton/>
            </IntlProvider>,
        );
        expect(screen.queryByTestId('ask-channel-button') !== null).toBe(rendered);
    });
});
