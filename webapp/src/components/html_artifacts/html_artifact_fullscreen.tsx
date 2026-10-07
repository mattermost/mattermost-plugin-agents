// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef} from 'react';
import ReactDOM from 'react-dom';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {Tag} from '@mattermost/compass-ui/components/tag';
import {CloseIcon, DownloadOutlineIcon, FileCodeOutlineIcon, RefreshIcon} from '@mattermost/compass-icons/components';

import {cancelPendingConsent} from './artifact_broker';
import ArtifactFrame, {ArtifactFrameHandle} from './artifact_frame';
import {downloadArtifact} from './download';

interface Props {
    fileId: string;
    fileName: string;
    onClose: () => void;
}

const FOCUSABLE = 'button, [href], iframe, [tabindex]:not([tabindex="-1"])';

const HTMLArtifactFullscreen = ({fileId, fileName, onClose}: Props) => {
    const intl = useIntl();
    const rootRef = useRef<HTMLDivElement>(null);
    const closeRef = useRef<HTMLButtonElement>(null);
    const frameRef = useRef<ArtifactFrameHandle>(null);

    // The viewer covers the inline card: a prompt pending there would be
    // invisible, so drop it and let this frame ask instead.
    useEffect(() => {
        cancelPendingConsent(fileId);
    }, [fileId]);
    const onCloseRef = useRef(onClose);
    onCloseRef.current = onClose;

    useEffect(() => {
        const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
        closeRef.current?.focus();

        const onKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.preventDefault();
                e.stopPropagation();
                onCloseRef.current();
                return;
            }
            const root = rootRef.current;
            if (e.key !== 'Tab' || !root) {
                return;
            }
            const focusables = Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)).
                filter((el) => !el.hasAttribute('disabled'));
            if (focusables.length === 0) {
                return;
            }
            const first = focusables[0];
            const last = focusables[focusables.length - 1];
            const active = document.activeElement;
            const outside = !root.contains(active);
            if (e.shiftKey && (active === first || outside)) {
                e.preventDefault();
                last.focus();
            } else if (!e.shiftKey && (active === last || outside)) {
                e.preventDefault();
                first.focus();
            }
        };
        document.addEventListener('keydown', onKeyDown, true);
        return () => {
            document.removeEventListener('keydown', onKeyDown, true);
            previousFocus?.focus?.({preventScroll: true});
        };
    }, []);

    const downloadLabel = intl.formatMessage({defaultMessage: 'Download'});
    const closeLabel = intl.formatMessage({defaultMessage: 'Close'});
    const reloadLabel = intl.formatMessage({defaultMessage: 'Reload'});

    return ReactDOM.createPortal(
        <Root
            ref={rootRef}
            role='dialog'
            aria-modal='true'
            aria-label={fileName}
            data-testid='html-artifact-fullscreen'
        >
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
                <Spacer/>
                <IconButton
                    size='small'
                    icon={<Icon glyph={<DownloadOutlineIcon/>}/>}
                    aria-label={downloadLabel}
                    title={downloadLabel}
                    onClick={() => downloadArtifact(fileId)}
                />
                <IconButton
                    size='small'
                    icon={<Icon glyph={<RefreshIcon/>}/>}
                    aria-label={reloadLabel}
                    title={reloadLabel}
                    onClick={() => frameRef.current?.reload()}
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
                <ArtifactFrame
                    ref={frameRef}
                    fileId={fileId}
                    fileName={fileName}
                    displayMode='fullscreen'
                    onEscape={onClose}
                />
            </Body>
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
    gap: 8px;
    min-height: 56px;
    padding: 0 12px 0 20px;
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
    margin: 0 4px;
    background: rgba(var(--center-channel-color-rgb), 0.16);
`;

// The artifact fills the area below the header edge to edge.
const Body = styled.div`
    position: relative;
    display: flex;
    flex: 1;
    flex-direction: column;
    min-height: 0;
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
