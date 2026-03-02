// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {MessageTextOutlineIcon} from '@mattermost/compass-icons/components';

import {CitationBase, CitationWrapper} from './citation_base';
import {Annotation} from './types';

interface PostCitationComponentProps {
    annotation: Annotation;
}

export const PostCitationComponent = (props: PostCitationComponentProps) => {
    const intl = useIntl();

    const handleClick = (e: React.MouseEvent | React.KeyboardEvent) => {
        e.preventDefault();
        e.stopPropagation();

        if (props.annotation.post_id) {
            const permalinkPath = `/_redirect/pl/${props.annotation.post_id}`;
            if (window.WebappUtils?.browserHistory) {
                window.WebappUtils.browserHistory.push(permalinkPath);
            } else {
                window.history.pushState(null, '', permalinkPath);
                window.dispatchEvent(new PopStateEvent('popstate'));
            }
        }
    };

    const username = props.annotation.username || intl.formatMessage({defaultMessage: 'Unknown User'});
    const channelName = props.annotation.channel_name || intl.formatMessage({defaultMessage: 'Unknown Channel'});

    return (
        <CitationBase
            icon={<CitationIcon size={12}/>}
            tooltipContent={
                <TooltipContent>
                    <TooltipRow>
                        <TooltipLabel>{'@'}</TooltipLabel>
                        <TooltipValue>{username}</TooltipValue>
                    </TooltipRow>
                    <TooltipRow>
                        <TooltipLabel>{'#'}</TooltipLabel>
                        <TooltipValue>{channelName}</TooltipValue>
                    </TooltipRow>
                </TooltipContent>
            }
            onClick={handleClick}
            ariaLabel={intl.formatMessage(
                {defaultMessage: 'Citation from {username} in {channelName}'},
                {username, channelName},
            )}
        />
    );
};

const CitationIcon = styled(MessageTextOutlineIcon)`
    color: rgba(var(--center-channel-color-rgb), 0.75);
    transition: color 0.15s ease;

    ${CitationWrapper}:hover &,
    ${CitationWrapper}:focus & {
        color: rgba(var(--center-channel-color-rgb), 0.85);
    }
`;

const TooltipContent = styled.div`
    background: var(--center-channel-color);
    border-radius: 4px;
    box-shadow: 0px 6px 14px 0px rgba(0, 0, 0, 0.12);
    padding: 6px 10px;
    display: flex;
    flex-direction: column;
    gap: 2px;
    white-space: nowrap;
`;

const TooltipRow = styled.div`
    display: flex;
    align-items: center;
    gap: 2px;
`;

const TooltipLabel = styled.span`
    font-family: 'Open Sans', sans-serif;
    font-weight: 400;
    font-size: 11px;
    line-height: 14px;
    color: rgba(var(--center-channel-bg-rgb), 0.64);
`;

const TooltipValue = styled.span`
    font-family: 'Open Sans', sans-serif;
    font-weight: 600;
    font-size: 12px;
    line-height: 15px;
    color: var(--center-channel-bg);
`;
