// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

// Mirrors the upstream Mattermost RHS drag-drop behavior. The plugin embeds
// AdvancedTextEditor outside the standard RHS chrome (no `.post-right__container`
// / `.row.main` wrapper), so the editor's internal FileUpload component cannot
// attach its drag listeners. We attach our own and forward dropped files to the
// AdvancedTextEditor's hidden file input, which then runs the normal upload +
// draft-update pipeline.
const Wrapper = styled.div`
    position: relative;
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
`;

const Overlay = styled.div<{$visible: boolean}>`
    position: absolute;
    inset: 0;
    z-index: 1000;
    display: ${({$visible}) => ($visible ? 'flex' : 'none')};
    align-items: center;
    justify-content: center;
    background: rgba(var(--button-bg-rgb), 0.12);
    border: 2px dashed rgba(var(--button-bg-rgb), 0.6);
    border-radius: 4px;
    pointer-events: none;
`;

const OverlayMessage = styled.div`
    background: rgb(var(--center-channel-bg-rgb));
    color: rgb(var(--center-channel-color));
    border-radius: 4px;
    padding: 12px 20px;
    font-size: 14px;
    font-weight: 600;
    box-shadow: 0 4px 6px rgba(0, 0, 0, 0.1);
`;

const dataTransferHasFiles = (dataTransfer: DataTransfer | null): boolean =>
    Boolean(dataTransfer?.types.includes('Files'));

type Props = {
    children: React.ReactNode;
    className?: string;
}

const RhsFileDropZone = ({children, className}: Props) => {
    const containerRef = useRef<HTMLDivElement>(null);
    const dragCounterRef = useRef(0);
    const [isDragging, setIsDragging] = useState(false);

    const forwardFilesToEditor = useCallback((files: FileList) => {
        const container = containerRef.current;
        if (!container) {
            return;
        }
        const fileInput = container.querySelector<HTMLInputElement>('input[type="file"]');
        if (!fileInput) {
            return;
        }
        const dataTransfer = new DataTransfer();
        for (const file of Array.from(files)) {
            dataTransfer.items.add(file);
        }
        fileInput.files = dataTransfer.files;
        fileInput.dispatchEvent(new Event('change', {bubbles: true}));
    }, []);

    useEffect(() => {
        const container = containerRef.current;
        if (!container) {
            return () => {
                // no listeners were attached
            };
        }

        const handleDragEnter = (e: DragEvent) => {
            if (!dataTransferHasFiles(e.dataTransfer)) {
                return;
            }
            e.preventDefault();
            dragCounterRef.current += 1;
            setIsDragging(true);
        };

        const handleDragOver = (e: DragEvent) => {
            if (!dataTransferHasFiles(e.dataTransfer)) {
                return;
            }
            e.preventDefault();
            if (e.dataTransfer) {
                e.dataTransfer.dropEffect = 'copy';
            }
        };

        const handleDragLeave = (e: DragEvent) => {
            if (!dataTransferHasFiles(e.dataTransfer)) {
                return;
            }
            e.preventDefault();
            dragCounterRef.current -= 1;
            if (dragCounterRef.current <= 0) {
                dragCounterRef.current = 0;
                setIsDragging(false);
            }
        };

        const handleDrop = (e: DragEvent) => {
            if (!dataTransferHasFiles(e.dataTransfer)) {
                return;
            }
            e.preventDefault();
            dragCounterRef.current = 0;
            setIsDragging(false);

            const files = e.dataTransfer?.files;
            if (!files || files.length === 0) {
                return;
            }
            forwardFilesToEditor(files);
        };

        container.addEventListener('dragenter', handleDragEnter);
        container.addEventListener('dragover', handleDragOver);
        container.addEventListener('dragleave', handleDragLeave);
        container.addEventListener('drop', handleDrop);

        return () => {
            container.removeEventListener('dragenter', handleDragEnter);
            container.removeEventListener('dragover', handleDragOver);
            container.removeEventListener('dragleave', handleDragLeave);
            container.removeEventListener('drop', handleDrop);
        };
    }, [forwardFilesToEditor]);

    return (
        <Wrapper
            ref={containerRef}
            className={className}
            data-testid='rhs-file-drop-zone'
        >
            {children}
            <Overlay
                $visible={isDragging}
                data-testid='rhs-file-drop-overlay'
                aria-hidden={!isDragging}
            >
                <OverlayMessage>
                    <FormattedMessage defaultMessage='Drop a file to attach it'/>
                </OverlayMessage>
            </Overlay>
        </Wrapper>
    );
};

export default RhsFileDropZone;
