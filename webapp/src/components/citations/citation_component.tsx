// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useRef} from 'react';
import styled from 'styled-components';

import {LinkVariantIcon} from '@mattermost/compass-icons/components';

import {Annotation} from './types';

interface CitationComponentProps {
    annotation: Annotation;
}

export const CitationComponent = (props: CitationComponentProps) => {
    const [showTooltip, setShowTooltip] = useState(false);
    const markerRef = useRef<HTMLSpanElement>(null);

    const handleClick = (e: React.MouseEvent) => {
        e.preventDefault();
        e.stopPropagation();
        window.open(props.annotation.url, '_blank', 'noopener,noreferrer');
    };

    // Extract domain from URL for display
    const domain = (() => {
        try {
            return new URL(props.annotation.url).hostname;
        } catch {
            return props.annotation.url;
        }
    })();

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
                        <FaviconIcon domain={domain}/>
                        <TooltipDomain>{domain}</TooltipDomain>
                    </TooltipContent>
                    <TooltipArrow/>
                </TooltipContainer>
            )}
        </CitationWrapper>
    );
};

// Favicon component
interface FaviconIconProps {
    domain: string;
}

const FaviconIcon = (props: FaviconIconProps) => {
    const [showFallback, setShowFallback] = useState(false);

    const faviconUrl = `https://${props.domain}/favicon.ico`;

    if (showFallback) {
        return <FallbackIcon>{'🌐'}</FallbackIcon>;
    }

    return (
        <FaviconImage
            src={faviconUrl}
            alt={`${props.domain} favicon`}
            onError={() => setShowFallback(true)}
            onLoad={() => setShowFallback(false)}
        />
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

const CitationIcon = styled(LinkVariantIcon)`
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
    padding: 4px 8px;
    display: flex;
    align-items: center;
    gap: 4px;
    white-space: nowrap;
`;

const FaviconImage = styled.img`
    width: 12px;
    height: 12px;
    border-radius: 2px;
    flex-shrink: 0;
`;

const FallbackIcon = styled.span`
    font-size: 12px;
    line-height: 1;
    width: 12px;
    height: 12px;
    display: flex;
    align-items: center;
    justify-content: center;
`;

const TooltipDomain = styled.span`
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
