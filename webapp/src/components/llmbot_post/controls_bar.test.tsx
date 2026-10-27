// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen} from '@testing-library/react';

import {ControlsBarComponent} from './controls_bar';

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const real = actual.createIntl({locale: 'en', defaultLocale: 'en', onError: () => null});
    const intl = {
        formatMessage: (descriptor: {id?: string; defaultMessage: string}) =>
            real.formatMessage({id: descriptor.id ?? descriptor.defaultMessage, ...descriptor}),
    };
    return {...actual, useIntl: () => intl, FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage};
});

describe('ControlsBarComponent Regenerate', () => {
    const renderBar = (onRegenerate = jest.fn()) => {
        render(
            <ControlsBarComponent
                showStopGeneratingButton={false}
                showPostbackButton={false}
                showRegenerate={true}
                onStopGenerating={jest.fn()}
                onPostSummary={jest.fn()}
                onRegenerate={onRegenerate}
            />,
        );
        return onRegenerate;
    };

    test('is an icon button named Regenerate that regenerates', () => {
        const onRegenerate = renderBar();

        const button = screen.getByRole('button', {name: 'Regenerate'});
        expect(button.getAttribute('data-testid')).toBe('regenerate-button');
        expect(button.textContent).toBe('');

        fireEvent.click(button);
        expect(onRegenerate).toHaveBeenCalledTimes(1);
    });

    // Jest maps the host's WithTooltip to a stand-in that exposes the title it was given.
    test('is wrapped in a tooltip titled with its label', () => {
        renderBar();
        const button = screen.getByRole('button', {name: 'Regenerate'});

        const tooltip = button.closest('[data-testid="with-tooltip"]');
        expect(tooltip?.getAttribute('data-title')).toBe('Regenerate');
        expect(tooltip?.getAttribute('data-disabled')).toBe('false');
    });
});
