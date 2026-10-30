// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {HOST_COMPOSER_FILE_INPUT_SELECTOR} from './agent_chat_host_styles';

const hasFiles = (dataTransfer: DataTransfer | null): boolean => Boolean(dataTransfer?.types && Array.from(dataTransfer.types).includes('Files'));

// Above the host ThreadViewer's own stacking (its sticky date separators and
// post menus); no compass token covers z-index.
const DROP_OVERLAY_Z_INDEX = 20;

// The prototype's dashed drop outline; neither the host nor compass has a
// dashed border variable.
const DROP_OVERLAY_BORDER = '1.5px dashed var(--button-bg)';

interface AgentChatDropZoneProps {
    children: React.ReactNode;
    testId?: string;
}

// Makes a whole chat surface (the product view chat, the RHS pane) a drop
// target for the host composer inside it: while files are dragged over it shows
// the prototype's "Drop file to attach" overlay, and a drop hands the files to
// the composer's own file input, so the host uploads and attaches them as if
// they were picked with its attach button. The host binds its own drop target
// with a page-level selector (components/file_upload/file_upload.tsx) that
// does not exist in the plugin's views. Drags that carry no files (text,
// links) are left alone, and so is everything while no composer is there.
const AgentChatDropZone = ({children, testId}: AgentChatDropZoneProps) => {
    const intl = useIntl();
    const zoneRef = useRef<HTMLDivElement>(null);
    const depth = useRef(0);
    const [dragging, setDragging] = useState(false);

    const fileInput = () => zoneRef.current?.querySelector<HTMLInputElement>(HOST_COMPOSER_FILE_INPUT_SELECTOR) ?? null;
    const accepts = (e: React.DragEvent) => hasFiles(e.dataTransfer) && fileInput() !== null;

    const onDragEnter = (e: React.DragEvent) => {
        if (!accepts(e)) {
            return;
        }
        e.preventDefault();
        depth.current += 1;
        setDragging(true);
    };

    const onDragOver = (e: React.DragEvent) => {
        if (!accepts(e)) {
            return;
        }
        e.preventDefault();
        e.dataTransfer.dropEffect = 'copy';
    };

    const onDragLeave = (e: React.DragEvent) => {
        if (!accepts(e)) {
            return;
        }
        depth.current = Math.max(0, depth.current - 1);
        if (depth.current === 0) {
            setDragging(false);
        }
    };

    const onDrop = (e: React.DragEvent) => {
        if (!hasFiles(e.dataTransfer)) {
            return;
        }
        e.preventDefault();
        depth.current = 0;
        setDragging(false);
        const input = fileInput();
        const files = e.dataTransfer.files;
        if (!input || !files?.length) {
            return;
        }

        // The host's change handler on that input (FileUpload.handleChange) uploads input.files.
        input.files = files;
        input.dispatchEvent(new Event('change', {bubbles: true}));
    };

    return (
        <Zone
            ref={zoneRef}
            data-testid={testId}
            onDragEnter={onDragEnter}
            onDragOver={onDragOver}
            onDragLeave={onDragLeave}
            onDrop={onDrop}
        >
            {children}
            {dragging && (
                <Overlay
                    data-testid='agent-chat-drop-overlay'
                    aria-hidden={true}
                >
                    <OverlayLabel>{intl.formatMessage({defaultMessage: 'Drop file to attach'})}</OverlayLabel>
                </Overlay>
            )}
        </Zone>
    );
};

const Zone = styled.div`
    position: relative;
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    min-width: 0;
    min-height: 0;
`;

const Overlay = styled.div`
    position: absolute;
    inset: 0;
    z-index: ${DROP_OVERLAY_Z_INDEX};
    display: flex;
    align-items: center;
    justify-content: center;
    border: ${DROP_OVERLAY_BORDER};
    background: rgba(var(--button-bg-rgb), 0.08);
    pointer-events: none;
`;

const OverlayLabel = styled.p`
    margin: 0;
    padding: var(--spacing-s) var(--spacing-l);
    border: var(--border-default);
    border-radius: var(--radius-s);
    background: var(--center-channel-bg);
    font-family: var(--font-family-body);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-100);
    color: var(--button-bg);
`;

export default AgentChatDropZone;
