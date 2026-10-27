// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';

import {CloseIcon, RefreshIcon, SendIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

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
    const intl = useIntl();
    const regenerateLabel = intl.formatMessage({defaultMessage: 'Regenerate'});

    return (
        <ControlsBar>
            {showStopGeneratingButton && (
                <Button
                    emphasis='tertiary'
                    size='x-small'
                    leadingIcon={<Icon glyph={<CloseIcon/>}/>}
                    data-testid='stop-generating-button'
                    onClick={onStopGenerating}
                >
                    <FormattedMessage defaultMessage='Stop Generating'/>
                </Button>
            )}
            {showPostbackButton && (
                <Button
                    emphasis='primary'
                    size='x-small'
                    leadingIcon={<Icon glyph={<SendIcon/>}/>}
                    data-testid='llm-bot-post-summary'
                    onClick={onPostSummary}
                >
                    <FormattedMessage defaultMessage='Post summary'/>
                </Button>
            )}
            {showRegenerate && (
                <IconButton
                    size='x-small'
                    icon={<Icon glyph={<RefreshIcon/>}/>}
                    aria-label={regenerateLabel}
                    title={regenerateLabel}
                    data-testid='regenerate-button'
                    onClick={onRegenerate}
                />
            )}
        </ControlsBar>
    );
};

const ControlsBar = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	height: 28px;
	margin-top: 8px;
	gap: 4px;
`;
