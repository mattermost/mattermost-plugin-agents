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

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children, overlay}: {children: React.ReactNode; overlay: React.ReactNode}) => <>{children}{overlay}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('@/license', () => ({
    useIsLicensedFor: jest.fn(() => true),
    useLicenseLevelName: jest.fn(() => () => 'Enterprise'),
    requiredLevelFor: jest.fn(() => 2),
}));

const enabledConfig: WebSearchConfig = {
    enabled: true,
    provider: 'brave',
    google: {apiKey: '', searchEngineId: '', resultLimit: 5, apiURL: ''},
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
