// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
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

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Overlay: () => null,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

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
    ChannelSummarizePopover: (props: {showAskInput?: boolean; showSummarizeOptions?: boolean}) => (
        <div
            data-testid='channel-summarize-popover'
            data-show-ask={String(Boolean(props.showAskInput))}
            data-show-summarize={String(Boolean(props.showSummarizeOptions))}
        />
    ),
}));

describe('AskChannelButton license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
    });

    test('renders the channel button when either summarization or search is licensed', () => {
        useIsLicensedFor.mockImplementation((capability: string) => capability === 'channel_summarization');
        render(
            <IntlProvider locale='en'>
                <AskChannelButton/>
            </IntlProvider>,
        );
        expect(screen.getByTestId('ask-channel-button')).not.toBeNull();
    });

    test('hides the channel button when neither summarization nor search is licensed', () => {
        useIsLicensedFor.mockReturnValue(false);
        render(
            <IntlProvider locale='en'>
                <AskChannelButton/>
            </IntlProvider>,
        );
        expect(screen.queryByTestId('ask-channel-button')).toBeNull();
    });
});
