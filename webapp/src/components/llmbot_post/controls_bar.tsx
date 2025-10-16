// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';

import {SendIcon} from '@mattermost/compass-icons/components';

import IconRegenerate from '../assets/icon_regenerate';
import IconCancel from '../assets/icon_cancel';

import {
    ControlsBar,
    StopGeneratingButton,
    PostSummaryButton,
    GenerationButton,
} from './styles';

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
                    <IconCancel/>
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

