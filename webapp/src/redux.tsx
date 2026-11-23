// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combineReducers, Dispatch, Store, UnknownAction} from 'redux';
import {GlobalState} from '@mattermost/types/store';

import {makeCallsPostButtonClickedHandler} from './calls_button';
import {ChannelArtifactEntry, getCustomPrompts as fetchCustomPromptsAPI, getCustomPromptPins} from './client';
import {licenseAllows} from './license';
import manifest from './manifest';
import {CustomPrompt} from './types';
import type {AIThread} from './types/ai_thread';
import type {Project, ProjectPaneMode, ProjectPaneState, ProjectTab} from './types/projects';
import {LinkPolicy, PLAIN_TEXT_LINK_POLICY} from './utils/link_allowlist';

type WebappStore = Store<GlobalState, UnknownAction>

const CallsClickHandler = 'calls_post_button_clicked_handler';
export const BotsHandler = manifest.id + '_bots';
export const CustomPromptsHandler = 'SET_CUSTOM_PROMPTS';
export const PinnedPromptIdsHandler = 'SET_PINNED_PROMPT_IDS';
export const ShowCustomPromptsModalHandler = 'SHOW_CUSTOM_PROMPTS_MODAL';
export const SetLinkPolicyHandler = 'SET_LINK_POLICY';

export async function setupRedux(registry: any, store: WebappStore) {
    registry.registerReducer(pluginReducer);

    if (licenseAllows(store.getState(), 'meetings')) {
        store.dispatch({
            type: CallsClickHandler as any,
            handler: makeCallsPostButtonClickedHandler(store.dispatch, store.getState),
        });
    }

    // This is a workaround for a bug where the RHS was inaccessible to
    // users that where not system admins. This is unable to be fixed properly
    // because the Webapp does not export the AdvancedCreateComment directly.
    // #120 filed to remove this workaround.
    store.dispatch({
        type: 'RECEIVED_MY_CHANNEL_MEMBER' as any,
        data: {
            channel_id: undefined, // eslint-disable-line no-undefined
            roles: 'special_workaround',
        },
    });
    store.dispatch({
        type: 'RECEIVED_ROLE' as any,
        data: {
            name: 'special_workaround',
            permissions: ['create_post'],
        },
    });
}

function callsPostButtonClickedTranscription(state = false, action: any) {
    switch (action.type) {
    case CallsClickHandler:
        return action.handler || false;
    default:
        return state;
    }
}

function bots(state = null, action: any) {
    switch (action.type) {
    case BotsHandler:
        return action.bots;
    default:
        return state;
    }
}

function linkPolicy(state: LinkPolicy = PLAIN_TEXT_LINK_POLICY, action: any) {
    switch (action.type) {
    case SetLinkPolicyHandler:
        return action.linkPolicy ?? PLAIN_TEXT_LINK_POLICY;
    default:
        return state;
    }
}

function htmlArtifactsEnabled(state = false, action: any) {
    switch (action.type) {
    case 'SET_HTML_ARTIFACTS_ENABLED':
        return action.htmlArtifactsEnabled;
    default:
        return state;
    }
}

function channelArtifactsEnabled(state = false, action: any) {
    switch (action.type) {
    case 'SET_CHANNEL_ARTIFACTS_ENABLED':
        return action.channelArtifactsEnabled;
    default:
        return state;
    }
}

// A change that arrived while the list was loading; it is replayed over the
// response so a save or websocket event during the fetch is not lost.
type PendingChange = {upsert: ChannelArtifactEntry} | {remove: string};

export type ChannelLibraryState = {
    status: 'loading' | 'ready' | 'error';
    pending?: PendingChange[];
    entries: ChannelArtifactEntry[];
    canAdd: boolean;
    canEdit: boolean;
    canDelete: boolean;
    maxPerChannel: number;
};

export type ChannelArtifactLibrary = Record<string, ChannelLibraryState>;

function upsertEntry(entries: ChannelArtifactEntry[], entry: ChannelArtifactEntry): ChannelArtifactEntry[] {
    return entries.some((e) => e.id === entry.id) ? entries.map((e) => (e.id === entry.id ? entry : e)) : [entry, ...entries];
}

const emptyLibrary: ChannelLibraryState = {status: 'loading', entries: [], canAdd: false, canEdit: false, canDelete: false, maxPerChannel: 0};

