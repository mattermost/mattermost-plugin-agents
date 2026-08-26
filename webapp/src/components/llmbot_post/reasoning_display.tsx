// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import styled from 'styled-components';

import {ChevronRightIcon} from '@mattermost/compass-icons/components';

import {CollapseChevron, CollapseHeaderRow} from './collapse_header';

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
                <CollapseChevron $expanded={false}>
                    <ChevronRightIcon/>
                </CollapseChevron>
                {isReasoningLoading && <SpinnerWrapper><LoadingSpinner/></SpinnerWrapper>}
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
                <CollapseChevron $expanded={true}>
                    <ChevronRightIcon/>
                </CollapseChevron>
                {isReasoningLoading && <SpinnerWrapper><LoadingSpinner/></SpinnerWrapper>}
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

// Styled components
const ExpandedReasoningContainer = styled.div`
	background: rgba(var(--center-channel-color-rgb), 0.02);
	border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
	border-radius: 8px;
	margin-bottom: 16px;
	margin-top: 4px;
	overflow: hidden;
`;

const ExpandedReasoningHeader = styled(CollapseHeaderRow)`
	margin-bottom: 12px;
`;

const SpinnerWrapper = styled.div`
	display: flex;
	align-items: center;
	justify-content: center;
	width: 16px;
	height: 16px;
`;

export const LoadingSpinner = styled.div`
	display: inline-block;
	width: 14px;
	height: 14px;
	border: 2px solid rgba(var(--center-channel-color-rgb), 0.16);
	border-radius: 50%;
	border-top-color: rgba(var(--center-channel-color-rgb), 0.75);
	animation: spin 1s linear infinite;

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
`;

export const MinimalReasoningContainer = styled(CollapseHeaderRow)`
	margin: 4px 0;
`;

const ReasoningContent = styled.div<{collapsed: boolean}>`
	max-height: ${(props) => (props.collapsed ? '0' : '600px')};
	overflow-y: auto;
	transition: max-height 0.3s ease-in-out;
	opacity: ${(props) => (props.collapsed ? '0' : '1')};
	transition: opacity 0.2s ease-in-out, max-height 0.3s ease-in-out;
`;

const ReasoningText = styled.div`
	padding: 16px;
	font-size: 14px;
	line-height: 22px;
	color: rgba(var(--center-channel-color-rgb), 0.8);
	white-space: pre-wrap;
	word-break: break-word;
`;
