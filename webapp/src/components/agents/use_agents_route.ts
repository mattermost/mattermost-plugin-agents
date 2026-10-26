// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useMemo, useState} from 'react';

import manifest from '@/manifest';

export const AGENTS_ROUTE = `/plug/${manifest.id}/agents`;

export type AgentsView = 'all' | 'prompts' | 'chat';

export interface AgentsRoute {
    view: AgentsView;
    botId?: string;
    rootId?: string;
}

export interface AgentsNavigation {
    toAll: () => void;
    toPrompts: () => void;
    toChat: (botId: string, rootId?: string) => void;
}

// Minimal slice of the host's history object. v4 passes (location, action) to
// listeners and v5 passes ({location}); we ignore the arguments and re-read
// `location`, so both work.
interface HostHistory {
    location?: {pathname: string};
    push: (path: string) => void;
    listen?: (listener: () => void) => (() => void) | void;
}

const getHistory = (): HostHistory | undefined => {
    return window.WebappUtils?.browserHistory as HostHistory | undefined;
};

const decode = (segment: string): string => {
    try {
        return decodeURIComponent(segment);
    } catch {
        return segment;
    }
};

// Anything that is not a recognized sub-route falls back to the all-agents view.
export const parseAgentsRoute = (pathname: string | undefined): AgentsRoute => {
    if (!pathname || !pathname.startsWith(AGENTS_ROUTE)) {
        return {view: 'all'};
    }
    const rest = pathname.slice(AGENTS_ROUTE.length);
    if (rest !== '' && !rest.startsWith('/')) {
        return {view: 'all'};
    }
    const segments = rest.split('/').filter(Boolean).map(decode);

    if (segments[0] === 'prompts' && segments.length === 1) {
        return {view: 'prompts'};
    }
    if (segments[0] === 'chat' && segments[1] && segments.length <= 3) {
        const route: AgentsRoute = {view: 'chat', botId: segments[1]};
        if (segments[2]) {
            route.rootId = segments[2];
        }
        return route;
    }
    return {view: 'all'};
};

export const buildChatPath = (botId: string, rootId?: string): string => {
    const base = `${AGENTS_ROUTE}/chat/${encodeURIComponent(botId)}`;
    return rootId ? `${base}/${encodeURIComponent(rootId)}` : base;
};

const readRoute = (): AgentsRoute => parseAgentsRoute(getHistory()?.location?.pathname);

export const useAgentsRoute = (): AgentsRoute & AgentsNavigation => {
    const [route, setRoute] = useState<AgentsRoute>(readRoute);

    useEffect(() => {
        const history = getHistory();
        const sync = () => {
            const next = readRoute();
            setRoute((prev) => (
                prev.view === next.view && prev.botId === next.botId && prev.rootId === next.rootId ? prev : next
            ));
        };
        sync();
        const unlisten = history?.listen?.(sync);
        return () => {
            if (typeof unlisten === 'function') {
                unlisten();
            }
        };
    }, []);

    const push = useCallback((path: string) => {
        getHistory()?.push(path);
    }, []);

    const nav = useMemo<AgentsNavigation>(() => ({
        toAll: () => push(AGENTS_ROUTE),
        toPrompts: () => push(`${AGENTS_ROUTE}/prompts`),
        toChat: (botId, rootId) => push(buildChatPath(botId, rootId)),
    }), [push]);

    return {...route, ...nav};
};
