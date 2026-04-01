// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import manifest from './manifest';
import {CustomPrompt} from './types';

export const getCustomPrompts = (state: any): CustomPrompt[] =>
    state[`plugins-${manifest.id}`]?.customPrompts ?? [];

export const getPinnedPromptIds = (state: any): string[] =>
    state[`plugins-${manifest.id}`]?.pinnedPromptIds ?? [];

export const getShowCustomPromptsModal = (state: any): boolean =>
    state[`plugins-${manifest.id}`]?.showCustomPromptsModal ?? false;

export const getSelectedBotId = (state: any): string | null =>
    state[`plugins-${manifest.id}`]?.selectedBotId ?? null;
