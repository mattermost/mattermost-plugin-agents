// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import styled from 'styled-components';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

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
        <WithTooltip
            title={<span data-testid={props.tooltipTestId}>{props.tooltipContent}</span>}
        >
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
            </CitationWrapper>
        </WithTooltip>
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
