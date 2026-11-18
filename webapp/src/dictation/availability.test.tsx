// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';
import {render, screen, waitFor} from '@testing-library/react';

import manifest from '@/manifest';

import {dictationAvailability, useDictationAvailable} from './availability';

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
        {name: 'request fails', response: () => Promise.reject(new Error('401')), want: 'false'},
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
