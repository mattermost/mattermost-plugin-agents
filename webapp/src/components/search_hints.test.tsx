// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import SearchHints from './search_hints';

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
    useSelector: () => true,
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

jest.mock('./bot_selector', () => ({
    BotDropdown: () => <div>{'bot-dropdown'}</div>,
}));

describe('SearchHints license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
    });

    test('shows search hints at Enterprise', () => {
        render(
            <IntlProvider locale='en'>
                <SearchHints/>
            </IntlProvider>,
        );
        expect(screen.getByText('SEARCH WITH')).not.toBeNull();
    });

    test('hides search hints below Enterprise', () => {
        useIsLicensedFor.mockReturnValue(false);
        render(
            <IntlProvider locale='en'>
                <SearchHints/>
            </IntlProvider>,
        );
        expect(screen.queryByText('SEARCH WITH')).toBeNull();
    });
});
