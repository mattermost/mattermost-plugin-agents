// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, fireEvent, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import ConfirmationDialog from './confirmation_dialog';

function renderDialog(props: Partial<React.ComponentProps<typeof ConfirmationDialog>> = {}) {
    const onConfirm = jest.fn();
    const onCancel = jest.fn();
    render(
        <IntlProvider locale='en'>
            <ConfirmationDialog
                title='Delete agent?'
                message='This cannot be undone.'
                confirmButtonText='Delete'
                cancelButtonText='Cancel'
                onConfirm={onConfirm}
                onCancel={onCancel}
                {...props}
            />
        </IntlProvider>,
    );
    return {onConfirm, onCancel};
}

describe('ConfirmationDialog', () => {
    it.each([
        {name: 'primary confirm', isDestructive: false},
        {name: 'destructive confirm', isDestructive: true},
    ])('focuses the $name button on open with managed accessibility', async ({isDestructive}) => {
        renderDialog({managedAccessibility: true, isDestructive});

        const confirm = screen.getByRole('button', {name: 'Delete'});
        await waitFor(() => expect(document.activeElement).toBe(confirm));
    });

    it.each([
        {name: 'confirm fires onConfirm', button: 'Delete', pending: false, expectConfirm: 1, expectCancel: 0},
        {name: 'cancel fires onCancel', button: 'Cancel', pending: false, expectConfirm: 0, expectCancel: 1},
        {name: 'pending blocks confirm', button: 'Delete', pending: true, expectConfirm: 0, expectCancel: 0},
        {name: 'pending blocks cancel', button: 'Cancel', pending: true, expectConfirm: 0, expectCancel: 0},
    ])('$name', ({button, pending, expectConfirm, expectCancel}) => {
        const {onConfirm, onCancel} = renderDialog({confirmPending: pending});

        fireEvent.click(screen.getByRole('button', {name: button}));

        expect(onConfirm).toHaveBeenCalledTimes(expectConfirm);
        expect(onCancel).toHaveBeenCalledTimes(expectCancel);
    });
});
