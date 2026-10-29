// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {createContext, useCallback, useContext, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

export type AttachmentDropHandler = (files: File[]) => void;

// Registers the active composer's file handler; returns the unregister call.
type RegisterDropHandler = (handler: AttachmentDropHandler) => () => void;

const AttachmentDropContext = createContext<RegisterDropHandler | null>(null);

// The composer inside a drop zone calls this to receive dropped files. Outside
// a drop zone it is a no-op.
export const useAttachmentDropTarget = (): RegisterDropHandler | null => useContext(AttachmentDropContext);

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
// target for the composer inside it: while files are dragged over it shows
// the prototype's "Drop file to attach" overlay, and a drop hands the files to
// the composer, which uploads them. Drags that carry no files (text, links)
// are left alone, and so is everything while no composer is ready to attach.
const AgentChatDropZone = ({children, testId}: AgentChatDropZoneProps) => {
    const intl = useIntl();
    const handlerRef = useRef<AttachmentDropHandler | null>(null);
    const depth = useRef(0);
    const [dragging, setDragging] = useState(false);

    const register = useCallback<RegisterDropHandler>((handler) => {
        handlerRef.current = handler;
        return () => {
            if (handlerRef.current === handler) {
                handlerRef.current = null;
                depth.current = 0;
                setDragging(false);
            }
        };
    }, []);

    const accepts = (e: React.DragEvent) => Boolean(handlerRef.current) && hasFiles(e.dataTransfer);

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
        if (!accepts(e)) {
            return;
        }
        e.preventDefault();
        depth.current = 0;
        setDragging(false);
        const files = Array.from(e.dataTransfer.files ?? []);
        if (files.length > 0) {
            handlerRef.current?.(files);
        }
    };

    const value = useMemo(() => register, [register]);

    return (
        <AttachmentDropContext.Provider value={value}>
            <Zone
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
        </AttachmentDropContext.Provider>
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
