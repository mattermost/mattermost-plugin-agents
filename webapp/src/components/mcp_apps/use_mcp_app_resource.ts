// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';

import {AppResourceContents, getMCPAppResource, MCPAppResourceError} from '@/client';
import {useMCPConnectionEvents} from '@/hooks/use_mcp_connection_events';

export type AppPhase =
    | {phase: 'loading'}
    | {phase: 'ready'; contents: AppResourceContents}
    | {phase: 'auth_required'; authURL: string; connectAttempted: boolean}
    | {phase: 'no_access'}
    | {phase: 'unavailable'};

/**
 * Single epoch-guarded loader for GET /mcp/app-resource. Mount, reconnect,
 * focus-after-connect, and explicit retry all share this path; newest request
 * wins and unmount / identity changes discard stale completions.
 */
export function useMCPAppResource(opts: {
    postID: string;
    toolCallID: string;
    serverOrigin?: string;
    enabled: boolean;
}): {
    phase: AppPhase;
    retry: () => void;
    markConnectAttempted: () => void;
    setUnavailable: () => void;
} {
    const {postID, toolCallID, serverOrigin, enabled} = opts;
    const [phase, setPhase] = useState<AppPhase>({phase: 'loading'});
    const epochRef = useRef(0);
    const connectAttemptedRef = useRef(false);

    const load = useCallback(() => {
        if (!enabled) {
            return;
        }
        const epoch = ++epochRef.current;
        setPhase({phase: 'loading'});
        (async () => {
            try {
                const response = await getMCPAppResource(postID, toolCallID);
                if (epoch !== epochRef.current) {
                    return;
                }
                const contents = response.contents?.[0];
                if (!contents?.text) {
                    setPhase({phase: 'unavailable'});
                    return;
                }
                setPhase({phase: 'ready', contents});
            } catch (err) {
                if (epoch !== epochRef.current) {
                    return;
                }
                if (err instanceof MCPAppResourceError) {
                    if (err.status === 401 && err.authURL) {
                        if (connectAttemptedRef.current) {
                            setPhase({phase: 'no_access'});
                        } else {
                            setPhase({phase: 'auth_required', authURL: err.authURL, connectAttempted: false});
                        }
                        return;
                    }
                    if (err.status === 401 || err.status === 403) {
                        setPhase({phase: 'no_access'});
                        return;
                    }
                }
                setPhase({phase: 'unavailable'});
            }
        })();
    }, [enabled, postID, toolCallID]);

    // Reset connect-attempt state and load when identity / enablement changes.
    useEffect(() => {
        connectAttemptedRef.current = false;
        if (!enabled) {
            epochRef.current += 1;
            setPhase({phase: 'loading'});
            return () => {
                epochRef.current += 1;
            };
        }
        load();
        return () => {
            epochRef.current += 1;
        };
    }, [enabled, load, postID, toolCallID]);

    const markConnectAttempted = useCallback(() => {
        connectAttemptedRef.current = true;
        setPhase((prev) => {
            if (prev.phase === 'auth_required') {
                return {...prev, connectAttempted: true};
            }
            return prev;
        });
    }, []);

    const retry = useCallback(() => {
        load();
    }, [load]);

    useMCPConnectionEvents(useCallback((event) => {
        if (event.status !== 'connected') {
            return;
        }
        if (event.serverOrigin && serverOrigin && event.serverOrigin !== serverOrigin) {
            return;
        }
        load();
    }, [load, serverOrigin]));

    // After the user opened the OAuth popup, re-check when the window regains
    // focus (denied/failed OAuth never emits a websocket event).
    useEffect(() => {
        const onFocus = () => {
            if (connectAttemptedRef.current) {
                load();
            }
        };
        window.addEventListener('focus', onFocus);
        return () => window.removeEventListener('focus', onFocus);
    }, [load]);

    const setUnavailable = useCallback(() => {
        setPhase({phase: 'unavailable'});
    }, []);

    return {phase, retry, markConnectAttempted, setUnavailable};
}
