// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled, {css, keyframes} from 'styled-components';

import {noMotionWhenReduced, prefersReducedMotion} from './motion';

// Replies that start within this long never show the dots, so fast replies do not flash.
export const THINKING_DELAY_MS = 150;

// Once shown, the dots stay at least this long so they do not flicker.
export const THINKING_MIN_VISIBLE_MS = 300;

// Matches --duration-quick, which the cross-fade out uses in CSS. The unmount
// waits this long so the fade can finish; tests advance by it.
export const THINKING_FADE_MS = 150;

// One wave cycle. compass has no token for a looping ambient animation (its
// --duration-* tokens top out at --duration-moderate, 300ms, and are for
// one-shot transitions), so this is a local value.
const WAVE_CYCLE_MS = 1200;

// The dots are the host text color at reduced emphasis.
const DOT_EMPHASIS_PERCENT = 48;

export type ThinkingPhase = 'hidden' | 'visible' | 'leaving';

/**
 * Drives the indicator for `active`: hidden, then visible after the delay, then
 * leaving (cross-fading out) once inactive and the minimum time has passed.
 * Going inactive before the delay elapses never shows anything. Timers are
 * cleared whenever `active` changes and on unmount.
 */
export function useThinkingPhase(
    active: boolean,
    delayMs = THINKING_DELAY_MS,
    minVisibleMs = THINKING_MIN_VISIBLE_MS,
    fadeMs = THINKING_FADE_MS,
): ThinkingPhase {
    const [phase, setPhase] = useState<ThinkingPhase>('hidden');
    const phaseRef = useRef<ThinkingPhase>('hidden');
    const shownAtRef = useRef(0);

    useEffect(() => {
        const timers: Array<ReturnType<typeof setTimeout>> = [];
        const update = (next: ThinkingPhase) => {
            phaseRef.current = next;
            setPhase(next);
        };

        if (active) {
            if (phaseRef.current === 'hidden') {
                timers.push(setTimeout(() => {
                    shownAtRef.current = Date.now();
                    update('visible');
                }, delayMs));
            } else if (phaseRef.current === 'leaving') {
                update('visible');
            }
        } else if (phaseRef.current === 'visible') {
            const remaining = Math.max(0, minVisibleMs - (Date.now() - shownAtRef.current));
            timers.push(setTimeout(() => {
                update('leaving');
                timers.push(setTimeout(() => update('hidden'), fadeMs));
            }, remaining));
        } else if (phaseRef.current === 'leaving') {
            timers.push(setTimeout(() => update('hidden'), fadeMs));
        }

        return () => timers.forEach(clearTimeout);
    }, [active, delayMs, minVisibleMs, fadeMs]);

    return phase;
}

interface ThinkingDotsProps {

    /** Generating, with nothing renderable yet. */
    active: boolean;

    /** The agent's display name, for the announcement. */
    agentName?: string;
}

/** Three dots in a rising wave, where the reply text will appear. */
export const ThinkingDots: React.FC<ThinkingDotsProps> = ({active, agentName}) => {
    const intl = useIntl();
    const phase = useThinkingPhase(active);
    const animate = !prefersReducedMotion();

    // The text is set once when the dots appear and cleared when they leave, so
    // re-renders in between do not re-announce it.
    let announcement = '';
    if (phase === 'visible') {
        announcement = agentName ? intl.formatMessage(
            {defaultMessage: '{name} is thinking'},
            {name: agentName},
        ) : intl.formatMessage({defaultMessage: 'The agent is thinking'});
    }

    return (
        <>
            <LiveStatus role='status'>{announcement}</LiveStatus>
            {phase !== 'hidden' && (
                <Dots
                    data-testid='llm-bot-thinking'
                    data-phase={phase}
                    data-motion={animate ? 'full' : 'reduced'}
                    $leaving={phase === 'leaving'}
                    $animate={animate}
                    aria-hidden={true}
                >
                    <Dot $animate={animate}/>
                    <Dot $animate={animate}/>
                    <Dot $animate={animate}/>
                </Dots>
            )}
        </>
    );
};

/** The wave itself, for other indicators that mean "the agent is responding". */
export const WaveDots: React.FC<{animate: boolean; half?: boolean}> = ({animate, half = false}) => (
    <DotRow
        aria-hidden={true}
        $half={half}
    >
        <Dot $animate={animate}/>
        <Dot $animate={animate}/>
        <Dot $animate={animate}/>
    </DotRow>
);

const wave = keyframes`
    0%, 60%, 100% {
        transform: translateY(0);
    }

    30% {
        transform: translateY(calc(-1 * var(--wave-dot-rise, var(--spacing-xxxs))));
    }
`;

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

// One line of body text tall, so the bubble does not jump when text arrives.
// Leaving, it drops out of layout (absolute keeps its static position) and
// fades, so the text that replaced it takes its place.
const Dots = styled.div<{$leaving: boolean; $animate: boolean}>`
    display: flex;
    align-items: center;
    gap: var(--spacing-xxxs);
    min-height: var(--line-height-100);
    opacity: 1;
    transition: opacity var(--duration-quick) var(--ease-exit);

    ${(props) => props.$leaving && css`
        position: absolute;
        opacity: 0;
        pointer-events: none;
    `}

    ${noMotionWhenReduced}
`;

// `half` is for small indicators (the floating jump button): the dots, the gaps and the rise all
// at half size, through the variables the dots read. `--wave-dot-color` set on an ancestor recolors the dots (the sidebar sets it to its icon color).
const DotRow = styled.span<{$half: boolean}>`
    --wave-dot-size: ${(p) => (p.$half ? 'calc(var(--spacing-xxs) / 2)' : 'var(--spacing-xxs)')};
    --wave-dot-gap: ${(p) => (p.$half ? 'calc(var(--spacing-xxxs) / 2)' : 'var(--spacing-xxxs)')};
    --wave-dot-rise: ${(p) => (p.$half ? 'calc(var(--spacing-xxxs) / 2)' : 'var(--spacing-xxxs)')};

    display: inline-flex;
    align-items: center;
    gap: var(--wave-dot-gap);
`;

const Dot = styled.span<{$animate: boolean}>`
    width: var(--wave-dot-size, var(--spacing-xxs));
    height: var(--wave-dot-size, var(--spacing-xxs));
    border-radius: 50%;
    background: var(--wave-dot-color, color-mix(in srgb, var(--center-channel-color) ${DOT_EMPHASIS_PERCENT}%, transparent));

    ${(props) => props.$animate && css`
        animation: ${wave} ${WAVE_CYCLE_MS}ms var(--ease-transition) infinite;

        &:nth-child(2) {
            animation-delay: ${WAVE_CYCLE_MS / 6}ms;
        }

        &:nth-child(3) {
            animation-delay: ${WAVE_CYCLE_MS / 3}ms;
        }
    `}

    ${noMotionWhenReduced}
`;
