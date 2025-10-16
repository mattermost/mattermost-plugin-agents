// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';

import {ChevronRightIcon} from '@mattermost/compass-icons/components';

import {
    MinimalReasoningContainer,
    MinimalExpandIcon,
    LoadingSpinner,
    ExpandedReasoningHeader,
    ExpandedChevron,
    ExpandedReasoningContainer,
    ReasoningContent,
    ReasoningText,
} from './styles';

interface ReasoningDisplayProps {
    reasoningSummary: string;
    isReasoningCollapsed: boolean;
    isReasoningLoading: boolean;
    onToggleCollapse: (collapsed: boolean) => void;
}

export const ReasoningDisplay: React.FC<ReasoningDisplayProps> = ({
    reasoningSummary,
    isReasoningCollapsed,
    isReasoningLoading,
    onToggleCollapse,
}) => {
    // Ref for the expanded reasoning header to scroll to
    const expandedReasoningHeaderRef = useRef<HTMLDivElement>(null);

    const handleExpand = () => {
        onToggleCollapse(false);

        // Wait for expansion animation to complete before scrolling (300ms transition + buffer)
        setTimeout(() => {
            if (expandedReasoningHeaderRef.current) {
                expandedReasoningHeaderRef.current.scrollIntoView({
                    behavior: 'smooth',
                    block: 'start',
                    inline: 'nearest',
                });
            }
        }, 350);
    };

    if (isReasoningCollapsed) {
        return (
            <MinimalReasoningContainer onClick={handleExpand}>
                <MinimalExpandIcon isExpanded={false}>
                    <ChevronRightIcon/>
                </MinimalExpandIcon>
                {isReasoningLoading && <LoadingSpinner/>}
                <span>{'Thinking'}</span>
            </MinimalReasoningContainer>
        );
    }

    return (
        <>
            <ExpandedReasoningHeader
                ref={expandedReasoningHeaderRef}
                onClick={() => onToggleCollapse(true)}
            >
                <ExpandedChevron>
                    <ChevronRightIcon/>
                </ExpandedChevron>
                {isReasoningLoading && <LoadingSpinner/>}
                <span>{'Thinking'}</span>
            </ExpandedReasoningHeader>
            {reasoningSummary && (
                <ExpandedReasoningContainer>
                    <ReasoningContent collapsed={false}>
                        <ReasoningText>
                            {reasoningSummary}
                        </ReasoningText>
                    </ReasoningContent>
                </ExpandedReasoningContainer>
            )}
        </>
    );
};

