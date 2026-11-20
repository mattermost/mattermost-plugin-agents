// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import TutorialTourTip, {computeAnchoredPosition} from './tutorial_tour_tip';

const mockHandleOpen = jest.fn();
const mockHandleDismiss = jest.fn();
let mockShow = false;

jest.mock('./hooks', () => ({
    useTourManager: () => ({show: mockShow, handleOpen: mockHandleOpen, handleDismiss: mockHandleDismiss}),
    useMeasurePunchouts: jest.fn(),
    useShowTutorialStep: jest.fn(),
}));

jest.mock('react-intl', () => ({
    useIntl: () => ({formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage}),
}));

describe('TutorialTourTip', () => {
    beforeEach(() => {
        mockShow = false;
        mockHandleOpen.mockReset();
        mockHandleDismiss.mockReset();
    });

    test('the pulsating dot is a focusable button that opens the tip', () => {
        render(
            <TutorialTourTip
                title='Meet Agents'
                screen='Ask anything'
                step={0}
                tutorialCategory='agents_tour'
            />,
        );

        const dot = screen.getByRole('button', {name: 'Show tip'});
        dot.focus();
        expect(document.activeElement).toBe(dot);

        fireEvent.click(dot);
        expect(mockHandleOpen).toHaveBeenCalledTimes(1);
    });

    test('renders the tour tip into #root-portal when open and dismisses on Escape or backdrop click', () => {
        const portal = document.createElement('div');
        portal.id = 'root-portal';
        document.body.appendChild(portal);
        mockShow = true;

        try {
            render(
                <TutorialTourTip
                    title='Meet Agents'
                    screen='Ask anything'
                    step={0}
                    tutorialCategory='agents_tour'
                />,
            );

            expect(portal.querySelector('.tour-tip-tippy')).not.toBeNull();
            expect(screen.getByText('Meet Agents')).toBeTruthy();

            fireEvent.keyDown(document, {key: 'Escape'});
            expect(mockHandleDismiss).toHaveBeenCalledTimes(1);

            const overlay = screen.getByTestId('agents-tour-overlay');
            fireEvent.mouseDown(overlay);
            expect(mockHandleDismiss).toHaveBeenCalledTimes(1);

            fireEvent.click(overlay);
            expect(mockHandleDismiss).toHaveBeenCalledTimes(2);
        } finally {
            portal.remove();
        }
    });

    test('repositions the tip when a parent rerender moves the trigger without resize or scroll', () => {
        const portal = document.createElement('div');
        portal.id = 'root-portal';
        document.body.appendChild(portal);
        mockShow = true;

        let triggerTop = 200;
        const makeRect = (top: number): DOMRect => ({
            top,
            left: 500,
            right: 520,
            bottom: top + 20,
            width: 20,
            height: 20,
            x: 500,
            y: top,
            toJSON: () => ({}),
        });

        try {
            const {rerender} = render(
                <div data-pos='initial'>
                    <TutorialTourTip
                        title='Meet Agents'
                        screen='Ask anything'
                        step={0}
                        tutorialCategory='agents_tour'
                        width={300}
                    />
                </div>,
            );

            const dot = screen.getByTestId('agents-tour-dot');
            const tip = portal.querySelector('.tour-tip-tippy') as HTMLElement;
            jest.spyOn(dot, 'getBoundingClientRect').mockImplementation(() => makeRect(triggerTop));
            jest.spyOn(tip, 'getBoundingClientRect').mockReturnValue({
                top: 0,
                left: 0,
                right: 300,
                bottom: 100,
                width: 300,
                height: 100,
                x: 0,
                y: 0,
                toJSON: () => ({}),
            });

            rerender(
                <div data-pos='first-measure'>
                    <TutorialTourTip
                        title='Meet Agents'
                        screen='Ask anything'
                        step={0}
                        tutorialCategory='agents_tour'
                        width={300}
                    />
                </div>,
            );
            expect(tip.style.top).toBe('160px');

            // Simulate an announcement banner shifting the dot down without firing resize/scroll.
            triggerTop = 280;
            rerender(
                <div data-pos='shifted'>
                    <TutorialTourTip
                        title='Meet Agents'
                        screen='Ask anything'
                        step={0}
                        tutorialCategory='agents_tour'
                        width={300}
                    />
                </div>,
            );
            expect(tip.style.top).toBe('240px');
        } finally {
            portal.remove();
        }
    });
});

describe('computeAnchoredPosition', () => {
    const trigger = {top: 200, left: 500, right: 520, bottom: 220, width: 20, height: 20};
    const tip = {width: 300, height: 100};
    const viewport = {width: 1000, height: 800};

    test.each([
        {placement: 'left' as const, offset: [0, 12] as [number, number], want: {top: 160, left: 188, placement: 'left'}},
        {placement: 'right' as const, offset: [0, 12] as [number, number], want: {top: 160, left: 532, placement: 'right'}},
        {placement: 'top-start' as const, offset: [4, 10] as [number, number], want: {top: 90, left: 504, placement: 'top-start'}},
        {placement: 'bottom-end' as const, offset: [0, 8] as [number, number], want: {top: 228, left: 220, placement: 'bottom-end'}},
    ])('positions $placement relative to trigger', ({placement, offset, want}) => {
        expect(computeAnchoredPosition(trigger, tip, placement, offset, viewport)).toEqual(want);
    });

    test('flips to the opposite side when the requested side has no room', () => {
        const nearLeftEdge = {top: 200, left: 10, right: 30, bottom: 220, width: 20, height: 20};
        expect(computeAnchoredPosition(nearLeftEdge, tip, 'left', [0, 12], viewport)).toEqual({
            top: 160,
            left: 42,
            placement: 'right',
        });
    });

    test('falls back to a perpendicular side when neither horizontal side fits', () => {
        const narrowViewport = {width: 320, height: 800};
        const nearLeftEdge = {top: 200, left: 10, right: 30, bottom: 220, width: 20, height: 20};
        expect(computeAnchoredPosition(nearLeftEdge, tip, 'left', [0, 12], narrowViewport)).toEqual({
            top: 232,
            left: 8,
            placement: 'bottom',
        });
    });

    test('clamps coordinates inside the viewport when no side fits', () => {
        const tinyViewport = {width: 200, height: 120};
        const nearEdge = {top: 10, left: 10, right: 30, bottom: 30, width: 20, height: 20};
        expect(computeAnchoredPosition(nearEdge, tip, 'left', [0, 12], tinyViewport)).toEqual({
            top: 8,
            left: 8,
            placement: 'left',
        });
    });
});
