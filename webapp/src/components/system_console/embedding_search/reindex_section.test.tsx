// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';

jest.mock('react-intl', () => {
    const React = require('react'); // eslint-disable-line @typescript-eslint/no-shadow, no-shadow, global-require

    return {
        __esModule: true,
        IntlProvider: ({children}: {children: React.ReactNode}) => React.createElement(React.Fragment, null, children),
        FormattedMessage: ({defaultMessage}: {defaultMessage?: string}) =>
            React.createElement(React.Fragment, null, defaultMessage ?? ''),
        useIntl: () => ({
            formatMessage: ({defaultMessage}: {defaultMessage?: string}, values?: Record<string, unknown>) => {
                if (!values) {
                    return defaultMessage ?? '';
                }
                return Object.entries(values).reduce(
                    (msg, [key, value]) => msg.replace(new RegExp(`\\{${key}\\}`, 'g'), String(value)),
                    defaultMessage ?? '',
                );
            },
        }),
    };
});

/* eslint-disable import/first, import/order */
import {IntlProvider} from 'react-intl';

import {ReindexSection} from './reindex_section';
import {JobStatusType} from './types';
/* eslint-enable import/first, import/order */

const noop = jest.fn();

const defaultProps = {
    jobStatus: null,
    statusMessage: {},
    healthCheckResult: null,
    healthCheckLoading: false,
    hasLocalModelMismatch: false,
    localMismatchReason: '',
    hasLocalHNSWMismatch: false,
    hasLocalRetentionWiden: false,
    hasUnsavedRetentionWiden: false,
    hasLocalRetentionTighten: false,
    isJobStale: false,
    onReindexClick: noop,
    onCancelJob: noop,
    onCatchUpClick: noop,
    onRebuildVectorIndexClick: noop,
    onHealthCheck: noop,
    onResumeClick: noop,
};

const job = (overrides: Partial<JobStatusType> = {}): JobStatusType => ({
    status: 'running',
    started_at: '2026-01-01T00:00:00Z',
    processed_rows: 50,
    total_rows: 100,
    resumable: false,
    ...overrides,
});

const renderSection = (overrides: Partial<React.ComponentProps<typeof ReindexSection>> = {}) => {
    return render(
        <IntlProvider locale='en'>
            <ReindexSection
                {...defaultProps}
                {...overrides}
            />
        </IntlProvider>,
    );
};

describe('ReindexSection resume visibility', () => {
    it.each([
        {label: 'omitted operation'},
        {label: 'operation reindex', operation: 'reindex' as const},
    ])(
        'shows Resume from checkpoint for a stale running full reindex ($label, resumable false)',
        ({operation}) => {
            renderSection({
                jobStatus: job({
                    status: 'running',
                    ...(operation ? {operation} : {}),
                    resumable: false,
                    processed_rows: 50,
                }),
                isJobStale: true,
            });

            expect(screen.getByRole('button', {name: 'Resume from checkpoint'})).toBeTruthy();
            expect(screen.getByRole('button', {name: 'Reindex from scratch'})).toBeTruthy();
            expect(screen.queryByRole('button', {name: 'Rebuild vector index'})).toBeNull();
        },
    );

    it('does not show Resume from checkpoint for a stale running rebuild', () => {
        renderSection({
            jobStatus: job({
                status: 'running',
                operation: 'rebuild_vector_index',
                resumable: false,
                processed_rows: 50,
            }),
            isJobStale: true,
        });

        expect(screen.queryByRole('button', {name: 'Resume from checkpoint'})).toBeNull();
        expect(screen.getByRole('button', {name: 'Rebuild vector index'})).toBeTruthy();
        expect(screen.getByRole('button', {name: 'Reindex from scratch'})).toBeTruthy();
    });

    it('shows Resume Reindex for a failed full reindex with progress even when resumable is false', () => {
        renderSection({
            jobStatus: job({
                status: 'failed',
                operation: 'reindex',
                resumable: false,
                processed_rows: 50,
            }),
            isJobStale: false,
        });

        expect(screen.getByRole('button', {name: 'Resume Reindex'})).toBeTruthy();
        expect(screen.queryByRole('button', {name: 'Resume from checkpoint'})).toBeNull();
    });

    it.each(['failed', 'canceled'] as const)(
        'does not show Resume Reindex for a %s rebuild',
        (status) => {
            renderSection({
                jobStatus: job({
                    status,
                    operation: 'rebuild_vector_index',
                    resumable: false,
                    processed_rows: 50,
                }),
                isJobStale: false,
            });

            expect(screen.queryByRole('button', {name: 'Resume Reindex'})).toBeNull();
            expect(screen.getByRole('button', {name: 'Rebuild vector index'})).toBeTruthy();
        },
    );
});
