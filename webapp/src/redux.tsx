// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combineReducers, Dispatch, Store, UnknownAction} from 'redux';
import {GlobalState} from '@mattermost/types/store';

import {agentDMRawView} from './agent_dm_view';
import {makeCallsPostButtonClickedHandler} from './calls_button';
import {getCustomPrompts as fetchCustomPromptsAPI, getCustomPromptPins} from './client';
import {licenseAllows} from './license';
import manifest from './manifest';
import {CustomPrompt} from './types';
import type {AIThread} from './types/ai_thread';
import {LinkPolicy, PLAIN_TEXT_LINK_POLICY} from './utils/link_allowlist';

type WebappStore = Store<GlobalState, UnknownAction>

const CallsClickHandler = 'calls_post_button_clicked_handler';
export const BotsHandler = manifest.id + '_bots';
export const CustomPromptsHandler = 'SET_CUSTOM_PROMPTS';
export const PinnedPromptIdsHandler = 'SET_PINNED_PROMPT_IDS';
export const ShowCustomPromptsModalHandler = 'SHOW_CUSTOM_PROMPTS_MODAL';
export const SetLinkPolicyHandler = 'SET_LINK_POLICY';

export async function setupRedux(registry: any, store: WebappStore) {
    const reducer = combineReducers({
        callsPostButtonClickedTranscription,
        bots,
        selectedPostId,
        rhsTab,
        aiThreads,
        linkPolicy,
        htmlArtifactsEnabled,
        customPrompts,
        pinnedPromptIds,
        showCustomPromptsModal,
        agentDMRawView,
    });
    registry.registerReducer(reducer);

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

export interface AIThreadsState {

    // null until the first successful load.
    threads: AIThread[] | null;
    loading: boolean;
    error: boolean;

    // The latest request: the response of a superseded one is dropped.
    requestId: number;

    // Title changes (a rename, a generated title) the latest request may predate, by conversation id,
    // so its response cannot undo them. `seq` orders them against `requestSeq`.
    titleEdits: Record<string, {title: string; seq: number}>;
    titleSeq: number;
    requestSeq: number;
}

export const initialAIThreadsState: AIThreadsState = {
    threads: null,
    loading: false,
    error: false,
    requestId: 0,
    titleEdits: {},
    titleSeq: 0,
    requestSeq: 0,
};

export function aiThreads(state: AIThreadsState = initialAIThreadsState, action: any): AIThreadsState {
    switch (action.type) {
    case AIThreadsRequested:
        return {...state, loading: true, requestId: action.requestId, requestSeq: state.titleSeq};
    case AIThreadsReceived: {
        if (action.requestId !== state.requestId) {
            return state;
        }

        // Edits made after the request started win over its (older) titles; the rest it reflects.
        const titleEdits = Object.fromEntries(Object.entries(state.titleEdits).filter(([, edit]) => edit.seq > state.requestSeq));
        const received: AIThread[] = Array.isArray(action.threads) ? action.threads : [];
        const threads = received.map((t) => (titleEdits[t.id] ? {...t, title: titleEdits[t.id].title} : t));
        return {...state, threads, loading: false, error: false, titleEdits};
    }
    case AIThreadsFailed:
        if (action.requestId !== state.requestId) {
            return state;
        }
        return {...state, loading: false, error: true};
    case AIThreadTitleSet: {
        const id: string = action.conversationId;
        const title: string = action.title;
        const titleSeq = state.titleSeq + 1;
        return {
            ...state,
            threads: state.threads ? state.threads.map((t) => (t.id === id ? {...t, title} : t)) : state.threads,
            titleEdits: {...state.titleEdits, [id]: {title, seq: titleSeq}},
            titleSeq,
        };
    }
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
