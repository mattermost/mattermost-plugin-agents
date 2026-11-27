// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {DictationSessionEndReason} from './types';

const SESSION_END_REASONS: readonly DictationSessionEndReason[] = ['duration_limit', 'policy', 'shutdown'];

type Listener = (reason: DictationSessionEndReason) => void;

const listeners = new Map<string, Listener>();

// onDictationSessionEnded calls listener when the server reports that it
// ended sessionId, and returns the unsubscribe function.
export function onDictationSessionEnded(sessionId: string, listener: Listener): () => void {
    listeners.set(sessionId, listener);
    return () => {
        if (listeners.get(sessionId) === listener) {
            listeners.delete(sessionId);
        }
    };
}

// notifyDictationSessionEnded handles the custom_mattermost-ai_dictation_session_ended
// websocket event. The payload is untrusted: unknown sessions and reasons are ignored.
export function notifyDictationSessionEnded(sessionId: unknown, reason: unknown) {
    if (typeof sessionId !== 'string' || typeof reason !== 'string') {
        return;
    }
    const known = SESSION_END_REASONS.find((r) => r === reason);
    if (known) {
        listeners.get(sessionId)?.(known);
    }
}