// One cached library per channel, shared by every card, panel and badge.
// Websocket events and local changes keep it current; a failed load is kept so
// the next consumer that asks retries it.
function channelArtifactLibrary(state: ChannelArtifactLibrary = {}, action: any): ChannelArtifactLibrary {
    switch (action.type) {
    case 'CHANNEL_ARTIFACTS_LOADING': {
        const current = state[action.channelId];
        return {...state, [action.channelId]: {...(current || emptyLibrary), status: 'loading', pending: []}};
    }
    case 'CHANNEL_ARTIFACTS_LOADED': {
        let entries: ChannelArtifactEntry[] = action.list.entries || [];
        for (const change of state[action.channelId]?.pending ?? []) {
            entries = 'upsert' in change ? upsertEntry(entries, change.upsert) : entries.filter((entry) => entry.id !== change.remove);
        }
        return {
            ...state,
            [action.channelId]: {
                status: 'ready',
                entries,
                canAdd: Boolean(action.list.can_add),
                canEdit: Boolean(action.list.can_edit),
                canDelete: Boolean(action.list.can_delete),
                maxPerChannel: action.list.max_per_channel || 0,
            },
        };
    }
    case 'CHANNEL_ARTIFACTS_FAILED': {
        const current = state[action.channelId] || emptyLibrary;
        return {...state, [action.channelId]: {...current, status: 'error', pending: []}};
    }
    case 'CHANNEL_ARTIFACT_UPSERT': {
        const current = state[action.entry.channel_id];
        if (!current || current.status === 'error') {
            return state;
        }
        const pending = current.status === 'loading' ? [...(current.pending ?? []), {upsert: action.entry}] : current.pending;
        return {...state, [action.entry.channel_id]: {...current, pending, entries: upsertEntry(current.entries, action.entry)}};
    }
    case 'CHANNEL_ARTIFACT_REMOVE': {
        const current = state[action.channelId];
        if (!current) {
            return state;
        }
        const pending = current.status === 'loading' ? [...(current.pending ?? []), {remove: action.id as string}] : current.pending;
        return {...state, [action.channelId]: {...current, pending, entries: current.entries.filter((entry) => entry.id !== action.id)}};
    }
    case 'SET_CHANNEL_ARTIFACTS_ENABLED':
        return action.channelArtifactsEnabled ? state : {};
    default:
        return state;
    }
}

function channelArtifactsLoaded(state = false, action: any) {
    switch (action.type) {
    case 'SET_CHANNEL_ARTIFACTS_ENABLED':
        return true;
    default:
        return state;
    }
}

function channelArtifactsLoadFailed(state = false, action: any) {
    switch (action.type) {
    case 'SET_CHANNEL_ARTIFACTS_ENABLED':
        return false;
    case 'SET_CHANNEL_ARTIFACTS_LOAD_FAILED':
        return true;
    default:
        return state;
    }
}

function scopedSessionsSupported(state: boolean | null = null, action: any) {
    switch (action.type) {
    case 'SET_SCOPED_SESSIONS_SUPPORTED':
        return action.scopedSessionsSupported;
    default:
        return state;
    }
}

export const ProjectChannelsEnabledSet = 'SET_PROJECT_CHANNELS_ENABLED';
export const ProjectAgentsSet = 'SET_PROJECT_AGENTS';
export const ProjectsReceived = 'PROJECTS_RECEIVED';
export const ProjectReceived = 'PROJECT_RECEIVED';
export const ProjectMissing = 'PROJECT_MISSING';
export const ProjectPaneOpened = 'PROJECT_PANE_OPENED';
export const ProjectPaneClosed = 'PROJECT_PANE_CLOSED';
export const ProjectPaneTabSet = 'PROJECT_PANE_TAB_SET';
export const ProjectPaneModeSet = 'PROJECT_PANE_MODE_SET';
export const ProjectPaneRestored = 'PROJECT_PANE_RESTORED';

function projectChannelsEnabled(state = false, action: any) {
    switch (action.type) {
    case ProjectChannelsEnabledSet:
        return Boolean(action.projectChannelsEnabled);
    default:
        return state;
    }
}

// Hidden project agents: kept apart from `bots` so the pickers never list them, while posts they
// author are still recognized as agent posts.
function projectAgents(state: ProjectAgentInfo[] = [], action: any): ProjectAgentInfo[] {
    switch (action.type) {
    case ProjectAgentsSet:
        return Array.isArray(action.projectAgents) ? action.projectAgents : [];
    case ProjectChannelsEnabledSet:
        return action.projectChannelsEnabled ? state : [];
    default:
        return state;
    }
}

export type ProjectAgentInfo = {id: string; displayName?: string; username?: string; dmChannelID?: string};

