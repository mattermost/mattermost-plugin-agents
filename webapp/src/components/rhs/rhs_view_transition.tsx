// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useLayoutEffect, useRef, useState} from 'react';
import styled, {css, keyframes} from 'styled-components';

import {prefersReducedMotion} from '../llmbot_post/motion';

// How long the outgoing view stays mounted: --duration-moderate (300ms) plus a little, so the exit
// finishes before it unmounts. The duration itself comes from the token in CSS.
const EXIT_UNMOUNT_MS = 350;

// How far the view underneath shifts while the new one slides over it. No compass token for a
// share of the pane's width.
const SHIFT = '30%';

export type TransitionDirection = 'forward' | 'back' | 'none';

interface Outgoing {
    key: string;
    node: React.ReactNode;
    direction: TransitionDirection;
}

interface RHSViewTransitionProps {

    // Identifies the view shown (the history, the new chat, a given chat); a change runs the transition.
    viewKey: string;

    // Depth of the view: moving to a higher one slides forward (history to a chat), to a lower one back.
    // Equal ranks (chat to chat, new chat to its first chat) cross-fade.
    rank: number;
    children: React.ReactNode;
}

interface Shown {
    key: string;
    rank: number;
    outgoing: Outgoing | null;

    // False for the first frames after a change, while the new pane waits off-screen; the slide
    // starts once it has painted.
    started: boolean;

    // False while a view slides in going forward: the chat is the heavy view (the host thread
    // viewer, the composer, long markdown), and mounting it mid-slide drops frames, so the pane
    // slides in empty and the content mounts when the slide is over.
    contentShown: boolean;

    // The content was held back, so it fades in when it mounts.
    fade: boolean;
}

// Shows one view and, when `viewKey` changes, keeps the previous one mounted (as it last rendered)
// while the two slide: forward the new view enters from the right over the old one, back it enters
// from the left under the old one leaving to the right. The outgoing view is inert and unmounts
// when its exit ends. With reduced motion, or no previous view, the views just swap.
const RHSViewTransition = ({viewKey, rank, children}: RHSViewTransitionProps) => {
    const [shown, setShown] = useState<Shown>({key: viewKey, rank, outgoing: null, started: true, contentShown: true, fade: false});

    // The previous render's children, which become the outgoing view when the key changes.
    const lastChildren = useRef<React.ReactNode>(children);
    useLayoutEffect(() => {
        lastChildren.current = children;
    });

    // Derived from props during render, so the old view is rendered in the same pass as the new one
    // (and stays mounted, instead of being unmounted for a frame).
    if (shown.key !== viewKey) {
        let direction: TransitionDirection = 'none';
        if (rank > shown.rank) {
            direction = 'forward';
        } else if (rank < shown.rank) {
            direction = 'back';
        }
        const animate = !prefersReducedMotion();
        setShown({
            key: viewKey,
            rank,
            outgoing: animate ? {key: shown.key, node: lastChildren.current, direction} : null,
            started: !animate,
            contentShown: !animate || direction !== 'forward',
            fade: animate && direction === 'forward',
        });
    }

    const outgoing = shown.outgoing;
    const started = shown.started;

    // Starts the slide two frames after the change, once the new view has mounted and painted, so
    // its mounting work does not land in the middle of the motion.
    useEffect(() => {
        if (!outgoing || started) {
            return () => null;
        }
        let second = 0;
        const first = requestAnimationFrame(() => {
            second = requestAnimationFrame(() => setShown((current) => ({...current, started: true})));
        });
        return () => {
            cancelAnimationFrame(first);
            cancelAnimationFrame(second);
        };
    }, [outgoing, started]);

    // Unmounts the outgoing view once its exit is over.
    useEffect(() => {
        if (!outgoing || !started) {
            return () => null;
        }
        const timer = setTimeout(() => setShown((current) => ({...current, outgoing: null, contentShown: true})), EXIT_UNMOUNT_MS);
        return () => clearTimeout(timer);
    }, [outgoing, started]);

    const direction = outgoing?.direction ?? 'none';

    // 'enter-wait' until the slide starts, 'enter' while it runs, 'idle' once the old view is gone.
    let enteringState = 'idle';
    if (outgoing) {
        enteringState = started ? 'enter' : 'enter-wait';
    }
    return (
        <Stage>
            {outgoing && (
                <Layer
                    key={outgoing.key}
                    data-state={started ? 'exit' : 'exit-wait'}
                    data-direction={direction}
                    data-testid='rhs-view-exiting'
                    aria-hidden={true}
                    inert={true}
                >
                    {outgoing.node}
                </Layer>
            )}
            <Layer
                key={viewKey}
                data-state={enteringState}
                data-direction={direction}
                data-reveal={shown.fade && shown.contentShown ? 'fade' : 'none'}
            >
                {shown.contentShown ? children : null}
            </Layer>
        </Stage>
    );
};

