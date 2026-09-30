// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {CustomPrompt} from '@/types';

import CustomPromptForm from './custom_prompt_form';

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

jest.mock('react-bootstrap', () => ({
    OverlayTrigger: ({children}: {children: React.ReactNode}) => <>{children}</>,
    Tooltip: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}), {virtual: true});

jest.mock('../dropdown', () => ({
    __esModule: true,
    default: ({children}: {children: React.ReactNode}) => <div>{children}</div>,
}));

jest.mock('./context_variables_dropdown', () => ({
    __esModule: true,
    default: () => null,
}));

const prompt: CustomPrompt = {
    id: 'p1',
    creator_id: 'u1',
    name: 'My prompt',
    description: '',
    template: 'Do the thing',
    is_shared: false,
    run_immediately: false,
    created_at: 0,
    updated_at: 0,
    deleted_at: 0,
};

function renderForm(overrides: Partial<CustomPrompt> = {}) {
    render(
        <IntlProvider locale='en'>
            <CustomPromptForm
                prompt={{...prompt, ...overrides}}
                onSave={jest.fn()}
                onDiscard={jest.fn()}
            />
        </IntlProvider>,
    );
}

describe('CustomPromptForm shared prompts license gating', () => {
    const {useIsLicensedFor} = jest.requireMock('@/license') as {useIsLicensedFor: jest.Mock};

    beforeEach(() => {
        useIsLicensedFor.mockReturnValue(true);
    });

    test('shows Public at Enterprise', () => {
        renderForm();
        expect(screen.getByText('Public')).not.toBeNull();
        expect(screen.getByText('Private')).not.toBeNull();
    });

    test('hides Public below Enterprise for a private prompt', () => {
        useIsLicensedFor.mockReturnValue(false);
        renderForm({is_shared: false});
        expect(screen.queryByText('Public')).toBeNull();
        expect(screen.getByText('Private')).not.toBeNull();
    });

    test('keeps Public visible for an already shared prompt so it can be turned off', () => {
        useIsLicensedFor.mockReturnValue(false);
        renderForm({is_shared: true});

        const publicRadio = screen.getByText('Public').closest('label')?.querySelector('input') as HTMLInputElement;
        expect(publicRadio.disabled).toBe(true);
        const privateRadio = screen.getByText('Private').closest('label')?.querySelector('input') as HTMLInputElement;
        expect(privateRadio.disabled).toBe(false);
        fireEvent.click(privateRadio);
        expect(privateRadio.checked).toBe(true);
    });
});
