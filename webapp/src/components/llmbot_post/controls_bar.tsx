// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled from 'styled-components';

import {CheckIcon, CloseIcon, ContentCopyIcon, RefreshIcon, SendIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {Timestamp} from '@/mm_webapp';
import {copyToClipboard} from '@/utils/clipboard';
import {visuallyHidden} from '@/components/visually_hidden';
import {TooltipTrigger} from '@/components/dropdown';

import {BUBBLE_CONTROLS_ATTRIBUTE, BUBBLE_CONTROLS_BELOW, CONTROLS_BAR_HEIGHT} from '../agent_chat/agent_chat_host_styles';

interface ControlsBarComponentProps {
    showStopGeneratingButton: boolean;
    showPostbackButton: boolean;
    showRegenerate: boolean;

    // Text the Copy button puts on the clipboard; null leaves the button out.
    copyText?: string | null;

    // Agent chat views place the bar under the bubble (agent_chat_host_styles.ts).
    belowBubble?: boolean;

    // The post's creation time, shown first in the bar below the bubble (the host's message
    // header, with the agent's name and tag, is hidden there; agent_chat_host_styles.ts).
    createAt?: number;
    onStopGenerating: () => void;
    onPostSummary: () => void;
    onRegenerate: () => void;
}

export const ControlsBarComponent: React.FC<ControlsBarComponentProps> = ({
    showStopGeneratingButton,
    showPostbackButton,
    showRegenerate,
    copyText = null,
    belowBubble = false,
    createAt,
    onStopGenerating,
    onPostSummary,
    onRegenerate,
}) => {
    const intl = useIntl();
    const regenerateLabel = intl.formatMessage({defaultMessage: 'Regenerate'});

    return (
        <ControlsBar {...(belowBubble ? {[BUBBLE_CONTROLS_ATTRIBUTE]: BUBBLE_CONTROLS_BELOW} : {})}>
            {belowBubble && typeof createAt === 'number' && (
                <Time data-testid='controls-bar-time'>
                    <Timestamp
                        value={createAt}
                        useDate={false}
                    />
                </Time>
            )}
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
            {copyText !== null && <CopyButton text={copyText}/>}
            {showRegenerate && (
                <WithTooltip title={regenerateLabel}>
                    <TooltipTrigger>
                        <IconButton
                            size='x-small'
                            icon={<Icon glyph={<RefreshIcon/>}/>}
                            aria-label={regenerateLabel}
                            data-testid='regenerate-button'
                            onClick={onRegenerate}
                        />
                    </TooltipTrigger>
                </WithTooltip>
            )}
        </ControlsBar>
    );
};

// How long the check glyph, 'Copied' tooltip and 'Couldn't copy' stay before reverting.
export const COPY_FEEDBACK_MS = 2000;

type CopyState = 'idle' | 'copied' | 'failed';

const CopyButton: React.FC<{text: string}> = ({text}) => {
    const intl = useIntl();
    const [state, setState] = useState<CopyState>('idle');
    const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
    const mountedRef = useRef(true);

    useEffect(() => {
        mountedRef.current = true;
        return () => {
            mountedRef.current = false;
            if (timerRef.current) {
                clearTimeout(timerRef.current);
            }
        };
    }, []);

    const onCopy = useCallback(async () => {
        const ok = await copyToClipboard(text);
        if (!mountedRef.current) {
            return;
        }

        // A repeated click restarts the feedback window.
        if (timerRef.current) {
            clearTimeout(timerRef.current);
        }
        setState(ok ? 'copied' : 'failed');
        timerRef.current = setTimeout(() => setState('idle'), COPY_FEEDBACK_MS);
    }, [text]);

    const copyLabel = intl.formatMessage({defaultMessage: 'Copy'});
    let tooltip = copyLabel;
    let announcement = '';
    if (state === 'copied') {
        tooltip = intl.formatMessage({defaultMessage: 'Copied'});
        announcement = intl.formatMessage({defaultMessage: 'Copied to clipboard'});
    } else if (state === 'failed') {
        tooltip = intl.formatMessage({defaultMessage: 'Couldn\'t copy'});
        announcement = tooltip;
    }

    return (
        <>
            <WithTooltip title={tooltip}>
                <TooltipTrigger>
                    <IconButton
                        size='x-small'
                        icon={<Icon glyph={state === 'copied' ? <CheckIcon/> : <ContentCopyIcon/>}/>}
                        aria-label={copyLabel}
                        data-testid='copy-button'
                        onClick={onCopy}
                    />
                </TooltipTrigger>
            </WithTooltip>
            <LiveStatus
                role='status'
                aria-live='polite'
            >
                {announcement}
            </LiveStatus>
        </>
    );
};

const LiveStatus = styled.span`
	${visuallyHidden}
`;

const ControlsBar = styled.div`
	display: flex;
	flex-direction: row;
	align-items: center;
	height: ${CONTROLS_BAR_HEIGHT};
	margin-top: 8px;
	gap: 4px;
`;

const Time = styled.span`
	margin-inline-end: var(--spacing-xs);
	color: rgba(var(--center-channel-color-rgb), 0.64);
	font-size: var(--font-size-75);
	line-height: var(--line-height-75);
	white-space: nowrap;
`;
