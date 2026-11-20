// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import WebSearchPanel, {WebSearchConfig} from './web_search_panel';

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
    useLicenseLevelName: jest.fn(() => () => 'Enterprise'),
    requiredLevelFor: jest.fn(() => 2),
}));

const enabledConfig: WebSearchConfig = {
    enabled: true,
    provider: 'brave',
    brave: {apiKey: '', resultLimit: 5, apiURL: ''},
    searxng: {baseURL: '', resultLimit: 5},
    domainDenylist: [],
};

describe('WebSearchPanel license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
    });

    test('allows enabling at Enterprise', () => {
        const onChange = jest.fn();
        render(
            <IntlProvider locale='en'>
                <WebSearchPanel
                    value={{...enabledConfig, enabled: false}}
                    onChange={onChange}
                />
            </IntlProvider>,
        );

        const trueRadio = screen.getAllByDisplayValue('true')[0] as HTMLInputElement;
        expect(trueRadio.disabled).toBe(false);
        fireEvent.click(trueRadio);
        expect(onChange).toHaveBeenCalledWith(expect.objectContaining({enabled: true}));
    });

    test('keeps current values visible and disables enabling below Enterprise', () => {
        useIsLicensedFor.mockReturnValue(false);
        const onChange = jest.fn();
        render(
            <IntlProvider locale='en'>
                <WebSearchPanel
                    value={enabledConfig}
                    onChange={onChange}
                />
            </IntlProvider>,
        );

        expect(screen.getByText('Enable Web Search')).not.toBeNull();
        expect(screen.getByText('Enterprise')).not.toBeNull();
        const trueRadio = screen.getAllByDisplayValue('true')[0] as HTMLInputElement;
        expect(trueRadio.disabled).toBe(true);
        const falseRadio = screen.getAllByDisplayValue('false')[0] as HTMLInputElement;
        expect(falseRadio.disabled).toBe(false);
        fireEvent.click(falseRadio);
        expect(onChange).toHaveBeenCalledWith(expect.objectContaining({enabled: false}));
    });
});

describe('WebSearchPanel provider', () => {
    const unsupportedMessage = 'Google Custom Search is no longer supported. Choose another provider.';

    test.each([
        {provider: 'google', shown: true},
        {provider: 'brave', shown: false},
        {provider: 'searxng', shown: false},
    ])('unsupported provider message for $provider shown: $shown', ({provider, shown}) => {
        render(
            <IntlProvider locale='en'>
                <WebSearchPanel
                    value={{...enabledConfig, provider}}
                    onChange={jest.fn()}
                />
            </IntlProvider>,
        );

        expect(screen.queryByText(unsupportedMessage) !== null).toBe(shown);
    });
});
