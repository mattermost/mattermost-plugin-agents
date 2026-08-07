// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useLayoutEffect, useRef, useState} from 'react';
import styled, {css, keyframes} from 'styled-components';

import PostText from '../post_text';

import {noMotionWhenReduced, prefersReducedMotion} from './motion';

/** Length of the collapse, and how long the snapshot stays mounted for it. */
const FOLD_MS = 300;

/** Length of the entrance the answer gets when the row hands it back. */
const REVEAL_MS = 200;

export interface AnswerHandover {

    /** Main-area text on its way out, to keep rendering while it collapses. */
    foldingText: string | null;

    /** True for the length of the entrance the returning answer plays. */
    revealAnswer: boolean;
}

/**
 * Smooths both ends of the handover between the post's main area and the
 * collapsed activity row: text leaving the main area collapses instead of
 * disappearing between two frames, and text coming back gets an entrance
 * instead of appearing all at once.
 *
 * `answerText` is watched for emptiness rather than for round identity as a
 * proxy for "a round left or entered `answerRounds`" — round ids do not
 * survive the live-round snapshot or the refetch that follows the end of a
 * response, so they cannot be compared across renders, while the text can.
 *
 * `withholding` says text is being held in the activity row right now. It is
 * what keeps every other text that ever leaves or enters the main area from
 * animating: a response that never called a tool has no row to hold anything
 * in, so it never sets the flag and never animates. Both directions are
 * therefore mirrors of each other — leaving while withholding folds, arriving
 * just after withholding reveals — and neither can fire on the first render,
 * so a settled post mounts statically.
 *
 * Runs as a layout effect because a passive one would let the browser paint a
 * frame with the text already gone, which is the jump this exists to remove.
 */
export function useAnswerHandover(answerText: string, withholding: boolean): AnswerHandover {
    const [foldingText, setFoldingText] = useState<string | null>(null);
    const [revealAnswer, setRevealAnswer] = useState(false);
    const previousRef = useRef({text: answerText, withholding});

    useLayoutEffect(() => {
        const previous = previousRef.current;
        previousRef.current = {text: answerText, withholding};

        // A snapshot that cannot animate is worse than no snapshot: it would
        // hold the outgoing text frozen on screen for the fold's duration and
        // then cut it. Reduced motion gets the plain instant swap instead.
        if (prefersReducedMotion()) {
            return undefined; // eslint-disable-line no-undefined
        }

        const folding = withholding && answerText === '' && previous.text !== '';
        const revealing = previous.withholding && answerText !== '' && previous.text === '';
        if (!folding && !revealing) {
            return undefined; // eslint-disable-line no-undefined
        }

        if (folding) {
            setFoldingText(previous.text);
        } else {
            setRevealAnswer(true);
        }

        const clear = () => {
            setFoldingText(null);
            setRevealAnswer(false);
        };

        // Clearing on an interrupted transition too, so a snapshot can never
        // be stranded on screen and the entrance can never stick on.
        const timer = setTimeout(clear, folding ? FOLD_MS : REVEAL_MS);
        return () => {
            clearTimeout(timer);
            clear();
        };
    }, [answerText, withholding]);

    return {foldingText, revealAnswer};
}

interface FoldingTextProps {
    text: string;
    channelID: string;
    postID: string;
}

/**
 * A snapshot of main-area text on its way out, collapsing to nothing. Mounted
 * only for the length of the animation, so the collapse runs on mount and
 * needs no trigger of its own.
 *
 * The snapshot renders the outgoing rounds' text joined through a single
 * PostText and drops their reasoning and annotations: it is an aria-hidden
 * exit ghost on screen for 300ms, so approximating the shape it is fading
 * from is enough and rebuilding the real rounds is not worth it.
 */
export const FoldingText: React.FC<FoldingTextProps> = ({text, channelID, postID}) => {
    const ref = useRef<HTMLDivElement>(null);

    useLayoutEffect(() => {
        const element = ref.current;
        if (!element) {
            return;
        }

        element.style.height = `${element.scrollHeight}px`;

        // Reading the layout back forces the measured height into the
        // computed style now. Without it both writes land in one style
        // recalculation and the transition has nothing to run from.
        void element.offsetHeight; // eslint-disable-line no-void

        element.style.height = '0';
        element.style.opacity = '0';
    }, []);

    return (
        <FoldViewport
            ref={ref}
            data-testid='llm-bot-folding-text'
            aria-hidden={true}
        >
            <PostText
                message={text}
                channelID={channelID}
                postID={postID}
            />
        </FoldViewport>
    );
};

const FoldViewport = styled.div`
    overflow: hidden;
    transition: height ${FOLD_MS}ms ease, opacity ${FOLD_MS}ms ease;

    ${noMotionWhenReduced}
`;

const answerIn = keyframes`
    from {
        opacity: 0;
        transform: translateY(4px);
    }
    to {
        opacity: 1;
        transform: translateY(0);
    }
`;

/**
 * Wraps the post's answer rounds. Always mounted, so the entrance is driven
 * by the handover signal rather than by the wrapper appearing — a settled
 * post scrolled back into view must not replay it.
 */
export const AnswerArea = styled.div<{$reveal: boolean}>`
    ${(props) => props.$reveal && css`
        animation: ${answerIn} ${REVEAL_MS}ms ease-out;
    `}

    ${noMotionWhenReduced}
`;
