// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import AgentsHeaderSettings from './agents_header_settings';

const mockDispatch = jest.fn();

jest.mock('react-redux', () => ({
    ...jest.requireActual('react-redux'),
    useDispatch: () => mockDispatch,
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        formatMessage: (descriptor: {id?: string; defaultMessage: string}) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}),
    };
    return {...actual, useIntl: () => intl};
});

describe('AgentsHeaderSettings', () => {
    beforeEach(() => {
        mockDispatch.mockClear();
    });

    afterEach(() => {
        delete window.WebappUtils;
    });

    // The host's openUserSettings only RETURNS the action that opens the modal; nothing opens
    // unless the caller dispatches it.
    test('dispatches the host action that opens the user settings modal as product settings', () => {
        const openAction = {type: 'MODAL_OPEN', modalId: 'user_settings'};
        const openUserSettings = jest.fn(() => openAction);
        window.WebappUtils = {sendWebSocketMessage: jest.fn(), openUserSettings};
        render(<AgentsHeaderSettings/>);

        fireEvent.click(screen.getByRole('button', {name: 'Settings'}));

        expect(openUserSettings).toHaveBeenCalledTimes(1);
        expect(openUserSettings).toHaveBeenCalledWith({isContentProductSettings: true});
        expect(mockDispatch).toHaveBeenCalledTimes(1);
        expect(mockDispatch).toHaveBeenCalledWith(openAction);
    });

    test('renders nothing on a host without the API', () => {
        window.WebappUtils = {sendWebSocketMessage: jest.fn()};
        const {container} = render(<AgentsHeaderSettings/>);

        expect(container.firstChild).toBeNull();
    });

    test('renders nothing without WebappUtils', () => {
        const {container} = render(<AgentsHeaderSettings/>);

        expect(container.firstChild).toBeNull();
    });
});
