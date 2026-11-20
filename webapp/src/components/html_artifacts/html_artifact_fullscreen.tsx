// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import ReactDOM from 'react-dom';
import {FormattedMessage, useIntl} from 'react-intl';
import {useStore} from 'react-redux';
import styled from 'styled-components';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {Button} from '@mattermost/compass-ui/components/button';
import {Tag} from '@mattermost/compass-ui/components/tag';
import {CloseIcon, DownloadOutlineIcon, FileCodeOutlineIcon, RefreshIcon} from '@mattermost/compass-icons/components';

import type {ArtifactKind, ChannelArtifactVersionRecord} from '@/client';

import {getChannelLibraryClient} from '../channel_artifacts/library_client';

import {cancelPendingConsent} from './artifact_broker';
import ArtifactFrame, {ArtifactFrameHandle} from './artifact_frame';
import ArtifactNotice from './artifact_notice';
import {downloadArtifact} from './download';

interface Props {
    fileId: string;
    fileName: string;
    artifactKind?: ArtifactKind;

    // Shows a document the host renders itself, such as Markdown, in place of
    // the sandboxed frame. revision changes when the shown version is reloaded.
    // Library artifacts only.
    renderDocument?: (version: number, revision: number) => React.ReactNode;

    // Opened by the viewer's trusted click: see ArtifactFrame.
    startedByClick?: boolean;
    onClose: () => void;

    // Library viewer extras: what the viewer may do, and the entry's current version.
    canEdit?: boolean;
    canDelete?: boolean;
    headVersion?: number;
}

// An artifact the viewer has not engaged is no tab stop (tabindex -1); the
// frame's interact overlay button stands in for it.
const FOCUSABLE = 'button, [href], iframe:not([tabindex="-1"]), [tabindex]:not([tabindex="-1"]), select';

function focusableControls(root: HTMLElement | null): HTMLElement[] {
    if (!root) {
        return [];
    }
    return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)).
        filter((el) => !el.hasAttribute('disabled') && !el.hasAttribute('data-focus-sentinel') && isShown(el));
}

// isShown excludes controls hidden by CSS or by an ancestor, e.g. the iframe
// while the artifact is still loading (visibility: hidden).
function isShown(el: HTMLElement): boolean {
    return el.getClientRects().length > 0 && window.getComputedStyle(el).visibility !== 'hidden';
}

export function viewVersionAfterRefresh(current: number, previousHead: number, head: number, listed: number[]): number {
    if (current === previousHead || !listed.includes(current)) {
        return head;
    }
    return current;
}

