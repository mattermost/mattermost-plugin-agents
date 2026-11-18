// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// The Channels route of a DM, with an optional post id: `/:team/messages/:identifier(/:postid)`.
// Anything else (the channel popout's `/_popout/channel/...`) is not one.
const DM_PATH = /^(\/[^/]+\/messages\/[^/]+)(?:\/([^/]+))?\/?$/;

const parseDMPath = (pathname: string | undefined): {path: string; postId: string} | null => {
    if (!pathname || pathname.startsWith('/_popout/')) {
        return null;
    }
    const match = DM_PATH.exec(pathname);
    return match ? {path: match[1], postId: match[2] ?? ''} : null;
};

// This DM's Channels URL without a post id, or '' when the current location is not one.
export const dmChannelPath = (pathname: string | undefined): string => parseDMPath(pathname)?.path ?? '';

// The post id in a DM URL ('' when it has none); null when the pathname is not a Channels DM route.
export const dmPostId = (pathname: string | undefined): string | null => parseDMPath(pathname)?.postId ?? null;

// How the host's history listener reports a navigation: (location, action) on history v4,
// ({location, action}) on v5.
export const navigationAction = (args: unknown[]): string | undefined => {
    if (typeof args[1] === 'string') {
        return args[1];
    }
    return (args[0] as {action?: string} | undefined)?.action;
};
