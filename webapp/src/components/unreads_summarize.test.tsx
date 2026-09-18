// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import UnreadsSumarize from './unreads_summarize';

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

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
}));

jest.mock('@/hooks', () => ({
    useSelectPost: () => jest.fn(),
}));

jest.mock('@/bots', () => ({
    useBotlistForChannel: () => ({
        bots: [{username: 'matty', displayName: 'Matty'}],
        activeBot: {username: 'matty', displayName: 'Matty'},
        setActiveBot: jest.fn(),
    }),
}));

jest.mock('@/client', () => ({
    getChannelInterval: jest.fn(),
}));

jest.mock('./dot_menu', () => ({
    __esModule: true,
    default: ({children}: {children: React.ReactNode}) => <div data-testid='unreads-menu'>{children}</div>,
    DropdownMenu: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
    DropdownMenuItem: ({children}: {children: React.ReactNode}) => <button type='button'>{children}</button>,
}));

jest.mock('./bot_selector', () => ({
    DropdownBotSelector: () => <div>{'bot-selector'}</div>,
}));

describe('UnreadsSumarize license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
    });

    test('shows channel summarization actions at Professional', () => {
        render(
            <IntlProvider locale='en'>
                <UnreadsSumarize
                    lastViewedAt={1}
                    channelId='chan1'
                    threadId=''
                />
            </IntlProvider>,
        );

        expect(screen.getByText('Summarize new messages')).not.toBeNull();
        expect(screen.getByText('Find action items')).not.toBeNull();
        expect(screen.getByText('Find open questions')).not.toBeNull();
    });

    test('hides the unreads entry point below Professional', () => {
        useIsLicensedFor.mockReturnValue(false);
        render(
            <IntlProvider locale='en'>
                <UnreadsSumarize
                    lastViewedAt={1}
                    channelId='chan1'
                    threadId=''
                />
            </IntlProvider>,
        );

        expect(screen.queryByText('Summarize new messages')).toBeNull();
        expect(screen.queryByTestId('unreads-menu')).toBeNull();
    });
});
