// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useMemo, useRef, useState} from 'react';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {doUIAction} from '@/client';
import {invalidateConversation} from '@/hooks/use_conversation';
import {isValidId} from '@/utils/ids';
import {
    BlockTypeToolUse,
    ConversationResponse,
    StatusPending,
} from '@/types/conversation';

import {GenerativeUIActions, PostInteraction, Submission, SubmissionRecord} from './actions';

export const GENERATIVE_UI_SUBMISSION_PROP = 'ai_genui_submission';

interface Options {
    post: {id: string; root_id?: string};
    conversation: ConversationResponse | null;
    currentUserId: string;
    generating: boolean;
}

const asSubmissionRecord = (value: unknown): SubmissionRecord | null => {
    if (!value || typeof value !== 'object') {
        return null;
    }
    const record = value as Partial<SubmissionRecord>;
    if (typeof record.source_post_id !== 'string' || typeof record.block !== 'number' || typeof record.label !== 'string') {
        return null;
    }
    return {
        source_post_id: record.source_post_id,
        block: record.block,
        form: typeof record.form === 'string' ? record.form : '',
        label: record.label,
        fields: Array.isArray(record.fields) ? record.fields : [],
    };
};

/**
 * Whether the reply is the conversation's latest turn and finished, which is
 * the server's condition for accepting a submission.
 */
const isLatestFinishedReply = (conversation: ConversationResponse, postId: string) => {
    const turns = [...conversation.turns].sort((a, b) => a.sequence - b.sequence);
    const last = turns[turns.length - 1];
    if (!last || last.role !== 'assistant' || last.post_id !== postId) {
        return false;
    }
    return !last.content.some((block) => block.type === BlockTypeToolUse && block.status === StatusPending);
};

/**
 * Finds the submission made from this reply: a post the plugin created for
 * the requester in the same thread, marked with the submission prop. Regular
 * clients cannot set from_plugin, so the prop cannot be forged by hand.
 */
const selectSubmissionProp = (state: GlobalState, postId: string, rootId: string, requesterId: string | undefined): unknown => {
    if (!requesterId) {
        return null;
    }
    const posts = state.entities.posts.posts;
    const threadIds = state.entities.posts.postsInThread?.[rootId] ?? [];
    for (const id of threadIds) {
        const candidate = posts[id];
        if (!candidate || candidate.user_id !== requesterId || !candidate.props?.from_plugin) {
            continue;
        }
        const prop = candidate.props[GENERATIVE_UI_SUBMISSION_PROP];
        if (asSubmissionRecord(prop)?.source_post_id === postId) {
            return prop;
        }
    }
    return null;
};

const newIdempotencyKey = () => (globalThis.crypto?.randomUUID?.() ?? `${Date.now()}-${Math.random().toString(36).slice(2, 12)}`);

/** The generative UI actions of one agent reply: who may submit, and the submit call. */
export const useGenerativeUIPostActions = ({post, conversation, currentUserId, generating}: Options): GenerativeUIActions => {
    const requesterId = conversation?.user_id;
    const rootId = post.root_id || post.id;
    const threadSubmissionProp = useSelector((state: GlobalState) => selectSubmissionProp(state, post.id, rootId, requesterId));
    const threadSubmission = useMemo(() => asSubmissionRecord(threadSubmissionProp), [threadSubmissionProp]);
    const requesterUsername = useSelector((state: GlobalState) => (requesterId ? state.entities.users.profiles?.[requesterId]?.username : undefined)); // eslint-disable-line no-undefined
    const [localSubmission, setLocalSubmission] = useState<SubmissionRecord | null>(null);
    const submission = threadSubmission ?? localSubmission;

    const interaction = useMemo<PostInteraction>(() => {
        if (submission) {
            return {kind: 'submitted', submission};
        }
        if (!conversation || generating) {
            return {kind: 'readonly'};
        }
        if (conversation.user_id !== currentUserId) {
            return {kind: 'onlooker', requesterUsername};
        }
        if (!isLatestFinishedReply(conversation, post.id)) {
            return {kind: 'stale'};
        }
        return {kind: 'editable'};
    }, [submission, conversation, generating, currentUserId, requesterUsername, post.id]);

    const conversationId = conversation?.id;
    const pendingKey = useRef<{payload: string; key: string} | null>(null);
    const submit = useCallback(async (next: Submission) => {
        // A retry of the same payload reuses its key, so a click the server
        // accepted before the response was lost is answered as already done.
        const payload = JSON.stringify(next);
        if (pendingKey.current?.payload !== payload) {
            pendingKey.current = {payload, key: newIdempotencyKey()};
        }
        await doUIAction(post.id, {...next, idempotency_key: pendingKey.current.key});
        pendingKey.current = null;
        setLocalSubmission({...next, source_post_id: post.id});
        if (conversationId && isValidId(conversationId)) {
            invalidateConversation(conversationId);
        }
    }, [post.id, conversationId]);

    return useMemo(() => ({interaction, submit}), [interaction, submit]);
};