export type ProjectsState = {
    byChannelId: Record<string, Project>;

    // Channels the server said are not projects, so they are not asked about again.
    notProject: Record<string, true>;
    loadedTeams: Record<string, true>;
};

export const initialProjectsState: ProjectsState = {byChannelId: {}, notProject: {}, loadedTeams: {}};

// A response older than the stored copy (a late fetch overtaken by a websocket event) is dropped.
function mergeProject(byChannelId: Record<string, Project>, project: Project): Record<string, Project> {
    const current = byChannelId[project.channel_id];
    if (current && current.update_at > project.update_at) {
        return byChannelId;
    }
    return {...byChannelId, [project.channel_id]: project};
}

export function projects(state: ProjectsState = initialProjectsState, action: any): ProjectsState {
    switch (action.type) {
    case ProjectsReceived: {
        const received: Project[] = action.projects ?? [];
        const receivedIds = new Set(received.map((p) => p.channel_id));
        let byChannelId: Record<string, Project> = {};
        for (const [id, project] of Object.entries(state.byChannelId)) {
            if (project.team_id !== action.teamId || receivedIds.has(id)) {
                byChannelId[id] = project;
            }
        }
        for (const project of received) {
            byChannelId = mergeProject(byChannelId, project);
        }
        const notProject = {...state.notProject};
        for (const id of receivedIds) {
            delete notProject[id];
        }
        return {byChannelId, notProject, loadedTeams: {...state.loadedTeams, [action.teamId]: true}};
    }
    case ProjectReceived: {
        const notProject = {...state.notProject};
        delete notProject[action.project.channel_id];
        return {...state, byChannelId: mergeProject(state.byChannelId, action.project), notProject};
    }
    case ProjectMissing:
        if (state.byChannelId[action.channelId]) {
            return state;
        }
        return {...state, notProject: {...state.notProject, [action.channelId]: true}};
    case ProjectChannelsEnabledSet:
        return action.projectChannelsEnabled ? state : initialProjectsState;
    default:
        return state;
    }
}

const PANE_TABS: readonly ProjectTab[] = ['setup', 'board', 'checklist', 'artifacts'];
const PANE_MODES: readonly ProjectPaneMode[] = ['split', 'pane', 'fullscreen'];

function onlyKnown<T extends string>(values: unknown, known: readonly T[]): Record<string, T> {
    const out: Record<string, T> = {};
    if (values && typeof values === 'object') {
        for (const [key, value] of Object.entries(values)) {
            if (known.includes(value as T)) {
                out[key] = value as T;
            }
        }
    }
    return out;
}

// Which project channels have their pane open, on which tab, and how wide the pane is laid out.
// The project pane host saves it per user, so a reload reopens what was open.
export function projectPane(state: ProjectPaneState = {open: {}, tab: {}, mode: 'split'}, action: any): ProjectPaneState {
    switch (action.type) {
    case ProjectPaneOpened: {
        const tab = PANE_TABS.includes(action.tab) ? {...state.tab, [action.channelId]: action.tab} : state.tab;
        return {...state, open: {...state.open, [action.channelId]: true}, tab};
    }
    case ProjectPaneClosed: {
        const open = {...state.open};
        delete open[action.channelId];
        return {...state, open};
    }
    case ProjectPaneTabSet:
        return PANE_TABS.includes(action.tab) ? {...state, tab: {...state.tab, [action.channelId]: action.tab}} : state;
    case ProjectPaneModeSet:
        return PANE_MODES.includes(action.mode) ? {...state, mode: action.mode} : state;
    case ProjectPaneRestored: {
        const restored = action.state ?? {};
        const open: Record<string, boolean> = {};
        if (restored.open && typeof restored.open === 'object') {
            for (const [key, value] of Object.entries(restored.open)) {
                if (value === true) {
                    open[key] = true;
                }
            }
        }
        return {
            open: {...open, ...state.open},
            tab: {...onlyKnown(restored.tab, PANE_TABS), ...state.tab},
            mode: PANE_MODES.includes(restored.mode) ? restored.mode : state.mode,
        };
    }
    default:
        return state;
    }
}

// The RHS's list view when no thread is open: the new-chat tab or the chat history. It lives here,
// not in the RHS body, because the header title (rendered by the host) shows the Back button and
// the tools button according to it. Showing a list view (showRHSList) also closes the open thread.
export type RHSTab = 'new' | 'threads';

export function rhsTab(state: RHSTab = 'new', action: any): RHSTab {
    switch (action.type) {
    case 'AI_RHS_SET_TAB':
        return action.tab === 'threads' ? 'threads' : 'new';
    default:
        return state;
    }
}

