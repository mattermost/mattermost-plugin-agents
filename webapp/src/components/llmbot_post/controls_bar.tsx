// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import styled, {css} from 'styled-components';

import {CheckIcon, CloseIcon, ContentCopyIcon, RefreshIcon, SendIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {Timestamp} from '@/mm_webapp';

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

// Set on the DOM node (not JSX) so the temporary fallback textarea never shifts layout.
const OFFSCREEN_TEXTAREA_STYLE: Partial<CSSStyleDeclaration> = {
    position: 'fixed',
    top: '0',
    left: '0',
    width: '1px',
    height: '1px',
    opacity: '0',
    pointerEvents: 'none',
};

async function copyToClipboard(text: string): Promise<boolean> {
    try {
        if (navigator.clipboard?.writeText) {
            await navigator.clipboard.writeText(text);
            return true;
        }
    } catch {
        // Fall through to the textarea fallback (permission denied, insecure context).
    }
    return copyWithTextarea(text);
}

function copyWithTextarea(text: string): boolean {
    const previouslyFocused = document.activeElement as HTMLElement | null;
    const selection = document.getSelection();
    const savedRanges = Array.from({length: selection?.rangeCount ?? 0}, (_, i) => selection!.getRangeAt(i));

    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', '');
    textarea.setAttribute('aria-hidden', 'true');
    textarea.setAttribute('tabindex', '-1');
    textarea.setAttribute('data-copy-fallback', 'true');
    Object.assign(textarea.style, OFFSCREEN_TEXTAREA_STYLE);
    document.body.appendChild(textarea);

    let ok = false;
    try {
        textarea.select();
        ok = typeof document.execCommand === 'function' && document.execCommand('copy');
    } catch {
        ok = false;
    }

    document.body.removeChild(textarea);
    if (selection) {
        selection.removeAllRanges();
        savedRanges.forEach((range) => selection.addRange(range));
    }
    previouslyFocused?.focus?.();
    return ok;
}

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

const visuallyHidden = css`
	position: absolute;
	width: 1px;
	height: 1px;
	margin: -1px;
	padding: 0;
	overflow: hidden;
	clip: rect(0, 0, 0, 0);
	white-space: nowrap;
	border: 0;
`;

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

// WithTooltip needs a single element that takes a ref and mouse/focus handlers.
const TooltipTrigger = styled.span`
	display: inline-flex;
`;
