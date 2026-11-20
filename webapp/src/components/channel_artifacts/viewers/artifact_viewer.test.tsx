// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {fireEvent, render, screen, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {getChannelArtifactContent, type ChannelArtifactEntry} from '@/client';

import {ArtifactViewer, openArtifact} from './artifact_viewer';
import {registerArtifactViewer, type ArtifactViewerProps} from './registry';

jest.mock('@/client', () => ({
    getChannelArtifactContent: jest.fn(),
    channelArtifactDownloadURL: jest.fn((id: string, version?: number) => `/download/${id}?version=${version}`),
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
        }),
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => <>{defaultMessage}</>,
    };
});

jest.mock('../../html_artifacts/html_artifact_fullscreen', () => {
    return function MockFullscreen(props: {
        fileId: string;
        artifactKind?: string;
        headVersion?: number;
        canEdit?: boolean;
        canDelete?: boolean;
        startedByClick?: boolean;
        renderDocument?: (version: number, revision: number) => React.ReactNode;
    }) {
        return (
            <div
                data-testid='html-artifact-fullscreen'
                data-file-id={props.fileId}
                data-kind={props.artifactKind}
                data-head={props.headVersion}
                data-can-edit={String(props.canEdit)}
                data-can-delete={String(props.canDelete)}
                data-started-by-click={String(props.startedByClick)}
            >
                {props.renderDocument?.(3, 0)}
            </div>
        );
    };
});

jest.mock('../../html_artifacts/artifact_frame', () => {
    const {forwardRef} = jest.requireActual('react');
    return {
        __esModule: true,
        default: forwardRef((props: {fileId: string; version?: number; displayMode: string; artifactKind?: string}) => {
            return (
                <div
                    data-testid='html-artifact-frame'
                    data-file-id={props.fileId}
                    data-version={props.version}
                    data-display-mode={props.displayMode}
                    data-kind={props.artifactKind}
                />
            );
        }),
    };
});

jest.mock('../../post_text', () => ({
    __esModule: true,
    default: (props: {message: string; channelID: string; postID: string}) => (
        <div
            data-testid='post-text'
            data-channel={props.channelID}
            data-post={props.postID}
        >
            {props.message}
        </div>
    ),
}));

const entry: ChannelArtifactEntry = {
    id: 'artifactid012345678901234',
    channel_id: 'channelid0123456789012345',
    title: 'Charter',
    version: 2,
    sha256: 'abc',
    size: 12,
    source_post_id: 'postid01234567890123456789',
    source_file_id: 'fileid01234567890123456789',
    source_bot_user_id: 'botid012345678901234567890',
    created_by: 'userid0123456789012345678',
    create_at: 1,
    updated_by: 'userid0123456789012345678',
    update_at: 1,
    available: true,
};

const markdownEntry: ChannelArtifactEntry = {...entry, format: 'markdown'};

function renderViewer(props: Partial<React.ComponentProps<typeof ArtifactViewer>> & {entry?: ChannelArtifactEntry} = {}) {
    const onClose = jest.fn();
    const result = render(
        <IntlProvider locale='en'>
            <ArtifactViewer
                entry={entry}
                onClose={onClose}
                {...props}
            />
        </IntlProvider>,
    );
    return {onClose, ...result};
}

