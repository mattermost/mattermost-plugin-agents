// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import RHSErrorBoundary from './rhs_error_boundary';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

function Boom({message}: {message: string}): React.ReactElement {
    throw new Error(message);
}

describe('RHSErrorBoundary', () => {
    test('renders children when nothing throws', () => {
        render(
            <IntlProvider locale='en'>
                <RHSErrorBoundary>
                    <div data-testid='ok'>{'ok'}</div>
                </RHSErrorBoundary>
            </IntlProvider>,
        );

        expect(screen.getByTestId('ok')).toBeTruthy();
        expect(screen.queryByTestId('mattermost-ai-rhs-error')).toBeNull();
    });

    test('exposes the thrown error message for e2e diagnostics', () => {
        const errorLog = jest.spyOn(console, 'error').mockImplementation(() => {
            // React logs the boundary error; swallow it so the test output stays readable.
        });

        render(
            <IntlProvider locale='en'>
                <RHSErrorBoundary>
                    <Boom message='Cannot read properties of undefined (reading rootId)'/>
                </RHSErrorBoundary>
            </IntlProvider>,
        );

        const fallback = screen.getByTestId('mattermost-ai-rhs-error');
        expect(fallback.textContent).toContain('An error occurred in the Agents panel.');
        expect(fallback.textContent).toContain('Cannot read properties of undefined (reading rootId)');

        errorLog.mockRestore();
    });
});
