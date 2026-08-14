// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, renderHook, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {useJobStatus} from './use_job_status';

jest.mock('../../../client', () => ({
    getReindexStatus: jest.fn(),
    checkIndexHealth: jest.fn(),
    doReindexPosts: jest.fn(),
    cancelReindex: jest.fn(),
    catchUpIndex: jest.fn(),
    rebuildVectorIndex: jest.fn(),
}));

const {
    getReindexStatus,
    checkIndexHealth,
} = jest.requireMock('../../../client') as {
    getReindexStatus: jest.Mock;
    checkIndexHealth: jest.Mock;
};

const wrapper = ({children}: {children: React.ReactNode}) => (
    <IntlProvider locale='en'>
        {children}
    </IntlProvider>
);

beforeEach(() => {
    getReindexStatus.mockReset();
    checkIndexHealth.mockReset();
    checkIndexHealth.mockResolvedValue({status: 'not_configured'});
});

afterEach(() => {
    jest.useRealTimers();
});

describe('useJobStatus', () => {
    test.each(['running', 'cancel_requested'] as const)(
        'polls repeatedly when fetch finds a %s job',
        async (status) => {
            jest.useFakeTimers();

            getReindexStatus.mockResolvedValue({
                status,
                started_at: '2026-01-01T00:00:00Z',
                processed_rows: 50,
                total_rows: 100,
            });

            const {result} = renderHook(() => useJobStatus(), {wrapper});

            await waitFor(() => {
                expect(result.current.polling).toBe(true);
            });

            const callsAfterMount = getReindexStatus.mock.calls.length;
            expect(callsAfterMount).toBeGreaterThanOrEqual(1);

            await act(async () => {
                await jest.advanceTimersByTimeAsync(2000);
            });

            expect(getReindexStatus.mock.calls.length).toBeGreaterThan(callsAfterMount);

            const callsAfterFirstPoll = getReindexStatus.mock.calls.length;

            await act(async () => {
                await jest.advanceTimersByTimeAsync(2000);
            });

            expect(getReindexStatus.mock.calls.length).toBeGreaterThan(callsAfterFirstPoll);
        },
    );
});
