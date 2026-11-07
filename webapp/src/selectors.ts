// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {GlobalState} from '@mattermost/types/store';

import type {LLMBot} from './bots';
import manifest from './manifest';
import {CustomPrompt} from './types';
import {LinkPolicy, PLAIN_TEXT_LINK_POLICY} from './utils/link_allowlist';

// Both start null (unloaded); the selectors below default them to shared empty arrays.
interface PluginState {
    customPrompts: CustomPrompt[] | null;
    pinnedPromptIds: string[] | null;
    showCustomPromptsModal: boolean;
    linkPolicy: LinkPolicy;
    htmlArtifactsEnabled?: boolean;
    generativeUIEnabled?: boolean;
    bots?: LLMBot[] | null;
}

type AppState = GlobalState & {
    [key: `plugins-${string}`]: PluginState;
};

// Stable references so useSelector doesn't re-render on every store update while unloaded.
const EMPTY_CUSTOM_PROMPTS: readonly CustomPrompt[] = Object.freeze([]);
const EMPTY_PINNED_PROMPT_IDS: readonly string[] = Object.freeze([]);

export const getCustomPrompts = (state: AppState): readonly CustomPrompt[] =>
    state[`plugins-${manifest.id}`]?.customPrompts ?? EMPTY_CUSTOM_PROMPTS;

export const getPinnedPromptIds = (state: AppState): readonly string[] =>
    state[`plugins-${manifest.id}`]?.pinnedPromptIds ?? EMPTY_PINNED_PROMPT_IDS;

export const getShowCustomPromptsModal = (state: AppState): boolean =>
    state[`plugins-${manifest.id}`]?.showCustomPromptsModal ?? false;

export const getLinkPolicy = (state: AppState): LinkPolicy =>
    state[`plugins-${manifest.id}`]?.linkPolicy ?? PLAIN_TEXT_LINK_POLICY;

export const getHTMLArtifactsEnabled = (state: AppState): boolean =>
    Boolean(state[`plugins-${manifest.id}`]?.htmlArtifactsEnabled);

/** The server-wide generative UI switch only; see isGenerativeUIEnabledForBot. */
export const getGenerativeUIEnabled = (state: AppState): boolean =>
    Boolean(state[`plugins-${manifest.id}`]?.generativeUIEnabled);

/**
 * Whether the agent with this bot user ID may reply with generative UI, and
 * whether its replies render it: the server-wide switch and the agent's own
 * opt-in, as reported by the server. False for agents the user can't see.
 */
export const isGenerativeUIEnabledForBot = (state: GlobalState, botUserId: string | undefined): boolean => {
    if (!botUserId) {
        return false;
    }
    const bots = (state as AppState)[`plugins-${manifest.id}`]?.bots;
    return Boolean(bots?.find((bot) => bot.id === botUserId)?.generativeUIEnabled);
};
