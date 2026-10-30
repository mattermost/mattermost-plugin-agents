// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl, type IntlShape} from 'react-intl';
import styled from 'styled-components';

import {AttachmentCard, type AttachmentCardFileType} from '@mattermost/compass-ui/components/attachment-card';
import {Button} from '@mattermost/compass-ui/components/button';

import {getFileThumbnailUrl} from '@/client';

import type {ComposerAttachment} from './use_composer_attachments';

const MEGABYTE = 1024 * 1024;

// The card icon for a file, as the prototype's attachmentFileType picks it.
export const attachmentFileType = (file: File): AttachmentCardFileType => {
    const name = file.name.toLowerCase();
    const type = file.type;
    if (type === 'application/pdf' || name.endsWith('.pdf')) {
        return 'pdf';
    }
    if (type.includes('spreadsheet') || type.includes('excel') || (/\.(xlsx?|csv)$/).test(name)) {
        return 'excel';
    }
    if (type.includes('word') || (/\.docx?$/).test(name)) {
        return 'word';
    }
    if (type.includes('presentation') || (/\.pptx?$/).test(name)) {
        return 'powerpoint';
    }
    if (type.startsWith('image/') || (/\.(png|jpe?g|gif|webp)$/).test(name)) {
        return 'image-icon';
    }
    if (type.startsWith('video/')) {
        return 'video';
    }
    if (type.startsWith('audio/')) {
        return 'audio';
    }
    if (type.includes('zip') || name.endsWith('.zip')) {
        return 'zip';
    }
    if ((/\.(txt|md|markdown)$/).test(name)) {
        return 'text';
    }
    if ((/\.(js|ts|tsx|jsx|json|html|css|go|py)$/).test(name)) {
        return 'code';
    }
    return 'generic';
};

// "PDF 15 kB", like the prototype's formatFileMeta, with localized units.
const formatFileMeta = (intl: IntlShape, file: File) => {
    const dot = file.name.lastIndexOf('.');
    const ext = dot > 0 ? file.name.slice(dot + 1).toUpperCase() : '';
    const size = file.size >= MEGABYTE ? intl.formatNumber(file.size / MEGABYTE, {style: 'unit', unit: 'megabyte', maximumFractionDigits: 1}) : intl.formatNumber(Math.max(1, Math.round(file.size / 1024)), {style: 'unit', unit: 'kilobyte'});
    return ext ? `${ext} ${size}` : size;
};

interface AgentChatAttachmentsProps {
    attachments: ComposerAttachment[];
    maxFileSize: number;
    onRemove: (clientId: string) => void;
    onRetry: (clientId: string) => void;
}

// The composer's pending attachments, one compass AttachmentCard each, as in
// the prototype: progress and cancel while uploading, remove once uploaded,
// and an inline error (plus retry when it can help) when an upload failed or
// the file is over the server's size limit.
const AgentChatAttachments = ({attachments, maxFileSize, onRemove, onRetry}: AgentChatAttachmentsProps) => {
    const intl = useIntl();
    if (attachments.length === 0) {
        return null;
    }

    return (
        <List aria-label={intl.formatMessage({defaultMessage: 'Attachments'})}>
            {attachments.map((a) => {
                const name = a.file.name;
                const errored = a.status === 'failed' || a.status === 'too_large';
                let fileMeta: React.ReactNode = formatFileMeta(intl, a.file);
                if (a.status === 'failed') {
                    fileMeta = <ErrorMeta role='alert'>{intl.formatMessage({defaultMessage: 'Couldn\'t upload this file.'})}</ErrorMeta>;
                } else if (a.status === 'too_large') {
                    fileMeta = (
                        <ErrorMeta role='alert'>
                            {intl.formatMessage({defaultMessage: 'This file is over the {max} MB limit.'}, {max: intl.formatNumber(maxFileSize / MEGABYTE, {maximumFractionDigits: 1})})}
                        </ErrorMeta>
                    );
                }
                const thumbnail = a.status === 'done' && a.fileInfo?.has_preview_image ? getFileThumbnailUrl(a.fileInfo.id) : '';

                return (
                    <Item
                        key={a.clientId}
                        data-testid='agent-chat-attachment'
                        data-status={a.status}
                    >
                        <Card
                            fileName={name}
                            fileMeta={fileMeta}
                            fileType={thumbnail ? 'image-thumbnail' : attachmentFileType(a.file)}
                            {...(thumbnail ? {thumbnailSrc: thumbnail} : {})}
                            state={a.status === 'uploading' ? 'uploading' : 'uploaded'}
                            progress={a.progress}
                            formatUploadingLabel={(progress) => intl.formatMessage({defaultMessage: 'Uploading… ({progress}%)'}, {progress})}
                            cancelUploadLabel={intl.formatMessage({defaultMessage: 'Cancel upload of {name}'}, {name})}
                            removeLabel={intl.formatMessage({defaultMessage: 'Remove {name}'}, {name})}
                            onRemove={() => onRemove(a.clientId)}
                            $errored={errored}
                        />
                        {a.status === 'failed' && (
                            <Button
                                emphasis='tertiary'
                                size='small'
                                aria-label={intl.formatMessage({defaultMessage: 'Retry uploading {name}'}, {name})}
                                onClick={() => onRetry(a.clientId)}
                            >
                                {intl.formatMessage({defaultMessage: 'Retry'})}
                            </Button>
                        )}
                    </Item>
                );
            })}
        </List>
    );
};

const List = styled.ul`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
    width: 100%;
    min-width: 0;
    margin: 0;
    padding: 0 var(--spacing-xs);
    list-style: none;
`;

const Item = styled.li`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    min-width: 0;
`;

// The compass card is a fixed 320px; never wider than a narrow RHS.
const Card = styled(AttachmentCard)<{$errored: boolean}>`
    && {
        max-width: 100%;
        ${({$errored}) => ($errored ? 'border-color: var(--error-text);' : '')}
    }
`;

const ErrorMeta = styled.span`
    color: var(--error-text);
`;

export default AgentChatAttachments;