describe('ArtifactViewer', () => {
    beforeEach(() => {
        jest.mocked(getChannelArtifactContent).mockReset();
    });

    test('shows an HTML artifact fullscreen by default, as a library artifact', () => {
        renderViewer({canEdit: true, startedByClick: true});
        const viewer = screen.getByTestId('html-artifact-fullscreen');
        expect(viewer.getAttribute('data-file-id')).toBe(entry.id);
        expect(viewer.getAttribute('data-kind')).toBe('library');
        expect(viewer.getAttribute('data-head')).toBe('2');
        expect(viewer.getAttribute('data-can-edit')).toBe('true');
        expect(viewer.getAttribute('data-can-delete')).toBe('false');
        expect(viewer.getAttribute('data-started-by-click')).toBe('true');
    });

    test('an entry from a server that sends no format is HTML', () => {
        renderViewer({entry: {...entry, format: undefined}}); // eslint-disable-line no-undefined
        expect(screen.getByTestId('html-artifact-fullscreen')).toBeTruthy();
    });

    test('an inline HTML artifact renders in its container with no overlay', () => {
        const onPopOut = jest.fn();
        const {onClose} = renderViewer({presentation: 'inline', onPopOut});

        expect(screen.queryByTestId('html-artifact-fullscreen')).toBeNull();
        const frame = screen.getByTestId('html-artifact-frame');
        expect(frame.getAttribute('data-display-mode')).toBe('inline');
        expect(frame.getAttribute('data-kind')).toBe('library');
        expect(frame.getAttribute('data-version')).toBe('2');

        fireEvent.click(screen.getByTestId('artifact-inline-pop-out'));
        expect(onPopOut).toHaveBeenCalledTimes(1);
        fireEvent.click(screen.getByTestId('artifact-inline-back'));
        expect(onClose).toHaveBeenCalledTimes(1);
        expect(screen.getByTestId(`channel-artifact-format-${entry.id}`).textContent).toBe('HTML');
    });

    test('an inline viewer offers no pop out unless the host handles it', () => {
        renderViewer({presentation: 'inline'});
        expect(screen.queryByTestId('artifact-inline-pop-out')).toBeNull();
    });

    test('an inline Markdown artifact is read through the library client and rendered by the host', async () => {
        jest.mocked(getChannelArtifactContent).mockResolvedValue('# Goals');
        renderViewer({entry: markdownEntry, presentation: 'inline'});

        const text = await screen.findByTestId('post-text');
        expect(text.textContent).toBe('# Goals');
        expect(text.getAttribute('data-channel')).toBe(entry.channel_id);
        expect(text.getAttribute('data-post')).toBe(entry.source_post_id);
        expect(getChannelArtifactContent).toHaveBeenCalledWith(entry.id, 2);
        expect(screen.queryByTestId('html-artifact-frame')).toBeNull();
        expect(screen.getByTestId(`channel-artifact-format-${entry.id}`).textContent).toBe('Markdown');
    });

    test('a fullscreen Markdown artifact hands the fullscreen viewer its document', async () => {
        jest.mocked(getChannelArtifactContent).mockResolvedValue('# Goals');
        renderViewer({entry: markdownEntry});

        expect(screen.getByTestId('html-artifact-fullscreen').getAttribute('data-file-id')).toBe(entry.id);
        await screen.findByTestId('post-text');
        expect(getChannelArtifactContent).toHaveBeenCalledWith(entry.id, 3);
    });

    test('a format with no viewer says so', () => {
        renderViewer({entry: {...entry, format: 'pdf' as never}});
        expect(screen.getByTestId('artifact-viewer-unsupported')).toBeTruthy();
    });

    test('a viewer registered for a format serves it, and can be withdrawn', () => {
        const Custom = (props: ArtifactViewerProps) => <div data-testid='custom-viewer'>{`${props.entry.title}:${props.presentation}`}</div>;
        const unregister = registerArtifactViewer('markdown', Custom);
        try {
            const {unmount} = renderViewer({entry: markdownEntry, presentation: 'inline'});
            expect(screen.getByTestId('custom-viewer').textContent).toBe('Charter:inline');
            unmount();
        } finally {
            unregister();
        }
        jest.mocked(getChannelArtifactContent).mockResolvedValue('x');
        renderViewer({entry: markdownEntry, presentation: 'inline'});
        expect(screen.queryByTestId('custom-viewer')).toBeNull();
    });

    test('openArtifact is the entry that surfaces use, keyed by artifact', async () => {
        jest.mocked(getChannelArtifactContent).mockResolvedValue('# Goals');
        const onClose = jest.fn();
        const element = openArtifact(markdownEntry, {presentation: 'inline', onClose});
        expect(element.key).toBe(entry.id);
        render(<IntlProvider locale='en'>{element}</IntlProvider>);
        await waitFor(() => expect(screen.getByTestId('post-text')).toBeTruthy());
    });
});
