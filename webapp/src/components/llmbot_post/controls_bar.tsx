// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import {SendIcon} from '@mattermost/compass-icons/components';

import IconRegenerate from '../assets/icon_regenerate';
import IconCancel from '../assets/icon_cancel';

interface ControlsBarComponentProps {
    showStopGeneratingButton: boolean;
    showPostbackButton: boolean;
    showRegenerate: boolean;
    onStopGenerating: () => void;
    onPostSummary: () => void;
    onRegenerate: () => void;
}

export const ControlsBarComponent: React.FC<ControlsBarComponentProps> = ({
    showStopGeneratingButton,
    showPostbackButton,
    showRegenerate,
    onStopGenerating,
    onPostSummary,
    onRegenerate,
}) => {
    return (
        <ControlsBar>
            {showStopGeneratingButton && (
                <StopGeneratingButton
                    data-testid='stop-generating-button'
                    onClick={onStopGenerating}
                >
                    <IconCancel size={10}/>
                    <FormattedMessage defaultMessage='Stop Generating'/>
                </StopGeneratingButton>
            )}
            {showPostbackButton && (
                <PostSummaryButton
                    data-testid='llm-bot-post-summary'
                    onClick={onPostSummary}
                >
                    <SendIcon/>
                    <FormattedMessage defaultMessage='Post summary'/>
                </PostSummaryButton>
            )}
            {showRegenerate && (
                <GenerationButton
                    data-testid='regenerate-button'
                    onClick={onRegenerate}
                >
                    <IconRegenerate/>
                    <FormattedMessage defaultMessage='Regenerate'/>
                </GenerationButton>
            )}
        </ControlsBar>
    );
};

// Styled components
const ControlsBar = styled.div`
	display: flex;
	flex-direction: row;
	justify-content: left;
	height: 28px;
	margin-top: 8px;
	gap: 4px;
`;

const GenerationButton = styled.button`
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

const PostSummaryButton = styled(GenerationButton)`
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

const StopGeneratingButton = styled(GenerationButton)`
`;
