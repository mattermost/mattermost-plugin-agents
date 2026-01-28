// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useRef} from 'react';
import styled from 'styled-components';

import {MessageTextOutlineIcon} from '@mattermost/compass-icons/components';

import {Annotation} from './types';

interface PostCitationComponentProps {
    annotation: Annotation;
}

export const PostCitationComponent = (props: PostCitationComponentProps) => {
    const [showTooltip, setShowTooltip] = useState(false);
    const markerRef = useRef<HTMLSpanElement>(null);

    const handleClick = (e: React.MouseEvent) => {
        e.preventDefault();
        e.stopPropagation();

        // Navigate to the post using Mattermost's permalink URL
        if (props.annotation.post_id) {
            // Get the current site URL from the window location
            const siteUrl = window.location.origin;
            const permalinkUrl = `${siteUrl}/_redirect/pl/${props.annotation.post_id}`;
            window.location.href = permalinkUrl;
        }
    };

    const username = props.annotation.username || 'Unknown User';
    const channelName = props.annotation.channel_name || 'Unknown Channel';

    return (
        <CitationWrapper
            ref={markerRef}
            onMouseEnter={() => setShowTooltip(true)}
            onMouseLeave={() => setShowTooltip(false)}
            onClick={handleClick}
        >
            <CitationIcon size={12}/>
            {showTooltip && (
                <TooltipContainer>
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
                    <TooltipArrow/>
                </TooltipContainer>
            )}
        </CitationWrapper>
    );
};

// Styled components
const CitationWrapper = styled.span`
    display: inline-flex;
    align-items: center;
    justify-content: center;
    margin-left: 4px;
    cursor: pointer;
    position: relative;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    background: rgba(var(--center-channel-color-rgb), 0.08);
    transition: background 0.15s ease;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.12);
    }
`;

const CitationIcon = styled(MessageTextOutlineIcon)`
    color: rgba(var(--center-channel-color-rgb), 0.75);
    transition: color 0.15s ease;

    ${CitationWrapper}:hover & {
        color: rgba(var(--center-channel-color-rgb), 0.85);
    }
`;

const TooltipContainer = styled.div`
    position: absolute;
    bottom: calc(100% + 8px);
    left: 50%;
    transform: translateX(-50%);
    z-index: 1000;
    pointer-events: none;
    opacity: 0;
    visibility: hidden;
    transition: opacity 0.2s ease, visibility 0.2s ease;

    ${CitationWrapper}:hover & {
        opacity: 1;
        visibility: visible;
    }
`;

const TooltipContent = styled.div`
    background: #1b1d22;
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
    color: rgba(255, 255, 255, 0.64);
`;

const TooltipValue = styled.span`
    font-family: 'Open Sans', sans-serif;
    font-weight: 600;
    font-size: 12px;
    line-height: 15px;
    color: white;
`;

const TooltipArrow = styled.div`
    position: absolute;
    bottom: -4px;
    left: 50%;
    transform: translateX(-50%);
    width: 0;
    height: 0;
    border-left: 4px solid transparent;
    border-right: 4px solid transparent;
    border-top: 4px solid #1b1d22;
`;
