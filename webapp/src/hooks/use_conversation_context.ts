// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useState} from 'react';

import {getConversationContext} from '@/client';
import type {Composition} from '@/types/conversation';

import {onConversationInvalidated} from './use_conversation';

export interface UseConversationContextResult {
    composition: Composition | null;
    loading: boolean;
    error: Error | null;
}

// Module-level cache, mirroring use_conversation.ts. Per-conversation context
// is small (one Composition per id) and changes only when the conversation
// itself does, so the same invalidate-on-WS pattern works.
const compositionCache = new Map<string, Composition>();
const errorCache = new Map<string, Error>();
const inflightRequests = new Map<string, Promise<Composition>>();
const subscribers = new Set<() => void>();

function notifySubscribers() {
    subscribers.forEach((cb) => cb());
}

/** Force re-fetch of a specific conversation context (called from WebSocket handler). */
export function invalidateConversationContext(conversationId: string) {
    compositionCache.delete(conversationId);
    errorCache.delete(conversationId);
    inflightRequests.delete(conversationId);
    notifySubscribers();
}

/** Clear all cached compositions. Exported for test cleanup only. */
export function clearConversationContextCache() {
    compositionCache.clear();
    errorCache.clear();
    inflightRequests.clear();
}

// Refresh the composition whenever the underlying conversation does, so the
// indicator follows new turns and tool approvals.
onConversationInvalidated(invalidateConversationContext);

function fetchContext(id: string): Promise<Composition> {
    const existing = inflightRequests.get(id);
    if (existing) {
        return existing;
    }

    // Identity-check the inflight promise so a fetch evicted mid-flight by
    // invalidateConversationContext can't overwrite the newer fetch's result.
    const settle = (data: Composition) => {
        if (inflightRequests.get(id) !== promise) {
            return data;
        }
        compositionCache.set(id, data);
        errorCache.delete(id);
        inflightRequests.delete(id);
        notifySubscribers();
        return data;
    };
    const fail = (err: Error): never => {
        if (inflightRequests.get(id) !== promise) {
            throw err;
        }
        errorCache.set(id, err);
        inflightRequests.delete(id);
        notifySubscribers();
        throw err;
    };
    const promise: Promise<Composition> = getConversationContext(id).then(settle, fail);
    inflightRequests.set(id, promise);
    return promise;
}

export function useConversationContext(conversationId: string | undefined): UseConversationContextResult {
    const [revision, setRevision] = useState(0);
    const [error, setError] = useState<Error | null>(null);

    useEffect(() => {
        const cb = () => setRevision((n) => n + 1);
        subscribers.add(cb);
        return () => {
            subscribers.delete(cb);
        };
    }, []);

    useEffect(() => {
        if (!conversationId) {
            return;
        }
        if (compositionCache.has(conversationId)) {
            return;
        }
        const cachedError = errorCache.get(conversationId);
        if (cachedError) {
            setError(cachedError);
            return;
        }
        if (inflightRequests.has(conversationId)) {
            return;
        }
        setError(null);
        fetchContext(conversationId).catch((err) => setError(err));
    }, [conversationId, revision]); // eslint-disable-line react-hooks/exhaustive-deps

    if (!conversationId) {
        return {composition: null, loading: false, error: null};
    }

    const cached = compositionCache.get(conversationId) ?? null;
    const effectiveError = error ?? errorCache.get(conversationId) ?? null;
    const loading = !cached && !effectiveError;

    return {composition: cached, loading, error: effectiveError};
}
