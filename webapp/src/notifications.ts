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

/**
 * Decide whether a webapp desktop notification for a post originating from an
 * AI agent (or a generic bot reply) should be suppressed.
 *
 * Returns true when the notification should be silenced. The caller is
 * responsible for forwarding the underlying `args` and toggling `notify` based
 * on this decision.
 *
 * Suppression rules:
 *   - Any post of type `custom_llmbot` is always suppressed. This type is set
 *     exclusively on AI agent responses, which are always a reaction to the
 *     receiving user's own action (mention, DM, or RHS action such as
 *     "Summarize Thread"). See MM-66720.
 *   - A threaded reply marked `from_bot=true` is suppressed when it lands
 *     within {@link LLM_BOT_REPLY_DEBOUNCE_TIMEOUT_MS} of the current user's
 *     own root post. This is the "fast bot reply" heuristic for non-agent bots.
 */
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
