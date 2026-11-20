// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getChannelArtifactContent} from '@/client';

import MarkdownArtifactBody from './markdown_artifact_body';

jest.mock('@/client', () => ({
    getChannelArtifactContent: jest.fn(),
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => <>{defaultMessage}</>,
    };
});

jest.mock('../../post_text', () => ({
    __esModule: true,
    default: (props: {message: string}) => <div data-testid='post-text'>{props.message}</div>,
}));

const entry = {id: 'artifactid012345678901234', channel_id: 'channelid0123456789012345', source_post_id: 'postid01234567890123456789'};

function deferred<T>() {
    let resolve!: (value: T) => void;
    let reject!: (reason?: unknown) => void;
    const promise = new Promise<T>((res, rej) => {
        resolve = res;
        reject = rej;
    });
    return {promise, resolve, reject};
}

function renderBody(version = 1, revision = '') {
    const ui = (v: number, r: string) => (
        <IntlProvider locale='en'>
            <MarkdownArtifactBody
                entry={entry}
                version={v}
                revision={r}
            />
        </IntlProvider>
    );
    const result = render(ui(version, revision));
    return {...result, update: (v: number, r = '') => result.rerender(ui(v, r))};
}

describe('MarkdownArtifactBody', () => {
    beforeEach(() => {
        jest.mocked(getChannelArtifactContent).mockReset();
    });

    test('shows loading, then the document', async () => {
        const pending = deferred<string>();
        jest.mocked(getChannelArtifactContent).mockReturnValue(pending.promise);
        renderBody();
        expect(screen.getByTestId('artifact-markdown-loading')).toBeTruthy();

        await act(async () => {
            pending.resolve('# Hello');
        });
        expect(screen.getByTestId('post-text').textContent).toBe('# Hello');
        expect(screen.queryByTestId('artifact-markdown-loading')).toBeNull();
    });

    test('a failed load can be retried', async () => {
        jest.mocked(getChannelArtifactContent).mockRejectedValueOnce(new Error('gone')).mockResolvedValueOnce('# Back');
        renderBody();

        fireEvent.click(await screen.findByTestId('artifact-markdown-retry'));
        expect((await screen.findByTestId('post-text')).textContent).toBe('# Back');
        expect(getChannelArtifactContent).toHaveBeenCalledTimes(2);
    });

    test('a response for a version that was left is ignored', async () => {
        const first = deferred<string>();
        const second = deferred<string>();
        jest.mocked(getChannelArtifactContent).mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
        const {update} = renderBody(1);
        update(2);

        await act(async () => {
            second.resolve('version two');
        });
        await act(async () => {
            first.resolve('version one');
        });
        expect(screen.getByTestId('post-text').textContent).toBe('version two');
        expect(getChannelArtifactContent).toHaveBeenNthCalledWith(1, entry.id, 1);
        expect(getChannelArtifactContent).toHaveBeenNthCalledWith(2, entry.id, 2);
    });

    test('a new revision of the same version is fetched again', async () => {
        jest.mocked(getChannelArtifactContent).mockResolvedValueOnce('before').mockResolvedValueOnce('after');
        const {update} = renderBody(1, 'a');
        expect((await screen.findByTestId('post-text')).textContent).toBe('before');

        update(1, 'b');
        await waitFor(() => expect(screen.getByTestId('post-text').textContent).toBe('after'));
    });

    test('nothing is set after it unmounts', async () => {
        const pending = deferred<string>();
        jest.mocked(getChannelArtifactContent).mockReturnValue(pending.promise);
        const errors = jest.spyOn(console, 'error').mockImplementation(jest.fn());
        const {unmount} = renderBody();
        unmount();
        await act(async () => {
            pending.resolve('late');
        });
        expect(errors).not.toHaveBeenCalled();
        errors.mockRestore();
    });
});
