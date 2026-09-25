// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';

import SearchButton from './search_button';

jest.mock('react-redux', () => ({
    useSelector: () => true,
}));

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
}));

jest.mock('./assets/icon_ai', () => ({
    __esModule: true,
    default: () => <span>{'ai-icon'}</span>,
}));

describe('SearchButton license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
    });

    test('shows the search entry at Enterprise', () => {
        render(<SearchButton/>);
        expect(screen.getByText('Agents')).not.toBeNull();
    });

    test('hides the search entry below Enterprise', () => {
        useIsLicensedFor.mockReturnValue(false);
        render(<SearchButton/>);
        expect(screen.queryByText('Agents')).toBeNull();
    });
});
