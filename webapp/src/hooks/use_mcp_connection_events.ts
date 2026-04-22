// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect} from 'react';

// MCPConnectionEvent mirrors the payload of the backend websocket event
// `custom_mattermost-ai_mcp_connection_updated` (see api.WebsocketEventMCPConnectionUpdated).
export type MCPConnectionEvent = {
    status: 'connected' | 'disconnected';
    serverName?: string;
    serverOrigin?: string;
}

// Module-level subscriber list. The websocket registration itself lives in
// index.tsx; all it does is call notifyMCPConnectionUpdated() so UI components
// can refresh their cached view of the user's MCP connection state without
// requiring a page reload.
const subscribers = new Set<(event: MCPConnectionEvent) => void>();

export function notifyMCPConnectionUpdated(event: MCPConnectionEvent) {
    subscribers.forEach((cb) => {
        try {
            cb(event);
        } catch {
            // Swallow listener errors so one misbehaving subscriber does not
            // prevent the others from seeing the event.
        }
    });
}

export function useMCPConnectionEvents(listener: (event: MCPConnectionEvent) => void) {
    useEffect(() => {
        subscribers.add(listener);
        return () => {
            subscribers.delete(listener);
        };
    }, [listener]);
}
