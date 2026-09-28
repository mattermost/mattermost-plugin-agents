// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {GlobalState} from '@mattermost/types/store';

import manifest from './manifest';
import {CustomPrompt} from './types';

// Both start null (unloaded); the selectors below default them to shared empty arrays.
interface PluginState {
    customPrompts: CustomPrompt[] | null;
    pinnedPromptIds: string[] | null;
    showCustomPromptsModal: boolean;
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
