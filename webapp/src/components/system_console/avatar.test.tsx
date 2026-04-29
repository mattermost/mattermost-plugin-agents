// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import AvatarItem from './avatar';

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

jest.mock('@/client', () => ({
    getBotProfilePictureUrl: jest.fn(),
}));

// Stub the static asset so the placeholder has a stable, predictable src in tests.
jest.mock('src/../../assets/bot_icon.png', () => 'placeholder-icon.png', {virtual: true});

const {getBotProfilePictureUrl} = jest.requireMock('@/client') as {
    getBotProfilePictureUrl: jest.Mock<Promise<string>, [string]>;
};

function renderAvatar(botusername: string) {
    return render(
        <IntlProvider locale='en'>
            <AvatarItem
                botusername={botusername}
                changedAvatar={jest.fn()}
            />
        </IntlProvider>,
    );
}

beforeEach(() => {
    getBotProfilePictureUrl.mockReset();
});

describe('AvatarItem', () => {
    it('refetches the avatar when botusername changes (no leak from previous bot)', async () => {
        getBotProfilePictureUrl.mockImplementation((username: string) =>
            Promise.resolve(`/profile/${username}.png`));

        const {rerender} = renderAvatar('alpha');

        // First fetch resolves to alpha's URL.
        await waitFor(() => {
            expect(screen.getByRole('img').getAttribute('src')).toBe('/profile/alpha.png');
        });

        // Switch to beta — same component instance, different bot. Avatar must reflect beta,
        // not the previously displayed alpha image (regression guard for MM-68531).
        rerender(
            <IntlProvider locale='en'>
                <AvatarItem
                    botusername='beta'
                    changedAvatar={jest.fn()}
                />
            </IntlProvider>,
        );

        await waitFor(() => {
            expect(screen.getByRole('img').getAttribute('src')).toBe('/profile/beta.png');
        });

        expect(getBotProfilePictureUrl).toHaveBeenCalledWith('alpha');
        expect(getBotProfilePictureUrl).toHaveBeenCalledWith('beta');
    });

    it('falls back to the placeholder when the bot has no resolvable avatar', async () => {
        getBotProfilePictureUrl.mockResolvedValue('');

        renderAvatar('newbot');

        await waitFor(() => {
            expect(getBotProfilePictureUrl).toHaveBeenCalledWith('newbot');
        });

        expect(screen.getByRole('img').getAttribute('src')).toBe('placeholder-icon.png');
    });

    it('keeps the placeholder when the avatar fetch rejects (no unhandled rejection)', async () => {
        // Simulates the 404 path that fires while a user is typing a draft username before
        // the underlying bot account exists, or any transient auth/network failure.
        getBotProfilePictureUrl.mockRejectedValue(new Error('Not Found'));

        const unhandled = jest.fn();
        process.on('unhandledRejection', unhandled);

        try {
            renderAvatar('draftbot');

            await waitFor(() => {
                expect(getBotProfilePictureUrl).toHaveBeenCalledWith('draftbot');
            });

            // Flush any pending microtasks so the rejection has a chance to surface.
            await new Promise((resolve) => setTimeout(resolve, 0));

            expect(screen.getByRole('img').getAttribute('src')).toBe('placeholder-icon.png');
            expect(unhandled).not.toHaveBeenCalled();
        } finally {
            process.off('unhandledRejection', unhandled);
        }
    });

    it('ignores a stale fetch result when botusername changes during the request', async () => {
        let resolveAlpha: ((value: string) => void) | undefined;
        const alphaPending = new Promise<string>((resolve) => {
            resolveAlpha = resolve;
        });
        getBotProfilePictureUrl.mockImplementationOnce(() => alphaPending);
        getBotProfilePictureUrl.mockImplementationOnce(() => Promise.resolve('/profile/beta.png'));

        const {rerender} = renderAvatar('alpha');

        rerender(
            <IntlProvider locale='en'>
                <AvatarItem
                    botusername='beta'
                    changedAvatar={jest.fn()}
                />
            </IntlProvider>,
        );

        await waitFor(() => {
            expect(screen.getByRole('img').getAttribute('src')).toBe('/profile/beta.png');
        });

        // Late alpha response must not overwrite beta's image.
        resolveAlpha?.('/profile/alpha.png');
        await Promise.resolve();
        expect(screen.getByRole('img').getAttribute('src')).toBe('/profile/beta.png');
    });

    it('preserves a locally uploaded preview when the username changes', async () => {
        getBotProfilePictureUrl.mockResolvedValue('');

        // Stub createObjectURL since jsdom doesn't implement it.
        const createObjectURL = jest.fn(() => 'blob:preview');
        const originalCreateObjectURL = (URL as unknown as {createObjectURL?: typeof createObjectURL}).createObjectURL;
        (URL as unknown as {createObjectURL: typeof createObjectURL}).createObjectURL = createObjectURL;

        // FileReader.readAsArrayBuffer just needs to fire onload; we don't care about the bytes.
        class FakeFileReader {
            onload: (() => void) | null = null;
            readAsArrayBuffer() {
                this.onload?.();
            }
        }
        const originalFileReader = global.FileReader;

        // @ts-expect-error - replacing for the test only
        global.FileReader = FakeFileReader;

        try {
            const {rerender} = renderAvatar('agentnew');
            await waitFor(() => {
                expect(getBotProfilePictureUrl).toHaveBeenCalledWith('agentnew');
            });

            const file = new File(['x'], 'a.png', {type: 'image/png'});
            const input = document.querySelector('input[type="file"]') as HTMLInputElement;
            fireEvent.change(input, {target: {files: [file]}});

            await waitFor(() => {
                expect(screen.getByRole('img').getAttribute('src')).toBe('blob:preview');
            });

            // The preview must persist when the user edits the username while a local upload
            // preview is active; the avatar must not snap back to the placeholder.
            rerender(
                <IntlProvider locale='en'>
                    <AvatarItem
                        botusername='agentnewx'
                        changedAvatar={jest.fn()}
                    />
                </IntlProvider>,
            );

            await Promise.resolve();
            expect(screen.getByRole('img').getAttribute('src')).toBe('blob:preview');
        } finally {
            (URL as unknown as {createObjectURL?: typeof createObjectURL}).createObjectURL = originalCreateObjectURL;
            global.FileReader = originalFileReader;
        }
    });
});
