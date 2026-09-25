// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback} from 'react';

import {renderCustomPrompt, createPost} from '@/client';

export interface RunPromptOptions {

    /** Channel the rendered prompt is posted to, and the render context. */
    channelId: string;

    /** Agent to render against. Omitted in a bot DM, where the bot is implied. */
    botUsername?: string;

    /**
     * Prefix the rendered text with `@botUsername` so the agent replies. Only
     * needed outside the agent's own DM channel.
     */
    mentionBot?: boolean;

    /** Post as a reply when the prompt is run from a thread. */
    rootId?: string;
}

/**
 * Renders a custom prompt against the current context and posts it without a
 * review step. Shared by pinned prompt buttons in the Agents pane and by
 * prompts flagged `run_immediately` in the composer menu, so both surfaces send
 * the same message for the same prompt.
 *
 * Returns the created post so the caller can navigate to the resulting thread.
 */
export function useRunPromptImmediately() {
    return useCallback(async (promptId: string, options: RunPromptOptions) => {
        const {channelId, botUsername, mentionBot, rootId} = options;
        const result = await renderCustomPrompt(promptId, channelId, botUsername);
        const message = mentionBot && botUsername ? `@${botUsername} ${result.rendered}` : result.rendered;

        return createPost({
            channel_id: channelId,
            root_id: rootId ?? '',
            message,
            props: {},
            file_ids: [],
        });
    }, []);
}
