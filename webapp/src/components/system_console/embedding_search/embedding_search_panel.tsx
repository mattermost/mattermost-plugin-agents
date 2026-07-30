// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl, FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import {useIsBasicsLicensed} from '@/license';

import {Pill} from '../../pill';
import EnterpriseChip from '../enterprise_chip';
import Panel from '../panel';
import {BooleanItem, ItemList, SelectionItem, SelectionItemOption} from '../item';
import {FloatItem, IntItem} from '../number_items';

import {EmbeddingSearchConfig, RECENCY_DEFAULTS, REINDEX_DEFAULTS, REINDEX_INDEX_STRATEGY, ReindexIndexStrategy} from './types';
import {OpenAIProviderConfig, OpenAICompatibleProviderConfig} from './provider_configs';
import {ChunkingOptionsConfig} from './chunking_options';
import {ReindexSection} from './reindex_section';
import {ReindexConfirmation} from './reindex_confirmation';
import {useJobStatus} from './use_job_status';

const Horizontal = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
`;

// Mirror the server's normalization (GetReindexWorkers/GetReindexBatchSize):
// unset or non-positive falls back to the default, oversized is clamped. This
// keeps the form showing the value the server will actually use.
const normalizeReindexValue = (value: number | undefined, fallback: number, max: number): number => {
    if (typeof value !== 'number' || isNaN(value) || value <= 0) {
        return fallback;
    }
    return Math.min(value, max);
};

// Mirror EffectiveReindexIndexStrategy: only 'defer' stays; else maintain.
const normalizeReindexIndexStrategy = (value: string | undefined): ReindexIndexStrategy => {
    if (value === REINDEX_INDEX_STRATEGY.defer) {
        return REINDEX_INDEX_STRATEGY.defer;
    }
    return REINDEX_INDEX_STRATEGY.maintain;
};

// Mirror the server's GetRecencyBiasSettings normalization: unset or
// non-positive falls back to the default, oversized is clamped.
const normalizeRecencyValue = (value: number | undefined, fallback: number, max?: number): number => {
    if (typeof value !== 'number' || isNaN(value) || value <= 0) {
        return fallback;
    }
    if (typeof max === 'number') {
        return Math.min(value, max);
    }
    return value;
};

interface Props {
    value: EmbeddingSearchConfig;
    onChange: (config: EmbeddingSearchConfig) => void;
}

const EmbeddingSearchPanel = ({value, onChange}: Props) => {
    const intl = useIntl();
    const isBasicsLicensed = useIsBasicsLicensed();
    const effectiveType = value.type || '';
    const isEnabled = effectiveType !== '';

    const {
        jobStatus,
        statusMessage,
        showReindexConfirmation,
        healthCheckResult,
        healthCheckLoading,
        modelCompatibility,
        isJobStale,
        handleReindexClick,
        handleConfirmReindex,
        handleCancelReindex,
        handleCancelJob,
        handleCatchUpClick,
        handleHealthCheck,
        handleResumeClick,
    } = useJobStatus();

    // Check if current form values differ from stored (indexed) values
    // This enables showing a warning immediately when editing, before save
    const currentModelName = value.embeddingProvider.parameters?.embeddingModel as string | null;
    const storedDimensions = modelCompatibility?.stored_dimensions ?? 0;
    const storedModelName = modelCompatibility?.stored_model_name ?? '';

    // Compute local mismatch and reason
    let localMismatchReason = '';
    if (modelCompatibility && storedDimensions > 0) {
        if (value.dimensions !== storedDimensions) {
            localMismatchReason = intl.formatMessage(
                {defaultMessage: 'dimension mismatch: stored={stored}, current={current}'},
                {stored: storedDimensions, current: value.dimensions},
            );
        } else if (currentModelName && currentModelName !== storedModelName) {
            localMismatchReason = intl.formatMessage(
                {defaultMessage: 'model changed: stored={stored}, current={current}'},
                {stored: storedModelName, current: currentModelName},
            );
        }
    }
    const hasLocalModelMismatch = localMismatchReason !== '';

    if (!isBasicsLicensed) {
        return (
            <Panel
                testId='system-console-embedding-search-panel'
                title={
                    <Horizontal>
                        <FormattedMessage defaultMessage='Embedding Search'/>
                        <Pill><FormattedMessage defaultMessage='EXPERIMENTAL'/></Pill>
                    </Horizontal>
                }
                subtitle={''}
            >
                <EnterpriseChip
                    text={intl.formatMessage({defaultMessage: 'Embedding search is available on qualifying Mattermost plans'})}
                    subtext={intl.formatMessage({defaultMessage: 'Embedding search is available on qualifying Mattermost plans'})}
                />
            </Panel>
        );
    }

    return (
        <Panel
            testId='system-console-embedding-search-panel'
            title={
                <Horizontal>
                    <FormattedMessage defaultMessage='Embedding Search'/>
                    <Pill><FormattedMessage defaultMessage='EXPERIMENTAL'/></Pill>
                </Horizontal>
            }
            subtitle={intl.formatMessage({defaultMessage: 'Configure embedding search settings. Note: The current implementation is experimental and subject to breaking changes. This includes having to reindex all posts.'})}
        >
            <ItemList>
                <BooleanItem
                    label={intl.formatMessage({defaultMessage: 'Enable Embedding Search'})}
                    value={isEnabled}
                    onChange={(enabled) => {
                        if (enabled) {
                            onChange({
                                type: 'composite',
                                vectorStore: {type: 'pgvector', parameters: {}},
                                embeddingProvider: {type: 'openai', parameters: {embeddingModel: '', apiKey: ''}},
                                parameters: {},
                                dimensions: 1536,
                                chunkingOptions: {
                                    chunkSize: 1000,
                                    chunkOverlap: 200,
                                    chunkingStrategy: 'sentences',
                                },
                                reindexWorkers: REINDEX_DEFAULTS.workers,
                                reindexBatchSize: REINDEX_DEFAULTS.batchSize,
                                reindexIndexStrategy: REINDEX_INDEX_STRATEGY.maintain,
                            });
                        } else {
                            onChange({
                                type: '',
                                vectorStore: {type: '', parameters: {}},
                                embeddingProvider: {type: '', parameters: {}},
                                parameters: {},
                                dimensions: 0,
                                chunkingOptions: {
                                    chunkSize: 1000,
                                    chunkOverlap: 200,
                                    chunkingStrategy: 'sentences',
                                },
                            });
                        }
                    }}
                    helpText={intl.formatMessage({defaultMessage: 'Enable or disable embedding-based semantic search.'})}
                />

                {isEnabled &&
                <SelectionItem
                    label={intl.formatMessage({defaultMessage: 'Vector Store Type'})}
                    value={value.vectorStore.type}
                    onChange={(e) => onChange({
                        ...value,
                        vectorStore: {...value.vectorStore, type: e.target.value},
                    })}
                >
                    <SelectionItemOption value='pgvector'>{'PostgreSQL pgvector'}</SelectionItemOption>
                </SelectionItem>
                }

                {isEnabled &&
                <SelectionItem
                    label={intl.formatMessage({defaultMessage: 'Embedding Provider Type'})}
                    value={value.embeddingProvider.type}
                    onChange={(e) => {
                        const newType = e.target.value;
                        let newParameters = {};
                        if (newType === 'openai-compatible') {
                            newParameters = {embeddingModel: '', apiKey: '', apiURL: ''};
                        } else if (newType === 'openai') {
                            newParameters = {embeddingModel: '', apiKey: ''};
                        }
                        onChange({
                            ...value,
                            embeddingProvider: {
                                type: newType,
                                parameters: newParameters,
                            },
                        });
                    }}
                >
                    <SelectionItemOption value='openai'>{'OpenAI'}</SelectionItemOption>
                    <SelectionItemOption value='openai-compatible'>{'OpenAI-compatible API'}</SelectionItemOption>
                </SelectionItem>
                }

                {isEnabled && value.embeddingProvider.type === 'openai' && (
                    <OpenAIProviderConfig
                        value={value.embeddingProvider}
                        onChange={(config) => onChange({...value, embeddingProvider: config})}
                    />
                )}

                {isEnabled && value.embeddingProvider.type === 'openai-compatible' && (
                    <OpenAICompatibleProviderConfig
                        value={value.embeddingProvider}
                        onChange={(config) => onChange({...value, embeddingProvider: config})}
                    />
                )}

                {isEnabled && (
                    <>
                        <IntItem
                            label={intl.formatMessage({defaultMessage: 'Dimensions'})}
                            placeholder='1024'
                            value={value?.dimensions}
                            onChange={(dimensionsValue) => {
                                onChange({
                                    ...value,
                                    dimensions: dimensionsValue,
                                });
                            }}
                            min={1}
                            helptext={intl.formatMessage({defaultMessage: 'The number of dimensions for the vector embeddings. Common values are 768, 1024, or 1536 depending on the model.'})}
                        />

                        <ChunkingOptionsConfig
                            value={value}
                            onChange={onChange}
                        />

                        <BooleanItem
                            label={intl.formatMessage({defaultMessage: 'Recency Bias'})}
                            value={value.recencyBiasEnabled ?? false}
                            onChange={(recencyBiasEnabled) => onChange({...value, recencyBiasEnabled})}
                            helpText={intl.formatMessage({defaultMessage: 'Rank more recent messages higher in semantic search results. Results are still selected by relevance; recency influences their ordering.'})}
                        />

                        {value.recencyBiasEnabled && (
                            <>
                                <FloatItem
                                    label={intl.formatMessage({defaultMessage: 'Recency Half-Life (days)'})}
                                    placeholder={RECENCY_DEFAULTS.halfLifeDays.toString()}
                                    value={normalizeRecencyValue(value.recencyHalfLifeDays, RECENCY_DEFAULTS.halfLifeDays)}
                                    onChange={(recencyHalfLifeDays) => onChange({...value, recencyHalfLifeDays})}
                                    min={0.1}
                                    helptext={intl.formatMessage({defaultMessage: 'How quickly the recency boost fades. A message this many days old loses half of its recency boost. Lower values favor newer messages more strongly.'})}
                                />

                                <FloatItem
                                    label={intl.formatMessage({defaultMessage: 'Recency Floor'})}
                                    placeholder={RECENCY_DEFAULTS.floor.toString()}
                                    value={normalizeRecencyValue(value.recencyFloor, RECENCY_DEFAULTS.floor, 1)}
                                    onChange={(recencyFloor) => onChange({...value, recencyFloor})}
                                    min={0.01}
                                    max={1}
                                    helptext={intl.formatMessage({defaultMessage: 'Minimum score multiplier for old messages (0-1). Higher values preserve old but highly relevant results; 1 disables the recency effect.'})}
                                />
                            </>
                        )}

                        <IntItem
                            label={intl.formatMessage({defaultMessage: 'Reindex Worker Count'})}
                            placeholder={REINDEX_DEFAULTS.workers.toString()}
                            value={normalizeReindexValue(value.reindexWorkers, REINDEX_DEFAULTS.workers, REINDEX_DEFAULTS.maxWorkers)}
                            onChange={(reindexWorkers) => onChange({...value, reindexWorkers})}
                            min={1}
                            max={REINDEX_DEFAULTS.maxWorkers}
                            helptext={intl.formatMessage({defaultMessage: 'Number of concurrent workers used during bulk reindexing. Higher values speed up reindexing but increase load on the embedding provider and database. Lower this if you hit provider rate limits frequently.'})}
                        />

                        <IntItem
                            label={intl.formatMessage({defaultMessage: 'Reindex Batch Size'})}
                            placeholder={REINDEX_DEFAULTS.batchSize.toString()}
                            value={normalizeReindexValue(value.reindexBatchSize, REINDEX_DEFAULTS.batchSize, REINDEX_DEFAULTS.maxBatchSize)}
                            onChange={(reindexBatchSize) => onChange({...value, reindexBatchSize})}
                            min={1}
                            max={REINDEX_DEFAULTS.maxBatchSize}
                            helptext={intl.formatMessage({defaultMessage: 'Number of posts fetched and embedded per batch during bulk reindexing.'})}
                        />

                        <SelectionItem
                            label={intl.formatMessage({defaultMessage: 'Reindex Index Strategy'})}
                            value={normalizeReindexIndexStrategy(value.reindexIndexStrategy)}
                            onChange={(e) => onChange({...value, reindexIndexStrategy: normalizeReindexIndexStrategy(e.target.value)})}
                            helptext={intl.formatMessage({defaultMessage: 'Controls how the vector index is handled during a full reindex. Dropping and rebuilding the index after the bulk load is much faster for large databases, but semantic search is unavailable until the rebuild completes.'})}
                        >
                            <SelectionItemOption value={REINDEX_INDEX_STRATEGY.maintain}>
                                {intl.formatMessage({defaultMessage: 'Maintain index during reindex (default)'})}
                            </SelectionItemOption>
                            <SelectionItemOption value={REINDEX_INDEX_STRATEGY.defer}>
                                {intl.formatMessage({defaultMessage: 'Drop and rebuild index after reindex (faster for large databases)'})}
                            </SelectionItemOption>
                        </SelectionItem>
                    </>
                )}

                {isEnabled && (
                    <ReindexSection
                        jobStatus={jobStatus}
                        statusMessage={statusMessage}
                        healthCheckResult={healthCheckResult}
                        healthCheckLoading={healthCheckLoading}
                        hasLocalModelMismatch={hasLocalModelMismatch}
                        localMismatchReason={localMismatchReason}
                        isJobStale={isJobStale}
                        onReindexClick={handleReindexClick}
                        onCancelJob={handleCancelJob}
                        onCatchUpClick={handleCatchUpClick}
                        onHealthCheck={handleHealthCheck}
                        onResumeClick={handleResumeClick}
                    />
                )}
            </ItemList>

            <ReindexConfirmation
                show={showReindexConfirmation}
                onConfirm={handleConfirmReindex}
                onCancel={handleCancelReindex}
                embeddingProviderType={value.embeddingProvider.type}
            />
        </Panel>
    );
};

export default EmbeddingSearchPanel;
