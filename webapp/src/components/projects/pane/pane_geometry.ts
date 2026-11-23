// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ProjectPaneMode} from '@/types/projects';

// The narrowest the pane gets beside the channel.
export const PANE_MIN_WIDTH = 360;

// Mattermost keeps the center column at least this wide; the channel's messages get no less.
export const POST_COLUMN_MIN_WIDTH = 385;

export const PANE_DEFAULT_WIDTH = 480;

export const PANE_KEYBOARD_STEP = 16;

// Below this window width Mattermost lays the channel out for phones.
export const MOBILE_VIEW_MAX_WIDTH = 768;

// The pane's width beside the channel, given the width of the center column: at least the
// pane's minimum, and never so wide that the messages get less than theirs. There is no other cap,
// and none at all before the column is measured.
export function clampPaneWidth(available: number, desired: number): number {
    if (available <= 0) {
        return Math.round(Math.max(desired, PANE_MIN_WIDTH));
    }
    const max = Math.max(PANE_MIN_WIDTH, available - POST_COLUMN_MIN_WIDTH);
    return Math.round(Math.min(Math.max(desired, PANE_MIN_WIDTH), max));
}

// Whether the pane and the messages both fit side by side.
export function fitsBesideMessages(available: number, viewportWidth: number): boolean {
    return viewportWidth >= MOBILE_VIEW_MAX_WIDTH && available >= PANE_MIN_WIDTH + POST_COLUMN_MIN_WIDTH;
}

// The layout the pane shows: the chosen one, except that a pane that doesn't fit beside the
// messages covers the center column instead. The choice itself is kept for when there's room.
export function effectivePaneMode(chosen: ProjectPaneMode, available: number, viewportWidth: number): ProjectPaneMode {
    if (chosen === 'split' && !fitsBesideMessages(available, viewportWidth)) {
        return 'pane';
    }
    return chosen;
}
