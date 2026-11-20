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

            fireEvent.click(screen.getByTestId('agents-tour-overlay'));
            expect(mockHandleDismiss).toHaveBeenCalledTimes(2);
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
        {placement: 'left' as const, offset: [0, 12] as [number, number], want: {top: 160, left: 188}},
        {placement: 'right' as const, offset: [0, 12] as [number, number], want: {top: 160, left: 532}},
        {placement: 'top-start' as const, offset: [4, 10] as [number, number], want: {top: 90, left: 504}},
        {placement: 'bottom-end' as const, offset: [0, 8] as [number, number], want: {top: 228, left: 220}},
    ])('positions $placement relative to trigger', ({placement, offset, want}) => {
        expect(computeAnchoredPosition(trigger, tip, placement, offset, viewport)).toEqual(want);
    });

    test('clamps coordinates inside the viewport with padding', () => {
        const nearEdge = {top: 10, left: 10, right: 30, bottom: 30, width: 20, height: 20};
        expect(computeAnchoredPosition(nearEdge, tip, 'left', [0, 12], viewport)).toEqual({top: 8, left: 8});
    });
});
