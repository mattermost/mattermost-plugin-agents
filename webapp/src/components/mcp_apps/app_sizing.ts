// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export const APP_MIN_HEIGHT = 160;
export const APP_DEFAULT_HEIGHT = 420;
export const APP_MAX_VIEWPORT_RATIO = 0.7;

export function maxAppHeight(viewportHeight: number): number {
    return Math.max(APP_MIN_HEIGHT, Math.round(viewportHeight * APP_MAX_VIEWPORT_RATIO));
}

export function clampAppHeight(reported: number, viewportHeight: number = window.innerHeight): number {
    return Math.min(Math.max(Math.round(reported), APP_MIN_HEIGHT), maxAppHeight(viewportHeight));
}
