// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {combineReducers, Dispatch, Store, UnknownAction} from 'redux';
import {GlobalState} from '@mattermost/types/store';

import {makeCallsPostButtonClickedHandler} from './calls_button';
import {getCustomPrompts as fetchCustomPromptsAPI, getCustomPromptPins} from './client';
import manifest from './manifest';
import {CustomPrompt} from './types';

type WebappStore = Store<GlobalState, UnknownAction>

const CallsClickHandler = 'calls_post_button_clicked_handler';
export const BotsHandler = manifest.id + '_bots';
export const CustomPromptsHandler = 'SET_CUSTOM_PROMPTS';
export const PinnedPromptIdsHandler = 'SET_PINNED_PROMPT_IDS';
export const ShowCustomPromptsModalHandler = 'SHOW_CUSTOM_PROMPTS_MODAL';
export const SelectedBotIdHandler = 'SET_SELECTED_BOT_ID';

export async function setupRedux(registry: any, store: WebappStore) {
    const reducer = combineReducers({
        callsPostButtonClickedTranscription,
        bots,
        botChannelId,
        selectedPostId,
        searchEnabled,
        allowUnsafeLinks,
        customPrompts,
        pinnedPromptIds,
        showCustomPromptsModal,
        selectedBotId,
    });
    registry.registerReducer(reducer);

    store.dispatch({
        type: CallsClickHandler as any,
        handler: makeCallsPostButtonClickedHandler(store.dispatch),
    });

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

function searchEnabled(state = false, action: any) {
    switch (action.type) {
    case 'SET_SEARCH_ENABLED':
        return action.searchEnabled;
    default:
        return state;
    }
}

function allowUnsafeLinks(state = false, action: any) {
    switch (action.type) {
    case 'SET_ALLOW_UNSAFE_LINKS':
        return action.allowUnsafeLinks;
    default:
        return state;
    }
}

function botChannelId(state = '', action: any) {
    switch (action.type) {
    case 'SET_AI_BOT_CHANNEL':
        return action.botChannelId;
    default:
        return state;
    }
}

function selectedPostId(state = '', action: any) {
    switch (action.type) {
    case 'SELECT_AI_POST':
        return action.postId;
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

function selectedBotId(state: string | null = null, action: any) {
    switch (action.type) {
    case SelectedBotIdHandler:
        return action.botId;
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