// The current user's AI threads (GET /ai_threads), shared by every view that lists them: the
// Agents sidebar, the chat name menus, Find agents and the RHS history. Only hooks/use_agent_threads
// fetches it; views read it through its selectors.
export const AIThreadsRequested = 'AI_THREADS_REQUESTED';
export const AIThreadsReceived = 'AI_THREADS_RECEIVED';
export const AIThreadsFailed = 'AI_THREADS_FAILED';
export const AIThreadTitleSet = 'AI_THREAD_TITLE_SET';

// An archive takes the chat out of the list at once; a failed archive puts it back.
export const AIThreadRemoved = 'AI_THREAD_REMOVED';

export interface AIThreadsState {

    // null until the first successful load.
    threads: AIThread[] | null;
    loading: boolean;
    error: boolean;

    // The latest request: the response of a superseded one is dropped.
    requestId: number;

    // Titles set while that request was in flight, which its response may predate.
    titles: Record<string, string>;
}

export const initialAIThreadsState: AIThreadsState = {
    threads: null,
    loading: false,
    error: false,
    requestId: 0,
    titles: {},
};

export function aiThreads(state: AIThreadsState = initialAIThreadsState, action: any): AIThreadsState {
    switch (action.type) {
    case AIThreadsRequested:
        return {...state, loading: true, requestId: action.requestId, titles: {}};
    case AIThreadsReceived:
        if (action.requestId !== state.requestId) {
            return state;
        }
        return {
            ...state,
            threads: (Array.isArray(action.threads) ? action.threads as AIThread[] : []).map((t) => (t.id in state.titles ? {...t, title: state.titles[t.id]} : t)),
            loading: false,
            error: false,
            titles: {},
        };
    case AIThreadsFailed:
        if (action.requestId !== state.requestId) {
            return state;
        }
        return {...state, loading: false, error: true, titles: {}};
    case AIThreadTitleSet:
        return {
            ...state,
            threads: state.threads?.map((t) => (t.id === action.conversationId ? {...t, title: action.title} : t)) ?? null,
            titles: state.loading ? {...state.titles, [action.conversationId]: action.title} : state.titles,
        };
    case AIThreadRemoved:
        return {...state, threads: state.threads?.filter((t) => t.id !== action.conversationId) ?? null};
    default:
        return state;
    }
}

export function selectedPostId(state = '', action: any) {
    switch (action.type) {
    case 'SELECT_AI_POST':
        return action.postId;
    case 'AI_RHS_SET_TAB':
        return '';
    default:
        return state;
    }
}

function customPrompts(state: CustomPrompt[] | null = null, action: any) {
    switch (action.type) {
    case CustomPromptsHandler:
        return action.customPrompts;
    default:
        return state;
    }
}

function pinnedPromptIds(state: string[] | null = null, action: any) {
    switch (action.type) {
    case PinnedPromptIdsHandler:
        return action.pinnedPromptIds;
    default:
        return state;
    }
}

function showCustomPromptsModal(state = false, action: any) {
    switch (action.type) {
    case ShowCustomPromptsModalHandler:
        return action.show;
    default:
        return state;
    }
}

export function fetchCustomPrompts() {
    return async (dispatch: Dispatch) => {
        try {
            const prompts = await fetchCustomPromptsAPI();
            dispatch({type: CustomPromptsHandler, customPrompts: prompts});
        } catch (e) {
            console.error('Failed to fetch custom prompts:', e); // eslint-disable-line no-console
        }
    };
}

export function fetchPinnedPromptIds() {
    return async (dispatch: Dispatch) => {
        try {
            const ids = await getCustomPromptPins();
            dispatch({type: PinnedPromptIdsHandler, pinnedPromptIds: ids});
        } catch (e) {
            console.error('Failed to fetch pinned prompt IDs:', e); // eslint-disable-line no-console
        }
    };
}

export const pluginReducer = combineReducers({
    callsPostButtonClickedTranscription,
    bots,
    selectedPostId,
    rhsTab,
    aiThreads,
    linkPolicy,
    htmlArtifactsEnabled,
    channelArtifactsEnabled,
    channelArtifactsLoaded,
    channelArtifactsLoadFailed,
    channelArtifactLibrary,
    scopedSessionsSupported,
    projectChannelsEnabled,
    projectAgents,
    projects,
    projectPane,
    customPrompts,
    pinnedPromptIds,
    showCustomPromptsModal,
});
