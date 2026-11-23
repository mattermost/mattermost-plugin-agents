// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useState} from 'react';

import {historyPathWithoutBasename} from '../../channel_artifacts/bookmark';

export type BrowserLocation = {pathname: string; search: string};

const currentLocation = (): BrowserLocation => ({pathname: window.location.pathname, search: window.location.search});

// The browser's location, kept current through the host router and back/forward navigation.
export function useBrowserLocation(): BrowserLocation {
    const [location, setLocation] = useState(currentLocation);

    useEffect(() => {
        const update = () => setLocation((previous) => {
            const next = currentLocation();
            return previous.pathname === next.pathname && previous.search === next.search ? previous : next;
        });
        const unlisten = window.WebappUtils?.browserHistory?.listen?.(update);
        window.addEventListener('popstate', update);
        update();
        return () => {
            unlisten?.();
            window.removeEventListener('popstate', update);
        };
    }, []);

    return location;
}

function decodeSegment(segment: string): string {
    try {
        return decodeURIComponent(segment);
    } catch {
        return segment;
    }
}

// Whether pathname is the channel view of teamName/channelName (a permalink in it included), not
// another view, such as Threads or Drafts, that keeps the last channel selected.
export function isChannelRoute(pathname: string, teamName: string, channelName: string): boolean {
    if (!teamName || !channelName) {
        return false;
    }
    const segments = historyPathWithoutBasename(pathname).split('/').filter(Boolean);
    if (segments.length !== 3 && segments.length !== 4) {
        return false;
    }
    return decodeSegment(segments[0]) === teamName && segments[1] === 'channels' && decodeSegment(segments[2]) === channelName;
}
