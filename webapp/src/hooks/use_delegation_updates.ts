// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect} from 'react';

import type {DelegationUpdate} from '@/types/delegation';

const subscribers = new Set<(event: DelegationUpdate) => void>();

// notifyDelegationUpdate fans a delegation_update websocket event out to all
// mounted delegation cards. Called from the plugin's websocket handler.
export function notifyDelegationUpdate(event: DelegationUpdate) {
    subscribers.forEach((cb) => {
        try {
            cb(event);
        } catch {
            // Subscriber errors must not block other listeners.
        }
    });
}

// useDelegationUpdates subscribes to live updates for one delegation, keyed
// by the parent ask_agent tool call ID.
export function useDelegationUpdates(parentToolCallID: string, listener: (event: DelegationUpdate) => void) {
    useEffect(() => {
        const filtered = (event: DelegationUpdate) => {
            if (event.parent_tool_call_id === parentToolCallID) {
                listener(event);
            }
        };
        subscribers.add(filtered);
        return () => {
            subscribers.delete(filtered);
        };
    }, [parentToolCallID, listener]);
}
