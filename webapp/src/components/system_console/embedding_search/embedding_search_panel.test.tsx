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

jest.mock('@/license', () => ({
    useIsBasicsLicensed: jest.fn(() => true),
}));

jest.mock('../enterprise_chip', () => ({
    __esModule: true,
    default: () => null,
}));

jest.mock('./use_job_status', () => ({
    useJobStatus: jest.fn(),
}));

/* eslint-disable import/first, import/order */
import {IntlProvider} from 'react-intl';

import {useJobStatus} from './use_job_status';
import EmbeddingSearchPanel from './embedding_search_panel';
import {EmbeddingSearchConfig, REINDEX_INDEX_STRATEGY} from './types';
/* eslint-enable import/first, import/order */

const mockUseJobStatus = useJobStatus as jest.Mock;

const enabledConfig = (providerType: string): EmbeddingSearchConfig => ({
    type: 'composite',
    vectorStore: {type: 'pgvector', parameters: {}},
    embeddingProvider: {type: providerType, parameters: {embeddingModel: 'text-embedding-3-small'}},
    parameters: {},
    dimensions: 1536,
    hnswM: 8,
    reindexIndexStrategy: REINDEX_INDEX_STRATEGY.maintain,
});

const idleJobStatus = {
    jobStatus: null,
    statusMessage: {},
    showReindexConfirmation: false,
    showRebuildConfirmation: false,
    healthCheckResult: {
        db_post_count: 10,
        indexed_post_count: 10,
        missing_posts: 0,
        status: 'healthy',
        checked_at: '2026-01-01T00:00:00Z',
        model_compatible: true,
        model_needs_reindex: false,
        stored_provider_type: 'openai',
        stored_dimensions: 1536,
        stored_model_name: 'text-embedding-3-small',
        stored_hnsw_m: 8,
    },
    healthCheckLoading: false,
    modelCompatibility: {
        compatible: true,
        needs_reindex: false,
        stored_provider_type: 'openai',
        stored_dimensions: 1536,
        stored_model_name: 'text-embedding-3-small',
        stored_hnsw_m: 8,
    },
    isJobStale: false,
    handleReindexClick: jest.fn(),
    handleConfirmReindex: jest.fn(),
    handleCancelReindex: jest.fn(),
    handleCancelJob: jest.fn(),
    handleCatchUpClick: jest.fn(),
    handleRebuildVectorIndexClick: jest.fn(),
    handleConfirmRebuildVectorIndex: jest.fn(),
    handleCancelRebuildVectorIndex: jest.fn(),
    handleHealthCheck: jest.fn(),
    handleResumeClick: jest.fn(),
};

describe('EmbeddingSearchPanel rebuild gating', () => {
    beforeEach(() => {
        mockUseJobStatus.mockReset();
        mockUseJobStatus.mockReturnValue(idleJobStatus);
    });

    it('disables Rebuild vector index on a provider-only form change', () => {
        render(
            <IntlProvider locale='en'>
                <EmbeddingSearchPanel
                    value={enabledConfig('anthropic')}
                    onChange={jest.fn()}
                />
            </IntlProvider>,
        );

        const rebuild = screen.getByRole('button', {name: 'Rebuild vector index'}) as HTMLButtonElement;
        expect(rebuild.disabled).toBe(true);
        expect(screen.getByText('Embedding Model Changed')).toBeTruthy();
    });

    it('keeps Rebuild vector index enabled when only HNSW M differs', () => {
        mockUseJobStatus.mockReturnValue({
            ...idleJobStatus,
            modelCompatibility: {
                ...idleJobStatus.modelCompatibility,
                stored_hnsw_m: 16,
            },
        });

        render(
            <IntlProvider locale='en'>
                <EmbeddingSearchPanel
                    value={enabledConfig('openai')}
                    onChange={jest.fn()}
                />
            </IntlProvider>,
        );

        const rebuild = screen.getByRole('button', {name: 'Rebuild vector index'}) as HTMLButtonElement;
        expect(rebuild.disabled).toBe(false);
    });
});