const HTMLArtifactFullscreen = ({
    fileId,
    fileName,
    artifactKind = 'file',
    renderDocument,
    startedByClick = false,
    onClose,
    canEdit = false,
    canDelete = false,
    headVersion = 1,
}: Props) => {
    const intl = useIntl();
    const rootRef = useRef<HTMLDivElement>(null);
    const closeRef = useRef<HTMLButtonElement>(null);
    const frameRef = useRef<ArtifactFrameHandle>(null);
    const markdown = Boolean(renderDocument);
    const [documentRevision, setDocumentRevision] = useState(0);
    const reloadBody = (trusted: boolean) => {
        if (markdown) {
            setDocumentRevision((n) => n + 1);
        } else {
            frameRef.current?.reload(trusted);
        }
    };

    const store = useStore();

    const [versions, setVersions] = useState<ChannelArtifactVersionRecord[]>([]);
    const [viewVersion, setViewVersion] = useState<number>(headVersion);
    const [head, setHead] = useState<number>(headVersion);
    const followedHeadRef = useRef(headVersion);
    const [restoring, setRestoring] = useState(false);
    const [restoreError, setRestoreError] = useState(false);
    const [removeError, setRemoveError] = useState(false);

    // Read before the frame mounts: a frame the viewer's click started takes
    // focus as it mounts.
    const [previousFocus] = useState(() => (document.activeElement instanceof HTMLElement ? document.activeElement : null));

    // The viewer covers the inline card: a prompt pending there would be
    // invisible, so drop it and let this frame ask instead.
    useEffect(() => {
        cancelPendingConsent(artifactKind === 'library' ? undefined : fileId); // eslint-disable-line no-undefined
    }, [fileId, artifactKind]);
    const onCloseRef = useRef(onClose);
    onCloseRef.current = onClose;

    useEffect(() => {
        if (artifactKind !== 'library') {
            return undefined; // eslint-disable-line no-undefined
        }
        let cancelled = false;
        getChannelLibraryClient().versions(fileId).then((list) => {
            if (cancelled) {
                return;
            }
            const previousHead = followedHeadRef.current;
            setVersions(list);
            setHead(headVersion);
            setViewVersion((current) => viewVersionAfterRefresh(current, previousHead, headVersion, list.map((v) => v.version)));
            followedHeadRef.current = headVersion;
        }, () => {
            if (!cancelled) {
                setVersions([]);
            }
        });
        return () => {
            cancelled = true;
        };
    }, [artifactKind, fileId, headVersion]);

    useEffect(() => {
        // A frame the viewer's click started already holds focus inside the
        // dialog; moving it to Close would end that engagement.
        if (!rootRef.current?.contains(document.activeElement)) {
            closeRef.current?.focus();
        }

        // Tab wrapping is done by the focus sentinels below: a keydown check
        // cannot see Tab presses inside the artifact, so tabbing past its
        // last control would otherwise leave the dialog.
        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.preventDefault();
                e.stopPropagation();
                onCloseRef.current();
            }
        };
        document.addEventListener('keydown', onKeyDown, true);
        return () => {
            document.removeEventListener('keydown', onKeyDown, true);
            previousFocus?.focus?.({preventScroll: true});
        };
    }, [previousFocus]);

    const focusFirst = () => focusableControls(rootRef.current)[0]?.focus();
    const focusLast = () => focusableControls(rootRef.current).at(-1)?.focus();

    const downloadLabel = intl.formatMessage({defaultMessage: 'Download'});
    const closeLabel = intl.formatMessage({defaultMessage: 'Close'});
    const reloadLabel = intl.formatMessage({defaultMessage: 'Reload'});
    const viewingOlder = artifactKind === 'library' && viewVersion > 0 && viewVersion !== head;
    const viewedRecord = versions.find((v) => v.version === viewVersion);

    const onRemoveVersion = async () => {
        // Destructive confirm: removes this version's record, not the original message.
        if (!canDelete || versions.length < 2 || restoring) {
            return;
        }
        if (!window.confirm(intl.formatMessage({defaultMessage: 'Remove this version from the artifact? The original message and file are not affected.'}))) { // eslint-disable-line no-alert
            return;
        }
        setRestoring(true);
        setRemoveError(false);
        const removed = viewVersion;
        let entry;
        try {
            entry = await getChannelLibraryClient().removeVersion(fileId, removed);
        } catch {
            setRemoveError(true);
            setRestoring(false);
            return;
        }

        // The version is gone. Show that from what is already known, then try to
        // refresh the list; a failed refresh must not read as a failed removal.
        store.dispatch({type: 'CHANNEL_ARTIFACT_UPSERT', entry});
        setHead(entry.version);
        setViewVersion(entry.version);
        setVersions((current) => current.filter((v) => v.version !== removed).map((v) => ({...v, head: v.version === entry.version})));
        reloadBody(true);
        try {
            setVersions(await getChannelLibraryClient().versions(fileId));
        } catch {
            // Keep the locally updated list.
        }
        setRestoring(false);
    };

    const onRestore = async () => {
        if (!canEdit || !viewingOlder || restoring) {
            return;
        }
        setRestoring(true);
        setRestoreError(false);
        try {
            const entry = await getChannelLibraryClient().rollback(fileId, viewVersion);
            store.dispatch({type: 'CHANNEL_ARTIFACT_UPSERT', entry});
            const nextHead = entry.version || viewVersion;
            setHead(nextHead);
            setViewVersion(nextHead);
            try {
                setVersions(await getChannelLibraryClient().versions(fileId));
            } catch {
                // The head already moved. A later headVersion change reloads the menu.
            }
            reloadBody(true);
        } catch {
            setRestoreError(true);
        } finally {
            setRestoring(false);
        }
    };

    return ReactDOM.createPortal(
        <Root
            ref={rootRef}
            role='dialog'
            aria-modal='true'
            aria-label={fileName}
            data-testid='html-artifact-fullscreen'
        >
            <FocusSentinel
                tabIndex={0} // eslint-disable-line agents/no-noninteractive-tabindex -- focus-trap sentinel that wraps Tab back into the dialog
                data-focus-sentinel='start'
                data-testid='html-artifact-fullscreen-sentinel-start'
                onFocus={focusLast}
            />
            <Header>
                <Icon
                    size='20'
                    glyph={<FileCodeOutlineIcon/>}
                />
                <Title title={fileName}>{fileName}</Title>
                <Tag
                    size='small'
                    type='info'
                    label={markdown ? <FormattedMessage defaultMessage='Document'/> : <FormattedMessage defaultMessage='Interactive'/>}
                />
                {artifactKind === 'library' && versions.length > 0 && (
                    <VersionSelect
                        aria-label={intl.formatMessage({defaultMessage: 'Version'})}
                        value={viewVersion}
                        onChange={(e) => {
                            setViewVersion(Number(e.target.value));
                            setRestoreError(false);
                            setRemoveError(false);
                        }}
                        data-testid='html-artifact-version-select'
                    >
                        {versions.map((v) => (
                            <option
                                key={v.version}
                                value={v.version}
                            >
                                {`v${v.version} · ${new Date(v.create_at).toLocaleString()}${v.source_agent_display_name ? ` · ${v.source_agent_display_name}` : ''}${v.version === head ? ` · ${intl.formatMessage({defaultMessage: 'current'})}` : ''}${v.available ? '' : ` · ${intl.formatMessage({defaultMessage: 'unavailable'})}`}`}
                            </option>
                        ))}
                    </VersionSelect>
                )}
                {viewingOlder && canEdit && (
                    <Button
                        emphasis='tertiary'
                        size='x-small'
                        onClick={onRestore}
                        disabled={restoring || viewedRecord?.available === false}
                        data-testid='html-artifact-restore-version'
                    >
                        <FormattedMessage defaultMessage='Restore this version'/>
                    </Button>
                )}
                {canDelete && artifactKind === 'library' && versions.length > 1 && (
                    <Button
                        emphasis='tertiary'
                        size='x-small'
                        onClick={onRemoveVersion}
                        disabled={restoring}
                        data-testid='html-artifact-remove-version'
                    >
                        <FormattedMessage defaultMessage='Remove this version'/>
                    </Button>
                )}
                {removeError && (
                    <RestoreError
                        role='alert'
                        data-testid='html-artifact-remove-version-error'
                    >
                        <FormattedMessage defaultMessage='Could not remove this version.'/>
                    </RestoreError>
                )}
                {viewingOlder && canEdit && restoreError && (
                    <RestoreError
                        role='alert'
                        data-testid='html-artifact-restore-error'
                    >
                        <FormattedMessage defaultMessage='Could not restore this version.'/>
                    </RestoreError>
                )}
                <Spacer/>
                {!markdown && <ArtifactNotice/>}
                <Divider/>
                <IconButton
                    size='small'
                    icon={<Icon glyph={<DownloadOutlineIcon/>}/>}
                    aria-label={downloadLabel}
                    title={downloadLabel}
                    onClick={() => downloadArtifact(
                        fileId,
                        artifactKind,
                        artifactKind === 'library' ? viewVersion : 0,
                    )}
                    data-testid='html-artifact-fullscreen-download'
                />
                <IconButton
                    size='small'
                    icon={<Icon glyph={<RefreshIcon/>}/>}
                    aria-label={reloadLabel}
                    title={reloadLabel}
                    onClick={(e) => reloadBody(e.nativeEvent.isTrusted)}
                    data-testid='html-artifact-fullscreen-reload'
                />
                <Divider/>
                <IconButton
                    ref={closeRef}
                    size='small'
                    icon={<Icon glyph={<CloseIcon/>}/>}
                    aria-label={closeLabel}
                    title={closeLabel}
                    onClick={onClose}
                    data-testid='html-artifact-fullscreen-close'
                />
            </Header>
            <Body>
                {renderDocument ? (
                    <Document key={viewVersion}>
                        {renderDocument(viewVersion, documentRevision)}
                    </Document>
                ) : (
                    <ArtifactFrame
                        key={artifactKind === 'library' ? viewVersion : 'file'}
                        ref={frameRef}
                        fileId={fileId}
                        fileName={fileName}
                        artifactKind={artifactKind}
                        version={artifactKind === 'library' ? viewVersion : 0}
                        displayMode='fullscreen'
                        startedByClick={startedByClick}
                        onEscape={onClose}
                    />
                )}
            </Body>
            <FocusSentinel
                tabIndex={0} // eslint-disable-line agents/no-noninteractive-tabindex -- focus-trap sentinel that wraps Tab back into the dialog
                data-focus-sentinel='end'
                data-testid='html-artifact-fullscreen-sentinel-end'
                onFocus={focusFirst}
            />
        </Root>,
        document.body,
    );
};

