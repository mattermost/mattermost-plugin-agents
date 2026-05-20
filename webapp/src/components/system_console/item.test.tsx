// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';

import {CopyableTextItem} from './item';

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

function renderItem(value: string, helptext?: string) {
    return render(
        <CopyableTextItem
            label='OAuth Callback URL'
            value={value}
            helptext={helptext}
        />,
    );
}

describe('CopyableTextItem', () => {
    const writeTextMock = jest.fn();

    beforeEach(() => {
        writeTextMock.mockReset();
        writeTextMock.mockImplementation(() => Promise.resolve());
        Object.defineProperty(navigator, 'clipboard', {
            value: {writeText: writeTextMock},
            configurable: true,
        });
    });

    afterEach(() => {
        jest.useRealTimers();
    });

    it('renders the label, value, and help text', () => {
        renderItem('https://example.com/plugins/mattermost-ai/oauth/callback', 'Register this URL.');

        expect(screen.getByText('OAuth Callback URL')).toBeTruthy();
        expect(screen.getByDisplayValue('https://example.com/plugins/mattermost-ai/oauth/callback')).toBeTruthy();
        expect(screen.getByText('Register this URL.')).toBeTruthy();
    });

    it('renders the value as a read-only text input', () => {
        renderItem('https://example.com/plugins/mattermost-ai/oauth/callback');

        const input = screen.getByDisplayValue('https://example.com/plugins/mattermost-ai/oauth/callback') as HTMLInputElement;
        expect(input.tagName).toBe('INPUT');
        expect(input.readOnly).toBe(true);
    });

    it('selects the entire value when the input is focused, so a manual copy is one keystroke away', () => {
        renderItem('https://example.com/plugins/mattermost-ai/oauth/callback');

        const input = screen.getByDisplayValue('https://example.com/plugins/mattermost-ai/oauth/callback') as HTMLInputElement;
        input.focus();
        fireEvent.focus(input);

        expect(input.selectionStart).toBe(0);
        expect(input.selectionEnd).toBe(input.value.length);
    });

    it('writes the value to the clipboard when the copy button is clicked', async () => {
        renderItem('https://example.com/plugins/mattermost-ai/oauth/callback');

        const copyButton = screen.getByRole('button', {name: /copy to clipboard/i});
        fireEvent.click(copyButton);

        await waitFor(() => {
            expect(writeTextMock).toHaveBeenCalledWith('https://example.com/plugins/mattermost-ai/oauth/callback');
        });
    });

    it('updates the button label to "Copied" after a successful copy', async () => {
        renderItem('https://example.com/plugins/mattermost-ai/oauth/callback');

        fireEvent.click(screen.getByRole('button', {name: /copy to clipboard/i}));

        await waitFor(() => {
            expect(screen.getByRole('button', {name: /^copied$/i})).toBeTruthy();
        });
    });

    it('reverts the button label back to "Copy to clipboard" after the confirmation timeout', async () => {
        jest.useFakeTimers();
        renderItem('https://example.com/plugins/mattermost-ai/oauth/callback');

        await act(async () => {
            fireEvent.click(screen.getByRole('button', {name: /copy to clipboard/i}));
        });

        expect(screen.getByRole('button', {name: /^copied$/i})).toBeTruthy();

        act(() => {
            jest.advanceTimersByTime(2000);
        });

        expect(screen.getByRole('button', {name: /copy to clipboard/i})).toBeTruthy();
    });

    it('falls back to document.execCommand when navigator.clipboard is unavailable', async () => {
        const originalClipboard = navigator.clipboard;
        const originalExecCommand = document.execCommand;
        const execCommand = jest.fn().mockReturnValue(true);

        try {
            delete (navigator as unknown as {clipboard?: unknown}).clipboard;
            document.execCommand = execCommand;

            renderItem('https://example.com/plugins/mattermost-ai/oauth/callback');

            fireEvent.click(screen.getByRole('button', {name: /copy to clipboard/i}));

            await waitFor(() => {
                expect(execCommand).toHaveBeenCalledWith('copy');
            });
        } finally {
            document.execCommand = originalExecCommand;
            Object.defineProperty(navigator, 'clipboard', {
                value: originalClipboard,
                configurable: true,
            });
        }
    });

    it('does not show success when document.execCommand fails', async () => {
        const originalClipboard = navigator.clipboard;
        const originalExecCommand = document.execCommand;
        const execCommand = jest.fn().mockReturnValue(false);
        const consoleErrorSpy = jest.spyOn(console, 'error').mockImplementation(jest.fn());

        try {
            delete (navigator as unknown as {clipboard?: unknown}).clipboard;
            document.execCommand = execCommand;

            renderItem('https://example.com/plugins/mattermost-ai/oauth/callback');

            await act(async () => {
                fireEvent.click(screen.getByRole('button', {name: /copy to clipboard/i}));
            });

            expect(execCommand).toHaveBeenCalledWith('copy');
            expect(screen.getByRole('button', {name: /copy to clipboard/i})).toBeTruthy();
            expect(screen.queryByRole('button', {name: /^copied$/i})).toBeNull();
        } finally {
            document.execCommand = originalExecCommand;
            Object.defineProperty(navigator, 'clipboard', {
                value: originalClipboard,
                configurable: true,
            });
            consoleErrorSpy.mockRestore();
        }
    });

    it('does not call onChange when the user types into the field (read-only)', () => {
        renderItem('https://example.com/plugins/mattermost-ai/oauth/callback');

        const input = screen.getByDisplayValue('https://example.com/plugins/mattermost-ai/oauth/callback') as HTMLInputElement;
        fireEvent.change(input, {target: {value: 'tampered'}});

        expect(input.value).toBe('https://example.com/plugins/mattermost-ai/oauth/callback');
    });
});
