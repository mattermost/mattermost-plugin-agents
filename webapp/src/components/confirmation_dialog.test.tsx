// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, fireEvent, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import ConfirmationDialog from './confirmation_dialog';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const intl = {
        formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
    return {...actual, useIntl: () => intl};
});

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

    it('names the dialog after its title', () => {
        renderDialog();

        expect(screen.getByRole('dialog', {name: 'Delete agent?'})).toBeTruthy();
    });

    it('Escape cancels with managed accessibility', () => {
        const {onCancel} = renderDialog({managedAccessibility: true});

        fireEvent.keyDown(document, {key: 'Escape'});

        expect(onCancel).toHaveBeenCalledTimes(1);
    });

    describe('Tab trap with managed accessibility', () => {
        beforeEach(() => {
            // jsdom has no layout, so offsetParent is always null and every control looks hidden.
            jest.spyOn(HTMLElement.prototype, 'offsetParent', 'get').mockReturnValue(document.body);
        });

        afterEach(() => {
            jest.restoreAllMocks();
        });

        it.each([
            {name: 'Tab from a middle control moves naturally', from: 'Cancel', shiftKey: false, wraps: false, to: 'Cancel'},
            {name: 'Tab from the last control wraps to the first', from: 'Delete', shiftKey: false, wraps: true, to: 'Close'},
            {name: 'Shift+Tab from a middle control moves naturally', from: 'Cancel', shiftKey: true, wraps: false, to: 'Cancel'},
            {name: 'Shift+Tab from the first control wraps to the last', from: 'Close', shiftKey: true, wraps: true, to: 'Delete'},
        ])('$name', ({from, shiftKey, wraps, to}) => {
            renderDialog({managedAccessibility: true});
            screen.getByRole('button', {name: from}).focus();

            const notPrevented = fireEvent.keyDown(document.activeElement as Element, {key: 'Tab', shiftKey});

            expect(notPrevented).toBe(!wraps);
            expect(document.activeElement).toBe(screen.getByRole('button', {name: to}));
        });
    });

    it('close icon button cancels', () => {
        const {onCancel} = renderDialog();

        fireEvent.click(screen.getByRole('button', {name: 'Close'}));

        expect(onCancel).toHaveBeenCalledTimes(1);
    });

    it.each([
        {name: 'backdrop press cancels', pending: false, managed: false, expectCancel: 1},
        {name: 'backdrop press cancels with managed accessibility', pending: false, managed: true, expectCancel: 1},
        {name: 'backdrop press is ignored while pending', pending: true, managed: true, expectCancel: 0},
    ])('$name', ({pending, managed, expectCancel}) => {
        const {onCancel} = renderDialog({confirmPending: pending, managedAccessibility: managed});
        const backdrop = screen.getByRole('dialog').parentElement as HTMLElement;

        fireEvent.click(backdrop);

        expect(onCancel).toHaveBeenCalledTimes(expectCancel);
    });

    it('a press that starts inside the dialog does not cancel', () => {
        const {onCancel} = renderDialog({managedAccessibility: true});
        const dialog = screen.getByRole('dialog');

        fireEvent.mouseDown(dialog);
        fireEvent.click(dialog.parentElement as HTMLElement);

        expect(onCancel).not.toHaveBeenCalled();
    });

    it('Escape does not cancel while pending', () => {
        const {onCancel} = renderDialog({managedAccessibility: true, confirmPending: true});

        fireEvent.keyDown(document, {key: 'Escape'});

        expect(onCancel).not.toHaveBeenCalled();
    });

    it('Escape is ignored without managed accessibility', () => {
        const {onCancel} = renderDialog();

        fireEvent.keyDown(document, {key: 'Escape'});

        expect(onCancel).not.toHaveBeenCalled();
    });

    it('show=false keeps the dialog through the exit, then unmounts and restores focus', async () => {
        const trigger = document.createElement('button');
        document.body.appendChild(trigger);
        trigger.focus();
        const props = {title: 'Delete agent?', message: 'x', confirmButtonText: 'Delete', cancelButtonText: 'Cancel', onConfirm: jest.fn(), onCancel: jest.fn(), managedAccessibility: true};
        const {rerender} = render(
            <IntlProvider locale='en'>
                <ConfirmationDialog
                    {...props}
                    show={true}
                />
            </IntlProvider>,
        );
        await waitFor(() => expect(document.activeElement).toBe(screen.getByRole('button', {name: 'Delete'})));

        rerender(
            <IntlProvider locale='en'>
                <ConfirmationDialog
                    {...props}
                    show={false}
                />
            </IntlProvider>,
        );

        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
        expect(document.activeElement).toBe(trigger);
        trigger.remove();
    });
});
