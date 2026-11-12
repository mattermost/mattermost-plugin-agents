// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type PostedPost = {
    id: string;
    root_id?: string;
    update_at?: number;
};

type PostedListener = (post: PostedPost) => void;

const listeners = new Set<PostedListener>();

export function subscribeToPosted(listener: PostedListener): () => void {
    listeners.add(listener);
    return () => {
        listeners.delete(listener);
    };
}

// Handles the core `posted` event, which the server also sends when it
// delivers the notifications of an agent post it already broadcast.
export function handlePostedWebsocket(msg: {data?: {post?: unknown}}) {
    if (listeners.size === 0 || typeof msg?.data?.post !== 'string') {
        return;
    }

    let parsed: unknown;
    try {
        parsed = JSON.parse(msg.data.post);
    } catch {
        return;
    }
    if (!parsed || typeof parsed !== 'object' || typeof (parsed as PostedPost).id !== 'string') {
        return;
    }

    const post = parsed as PostedPost;
    listeners.forEach((listener) => listener(post));
}
