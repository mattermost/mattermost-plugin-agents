// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';
import {render, screen, waitFor} from '@testing-library/react';

import manifest from '@/manifest';

import {AVAILABILITY_RETRY_DELAYS_MS, SetDictationAvailability, dictationAvailability, refreshDictationAvailability, useDictationAvailable} from './availability';

const mockGetDictationAvailability = jest.fn();
jest.mock('@/client', () => ({
    getDictationAvailability: (...args: unknown[]) => mockGetDictationAvailability(...args),
}));

const Probe = () => <div data-testid='available'>{String(useDictationAvailable())}</div>;

function renderProbe() {
    const store = createStore(combineReducers({
        [`plugins-${manifest.id}`]: combineReducers({dictationAvailability}),
    }));
    render(
        <Provider store={store}>
            <Probe/>
            <Probe/>
        </Provider>,
    );
}

describe('useDictationAvailable', () => {
    beforeEach(() => {
        mockGetDictationAvailability.mockReset();
    });

    test.each([
        {name: 'available', response: () => Promise.resolve({enabled: true, available: true, provider: 'openai', model: 'gpt-live-transcribe', transport: 'webrtc'}), want: 'true'},
        {name: 'unlicensed', response: () => Promise.resolve({enabled: true, available: false, provider: 'openai', model: 'gpt-live-transcribe', transport: 'webrtc', reason: 'license'}), want: 'false'},
        {name: 'request fails', response: () => Promise.reject(Object.assign(new Error('401'), {status_code: 401})), want: 'false'},
    ])('loads availability on first use: $name', async ({response, want}) => {
        mockGetDictationAvailability.mockImplementation(response);
        renderProbe();
        await waitFor(() => expect(mockGetDictationAvailability).toHaveBeenCalled());
        await waitFor(() => expect(screen.getAllByTestId('available').map((el) => el.textContent)).toEqual([want, want]));
    });

    test('does not refetch once loaded', async () => {
        mockGetDictationAvailability.mockResolvedValue({enabled: true, available: true, provider: 'openai', model: 'm', transport: 'webrtc'});
        renderProbe();
        await waitFor(() => expect(screen.getAllByTestId('available')[0].textContent).toBe('true'));
        await new Promise((r) => setTimeout(r, 20));
        expect(mockGetDictationAvailability.mock.calls.length).toBeLessThanOrEqual(2);
    });
});

describe('refreshDictationAvailability', () => {
    test('drops a response superseded by a newer request', async () => {
        const stale = {enabled: true, available: true, provider: 'openai', model: 'm', transport: 'webrtc'};
        const fresh = {enabled: true, available: false, provider: '', model: '', transport: 'webrtc', reason: 'disabled'};
        let resolveFirst: (v: unknown) => void = () => { /* set below */ };
        mockGetDictationAvailability.mockReset();
        mockGetDictationAvailability.mockReturnValueOnce(new Promise((resolve) => {
            resolveFirst = resolve;
        }));
        mockGetDictationAvailability.mockResolvedValueOnce(fresh);
        const dispatch = jest.fn();

        const first = refreshDictationAvailability(dispatch);
        await refreshDictationAvailability(dispatch);
        resolveFirst(stale);
        await first;

        expect(dispatch.mock.calls).toEqual([[{type: SetDictationAvailability, availability: fresh}]]);
    });
});

describe('refreshDictationAvailability retries', () => {
    const available = {enabled: true, available: true, provider: 'openai', model: 'm', transport: 'webrtc'};
    const fail = (status?: number) => Promise.reject(Object.assign(new Error('failed'), {status_code: status}));

    beforeEach(() => {
        jest.useFakeTimers();
        mockGetDictationAvailability.mockReset();
    });
    afterEach(() => {
        jest.useRealTimers();
    });

    test('a transient failure is retried with backoff until it recovers', async () => {
        mockGetDictationAvailability.mockImplementationOnce(() => fail(503)).mockImplementationOnce(() => fail()).mockResolvedValueOnce(available);
        const dispatch = jest.fn();

        await refreshDictationAvailability(dispatch);
        expect(dispatch).toHaveBeenLastCalledWith({type: SetDictationAvailability, availability: null});

        await jest.advanceTimersByTimeAsync(AVAILABILITY_RETRY_DELAYS_MS[0]);
        expect(mockGetDictationAvailability).toHaveBeenCalledTimes(2);

        await jest.advanceTimersByTimeAsync(AVAILABILITY_RETRY_DELAYS_MS[1]);
        expect(mockGetDictationAvailability).toHaveBeenCalledTimes(3);
        expect(dispatch).toHaveBeenLastCalledWith({type: SetDictationAvailability, availability: available});
    });

    test('gives up after the last retry', async () => {
        mockGetDictationAvailability.mockImplementation(() => fail(500));
        await refreshDictationAvailability(jest.fn());
        await jest.advanceTimersByTimeAsync(AVAILABILITY_RETRY_DELAYS_MS.reduce((a, b) => a + b) * 2);
        expect(mockGetDictationAvailability).toHaveBeenCalledTimes(AVAILABILITY_RETRY_DELAYS_MS.length + 1);
    });

    test.each([401, 403, 404])('a %s is not retried', async (status) => {
        mockGetDictationAvailability.mockImplementation(() => fail(status));
        await refreshDictationAvailability(jest.fn());
        await jest.advanceTimersByTimeAsync(60000);
        expect(mockGetDictationAvailability).toHaveBeenCalledTimes(1);
    });

    test('a newer request cancels a pending retry', async () => {
        mockGetDictationAvailability.mockImplementationOnce(() => fail(503)).mockResolvedValue(available);
        const dispatch = jest.fn();
        await refreshDictationAvailability(dispatch);
        await refreshDictationAvailability(dispatch);
        await jest.advanceTimersByTimeAsync(60000);
        expect(mockGetDictationAvailability).toHaveBeenCalledTimes(2);
    });
});
