// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen} from '@testing-library/react';

import {COPY_FEEDBACK_MS, ControlsBarComponent} from './controls_bar';

jest.mock('@/mm_webapp', () => ({
    Timestamp: ({value}: {value: number}) => `time:${value}`,
}));

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

describe('ControlsBarComponent placement marker', () => {
    test.each([
        {name: 'below the bubble in the agent chat views', belowBubble: true, marker: 'below'},
        {name: 'unmarked when not below', belowBubble: false, marker: null},
    ])('$name', ({belowBubble, marker}) => {
        const {container} = render(
            <ControlsBarComponent
                showStopGeneratingButton={false}
                showPostbackButton={false}
                showRegenerate={true}
                belowBubble={belowBubble}
                onStopGenerating={jest.fn()}
                onPostSummary={jest.fn()}
                onRegenerate={jest.fn()}
            />,
        );
        expect(container.querySelector('[data-bubble-controls]')?.getAttribute('data-bubble-controls') ?? null).toBe(marker);
    });
});

describe('ControlsBarComponent Copy', () => {
    type Clipboard = {writeText: jest.Mock};
    const setClipboard = (clipboard: Clipboard | null) => {
        Object.defineProperty(navigator, 'clipboard', {value: clipboard, configurable: true});
    };
    const execCommand = jest.fn();

    const renderBar = (props: Partial<React.ComponentProps<typeof ControlsBarComponent>> = {}) => render(
        <ControlsBarComponent
            showStopGeneratingButton={false}
            showPostbackButton={false}
            showRegenerate={false}
            copyText='Some **markdown**'
            onStopGenerating={jest.fn()}
            onPostSummary={jest.fn()}
            onRegenerate={jest.fn()}
            {...props}
        />,
    );
    const title = () => screen.getByTestId('copy-button').closest('[data-testid="with-tooltip"]')?.getAttribute('data-title');
    const click = async () => {
        await act(async () => {
            fireEvent.click(screen.getByTestId('copy-button'));
        });
    };

    beforeEach(() => {
        jest.useFakeTimers();
        execCommand.mockReset().mockReturnValue(true);
        Object.defineProperty(document, 'execCommand', {value: execCommand, configurable: true});
        setClipboard({writeText: jest.fn().mockResolvedValue(null)});
    });

    afterEach(() => {
        jest.clearAllTimers();
        jest.useRealTimers();
    });

    test('is an icon button named Copy with a test id and a Copy tooltip', () => {
        renderBar();
        const button = screen.getByRole('button', {name: 'Copy'});
        expect(button.getAttribute('data-testid')).toBe('copy-button');
        expect(button.textContent).toBe('');
        expect(title()).toBe('Copy');
    });

    test('sits before Regenerate and renders alone', () => {
        renderBar({showRegenerate: true});
        const ids = screen.getAllByRole('button').map((b) => b.getAttribute('data-testid'));
        expect(ids).toEqual(['copy-button', 'regenerate-button']);
    });

    test('is left out without copy text', () => {
        renderBar({copyText: null, showRegenerate: true});
        expect(screen.queryByTestId('copy-button')).toBeNull();
        expect(screen.getByTestId('regenerate-button')).toBeTruthy();
    });

    test('copies the text through the async clipboard, then shows Copied and reverts', async () => {
        renderBar();
        await click();

        expect((navigator.clipboard.writeText as jest.Mock)).toHaveBeenCalledWith('Some **markdown**');
        expect(title()).toBe('Copied');
        expect(screen.getByRole('status').textContent).toBe('Copied to clipboard');

        act(() => {
            jest.advanceTimersByTime(COPY_FEEDBACK_MS - 1);
        });
        expect(title()).toBe('Copied');
        act(() => {
            jest.advanceTimersByTime(1);
        });
        expect(title()).toBe('Copy');
        expect(screen.getByRole('status').textContent).toBe('');
    });

    test('a repeated click restarts the feedback window', async () => {
        renderBar();
        await click();
        act(() => {
            jest.advanceTimersByTime(COPY_FEEDBACK_MS - 500);
        });
        await click();
        act(() => {
            jest.advanceTimersByTime(COPY_FEEDBACK_MS - 500);
        });
        expect(title()).toBe('Copied');
        act(() => {
            jest.advanceTimersByTime(500);
        });
        expect(title()).toBe('Copy');
    });

    test('falls back to the textarea copy when the clipboard rejects or is missing', async () => {
        const cases: Array<Clipboard | null> = [{writeText: jest.fn().mockRejectedValue(new Error('denied'))}, null];
        for (const clipboard of cases) {
            execCommand.mockClear();
            setClipboard(clipboard);
            const {unmount} = renderBar();
            const button = screen.getByTestId('copy-button');
            button.focus();
            // eslint-disable-next-line no-await-in-loop
            await click();

            expect(execCommand).toHaveBeenCalledWith('copy');
            expect(title()).toBe('Copied');
            expect(document.querySelector('[data-copy-fallback]')).toBeNull();
            expect(document.activeElement).toBe(button);
            unmount();
        }
    });

    test('reports Couldn\'t copy when every route fails, then reverts', async () => {
        setClipboard({writeText: jest.fn().mockRejectedValue(new Error('denied'))});
        execCommand.mockReturnValue(false);
        const errorSpy = jest.spyOn(console, 'error').mockImplementation(() => null);
        renderBar();
        await click();

        expect(title()).toBe('Couldn\'t copy');
        expect(screen.getByRole('status').textContent).toBe('Couldn\'t copy');
        expect(errorSpy).not.toHaveBeenCalled();

        act(() => {
            jest.advanceTimersByTime(COPY_FEEDBACK_MS);
        });
        expect(title()).toBe('Copy');
        errorSpy.mockRestore();
    });

    test('unmounting clears the feedback timer', async () => {
        const {unmount} = renderBar();
        await click();
        const pending = jest.getTimerCount();
        unmount();

        // The feedback timer is gone (other timers belong to compass-ui).
        expect(jest.getTimerCount()).toBeLessThan(pending);
    });

    test('is operable with the keyboard', async () => {
        renderBar();
        const button = screen.getByTestId('copy-button');
        button.focus();
        await act(async () => {
            fireEvent.keyDown(button, {key: 'Enter'});
            fireEvent.click(button);
        });
        expect(navigator.clipboard.writeText).toHaveBeenCalled();
        expect(document.activeElement).toBe(button);
    });
});

describe('ControlsBarComponent timestamp', () => {
    const renderBar = (props: Partial<React.ComponentProps<typeof ControlsBarComponent>>) => render(
        <ControlsBarComponent
            showStopGeneratingButton={false}
            showPostbackButton={false}
            showRegenerate={true}
            onStopGenerating={jest.fn()}
            onPostSummary={jest.fn()}
            onRegenerate={jest.fn()}
            {...props}
        />,
    );

    test.each([
        {name: 'below the bubble with a creation time', props: {belowBubble: true, createAt: 1234}, shown: true},
        {name: 'not below the bubble', props: {belowBubble: false, createAt: 1234}, shown: false},
        {name: 'below the bubble without a creation time', props: {belowBubble: true}, shown: false},
    ])('shows the time only $name: $shown', ({props, shown}) => {
        renderBar(props);

        expect(screen.queryByText('time:1234') !== null).toBe(shown);
    });

    test('the time is the first thing in the bar', () => {
        renderBar({belowBubble: true, createAt: 1234});

        const time = screen.getByTestId('controls-bar-time');
        expect(time.parentElement?.firstElementChild).toBe(time);
        expect(time.parentElement?.children.length).toBeGreaterThan(1);
    });
});
