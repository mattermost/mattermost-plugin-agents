// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import styled from 'styled-components';

export const PostBody = styled.div`
`;

export const ControlsBar = styled.div`
	display: flex;
	flex-direction: row;
	justify-content: left;
	height: 28px;
	margin-top: 8px;
	gap: 4px;
`;

export const GenerationButton = styled.button`
	display: flex;
	border: none;
	height: 24px;
	padding: 4px 10px;
	align-items: center;
	justify-content: center;
	gap: 6px;
	border-radius: 4px;
	background: rgba(var(--center-channel-color-rgb), 0.08);
    color: rgba(var(--center-channel-color-rgb), 0.64);

	font-size: 12px;
	line-height: 16px;
	font-weight: 600;

	:hover {
		background: rgba(var(--center-channel-color-rgb), 0.12);
        color: rgba(var(--center-channel-color-rgb), 0.72);
	}

	:active {
		background: rgba(var(--button-bg-rgb), 0.08);
	}
`;

export const PostSummaryButton = styled(GenerationButton)`
	background: var(--button-bg);
    color: var(--button-color);

	:hover {
		background: rgba(var(--button-bg-rgb), 0.88);
		color: var(--button-color);
	}

	:active {
		background: rgba(var(--button-bg-rgb), 0.92);
	}
`;

export const StopGeneratingButton = styled(GenerationButton)`
`;

export const PostSummaryHelpMessage = styled.div`
	font-size: 14px;
	font-style: italic;
	font-weight: 400;
	line-height: 20px;
	border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.12);

	padding-top: 8px;
	padding-bottom: 8px;
	margin-top: 16px;
`;

export const ExpandedReasoningContainer = styled.div`
	background: rgba(var(--center-channel-color-rgb), 0.02);
	border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
	border-radius: 8px;
	margin-bottom: 16px;
	margin-top: 4px;
	overflow: hidden;
`;

export const ExpandedReasoningHeader = styled.div`
	display: flex;
	align-items: center;
	gap: 8px;
	margin-bottom: 12px;
	font-size: 14px;
	color: rgba(var(--center-channel-color-rgb), 0.64);
	cursor: pointer;
	user-select: none;

	&:hover {
		color: rgba(var(--center-channel-color-rgb), 0.8);
	}
`;

export const ExpandedChevron = styled.div`
	display: flex;
	align-items: center;
	justify-content: center;
	width: 16px;
	height: 16px;
	transition: transform 0.2s ease;
	transform: rotate(90deg);

	svg {
		width: 14px;
		height: 14px;
	}
`;

export const LoadingSpinner = styled.div`
	display: inline-block;
	width: 14px;
	height: 14px;
	border: 2px solid rgba(var(--center-channel-color-rgb), 0.16);
	border-radius: 50%;
	border-top-color: rgba(var(--center-channel-color-rgb), 0.64);
	animation: spin 1s linear infinite;

	@keyframes spin {
		to {
			transform: rotate(360deg);
		}
	}
`;

export const MinimalReasoningContainer = styled.div`
	display: flex;
	align-items: center;
	gap: 8px;
	margin-bottom: 12px;
	font-size: 14px;
	color: rgba(var(--center-channel-color-rgb), 0.64);
	cursor: pointer;
	user-select: none;

	&:hover {
		color: rgba(var(--center-channel-color-rgb), 0.8);
	}
`;

export const MinimalExpandIcon = styled.div<{isExpanded: boolean}>`
	display: flex;
	align-items: center;
	justify-content: center;
	width: 16px;
	height: 16px;
	transition: transform 0.2s ease;
	transform: ${(props) => (props.isExpanded ? 'rotate(180deg)' : 'rotate(0)')};

	svg {
		width: 14px;
		height: 14px;
	}
`;

export const ReasoningContent = styled.div<{collapsed: boolean}>`
	max-height: ${(props) => (props.collapsed ? '0' : '600px')};
	overflow-y: auto;
	transition: max-height 0.3s ease-in-out;
	opacity: ${(props) => (props.collapsed ? '0' : '1')};
	transition: opacity 0.2s ease-in-out, max-height 0.3s ease-in-out;
`;

export const ReasoningText = styled.div`
	padding: 16px;
	font-size: 14px;
	line-height: 22px;
	color: rgba(var(--center-channel-color-rgb), 0.8);
	white-space: pre-wrap;
	word-break: break-word;
`;

