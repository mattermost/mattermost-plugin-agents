// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {ArrowExpandIcon, ArrowLeftIcon, DownloadOutlineIcon} from '@mattermost/compass-icons/components';

import {getChannelLibraryClient} from '../library_client';

import FormatBadge from '../format_badge';

import type {ArtifactViewerProps} from './registry';

type Props = Pick<ArtifactViewerProps, 'entry' | 'onClose' | 'onPopOut'> & {
    children: React.ReactNode;
};

// The chrome around an inline viewer: a header with the title and actions,
// and a body that fills the rest of the host container.
const InlineViewerShell = ({entry, onClose, onPopOut, children}: Props) => {
    const intl = useIntl();
    const backLabel = intl.formatMessage({defaultMessage: 'Back to artifacts'});
    const popOutLabel = intl.formatMessage({defaultMessage: 'Open fullscreen'});
    const downloadLabel = intl.formatMessage({defaultMessage: 'Download'});

    return (
        <Root data-testid='artifact-inline-viewer'>
            <Header>
                <IconButton
                    size='x-small'
                    icon={<Icon glyph={<ArrowLeftIcon/>}/>}
                    aria-label={backLabel}
                    title={backLabel}
                    onClick={onClose}
                    data-testid='artifact-inline-back'
                />
                <Title title={entry.title}>{entry.title}</Title>
                <FormatBadge entry={entry}/>
                <IconButton
                    size='x-small'
                    icon={<Icon glyph={<DownloadOutlineIcon/>}/>}
                    aria-label={downloadLabel}
                    title={downloadLabel}
                    onClick={() => window.open(getChannelLibraryClient().downloadURL(entry.id, entry.version), '_self')}
                    data-testid='artifact-inline-download'
                />
                {onPopOut && (
                    <IconButton
                        size='x-small'
                        icon={<Icon glyph={<ArrowExpandIcon/>}/>}
                        aria-label={popOutLabel}
                        title={popOutLabel}
                        onClick={onPopOut}
                        data-testid='artifact-inline-pop-out'
                    />
                )}
            </Header>
            <Body>{children}</Body>
        </Root>
    );
};

export default InlineViewerShell;

const Root = styled.div`
    display: flex;
    flex-direction: column;
    min-height: 0;
    height: 100%;
`;

const Header = styled.div`
    display: flex;
    align-items: center;
    flex: none;
    gap: var(--spacing-xs, 8px);
    padding: var(--spacing-xs, 8px) 0;
`;

const Title = styled.h3`
    flex: 1;
    min-width: 0;
    margin: 0;
    overflow: hidden;
    color: var(--center-channel-color);
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const Body = styled.div`
    display: flex;
    flex: 1;
    flex-direction: column;
    min-height: 0;
    overflow: auto;
`;
