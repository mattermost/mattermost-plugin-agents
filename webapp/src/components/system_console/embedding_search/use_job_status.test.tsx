// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {renderHook, waitFor} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

import {useJobStatus} from './use_job_status';

jest.mock('../../../client', () => ({
    getReindexStatus: jest.fn(),
    checkIndexHealth: jest.fn(),
    doReindexPosts: jest.fn(),
    cancelReindex: jest.fn(),
    catchUpIndex: jest.fn(),
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

describe('useJobStatus', () => {
    test('enables polling when fetch finds a running job', async () => {
        getReindexStatus.mockResolvedValue({
            status: 'running',
            started_at: '2026-01-01T00:00:00Z',
            processed_rows: 50,
            total_rows: 100,
        });

        const {result} = renderHook(() => useJobStatus(), {wrapper});

        await waitFor(() => {
            expect(result.current.polling).toBe(true);
        });
        expect(getReindexStatus).toHaveBeenCalled();
    });
});
