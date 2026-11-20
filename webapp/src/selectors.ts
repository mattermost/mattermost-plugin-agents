// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {GlobalState} from '@mattermost/types/store';

import manifest from './manifest';
import type {ChannelArtifactLibrary, ChannelLibraryState, ProjectAgentInfo, ProjectsState} from './redux';
import {CustomPrompt} from './types';
import type {Project, ProjectTab} from './types/projects';
import {LinkPolicy, PLAIN_TEXT_LINK_POLICY} from './utils/link_allowlist';

// Both start null (unloaded); the selectors below default them to shared empty arrays.
interface PluginState {
    customPrompts: CustomPrompt[] | null;
    pinnedPromptIds: string[] | null;
    showCustomPromptsModal: boolean;
    linkPolicy: LinkPolicy;
    htmlArtifactsEnabled?: boolean;
    channelArtifactsEnabled?: boolean;
    channelArtifactsLoaded?: boolean;
    channelArtifactsLoadFailed?: boolean;
    channelArtifactLibrary?: ChannelArtifactLibrary;
    scopedSessionsSupported?: boolean | null;
    bots?: Array<{id: string}> | null;
    projectChannelsEnabled?: boolean;
    projectAgents?: ProjectAgentInfo[];
    projects?: ProjectsState;
    projectRHSTab?: ProjectTab;
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

// Whether the prompts and the pins have been fetched at least once (a failed fetch leaves them unloaded).
export const isCustomPromptsLoaded = (state: AppState): boolean =>
    Boolean(state[`plugins-${manifest.id}`]?.customPrompts);

export const isPinnedPromptIdsLoaded = (state: AppState): boolean =>
    Boolean(state[`plugins-${manifest.id}`]?.pinnedPromptIds);

export const getShowCustomPromptsModal = (state: AppState): boolean =>
    state[`plugins-${manifest.id}`]?.showCustomPromptsModal ?? false;

export const getLinkPolicy = (state: AppState): LinkPolicy =>
    state[`plugins-${manifest.id}`]?.linkPolicy ?? PLAIN_TEXT_LINK_POLICY;

export const getHTMLArtifactsEnabled = (state: AppState): boolean =>
    Boolean(state[`plugins-${manifest.id}`]?.htmlArtifactsEnabled);

export const getChannelArtifactsEnabled = (state: AppState): boolean =>
    Boolean(state[`plugins-${manifest.id}`]?.channelArtifactsEnabled);

export const getChannelArtifactsLoaded = (state: AppState): boolean =>
    Boolean(state[`plugins-${manifest.id}`]?.channelArtifactsLoaded);

export const getChannelArtifactsLoadFailed = (state: AppState): boolean =>
    Boolean(state[`plugins-${manifest.id}`]?.channelArtifactsLoadFailed);

export const getChannelLibrary = (state: AppState, channelId: string): ChannelLibraryState | undefined =>
    state[`plugins-${manifest.id}`]?.channelArtifactLibrary?.[channelId];

// Libraries exist only in public and private channels, never in direct or
// group messages. An unknown channel counts as unsupported.
export const channelSupportsArtifacts = (state: GlobalState, channelId: string): boolean => {
    const type = state.entities?.channels?.channels?.[channelId]?.type;
    return type === 'O' || type === 'P';
};

/** True when the library is on and the open channel can hold one. */
export const getChannelArtifactsAvailableHere = (state: AppState): boolean =>
    getChannelArtifactsEnabled(state) && channelSupportsArtifacts(state, state.entities?.channels?.currentChannelId || '');

// Null until /ai_bots has answered.
export const getScopedSessionsSupported = (state: AppState): boolean | null =>
    state[`plugins-${manifest.id}`]?.scopedSessionsSupported ?? null;

export const getProjectChannelsEnabled = (state: AppState): boolean =>
    Boolean(state[`plugins-${manifest.id}`]?.projectChannelsEnabled);

const EMPTY_PROJECT_AGENTS: readonly ProjectAgentInfo[] = Object.freeze([]);

export const getProjectAgents = (state: AppState): readonly ProjectAgentInfo[] =>
    state[`plugins-${manifest.id}`]?.projectAgents ?? EMPTY_PROJECT_AGENTS;

/** A project the viewer can see, or undefined for a non-project, an unknown channel, or when projects are off. */
export const getProject = (state: AppState, channelId: string): Project | undefined => {
    const pluginState = state[`plugins-${manifest.id}`];
    return pluginState?.projectChannelsEnabled ? pluginState.projects?.byChannelId?.[channelId] : undefined; // eslint-disable-line no-undefined
};

export const isKnownNotProject = (state: AppState, channelId: string): boolean =>
    Boolean(state[`plugins-${manifest.id}`]?.projects?.notProject?.[channelId]);

export const getCurrentChannelProject = (state: AppState): Project | undefined =>
    getProject(state, state.entities?.channels?.currentChannelId || '');

export const getProjectRHSTab = (state: AppState): ProjectTab =>
    state[`plugins-${manifest.id}`]?.projectRHSTab ?? 'board';

/** Whether a user is one of the agent bots, hidden project agents included. Only these authors' post props are trusted. */
export const isAgentBotUserId = (state: AppState, userId: string): boolean => {
    const pluginState = state[`plugins-${manifest.id}`];
    return Boolean(pluginState?.bots?.some((bot) => bot.id === userId) || pluginState?.projectAgents?.some((agent) => agent.id === userId));
};
