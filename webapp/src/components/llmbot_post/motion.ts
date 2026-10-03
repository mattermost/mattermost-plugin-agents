// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {css} from 'styled-components';

/** Disables animations and transitions for readers who ask for less motion. */
export const noMotionWhenReduced = css`
    @media (prefers-reduced-motion: reduce) {
        animation: none;
        transition: none;
    }
`;

/** For motion decisions that cannot be made in CSS. */
export function prefersReducedMotion(): boolean {
    return typeof window.matchMedia === 'function' &&
        window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}
