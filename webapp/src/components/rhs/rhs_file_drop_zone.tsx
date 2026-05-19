// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import fileOverlayImage from '../../images/file_overlay.svg';

// Mirrors the upstream Mattermost RHS drag-drop behavior. The plugin embeds
// AdvancedTextEditor outside the standard RHS chrome (no `.post-right__container`
// / `.row.main` wrapper), so the editor's internal FileUpload component cannot
// attach its drag listeners. We attach our own and forward dropped files to the
// AdvancedTextEditor's hidden file input, which then runs the normal upload +
// draft-update pipeline.
//
// Overlay visuals match `webapp/channels/src/components/file_upload_overlay/`
// in mattermost-server (`.file-overlay.right-file-overlay`).
const Wrapper = styled.div`
    position: relative;
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
`;

const Overlay = styled.div<{$visible: boolean}>`
    position: absolute;
    z-index: 13;
    top: 0;
    left: 0;
    width: 100%;
    height: 100%;
    display: ${({$visible}) => ($visible ? 'block' : 'none')};
    color: #ffffff;
    font-size: 18px;
    font-weight: 600;
    pointer-events: none;
    text-align: center;
`;

const OverlayIndent = styled.div`
    position: relative;
    display: flex;
    height: 100%;
    align-items: center;
    justify-content: center;
    background-color: rgba(0, 0, 0, 0.75);
`;

const OverlayCircle = styled.div`
    display: flex;
    width: 300px;
    height: 300px;
    max-height: 100%;
    flex-direction: column;
    flex-wrap: wrap;
    align-items: center;
    justify-content: center;
    gap: 20px;
    pointer-events: none;
`;

const OverlayFilesImage = styled.img`
    display: block;
    width: 150px;
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
                <OverlayIndent>
                    <OverlayCircle>
                        <OverlayFilesImage
                            src={fileOverlayImage}
                            alt=''
                            loading='lazy'
                        />
                        <FormattedMessage defaultMessage='Drop a file to upload it.'/>
                    </OverlayCircle>
                </OverlayIndent>
            </Overlay>
        </Wrapper>
    );
};

export default RhsFileDropZone;
