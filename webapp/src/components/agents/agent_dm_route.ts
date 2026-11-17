// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useState} from 'react';

import {getHistory} from './use_agents_route';

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

// The post id in the current location's DM URL ('' when it has none); null when the location is not
// a Channels DM route.
const readDMPostId = (): string | null => parseDMPath(getHistory()?.location?.pathname)?.postId ?? null;

// The post id in the DM URL, kept current as the host navigates. The URL is where a chat is opened
// from outside the chat view (the sidebar's nested chats, a permalink) and where the view records
// the chat it opened.
export const useDMPostId = (): string | null => {
    const [postId, setPostId] = useState<string | null>(readDMPostId);

    useEffect(() => {
        const sync = () => setPostId(readDMPostId());
        sync();
        const unlisten = getHistory()?.listen?.(sync);
        return () => {
            if (typeof unlisten === 'function') {
                unlisten();
            }
        };
    }, []);

    return postId;
};
