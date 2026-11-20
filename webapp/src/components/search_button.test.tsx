// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';

import SearchButton from './search_button';

jest.mock('react-intl', () => ({
    ...jest.requireActual('react-intl'),
    FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
}));

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
}));

jest.mock('./assets/icon_ai', () => ({
    __esModule: true,
    default: () => <span>{'ai-icon'}</span>,
}));

const renderButton = () => render(<SearchButton/>);

describe('SearchButton license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
    });

    test('shows the search entry at Enterprise', () => {
        renderButton();
        expect(screen.getByText('Ask Agents')).not.toBeNull();
    });

    test('hides the search entry below Enterprise', () => {
        useIsLicensedFor.mockReturnValue(false);
        renderButton();
        expect(screen.queryByText('Ask Agents')).toBeNull();
    });
});
