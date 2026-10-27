// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';

import {getAgentsBridgeStatus, rewriteMessage, type RewriteAction} from '@/client';

export type RewriteAvailability = 'checking' | 'available' | 'unavailable' | 'error';

// Whether the core server can run AI Rewrite through this plugin, as the host
// checks before showing it. Checked once, when `enabled` first turns true.
export function useRewriteAvailability(enabled: boolean): RewriteAvailability {
    const [availability, setAvailability] = useState<RewriteAvailability>('checking');
    useEffect(() => {
        if (!enabled) {
            return () => null;
        }
        let current = true;
        setAvailability('checking');
        getAgentsBridgeStatus().then((status) => {
            if (current) {
                setAvailability(status.available ? 'available' : 'unavailable');
            }
        }).catch((e) => {
            console.error('Failed to check AI rewrite availability:', e); // eslint-disable-line no-console
            if (current) {
                setAvailability('error');
            }
        });
        return () => {
            current = false;
        };
    }, [enabled]);
    return availability;
}

interface RewriteArgs {
    agentId: string;
    rootId: string;
    getText: () => string;
    setText: (text: string) => void;

    // Called with the server's message (may be empty: the caller supplies a fallback).
    onError: (serverMessage: string) => void;
}

export interface ComposerRewrite {
    processing: boolean;

    // The text before the last successful rewrite, while that rewrite is
    // still what the composer holds (Discard puts it back).
    canDiscard: boolean;
    rewrite: (action: RewriteAction, customPrompt?: string) => void;
    stop: () => void;
    discard: () => void;
    regenerate: () => void;
}

function isAbort(e: unknown): boolean {
    return (e as {name?: string} | null)?.name === 'AbortError';
}

// The host composer's useRewrite, against the plugin composer's text through
// callbacks: one request at a time (a new one aborts the last), a superseded
// or aborted response never writes, Stop cancels, Discard restores the
// original text and Regenerate reruns the last action on it.
export function useComposerRewrite({agentId, rootId, getText, setText, onError}: RewriteArgs): ComposerRewrite {
    const [processing, setProcessing] = useState(false);
    const controller = useRef<AbortController | null>(null);
    const latest = useRef(0);

    // The original text and the rewrite that replaced it, for Discard/Regenerate.
    const [undo, setUndo] = useState<{original: string; rewritten: string; action: RewriteAction; customPrompt?: string} | null>(null);

    useEffect(() => () => {
        latest.current++;
        controller.current?.abort();
    }, []);

    const run = useCallback(async (text: string, action: RewriteAction, customPrompt?: string) => {
        controller.current?.abort();
        const abort = new AbortController();
        controller.current = abort;
        const id = ++latest.current;
        setProcessing(true);
        try {
            const rewritten = await rewriteMessage(agentId, text, action, {customPrompt, rootId, signal: abort.signal});
            if (id !== latest.current) {
                return;
            }
            setText(rewritten);
            setUndo({original: text, rewritten, action, customPrompt});
        } catch (e) {
            if (id !== latest.current || isAbort(e)) {
                return;
            }
            console.error('Failed to rewrite message:', e); // eslint-disable-line no-console
            setUndo(null);
            onError((e as {message?: string})?.message ?? '');
        } finally {
            if (id === latest.current) {
                setProcessing(false);
                controller.current = null;
            }
        }
    }, [agentId, rootId, setText, onError]);

    const rewrite = useCallback((action: RewriteAction, customPrompt?: string) => {
        const text = getText();

        // Only a custom instruction can write a message from nothing.
        if (!text.trim() && action !== 'custom') {
            return;
        }
        run(text, action, customPrompt);
    }, [getText, run]);

    const stop = useCallback(() => {
        latest.current++;
        controller.current?.abort();
        controller.current = null;
        setProcessing(false);
    }, []);

    const canDiscard = Boolean(undo) && !processing && getText() === undo?.rewritten;

    const discard = useCallback(() => {
        if (undo) {
            setText(undo.original);
            setUndo(null);
        }
    }, [undo, setText]);

    const regenerate = useCallback(() => {
        if (undo) {
            setText(undo.original);
            run(undo.original, undo.action, undo.customPrompt);
        }
    }, [undo, setText, run]);

    return {processing, canDiscard, rewrite, stop, discard, regenerate};
}
