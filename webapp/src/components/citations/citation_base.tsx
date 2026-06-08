// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import styled from 'styled-components';

interface CitationBaseProps {
    icon: React.ReactNode;
    tooltipContent: React.ReactNode;
    onClick: (e: React.MouseEvent | React.KeyboardEvent) => void;
    ariaLabel: string;
    testId?: string;
    tooltipTestId?: string;
    citationIndex?: number;
}

export const CitationBase = (props: CitationBaseProps) => {
    const markerRef = useRef<HTMLSpanElement>(null);

    const handleKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === 'Enter' || e.key === ' ') {
            props.onClick(e);
        }
    };

    return (
        <CitationWrapper
            ref={markerRef}
            onClick={props.onClick}
            onKeyDown={handleKeyDown}
            role='button'
            tabIndex={0}
            aria-label={props.ariaLabel}
            data-testid={props.testId}
            data-citation-index={props.citationIndex}
        >
            {props.icon}
            <TooltipContainer data-testid={props.tooltipTestId}>
                {props.tooltipContent}
                <TooltipArrow/>
            </TooltipContainer>
        </CitationWrapper>
    );
};

export const CitationWrapper = styled.span`
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
    border: none;
    padding: 0;

    &:hover,
    &:focus {
        background: rgba(var(--center-channel-color-rgb), 0.12);
        outline: none;
    }

    &:focus-visible {
        box-shadow: 0 0 0 2px var(--button-bg);
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

    ${CitationWrapper}:hover &,
    ${CitationWrapper}:focus & {
        opacity: 1;
        visibility: visible;
    }
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
    border-top: 4px solid var(--center-channel-color);
`;
