// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useMemo, useState} from 'react';

import manifest from '@/manifest';

import {isSettingsSection, type SettingsSection} from './settings/sections';

export const AGENTS_ROUTE = `/plug/${manifest.id}/agents`;

// 'landing' is the product root, which hands over to a new chat with the user's agent (or All agents).
export type AgentsView = 'landing' | 'all' | 'prompts' | 'chat' | 'settings';

export interface AgentsRoute {
    view: AgentsView;
    botId?: string;
    rootId?: string;

    // 'all' only: the All agents view with the create-agent flow open.
    create?: boolean;

    // 'settings' only: the section to show; unset for the first one.
    section?: SettingsSection;
}

export interface AgentsNavigation {
    toAll: () => void;

    // The All agents view, replacing the current entry (the landing without an
    // agent to open) so Back does not return to it.
    replaceWithAll: () => void;

    // A new chat with the agent, replacing the current entry (the landing) so Back does not return to it.
    replaceWithChat: (botId: string) => void;

    // The All agents view with the create-agent flow open.
    toNewAgent: () => void;

    // Leaves the create flow for the plain All agents view, replacing the create
    // entry so Back does not reopen it.
    closeNewAgent: () => void;
    toPrompts: () => void;
    toChat: (botId: string, rootId?: string) => void;
    toSettings: (section?: SettingsSection) => void;
}

// Minimal slice of the host's history object. v4 passes (location, action) to
// listeners and v5 passes ({location}); we ignore the arguments and re-read
// `location`, so both work.
interface HostHistory {
    location?: {pathname: string};
    push: (path: string) => void;
    replace?: (path: string) => void;
    listen?: (listener: () => void) => (() => void) | void;

    // history v4: a string asks the user to confirm the transition, undefined lets it through.
    block?: (prompt: (location: {pathname: string}) => string | undefined) => (() => void) | void;
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

// The bare product root is the landing. Anything that is not a recognized
// sub-route falls back to the all-agents view rather than the landing, so a
// mistyped or stale link never starts a chat (or creates a DM channel) as a
// side effect.
export const parseAgentsRoute = (pathname: string | undefined): AgentsRoute => {
    if (!pathname || !pathname.startsWith(AGENTS_ROUTE)) {
        return {view: 'all'};
    }
    const rest = pathname.slice(AGENTS_ROUTE.length);
    if (rest !== '' && !rest.startsWith('/')) {
        return {view: 'all'};
    }
    const segments = rest.split('/').filter(Boolean).map(decode);

    if (segments.length === 0) {
        return {view: 'landing'};
    }
    if (segments[0] === 'all' && segments.length === 1) {
        return {view: 'all'};
    }
    if (segments[0] === 'all' && segments[1] === 'new' && segments.length === 2) {
        return {view: 'all', create: true};
    }
    if (segments[0] === 'prompts' && segments.length === 1) {
        return {view: 'prompts'};
    }

    // An unknown section shows the first one: it is a read of the settings,
    // nothing happens as a side effect.
    if (segments[0] === 'settings' && segments.length <= 2) {
        return isSettingsSection(segments[1]) ? {view: 'settings', section: segments[1]} : {view: 'settings'};
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

export const ALL_AGENTS_PATH = `${AGENTS_ROUTE}/all`;
export const NEW_AGENT_PATH = `${AGENTS_ROUTE}/all/new`;
export const PROMPTS_PATH = `${AGENTS_ROUTE}/prompts`;
export const SETTINGS_PATH = `${AGENTS_ROUTE}/settings`;

export const buildSettingsPath = (section?: SettingsSection): string => (
    section ? `${SETTINGS_PATH}/${section}` : SETTINGS_PATH
);

export const buildChatPath = (botId: string, rootId?: string): string => {
    const base = `${AGENTS_ROUTE}/chat/${encodeURIComponent(botId)}`;
    return rootId ? `${base}/${encodeURIComponent(rootId)}` : base;
};

// True while the host's current location is inside the Agents product (read at call time).
export const isAgentsPathActive = (): boolean => {
    const pathname = getHistory()?.location?.pathname ?? '';
    return pathname === AGENTS_ROUTE || pathname.startsWith(`${AGENTS_ROUTE}/`);
};

// While active, the host router asks the user to confirm before leaving for a
// path that allow rejects. Reloads and closing the tab are not covered.
export const useNavigationGuard = (active: boolean, allow: (pathname: string) => boolean, message: string) => {
    useEffect(() => {
        if (!active) {
            return () => {/* nothing to remove */};
        }
        const unblock = getHistory()?.block?.((location) => (allow(location.pathname) ? undefined : message)); // eslint-disable-line no-undefined
        return () => {
            if (typeof unblock === 'function') {
                unblock();
            }
        };
    }, [active, allow, message]);
};

const readRoute = (): AgentsRoute => parseAgentsRoute(getHistory()?.location?.pathname);

export const useAgentsRoute = (): AgentsRoute & AgentsNavigation => {
    const [route, setRoute] = useState<AgentsRoute>(readRoute);

    useEffect(() => {
        const history = getHistory();
        const sync = () => {
            const next = readRoute();
            setRoute((prev) => (
                prev.view === next.view && prev.botId === next.botId && prev.rootId === next.rootId && prev.create === next.create && prev.section === next.section ? prev : next
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

    const replace = useCallback((path: string) => {
        const history = getHistory();
        if (history?.replace) {
            history.replace(path);
        } else {
            history?.push(path);
        }
    }, []);

    const nav = useMemo<AgentsNavigation>(() => ({
        toAll: () => push(ALL_AGENTS_PATH),
        replaceWithAll: () => replace(ALL_AGENTS_PATH),
        replaceWithChat: (botId) => replace(buildChatPath(botId)),
        toNewAgent: () => push(NEW_AGENT_PATH),
        closeNewAgent: () => replace(ALL_AGENTS_PATH),
        toPrompts: () => push(PROMPTS_PATH),
        toChat: (botId, rootId) => push(buildChatPath(botId, rootId)),
        toSettings: (section) => push(buildSettingsPath(section)),
    }), [push, replace]);

    return {...route, ...nav};
};
