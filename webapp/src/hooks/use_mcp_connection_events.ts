// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect} from 'react';

// Payload for custom_mattermost-ai_mcp_connection_updated websocket events.
export type MCPConnectionEvent = {
    status: 'connected' | 'disconnected';
    serverName?: string;
    serverOrigin?: string;
}

const subscribers = new Set<(event: MCPConnectionEvent) => void>();

export function notifyMCPConnectionUpdated(event: MCPConnectionEvent) {
    subscribers.forEach((cb) => {
        try {
            cb(event);
        } catch {
            // Subscriber errors must not block other listeners.
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
