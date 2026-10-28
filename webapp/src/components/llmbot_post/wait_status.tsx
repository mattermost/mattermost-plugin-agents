// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import styled, {css, keyframes} from 'styled-components';

import {CheckIcon, ClockOutlineIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';

import {JSONValue} from '@/components/tool_types';

import {noMotionWhenReduced} from './motion';

// WAIT_TOOL_NAME mirrors mmtools.WaitForAsyncWorkToolName on the server: the
// built-in tool that pauses the bot and resumes it after a delay.
export const WAIT_TOOL_NAME = 'wait_for_async_work';

// minWaitMinutes / maxWaitMinutes mirror the server-side clamp in
// mmtools/wait_for_async_work.go so the displayed duration matches the wait
// that actually ran, even when the model asked for an out-of-range value.
const minWaitMinutes = 1;
const maxWaitMinutes = 30;

/** Clamp a requested wait length to the range the server enforces. */
export function clampWaitMinutes(minutes: number): number {
    return Math.min(Math.max(Math.round(minutes), minWaitMinutes), maxWaitMinutes);
}

/**
 * The clamped wait length parsed from a wait_for_async_work call's arguments,
 * or null when they are absent (redacted for onlookers) or malformed.
 */
export function parseWaitMinutes(args: JSONValue | undefined): number | null {
    if (args === null || typeof args !== 'object' || Array.isArray(args)) {
        return null;
    }
    const minutes = (args as {minutes?: unknown}).minutes;
    if (typeof minutes !== 'number' || !Number.isFinite(minutes)) {
        return null;
    }
    return clampWaitMinutes(minutes);
}

/** Format milliseconds remaining as m:ss, clamped at 0:00. */
export function formatCountdown(msRemaining: number): string {
    const totalSeconds = Math.max(0, Math.ceil(msRemaining / 1000));
    const minutes = Math.floor(totalSeconds / 60);
    const seconds = totalSeconds % 60;
    return `${minutes}:${seconds.toString().padStart(2, '0')}`;
}

// useTickingNow re-renders once a second while active so a countdown stays
// live; it stops ticking (and stops scheduling timers) once inactive.
function useTickingNow(active: boolean): number {
    const [now, setNow] = useState(() => Date.now());
    useEffect(() => {
        if (!active) {
            return undefined; // eslint-disable-line no-undefined
        }
        const id = window.setInterval(() => setNow(Date.now()), 1000);
        return () => window.clearInterval(id);
    }, [active]);
    return now;
}

// WaitToolState is the per-post pending-wait context the wait tool's row reads
// to switch its step indicator between "waiting" and the resolved check.
export interface WaitToolState {
    active: boolean;
}

// eslint-disable-next-line import/no-named-as-default-member
export const WaitToolContext = React.createContext<WaitToolState | null>(null);

export type WaitResolution = 'pending' | 'cancelled';

interface WaitStatusFooterProps {
    resumeAt: number; // Unix ms the bot is scheduled to resume.

    // The clamped wait length, for the resolved "Waited Nm" text.
    durationMinutes: number;

    // The current user requested the wait: only they get the controls.
    isOwner: boolean;

    // Display name of the requester (onlooker view) and the bot (owner view).
    requesterUsername?: string;
    botDisplayName?: string;

    // 'cancelled' once the owner cancels; otherwise 'pending' until the
    // countdown elapses, when the footer shows the resumed state.
    resolution: WaitResolution;

    // A control request is in flight; the buttons are disabled.
    busy: boolean;

    onCheckNow: () => void;
    onCancel: () => void;
}

/**
 * The footer shown on a bot post while it is paused on wait_for_async_work. It
 * renders a live countdown to the resume time and, for the requester, controls
 * to resume or cancel early. Once the wait resolves it shows a calm, resolved
 * state in place of the amber pending one.
 */
export const WaitStatusFooter: React.FC<WaitStatusFooterProps> = (props) => {
    const pending = props.resolution === 'pending';
    const now = useTickingNow(pending);
    const remaining = props.resumeAt - now;

    // A pending wait whose countdown has elapsed reads as resumed: the wake
    // fires at resume_at, and the refetch that removes the footer may lag.
    const resolved = props.resolution === 'cancelled' || (pending && remaining <= 0);

    if (resolved) {
        const message = props.resolution === 'cancelled' ? (
            <FormattedMessage
                id='ai.wait.cancelled'
                defaultMessage='Wait cancelled'
            />
        ) : (
            <FormattedMessage
                id='ai.wait.resumed'
                defaultMessage='Waited {minutes}m, then resumed'
                values={{minutes: props.durationMinutes}}
            />
        );
        return (
            <StatusBar
                $resolved={true}
                data-testid='llm-bot-wait-status'
                data-wait-state={props.resolution === 'cancelled' ? 'cancelled' : 'resumed'}
            >
                <IconBox $resolved={true}>
                    <CheckIcon size={16}/>
                </IconBox>
                <Content>
                    <Line>{message}</Line>
                </Content>
            </StatusBar>
        );
    }

    const countdown = formatCountdown(remaining);
    const bold = (chunks: React.ReactNode) => <strong>{chunks}</strong>;

    let line: React.ReactNode;
    if (props.isOwner) {
        line = (
            <FormattedMessage
                id='ai.wait.owner_line'
                defaultMessage='<b>Waiting on async work.</b> {bot} will check again in {countdown}'
                values={{
                    b: bold,
                    bot: props.botDisplayName || (
                        <FormattedMessage
                            id='ai.wait.assistant_fallback'
                            defaultMessage='the assistant'
                        />
                    ),
                    countdown: <Countdown>{countdown}</Countdown>,
                }}
            />
        );
    } else if (props.requesterUsername) {
        line = (
            <FormattedMessage
                id='ai.wait.onlooker_line'
                defaultMessage='<b>Waiting on async work</b> for {user} · {countdown}'
                values={{
                    b: bold,
                    user: <Mention>{'@' + props.requesterUsername}</Mention>,
                    countdown: <Countdown>{countdown}</Countdown>,
                }}
            />
        );
    } else {
        line = (
            <FormattedMessage
                id='ai.wait.fallback_line'
                defaultMessage='<b>Waiting on async work</b> · {countdown}'
                values={{b: bold, countdown: <Countdown>{countdown}</Countdown>}}
            />
        );
    }

    return (
        <StatusBar
            $resolved={false}
            data-testid='llm-bot-wait-status'
            data-wait-state='pending'
        >
            <IconBox $resolved={false}>
                <ClockOutlineIcon size={16}/>
            </IconBox>
            <Content>
                <Line>{line}</Line>
                {props.isOwner && (
                    <Actions>
                        <Button
                            emphasis='tertiary'
                            size='small'
                            disabled={props.busy}
                            onClick={props.onCheckNow}
                            data-testid='llm-bot-wait-check-now'
                        >
                            <FormattedMessage
                                id='ai.wait.check_now'
                                defaultMessage='Check now'
                            />
                        </Button>
                        <Button
                            emphasis='tertiary'
                            size='small'
                            disabled={props.busy}
                            onClick={props.onCancel}
                            data-testid='llm-bot-wait-cancel'
                        >
                            <FormattedMessage
                                id='ai.wait.cancel'
                                defaultMessage='Cancel wait'
                            />
                        </Button>
                    </Actions>
                )}
            </Content>
        </StatusBar>
    );
};

const pulse = keyframes`
    0%, 100% {
        opacity: 1;
        transform: scale(1);
    }
    50% {
        opacity: 0.35;
        transform: scale(0.75);
    }
`;

interface WaitStepIndicatorProps {
    active: boolean;
    minutes: number | null;
}

/**
 * The right-aligned state label on the wait tool's row inside the activity
 * group: a pulsing dot with "waiting" while paused, or a muted "waited Nm"
 * once it has resumed.
 */
export const WaitStepLabel: React.FC<WaitStepIndicatorProps> = ({active, minutes}) => {
    if (active) {
        return (
            <WaitingLabel data-testid='llm-bot-wait-step-waiting'>
                <PulseDot/>
                <FormattedMessage
                    id='ai.wait.step_waiting'
                    defaultMessage='waiting'
                />
            </WaitingLabel>
        );
    }
    if (minutes === null) {
        return null;
    }
    return (
        <WaitedLabel data-testid='llm-bot-wait-step-waited'>
            <FormattedMessage
                id='ai.wait.step_waited'
                defaultMessage='waited {minutes}m'
                values={{minutes}}
            />
        </WaitedLabel>
    );
};

// The amber clock shown in place of the green check on the wait tool's row
// while it is still paused.
export const WaitStepClock = styled(ClockOutlineIcon)`
    color: var(--away-indicator);
    width: 14px;
    height: 14px;
    flex-shrink: 0;
`;

// Amber tokens: the icon and text use the theme's away/warning color; the
// subtle fill and dashed border reuse the approved mockup's amber rgba, since
// the theme exposes no amber "-rgb" channel to tint from.
const StatusBar = styled.div<{$resolved: boolean}>`
    display: flex;
    align-items: flex-start;
    gap: 10px;
    border-radius: 8px;
    padding: 10px 12px;
    margin-top: 4px;

    ${(props) => (props.$resolved ? css`
        border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
        background: rgba(var(--center-channel-color-rgb), 0.04);
    ` : css`
        border: 1px dashed rgba(245, 171, 0, 0.5);
        background: rgba(245, 171, 0, 0.12);
    `)}
`;

// Fixed to the first text line's height so the icon aligns to the first line,
// not the vertical centre of the whole bar (which matters with the buttons).
const IconBox = styled.div<{$resolved: boolean}>`
    flex: none;
    height: 22px;
    display: flex;
    align-items: center;
    color: ${(props) => (props.$resolved ? 'var(--online-indicator)' : 'var(--away-indicator)')};
`;

const Content = styled.div`
    min-width: 0;
    flex: 1;
    font-size: 13px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const Line = styled.div`
    line-height: 22px;
`;

const Countdown = styled.span`
    font-variant-numeric: tabular-nums;
`;

const Mention = styled.span`
    color: var(--link-color);
`;

const Actions = styled.div`
    display: flex;
    gap: 8px;
    margin-top: 8px;
`;

const WaitingLabel = styled.span`
    display: inline-flex;
    align-items: center;
    gap: 6px;
    font-size: 11px;
    color: var(--away-indicator);
    flex-shrink: 0;
`;

const WaitedLabel = styled.span`
    font-size: 11px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    flex-shrink: 0;
`;

const PulseDot = styled.span`
    width: 7px;
    height: 7px;
    border-radius: 50%;
    background: var(--away-indicator);
    animation: ${pulse} 1.4s ease-in-out infinite;

    ${noMotionWhenReduced}
`;
