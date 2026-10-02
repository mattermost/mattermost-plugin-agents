// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
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

describe('CustomPromptForm validation', () => {
    test('blocks saving and reports both required fields when empty', () => {
        const onSave = jest.fn();
        render(
            <IntlProvider locale='en'>
                <CustomPromptForm
                    onSave={onSave}
                    onDiscard={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        expect(onSave).not.toHaveBeenCalled();
        expect(screen.getByText('Action title is required')).not.toBeNull();
        expect(screen.getByText('System prompt is required')).not.toBeNull();

        fireEvent.change(screen.getByPlaceholderText('Enter a title for your prompt'), {target: {value: 'Title'}});
        expect(screen.queryByText('Action title is required')).toBeNull();
    });

    test('saves trimmed values', async () => {
        const onSave = jest.fn();
        render(
            <IntlProvider locale='en'>
                <CustomPromptForm
                    onSave={onSave}
                    onDiscard={jest.fn()}
                />
            </IntlProvider>,
        );

        fireEvent.change(screen.getByPlaceholderText('Enter a title for your prompt'), {target: {value: ' Title '}});
        fireEvent.change(screen.getByPlaceholderText('Enter the system prompt template'), {target: {value: ' Body '}});
        fireEvent.click(screen.getByRole('button', {name: 'Save'}));

        expect(onSave).toHaveBeenCalledWith({name: 'Title', description: '', template: 'Body', is_shared: false});
        await waitFor(() => expect((screen.getByRole('button', {name: 'Save'}) as HTMLButtonElement).disabled).toBe(false));
    });
});
