// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useLayoutEffect, useRef, useState} from 'react';
import styled, {css, keyframes} from 'styled-components';

import {noMotionWhenReduced, prefersReducedMotion} from './motion';

export const ROLL_MS = 240;

interface RollingLineProps {

    /** Changing the key rolls the old content out and the new content in. */
    lineKey: string;

    /** Off for settled posts, so they render statically on mount. */
    animate: boolean;

    children: React.ReactNode;
}

interface Snapshot {
    key: string;
    content: React.ReactNode;
}

/** A one-line viewport whose content rolls upward whenever its key changes. */
export const RollingLine: React.FC<RollingLineProps> = ({lineKey, animate, children}) => {
    const [outgoing, setOutgoing] = useState<Snapshot | null>(null);
    const [rolledKey, setRolledKey] = useState<string | null>(null);
    const shownRef = useRef<Snapshot>({key: lineKey, content: children});
    const animateRef = useRef(animate);
    animateRef.current = animate;

    // Declared before the snapshot effect below, so it still sees the previous render's content.
    useLayoutEffect(() => {
        const previous = shownRef.current;
        if (previous.key === lineKey) {
            return undefined; // eslint-disable-line no-undefined
        }
        if (!animateRef.current || prefersReducedMotion()) {
            setRolledKey(null);
            return undefined; // eslint-disable-line no-undefined
        }

        setOutgoing(previous);
        setRolledKey(lineKey);
        const timer = setTimeout(() => setOutgoing(null), ROLL_MS);
        return () => clearTimeout(timer);
    }, [lineKey]);

    useLayoutEffect(() => {
        shownRef.current = {key: lineKey, content: children};
    });

    return (
        <Viewport>
            {outgoing !== null && outgoing.key !== lineKey && (
                <Line
                    key={`out:${outgoing.key}`}
                    $motion='out'
                    aria-hidden={true}
                >
                    {outgoing.content}
                </Line>
            )}
            <Line
                key={lineKey}
                $motion={rolledKey === lineKey ? 'in' : 'none'}
                data-testid='llm-bot-tool-activity-current'
            >
                {children}
            </Line>
        </Viewport>
    );
};

const Viewport = styled.div`
    position: relative;
    flex: 1;
    min-width: 0;
    height: 20px;
    overflow: hidden;
`;

const rollIn = keyframes`
    from {
        transform: translateY(100%);
        opacity: 0;
    }
    to {
        transform: translateY(0);
        opacity: 1;
    }
`;

const rollOut = keyframes`
    from {
        transform: translateY(0);
        opacity: 1;
    }
    to {
        transform: translateY(-100%);
        opacity: 0;
    }
`;

const Line = styled.div<{$motion: 'in' | 'out' | 'none'}>`
    display: flex;
    align-items: center;
    gap: 8px;
    height: 20px;
    line-height: 20px;

    ${(props) => props.$motion === 'in' && css`
        animation: ${rollIn} ${ROLL_MS}ms ease-out;
    `}

    ${(props) => props.$motion === 'out' && css`
        position: absolute;
        inset: 0;
        animation: ${rollOut} ${ROLL_MS}ms ease-in forwards;
    `}

    ${noMotionWhenReduced}
`;
