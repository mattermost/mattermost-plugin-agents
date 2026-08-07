// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useLayoutEffect, useRef, useState} from 'react';
import styled from 'styled-components';

import PostText from '../post_text';

/** Length of the collapse, and how long the snapshot stays mounted for it. */
const FOLD_MS = 300;

/**
 * Holds on to main-area text for the length of the fold animation after the
 * post stops rendering it, so it collapses instead of disappearing between two
 * frames. Returns the snapshot to keep on screen, or null.
 *
 * `enabled` is what keeps this from firing on every text that ever leaves the
 * main area: it is set only while a tool-using response is streaming with the
 * activity area collapsed, which is the one situation where text is pulled out
 * from under the reader. The end of a response, the refetch that follows it and
 * regeneration all leave it clear.
 *
 * Runs as a layout effect because a passive one would let the browser paint a
 * frame with the text already gone — the jump this exists to remove.
 */
export function useFoldingText(text: string, enabled: boolean): string | null {
    const [folding, setFolding] = useState<string | null>(null);
    const previousRef = useRef(text);

    useLayoutEffect(() => {
        const previous = previousRef.current;
        previousRef.current = text;

        if (!enabled || text !== '' || previous === '') {
            return undefined; // eslint-disable-line no-undefined
        }

        setFolding(previous);
        const timer = setTimeout(() => setFolding(null), FOLD_MS);

        // Clearing on an interrupted fold too, so a snapshot can never be left
        // on screen underneath whatever replaced it.
        return () => {
            clearTimeout(timer);
            setFolding(null);
        };
    }, [text, enabled]);

    return folding;
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
 */
export const FoldingText: React.FC<FoldingTextProps> = ({text, channelID, postID}) => {
    const ref = useRef<HTMLDivElement>(null);

    // Height has to be measured before it can be animated away, and the
    // measured value has to be committed in its own frame — set both ends in
    // one go and the browser collapses them into a single style change with
    // nothing to transition between.
    useLayoutEffect(() => {
        const element = ref.current;
        if (!element) {
            return undefined; // eslint-disable-line no-undefined
        }

        element.style.height = `${element.scrollHeight}px`;
        const frame = requestAnimationFrame(() => {
            element.style.height = '0';
            element.style.opacity = '0';
        });

        return () => cancelAnimationFrame(frame);
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

    @media (prefers-reduced-motion: reduce) {
        transition: none;
    }
`;
