// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useSyncExternalStore} from 'react';

import {getChannelInstructions} from '@/client';
import {createRequestGenerations} from '@/request_generation';

// Save failures are stored as machine-readable kinds; the instructions setting
// maps them to localized messages inside React.
export type ChannelInstructionsSaveErrorKind = 'forbidden' | 'invalid' | 'generic';

// Module-level draft bridging the host's schema callbacks and the custom
// instructions setting and pinned-posts section, which receive no channel
// from the host. Same single-modal assumption as the auto-reply draft.
export type ChannelInstructionsDraft = {
    channelId: string;

    // Latest known server value; remote changes update it while the modal is open.
    saved: string;
    saveError: ChannelInstructionsSaveErrorKind | null;
};

let draft: ChannelInstructionsDraft | null = null;
const generations = createRequestGenerations();
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

export function getChannelInstructionsDraft(): ChannelInstructionsDraft | null {
    return draft;
}

// Any change of the draft supersedes remote refreshes still in flight: they
// read the server before it.
export function setChannelInstructionsDraft(next: ChannelInstructionsDraft | null): void {
    generations.invalidateAll();
    draft = next;
    notifySubscribers();
}

export function setChannelInstructionsSaveError(kind: ChannelInstructionsSaveErrorKind | null): void {
    if (!draft) {
        return;
    }
    draft = {...draft, saveError: kind};
    notifySubscribers();
}

export function subscribeChannelInstructionsDraft(cb: () => void): () => void {
    subscribers.add(cb);
    return () => {
        subscribers.delete(cb);
    };
}

export function useChannelInstructionsDraft(): ChannelInstructionsDraft | null {
    return useSyncExternalStore(subscribeChannelInstructionsDraft, getChannelInstructionsDraft);
}

/**
 * Remote change while a modal may be open: if the event targets the hydrated
 * channel, re-fetch (the payload carries only channel_id) and update the
 * draft. Errors are swallowed — the draft simply stays as-is.
 */
export async function handleChannelInstructionsUpdated(event: {channel_id?: string}): Promise<void> {
    const hydrated = draft;
    if (!hydrated || !event.channel_id || event.channel_id !== hydrated.channelId) {
        return;
    }
    const token = generations.begin(hydrated.channelId);
    try {
        const {instructions} = await getChannelInstructions(hydrated.channelId);

        // The draft may have been re-hydrated or refreshed again while the
        // GET was in flight; never clobber newer state with this response.
        if (!generations.isCurrent(hydrated.channelId, token)) {
            return;
        }
        setChannelInstructionsDraft({channelId: hydrated.channelId, saved: instructions, saveError: null});
    } catch {
        // Best effort: keep the draft as-is when the re-fetch fails.
    }
}
