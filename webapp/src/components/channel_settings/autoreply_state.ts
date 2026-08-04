// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSyncExternalStore} from 'react';

import {ChannelAutoReplyMode, ChannelAutoReplySettings, getChannelAutoReply} from '@/client';
import {LLMBot, filterBotsByChannelAccess, resolveActiveBot} from '@/bots';

// Save failures are stored as machine-readable kinds; the picker maps them to
// localized messages inside React so they follow the live locale.
export type ChannelAutoReplySaveErrorKind = 'forbidden' | 'no_agent' | 'generic';

// Module-level draft bridging the host's schema callbacks (loadValues/onSave)
// and the custom agent-picker setting, which receives nothing but informChange
// from the host. Safe because only one Channel Settings modal exists at a time
// and loadValues resolves before the picker mounts (except the
// loadValues-failure path, which the picker tolerates by rendering a
// load-failure message).
export type ChannelAutoReplyDraft = {
    channelId: string;

    // Normalized values as hydrated / last saved.
    saved: ChannelAutoReplySettings;
    saveError: ChannelAutoReplySaveErrorKind | null;
};

let draft: ChannelAutoReplyDraft | null = null;
const subscribers = new Set<() => void>();

function notifySubscribers() {
    subscribers.forEach((cb) => {
        try {
            cb();
        } catch {
            // Subscriber errors must not block other listeners.
        }
    });
}

export function getChannelAutoReplyDraft(): ChannelAutoReplyDraft | null {
    return draft;
}

export function setChannelAutoReplyDraft(next: ChannelAutoReplyDraft | null): void {
    draft = next;
    notifySubscribers();
}

export function setChannelAutoReplySaveError(kind: ChannelAutoReplySaveErrorKind | null): void {
    if (!draft) {
        return;
    }
    draft = {...draft, saveError: kind};
    notifySubscribers();
}

export function subscribeChannelAutoReplyDraft(cb: () => void): () => void {
    subscribers.add(cb);
    return () => {
        subscribers.delete(cb);
    };
}

// React 18 hook for the picker.
export function useChannelAutoReplyDraft(): ChannelAutoReplyDraft | null {
    return useSyncExternalStore(subscribeChannelAutoReplyDraft, getChannelAutoReplyDraft);
}

/**
 * Validates untrusted GET/websocket data and resolves the effective agent:
 * unknown modes collapse to 'off', and the saved bot id falls back to the
 * system-default agent, then the first agent available in the channel, so the
 * picker can display a selection without ever calling informChange on mount.
 */
export function normalizeChannelAutoReply(raw: ChannelAutoReplySettings, bots: LLMBot[], channelId: string): ChannelAutoReplySettings {
    const mode: ChannelAutoReplyMode = raw.mode === 'root_posts' || raw.mode === 'threads' ? raw.mode : 'off';
    const resolved = resolveActiveBot(filterBotsByChannelAccess(bots, channelId), raw.bot_id ?? '');
    return {mode, bot_id: resolved?.id ?? ''};
}

// Websocket payload for custom_mattermost-ai_channel_autoreply_updated; only
// channel_id is trusted — the setting is re-fetched rather than consumed from
// the payload, keeping the phases decoupled.
export type ChannelAutoReplyUpdatedEvent = {channel_id?: string};

/**
 * Remote change while a modal may be open: if the event targets the hydrated
 * channel, re-fetch (never trust the payload beyond channel_id), re-normalize
 * against the current bots cache, and update the draft. Errors are swallowed —
 * the draft simply stays as-is. Fire-and-forget.
 */
export async function handleChannelAutoReplyUpdated(getBots: () => LLMBot[], event: ChannelAutoReplyUpdatedEvent): Promise<void> {
    const hydrated = draft;
    if (!hydrated || !event.channel_id || event.channel_id !== hydrated.channelId) {
        return;
    }
    try {
        const raw = await getChannelAutoReply(hydrated.channelId);

        // The modal may have re-hydrated for another channel while the GET was
        // in flight; never clobber the newer draft.
        if (draft?.channelId !== hydrated.channelId) {
            return;
        }
        const saved = normalizeChannelAutoReply(raw, getBots(), hydrated.channelId);

        // A remote change invalidates any stale save error.
        setChannelAutoReplyDraft({channelId: hydrated.channelId, saved, saveError: null});
    } catch {
        // Best effort: keep the draft as-is when the re-fetch fails.
    }
}
