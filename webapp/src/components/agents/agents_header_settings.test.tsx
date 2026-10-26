// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import AgentsHeaderSettings from './agents_header_settings';

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
    afterEach(() => {
        delete window.WebappUtils;
    });

    test('opens the host user settings modal as product settings', () => {
        const openUserSettings = jest.fn();
        window.WebappUtils = {sendWebSocketMessage: jest.fn(), openUserSettings};
        render(<AgentsHeaderSettings/>);

        fireEvent.click(screen.getByRole('button', {name: 'Settings'}));

        expect(openUserSettings).toHaveBeenCalledTimes(1);
        expect(openUserSettings).toHaveBeenCalledWith({isContentProductSettings: true});
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
