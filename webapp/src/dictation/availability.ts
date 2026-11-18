// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect} from 'react';
import {Dispatch} from 'redux';
import {useDispatch, useSelector} from 'react-redux';

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
let retryTimer: ReturnType<typeof setTimeout> | null = null;

// A failed load hides the button; these retries bring it back after a
// transient server or network error without waiting for an event.
export const AVAILABILITY_RETRY_DELAYS_MS = [5000, 30000];

const isRetryable = (err: unknown) => {
    const status = (err as {status_code?: number})?.status_code;
    return status !== 401 && status !== 403 && status !== 404;
};

// refreshDictationAvailability refetches whether the current user can
// dictate. A failed request hides the button rather than keeping a stale
// "available", and a superseded response is dropped.
export async function refreshDictationAvailability(dispatch: Dispatch, attempt = 0) {
    if (retryTimer !== null) {
        clearTimeout(retryTimer);
        retryTimer = null;
    }
    const request = ++latestRequest;
    let availability: DictationAvailability | null = null;
    let retry = false;
    try {
        availability = await getDictationAvailability();
    } catch (err) {
        retry = isRetryable(err) && attempt < AVAILABILITY_RETRY_DELAYS_MS.length;
    }
    if (request !== latestRequest) {
        return;
    }
    dispatch({type: SetDictationAvailability, availability});
    if (retry) {
        retryTimer = setTimeout(() => refreshDictationAvailability(dispatch, attempt + 1), AVAILABILITY_RETRY_DELAYS_MS[attempt]);
    }
}

export const AVAILABILITY_REFRESH_DEBOUNCE_MS = 250;
let refreshTimer: ReturnType<typeof setTimeout> | null = null;

// scheduleDictationAvailabilityRefresh coalesces bursts of change events:
// every HA node publishes its own copy of each change.
export function scheduleDictationAvailabilityRefresh(dispatch: Dispatch) {
    if (refreshTimer !== null) {
        clearTimeout(refreshTimer);
    }
    refreshTimer = setTimeout(() => {
        refreshTimer = null;
        refreshDictationAvailability(dispatch);
    }, AVAILABILITY_REFRESH_DEBOUNCE_MS);
}

export const getDictationAvailabilityState = (state: GlobalState): DictationAvailability | null =>
    (state as any)[`plugins-${manifest.id}`]?.dictationAvailability ?? null;

// useDictationAvailable loads availability on first use: the plugin can
// initialize before the user is logged in, when the request would fail.
export function useDictationAvailable(): boolean {
    const dispatch = useDispatch();
    const availability = useSelector(getDictationAvailabilityState);
    const loaded = availability !== null;
    useEffect(() => {
        if (!loaded) {
            refreshDictationAvailability(dispatch);
        }
    }, [loaded, dispatch]);
    return availability?.available === true;
}
