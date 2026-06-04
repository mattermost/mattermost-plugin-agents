// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export const LLM_BOT_REPLY_DEBOUNCE_TIMEOUT_MS = 1000;

type NotificationPost = {
    user_id?: string;
    root_id?: string;
    type?: string;
    create_at?: number;
    props?: Record<string, unknown> | null;
};

// shouldSuppressBotNotification reports whether a desktop notification is redundant.
export function shouldSuppressBotNotification(
    post: NotificationPost | undefined | null,
    context: {
        currentUserId?: string;
        parentPost?: {user_id?: string; create_at?: number} | null;
        now: number;
    },
): boolean {
    if (!post || !post.user_id) {
        return false;
    }

    if (post.type === 'custom_llmbot') {
        return true;
    }

    if (!post.root_id || post.props?.from_bot !== 'true') {
        return false;
    }

    if (!context.parentPost) {
        return false;
    }

    const timeSinceParentPost = context.now - (context.parentPost.create_at ?? 0);
    return (
        context.parentPost.user_id === context.currentUserId &&
        timeSinceParentPost < LLM_BOT_REPLY_DEBOUNCE_TIMEOUT_MS
    );
}
