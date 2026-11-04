// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen, fireEvent, waitFor} from '@testing-library/react';

import {Modal} from '@mattermost/compass-ui/components/modal';

import {OPEN_POPUP_ATTRIBUTE} from './dropdown';
import {AnimatedModalShell} from './animated_modal_shell';

function renderShell(props: Partial<React.ComponentProps<typeof AnimatedModalShell>> = {}) {
    const onBackdropClick = jest.fn();
    const onEscape = jest.fn();
    const ui = (show: boolean) => (
        <AnimatedModalShell
            show={show}
            onBackdropClick={onBackdropClick}
            onEscape={onEscape}
            {...props}
        >
            <Modal
                closeOnEscape={false}
                title='Find agents'
                footer={<button>{'Inside'}</button>}
            >
                {'body'}
            </Modal>
        </AnimatedModalShell>
    );
    const view = render(ui(true));
    return {onBackdropClick, onEscape, rerender: (show: boolean) => view.rerender(ui(show))};
}

describe('AnimatedModalShell', () => {
    it('backdrop press calls onBackdropClick, a press inside the dialog does not', () => {
        const {onBackdropClick} = renderShell();
        const dialog = screen.getByRole('dialog');

        fireEvent.mouseDown(dialog);
        fireEvent.click(dialog.parentElement as HTMLElement);
        expect(onBackdropClick).not.toHaveBeenCalled();

        fireEvent.click(dialog.parentElement as HTMLElement);
        expect(onBackdropClick).toHaveBeenCalledTimes(1);
    });

    it('Escape calls onEscape (not onBackdropClick) while shown', () => {
        const {onEscape, onBackdropClick} = renderShell();

        fireEvent.keyDown(document.body, {key: 'Escape'});

        expect(onEscape).toHaveBeenCalledTimes(1);
        expect(onBackdropClick).not.toHaveBeenCalled();
    });

    it('Escape defers to an open popup menu', () => {
        const {onEscape} = renderShell();
        const popup = document.createElement('div');
        popup.setAttribute(OPEN_POPUP_ATTRIBUTE, '');
        document.body.appendChild(popup);

        fireEvent.keyDown(document.body, {key: 'Escape'});

        popup.remove();
        expect(onEscape).not.toHaveBeenCalled();
    });

    it('Escape defers to another open dialog outside the shell', () => {
        const {onEscape} = renderShell();
        const other = document.createElement('div');
        other.setAttribute('role', 'dialog');
        other.getClientRects = () => [{}] as unknown as DOMRectList;
        document.body.appendChild(other);

        fireEvent.keyDown(document.body, {key: 'Escape'});

        other.remove();
        expect(onEscape).not.toHaveBeenCalled();
    });

    it('a modal opened from inside another shell takes Escape, and the opener defers to it', () => {
        const onOuter = jest.fn();
        const onInner = jest.fn();
        const ui = (innerShown: boolean) => (
            <AnimatedModalShell
                show={true}
                onEscape={onOuter}
            >
                <Modal
                    closeOnEscape={false}
                    title='Outer'
                >
                    {'outer'}
                    {innerShown && (
                        <AnimatedModalShell
                            show={true}
                            onEscape={onInner}
                        >
                            <Modal
                                closeOnEscape={false}
                                title='Inner'
                            >
                                {'inner'}
                            </Modal>
                        </AnimatedModalShell>
                    )}
                </Modal>
            </AnimatedModalShell>
        );
        const view = render(ui(false));

        // Opened after its opener, so it stacks above it.
        view.rerender(ui(true));
        document.querySelectorAll<HTMLElement>('[role="dialog"]').forEach((el) => {
            el.getClientRects = () => [{}] as unknown as DOMRectList;
        });

        fireEvent.keyDown(document.body, {key: 'Escape'});

        expect(onInner).toHaveBeenCalledTimes(1);
        expect(onOuter).not.toHaveBeenCalled();
    });

    it('show=false unmounts after the exit and stops handling Escape', async () => {
        const {onEscape, rerender} = renderShell();

        rerender(false);
        fireEvent.keyDown(document.body, {key: 'Escape'});

        expect(onEscape).not.toHaveBeenCalled();
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    });
});
