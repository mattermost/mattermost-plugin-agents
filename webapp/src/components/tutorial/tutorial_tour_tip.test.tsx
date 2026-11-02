// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import TutorialTourTip from './tutorial_tour_tip';

const mockHandleOpen = jest.fn();

jest.mock('./hooks', () => ({
    useTourManager: () => ({show: false, handleOpen: mockHandleOpen, handleDismiss: jest.fn()}),
    useMeasurePunchouts: jest.fn(),
    useShowTutorialStep: jest.fn(),
}));

jest.mock('react-intl', () => ({
    useIntl: () => ({formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage}),
}));

describe('TutorialTourTip', () => {
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
});
