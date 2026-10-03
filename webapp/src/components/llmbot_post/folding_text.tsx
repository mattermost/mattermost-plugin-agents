// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useLayoutEffect, useRef, useState} from 'react';
import styled, {keyframes} from 'styled-components';

import PostText from '../post_text';

import {prefersReducedMotion} from './motion';

const FOLD_MS = 300;

/**
 * Returns a snapshot of answer text that just folded into the activity area,
 * for as long as its collapse animation runs. Layout effect so no frame is
 * painted with the text already gone.
 */
export function useFoldingText(answerText: string, enabled: boolean): string | null {
    const [foldingText, setFoldingText] = useState<string | null>(null);
    const previousRef = useRef(answerText);

    useLayoutEffect(() => {
        const previous = previousRef.current;
        previousRef.current = answerText;

        if (!enabled || answerText !== '' || previous === '' || prefersReducedMotion()) {
            return undefined; // eslint-disable-line no-undefined
        }

        setFoldingText(previous);
        const timer = setTimeout(() => setFoldingText(null), FOLD_MS);
        return () => {
            clearTimeout(timer);
            setFoldingText(null);
        };
    }, [answerText, enabled]);

    return foldingText;
}

interface FoldingTextProps {
    text: string;
    channelID: string;
    postID: string;
}

/** An aria-hidden ghost of the outgoing text, collapsing to nothing on mount. */
export const FoldingText: React.FC<FoldingTextProps> = ({text, channelID, postID}) => (
    <FoldViewport
        data-testid='llm-bot-folding-text'
        aria-hidden={true}
    >
        <FoldContent>
            <PostText
                message={text}
                channelID={channelID}
                postID={postID}
            />
        </FoldContent>
    </FoldViewport>
);

const foldAway = keyframes`
    from {
        grid-template-rows: 1fr;
        opacity: 1;
    }
    to {
        grid-template-rows: 0fr;
        opacity: 0;
    }
`;

const FoldViewport = styled.div`
    display: grid;
    grid-template-rows: 0fr;
    opacity: 0;
    animation: ${foldAway} ${FOLD_MS}ms ease;
`;

const FoldContent = styled.div`
    min-height: 0;
    overflow: hidden;
`;
