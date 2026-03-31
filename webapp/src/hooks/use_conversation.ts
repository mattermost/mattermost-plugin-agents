// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useMemo, useState} from 'react';

import {getConversation} from '@/client';
import type {ConversationResponse, Turn} from '@/types/conversation';

export interface UseConversationResult {
    conversation: ConversationResponse | null;
    loading: boolean;
    error: Error | null;
}

// Module-level cache and subscriber management
const conversationCache = new Map<string, ConversationResponse>();
const inflightRequests = new Map<string, Promise<ConversationResponse>>();
const subscribers = new Set<() => void>();

function notifySubscribers() {
    subscribers.forEach((cb) => cb());
}

/** Force re-fetch of a specific conversation (called from WebSocket handler). */
export function invalidateConversation(conversationId: string) {
    conversationCache.delete(conversationId);
    inflightRequests.delete(conversationId);
    notifySubscribers();
}

/** Clear all cached conversations. Exported for test cleanup only. */
export function clearConversationCache() {
    conversationCache.clear();
    inflightRequests.clear();
}

function fetchConversation(id: string): Promise<ConversationResponse> {
    const existing = inflightRequests.get(id);
    if (existing) {
        return existing;
    }
    const promise = getConversation(id).then((data) => {
        conversationCache.set(id, data);
        inflightRequests.delete(id);
        notifySubscribers();
        return data;
    }).catch((err) => {
        inflightRequests.delete(id);
        throw err;
    });
    inflightRequests.set(id, promise);
    return promise;
}

export function useConversation(conversationId: string | undefined): UseConversationResult {
    const [revision, setRevision] = useState(0);
    const [error, setError] = useState<Error | null>(null);

    // Subscribe to cache changes so re-renders pick up new data
    useEffect(() => {
        const cb = () => setRevision((n) => n + 1);
        subscribers.add(cb);
        return () => {
            subscribers.delete(cb);
        };
    }, []);

    // Fetch when conversationId changes or cache is invalidated and entry is missing.
    // The `revision` dependency ensures the effect re-runs after invalidation clears
    // the cache, since conversationId alone would not change.
    useEffect(() => {
        if (!conversationId) {
            return;
        }
        if (conversationCache.has(conversationId)) {
            return;
        }
        if (inflightRequests.has(conversationId)) {
            return;
        }
        setError(null);
        fetchConversation(conversationId).catch((err) => setError(err));
    }, [conversationId, revision]); // eslint-disable-line react-hooks/exhaustive-deps

    if (!conversationId) {
        return {conversation: null, loading: false, error: null};
    }

    const cached = conversationCache.get(conversationId) ?? null;
    const loading = !cached && !error;

    return {conversation: cached, loading, error};
}

export function useTurnForPost(
    conversation: ConversationResponse | null,
    postId: string,
): Turn | null {
    return useMemo(() => {
        if (!conversation) {
            return null;
        }
        return conversation.turns.find((turn) => turn.post_id === postId) ?? null;
    }, [conversation, postId]);
}
