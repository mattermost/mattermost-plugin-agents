// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Dispatch} from 'redux';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {getDictationAvailability} from '@/client';
import manifest from '@/manifest';

import type {DictationAvailability} from './types';

export const SetDictationAvailability = 'SET_DICTATION_AVAILABILITY';

export function dictationAvailability(state: DictationAvailability | null = null, action: any): DictationAvailability | null {
    switch (action.type) {
    case SetDictationAvailability:
        return action.availability ?? null;
    default:
        return state;
    }
}

let latestRequest = 0;

// refreshDictationAvailability refetches whether the current user can
// dictate. A failed request hides the button rather than keeping a stale
// "available", and a superseded response is dropped.
export async function refreshDictationAvailability(dispatch: Dispatch) {
    const request = ++latestRequest;
    let availability: DictationAvailability | null = null;
    try {
        availability = await getDictationAvailability();
    } catch {
        availability = null;
    }
    if (request !== latestRequest) {
        return;
    }
    dispatch({type: SetDictationAvailability, availability});
}

export const getDictationAvailabilityState = (state: GlobalState): DictationAvailability | null =>
    (state as any)[`plugins-${manifest.id}`]?.dictationAvailability ?? null;

export function useDictationAvailable(): boolean {
    return useSelector((state: GlobalState) => getDictationAvailabilityState(state)?.available === true);
}