export default HTMLArtifactFullscreen;

const Root = styled.div`
    position: fixed;
    inset: 0;
    z-index: 10000; /* above the Agents onboarding tour (9999) */
    display: flex;
    flex-direction: column;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
`;

const Header = styled.div`
    display: flex;
    align-items: center;
    flex: none;
    gap: var(--spacing-xs, 8px);
    min-height: 56px;
    padding: 0 var(--spacing-sm, 12px) 0 20px;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    background: rgba(var(--center-channel-color-rgb), 0.04);
    box-shadow: 0 2px 3px rgba(0, 0, 0, 0.08);
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const Spacer = styled.div`
    flex: 1;
`;

const Divider = styled.div`
    width: 1px;
    height: 24px;
    margin: 0 var(--spacing-xxs, 4px);
    background: rgba(var(--center-channel-color-rgb), 0.16);
`;

// The artifact sits in an inset, bordered panel (see ArtifactFrame) so it
// never reads as part of the Mattermost interface.
const Body = styled.div`
    position: relative;
    display: flex;
    flex: 1;
    flex-direction: column;
    min-height: 0;
    padding: var(--spacing-sm, 12px);
    background: rgba(var(--center-channel-color-rgb), 0.04);
`;

// The reader scrolls inside the same inset panel the artifact frame has.
const Document = styled.div`
    flex: 1;
    min-height: 0;
    padding: var(--spacing-sm, 12px) var(--spacing-md, 16px);
    overflow: auto;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background: var(--center-channel-bg);
`;

const Title = styled.h2`
    flex: 0 1 auto;
    min-width: 0;
    margin: 0;
    overflow: hidden;
    color: var(--center-channel-color);
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const RestoreError = styled.p`
    margin: 0;
    color: var(--error-text);
    font-size: 12px;
    line-height: 16px;
`;

const VersionSelect = styled.select`
    max-width: 280px;
    padding: var(--spacing-xxs, 4px) var(--spacing-xs, 8px);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.24);
    border-radius: 4px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-size: 12px;
    line-height: 16px;
`;

// Invisible tab stops at both ends of the dialog that send focus back inside.
const FocusSentinel = styled.div`
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
`;
