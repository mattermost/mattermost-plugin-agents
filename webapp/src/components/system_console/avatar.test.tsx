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

jest.mock('src/../../assets/bot_icon.png', () => 'placeholder-icon.png', {virtual: true});

const {getBotProfilePictureUrl} = jest.requireMock('@/client') as {
    getBotProfilePictureUrl: jest.Mock<Promise<string>, [string]>;
};

function renderAvatar(botusername: string, avatarOwnerKey?: string) {
    return render(
        <IntlProvider locale='en'>
            <AvatarItem
                botusername={botusername}
                avatarOwnerKey={avatarOwnerKey}
                changedAvatar={jest.fn()}
            />
        </IntlProvider>,
    );
}

beforeEach(() => {
    getBotProfilePictureUrl.mockReset();
});

function mockObjectURL(previewURL = 'blob:preview') {
    const createObjectURL = jest.fn(() => previewURL);
    const revokeObjectURL = jest.fn();
    const url = URL as unknown as {
        createObjectURL?: typeof createObjectURL;
        revokeObjectURL?: typeof revokeObjectURL;
    };
    const originalCreateObjectURL = url.createObjectURL;
    const originalRevokeObjectURL = url.revokeObjectURL;

    url.createObjectURL = createObjectURL;
    url.revokeObjectURL = revokeObjectURL;

    return {
        createObjectURL,
        revokeObjectURL,
        restore: () => {
            url.createObjectURL = originalCreateObjectURL;
            url.revokeObjectURL = originalRevokeObjectURL;
        },
    };
}

describe('AvatarItem', () => {
    it('refetches the avatar when botusername changes (no leak from previous bot)', async () => {
        getBotProfilePictureUrl.mockImplementation((username: string) =>
            Promise.resolve(`/profile/${username}.png`));

        const {rerender} = renderAvatar('alpha');

        await waitFor(() => {
            expect(screen.getByRole('img').getAttribute('src')).toBe('/profile/alpha.png');
        });

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
        getBotProfilePictureUrl.mockRejectedValue(new Error('Not Found'));

        const unhandled = jest.fn();
        process.on('unhandledRejection', unhandled);

        try {
            renderAvatar('draftbot');

            await waitFor(() => {
                expect(getBotProfilePictureUrl).toHaveBeenCalledWith('draftbot');
            });

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

        resolveAlpha?.('/profile/alpha.png');
        await Promise.resolve();
        expect(screen.getByRole('img').getAttribute('src')).toBe('/profile/beta.png');
    });

    it('preserves a locally uploaded preview when the username changes', async () => {
        getBotProfilePictureUrl.mockResolvedValue('');

        const objectURL = mockObjectURL();

        let unmount: (() => void) | null = null;
        try {
            const rendered = renderAvatar('agentnew');
            unmount = rendered.unmount;
            await waitFor(() => {
                expect(getBotProfilePictureUrl).toHaveBeenCalledWith('agentnew');
            });

            const file = new File(['x'], 'a.png', {type: 'image/png'});
            const input = document.querySelector('input[type="file"]') as HTMLInputElement;
            fireEvent.change(input, {target: {files: [file]}});

            await waitFor(() => {
                expect(screen.getByRole('img').getAttribute('src')).toBe('blob:preview');
            });

            rendered.rerender(
                <IntlProvider locale='en'>
                    <AvatarItem
                        botusername='agentnewx'
                        changedAvatar={jest.fn()}
                    />
                </IntlProvider>,
            );

            await Promise.resolve();
            expect(screen.getByRole('img').getAttribute('src')).toBe('blob:preview');
            unmount();
            unmount = null;
        } finally {
            unmount?.();
            objectURL.restore();
        }
    });

    it('clears a locally uploaded preview when the avatar owner changes', async () => {
        getBotProfilePictureUrl.mockImplementation((username: string) =>
            Promise.resolve(`/profile/${username}.png`));

        const objectURL = mockObjectURL();

        let unmount: (() => void) | null = null;
        try {
            const rendered = renderAvatar('alpha', 'alpha-id');
            unmount = rendered.unmount;

            await waitFor(() => {
                expect(screen.getByRole('img').getAttribute('src')).toBe('/profile/alpha.png');
            });

            const file = new File(['x'], 'a.png', {type: 'image/png'});
            const input = document.querySelector('input[type="file"]') as HTMLInputElement;
            fireEvent.change(input, {target: {files: [file]}});

            await waitFor(() => {
                expect(screen.getByRole('img').getAttribute('src')).toBe('blob:preview');
            });

            rendered.rerender(
                <IntlProvider locale='en'>
                    <AvatarItem
                        botusername='alpha'
                        avatarOwnerKey='hydrated-alpha-id'
                        changedAvatar={jest.fn()}
                    />
                </IntlProvider>,
            );

            await waitFor(() => {
                expect(screen.getByRole('img').getAttribute('src')).toBe('/profile/alpha.png');
            });

            expect(getBotProfilePictureUrl).toHaveBeenCalledTimes(2);
            expect(objectURL.revokeObjectURL).toHaveBeenCalledWith('blob:preview');
            unmount();
            unmount = null;
        } finally {
            unmount?.();
            objectURL.restore();
        }
    });
});
