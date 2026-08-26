// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {css} from 'styled-components';

/**
 * Opts an animated element out for readers who ask for less motion. Covers
 * both properties so every animated part of a bot post can use the one rule,
 * whichever of the two it happens to use.
 */
export const noMotionWhenReduced = css`
    @media (prefers-reduced-motion: reduce) {
        animation: none;
        transition: none;
    }
`;

/** Whether the reader has asked for less motion, for effects that cannot use CSS. */
export function prefersReducedMotion(): boolean {
    return typeof window.matchMedia === 'function' &&
        window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}