export default RHSViewTransition;

const slideInFromRight = keyframes`
    from { transform: translateX(100%); }
    to { transform: translateX(0); }
`;

const slideInFromLeft = keyframes`
    from { opacity: 0; transform: translateX(-${SHIFT}); }
    to { opacity: 1; transform: translateX(0); }
`;

const slideOutToLeft = keyframes`
    from { opacity: 1; transform: translateX(0); }
    to { opacity: 0; transform: translateX(-${SHIFT}); }
`;

const slideOutToRight = keyframes`
    from { transform: translateX(0); }
    to { transform: translateX(100%); }
`;

const fadeIn = keyframes`
    from { opacity: 0; }
    to { opacity: 1; }
`;

const fadeOut = keyframes`
    from { opacity: 1; }
    to { opacity: 0; }
`;

const motion = (name: ReturnType<typeof keyframes>, easing: string) => css`
    animation: ${name} var(--duration-moderate) ${easing} both;
`;

// The layers stack in the stage and each fills it, so a view lays out exactly as it did alone. The
// stage clips the sliding views to the pane. The layer sliding over the other (the entering view
// going forward, the leaving one going back) has elevation-2 while it moves.
const Stage = styled.div`
    position: relative;
    display: flex;
    min-height: 0;
    flex: 1 1 auto;
    flex-direction: column;
    overflow: hidden;
`;

const Layer = styled.div`
    position: absolute;
    inset: 0;
    display: flex;
    min-height: 0;
    flex-direction: column;
    background: var(--center-channel-bg);

    /* While moving, each view is its own compositing layer, so the slide runs on the compositor while
       the main thread is busy with the new view, and its scrollbars are hidden (kept in layout, so
       nothing reflows). At rest neither applies: both would make the layer the containing block of
       any fixed-position descendant. */
    &:not([data-state='idle']) {
        will-change: transform;
        contain: layout style;
    }

    &:not([data-state='idle']) * {
        scrollbar-color: transparent transparent;
    }

    /* The content that mounts after a forward slide fades in, rather than popping. */
    &[data-reveal='fade'] > * {
        animation: ${fadeIn} var(--duration-quick) var(--ease-entrance) both;
    }

    &[data-state='exit'],
    &[data-state='exit-wait'] {
        z-index: 0;
        pointer-events: none;
    }

    &[data-state='enter'],
    &[data-state='enter-wait'] {
        z-index: 1;
    }

    /* Waiting to start: the entering view sits where its slide begins, out of sight. */
    &[data-state='enter-wait'][data-direction='forward'] {
        transform: translateX(100%);
    }

    &[data-state='enter-wait'][data-direction='back'] {
        opacity: 0;
        transform: translateX(-${SHIFT});
    }

    &[data-state='enter-wait'][data-direction='none'] {
        opacity: 0;
    }

    &[data-state='enter'][data-direction='forward'] {
        ${motion(slideInFromRight, 'var(--ease-entrance)')}
        box-shadow: var(--elevation-2);
    }

    &[data-state='exit'][data-direction='forward'] {
        ${motion(slideOutToLeft, 'var(--ease-exit)')}
    }

    /* Back: the old view leaves to the right, over the one returning from the left. */
    &[data-state='enter'][data-direction='back'],
    &[data-state='enter-wait'][data-direction='back'] {
        z-index: 0;
        ${motion(slideInFromLeft, 'var(--ease-entrance)')}
    }

    &[data-state='exit-wait'][data-direction='back'] {
        z-index: 1;
    }

    &[data-state='exit'][data-direction='back'] {
        z-index: 1;
        ${motion(slideOutToRight, 'var(--ease-exit)')}
        box-shadow: var(--elevation-2);
    }

    &[data-state='enter'][data-direction='none'] {
        ${motion(fadeIn, 'var(--ease-entrance)')}
    }

    &[data-state='exit'][data-direction='none'] {
        ${motion(fadeOut, 'var(--ease-exit)')}
    }

    @media (prefers-reduced-motion: reduce) {
        animation: none;
    }
`;
