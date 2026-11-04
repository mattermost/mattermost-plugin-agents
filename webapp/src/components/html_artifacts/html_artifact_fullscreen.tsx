// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import ReactDOM from 'react-dom';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector} from 'react-redux';
import styled from 'styled-components';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {Button} from '@mattermost/compass-ui/components/button';
import {Tag} from '@mattermost/compass-ui/components/tag';
import {CloseIcon, DownloadOutlineIcon, FileCodeOutlineIcon, RefreshIcon} from '@mattermost/compass-icons/components';

import {
    listChannelArtifactVersions,
    rollbackChannelArtifact,
    type ArtifactKind,
    type ChannelArtifactVersionRecord,
} from '@/client';
import {
    getChannelArtifactDownloadEnabled,
    getChannelArtifactRenderingEnabled,
} from '@/selectors';

import {cancelPendingConsent} from './artifact_broker';
import ArtifactFrame, {ArtifactFrameHandle} from './artifact_frame';
import ArtifactNotice from './artifact_notice';
import {downloadArtifact} from './download';

interface Props {
    fileId: string;
    fileName: string;
    artifactKind?: ArtifactKind;

    // Opened by the viewer's trusted click: see ArtifactFrame.
    startedByClick?: boolean;
    onClose: () => void;

    // Library viewer extras
    canManage?: boolean;
    downloadEnabled?: boolean;
    renderEnabled?: boolean;
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
    startedByClick = false,
    onClose,
    canManage = false,
    downloadEnabled: downloadEnabledProp,
    renderEnabled: renderEnabledProp,
    headVersion = 1,
}: Props) => {
    const intl = useIntl();
    const rootRef = useRef<HTMLDivElement>(null);
    const closeRef = useRef<HTMLButtonElement>(null);
    const frameRef = useRef<ArtifactFrameHandle>(null);

    const downloadFromSelector = useSelector(getChannelArtifactDownloadEnabled);
    const renderFromSelector = useSelector(getChannelArtifactRenderingEnabled);
    const downloadEnabled = artifactKind === 'library' ? (downloadEnabledProp ?? downloadFromSelector) : true;
    const renderEnabled = artifactKind === 'library' ? (renderEnabledProp ?? renderFromSelector) : true;

    const [versions, setVersions] = useState<ChannelArtifactVersionRecord[]>([]);
    const [viewVersion, setViewVersion] = useState<number>(headVersion);
    const [head, setHead] = useState<number>(headVersion);
    const followedHeadRef = useRef(headVersion);
    const [restoring, setRestoring] = useState(false);

    // Read before the frame mounts: a frame the viewer's click started takes
    // focus as it mounts.
    const [previousFocus] = useState(() => (document.activeElement instanceof HTMLElement ? document.activeElement : null));

    // The viewer covers the inline card: a prompt pending there would be
    // invisible, so drop it and let this frame ask instead.
    useEffect(() => {
        cancelPendingConsent(fileId);
    }, [fileId]);
    const onCloseRef = useRef(onClose);
    onCloseRef.current = onClose;

    useEffect(() => {
        if (artifactKind !== 'library') {
            return undefined; // eslint-disable-line no-undefined
        }
        let cancelled = false;
        listChannelArtifactVersions(fileId).then((resp) => {
            if (cancelled) {
                return;
            }
            const list = (resp.versions || []).slice().sort((a, b) => b.version - a.version);
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

    const onRestore = async () => {
        if (!canManage || !viewingOlder || restoring) {
            return;
        }
        setRestoring(true);
        try {
            const entry = await rollbackChannelArtifact(fileId, viewVersion);
            const nextHead = entry.version || viewVersion;
            setHead(nextHead);
            setViewVersion(nextHead);
            const resp = await listChannelArtifactVersions(fileId);
            const list = (resp.versions || []).slice().sort((a, b) => b.version - a.version);
            setVersions(list);
            frameRef.current?.reload(true);
        } catch {
            // best effort
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
                    label={<FormattedMessage defaultMessage='Interactive'/>}
                />
                {artifactKind === 'library' && versions.length > 0 && (
                    <VersionSelect
                        aria-label={intl.formatMessage({defaultMessage: 'Version'})}
                        value={viewVersion}
                        onChange={(e) => setViewVersion(Number(e.target.value))}
                        data-testid='html-artifact-version-select'
                    >
                        {versions.map((v) => (
                            <option
                                key={v.version}
                                value={v.version}
                            >
                                {`v${v.version} · ${new Date(v.update_at).toLocaleString()}${v.source_agent_display_name || v.updated_by ? ` · ${v.source_agent_display_name || v.updated_by}` : ''}${v.version === head ? ` · ${intl.formatMessage({defaultMessage: 'current'})}` : ''}`}
                            </option>
                        ))}
                    </VersionSelect>
                )}
                {viewingOlder && canManage && (
                    <Button
                        emphasis='tertiary'
                        size='x-small'
                        onClick={onRestore}
                        disabled={restoring}
                        data-testid='html-artifact-restore-version'
                    >
                        <FormattedMessage defaultMessage='Restore this version'/>
                    </Button>
                )}
                <Spacer/>
                <ArtifactNotice/>
                <Divider/>
                {downloadEnabled && (
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
                )}
                {renderEnabled && (
                    <IconButton
                        size='small'
                        icon={<Icon glyph={<RefreshIcon/>}/>}
                        aria-label={reloadLabel}
                        title={reloadLabel}
                        onClick={(e) => frameRef.current?.reload(e.nativeEvent.isTrusted)}
                        data-testid='html-artifact-fullscreen-reload'
                    />
                )}
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
                {renderEnabled ? (
                    <ArtifactFrame
                        ref={frameRef}
                        fileId={fileId}
                        fileName={fileName}
                        artifactKind={artifactKind}
                        version={artifactKind === 'library' ? viewVersion : 0}
                        downloadEnabled={downloadEnabled}
                        displayMode='fullscreen'
                        startedByClick={startedByClick}
                        onEscape={onClose}
                    />
                ) : (
                    <RenderDisabledNotice
                        role='status'
                        data-testid='html-artifact-render-disabled'
                    >
                        <FormattedMessage defaultMessage='Rendering of channel artifacts is disabled on this server. You can still download the HTML if downloads are allowed.'/>
                    </RenderDisabledNotice>
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

const RenderDisabledNotice = styled.div`
    display: flex;
    flex: 1;
    align-items: center;
    justify-content: center;
    padding: var(--spacing-xl, 24px);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background: var(--center-channel-bg);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    line-height: 20px;
    text-align: center;
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
