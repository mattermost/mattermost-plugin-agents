// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {Tag} from '@mattermost/compass-ui/components/tag';
import {
    ArrowExpandIcon,
    DownloadOutlineIcon,
    FileCodeOutlineIcon,
    RefreshIcon,
} from '@mattermost/compass-icons/components';

import ArtifactFrame, {ArtifactFrameHandle} from './artifact_frame';
import ArtifactNotice from './artifact_notice';
import {downloadArtifact} from './download';
import HTMLArtifactFullscreen from './html_artifact_fullscreen';

interface Props {
    fileId: string;
    fileName: string;
}

const HTMLArtifactCard = ({fileId, fileName}: Props) => {
    const intl = useIntl();
    const frameRef = useRef<ArtifactFrameHandle>(null);
    const [fullscreen, setFullscreen] = useState(false);

    const fullscreenLabel = intl.formatMessage({defaultMessage: 'Open fullscreen'});
    const downloadLabel = intl.formatMessage({defaultMessage: 'Download'});
    const reloadLabel = intl.formatMessage({defaultMessage: 'Reload'});

    return (
        <Card data-testid='html-artifact-card'>
            <Header>
                <Icon
                    size='16'
                    glyph={<FileCodeOutlineIcon/>}
                />
                <FileName
                    title={fileName}
                    data-testid='html-artifact-file-name'
                >{fileName}</FileName>
                <TagSlot>
                    <Tag
                        size='x-small'
                        type='info'
                        label={<FormattedMessage defaultMessage='Interactive'/>}
                    />
                </TagSlot>
                <Spacer/>
                <ArtifactNotice wrapBelowPx={NOTICE_WRAP_PX}/>
                <IconButton
                    size='x-small'
                    icon={<Icon glyph={<ArrowExpandIcon/>}/>}
                    aria-label={fullscreenLabel}
                    title={fullscreenLabel}
                    onClick={() => setFullscreen(true)}
                    data-testid='html-artifact-open-fullscreen'
                />
                <IconButton
                    size='x-small'
                    icon={<Icon glyph={<DownloadOutlineIcon/>}/>}
                    aria-label={downloadLabel}
                    title={downloadLabel}
                    onClick={() => downloadArtifact(fileId)}
                />
                <IconButton
                    size='x-small'
                    icon={<Icon glyph={<RefreshIcon/>}/>}
                    aria-label={reloadLabel}
                    title={reloadLabel}
                    onClick={() => frameRef.current?.reload()}
                    data-testid='html-artifact-reload'
                />
            </Header>
            <ArtifactFrame
                ref={frameRef}
                fileId={fileId}
                fileName={fileName}
                displayMode='inline'
                obscured={fullscreen}
            />
            {fullscreen && (
                <HTMLArtifactFullscreen
                    fileId={fileId}
                    fileName={fileName}
                    onClose={() => setFullscreen(false)}
                />
            )}
        </Card>
    );
};

export default HTMLArtifactCard;

// Narrower than this (e.g. the Agents RHS), the file name gets the room.
const NARROW_CARD_PX = 440;

// Narrower than this, the safety notice moves to its own row under the file
// name and buttons, so the file name keeps its room.
const NOTICE_WRAP_PX = 400;

const Card = styled.div`
    container-type: inline-size;
    margin-top: 12px;
    overflow: hidden;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 8px;
    background: var(--center-channel-bg);
`;

const Header = styled.div`
    display: flex;
    align-items: center;
    gap: 6px;
    flex-wrap: wrap;
    row-gap: 0;
    min-height: 40px;
    padding: 4px 8px 4px 12px;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    background: rgba(var(--center-channel-color-rgb), 0.04);
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const TagSlot = styled.span`
    display: inline-flex;
    flex: none;

    @container (max-width: ${NARROW_CARD_PX}px) {
        display: none;
    }
`;

const FileName = styled.span`
    flex: 0 1 auto;

    @container (max-width: ${NOTICE_WRAP_PX}px) {
        /* Zero basis: a long name ellipsizes instead of wrapping the buttons. */
        flex: 1 1 0;
    }

    min-width: 0;
    overflow: hidden;
    color: var(--center-channel-color);
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const Spacer = styled.div`
    flex: 1;
    min-width: 4px;

    @container (max-width: ${NOTICE_WRAP_PX}px) {
        display: none;
    }
`;
