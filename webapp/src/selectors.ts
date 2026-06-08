// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {GlobalState} from '@mattermost/types/store';

import manifest from './manifest';
import {CustomPrompt} from './types';

// Both start null (unloaded); the selectors below default them with ?? [].
interface PluginState {
    customPrompts: CustomPrompt[] | null;
    pinnedPromptIds: string[] | null;
    showCustomPromptsModal: boolean;
    selectedBotId: string | null;
}

type AppState = GlobalState & {
    [key: `plugins-${string}`]: PluginState;
};

export const getCustomPrompts = (state: AppState): CustomPrompt[] =>
    state[`plugins-${manifest.id}`]?.customPrompts ?? [];

export const getPinnedPromptIds = (state: AppState): string[] =>
    state[`plugins-${manifest.id}`]?.pinnedPromptIds ?? [];

export const getShowCustomPromptsModal = (state: AppState): boolean =>
    state[`plugins-${manifest.id}`]?.showCustomPromptsModal ?? false;

export const getSelectedBotId = (state: AppState): string | null =>
    state[`plugins-${manifest.id}`]?.selectedBotId ?? null;
