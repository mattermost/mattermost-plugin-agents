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
import {IntItem} from '../number_items';

import {EmbeddingSearchConfig, HNSW_DEFAULTS, REINDEX_DEFAULTS, REINDEX_INDEX_STRATEGY, ReindexIndexStrategy} from './types';
import {OpenAIProviderConfig, OpenAICompatibleProviderConfig} from './provider_configs';
import {ChunkingOptionsConfig} from './chunking_options';
import {ReindexSection} from './reindex_section';
import {ReindexConfirmation, RebuildVectorIndexConfirmation} from './reindex_confirmation';
import {useJobStatus} from './use_job_status';
import {embeddingIdentityMismatchKind} from './local_identity_mismatch';

const Horizontal = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 8px;
`;

const IndexStorageGroup = styled.div`
    display: flex;
    flex-direction: column;
    gap: 24px;
`;

const IndexStorageTitle = styled.div`
    font-weight: 600;
    font-size: 14px;
    color: var(--center-channel-color);
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

// Mirror GetHNSWM: unset/non-positive → 8, then clamp to [2, 100].
const normalizeHNSWM = (value: number | undefined): number => {
    if (typeof value !== 'number' || isNaN(value) || value <= 0) {
        return HNSW_DEFAULTS.m;
    }
	return Math.min(Math.max(value, HNSW_DEFAULTS.min), HNSW_DEFAULTS.max);
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
        handleRebuildVectorIndexClick,
        handleConfirmRebuildVectorIndex,
        handleCancelRebuildVectorIndex,
        handleHealthCheck,
        handleResumeClick,
        showRebuildConfirmation,
    } = useJobStatus();

    // Check if current form values differ from stored (indexed) values
    // This enables showing a warning immediately when editing, before save
    const currentModelName = (value.embeddingProvider.parameters?.embeddingModel as string | null) || '';
    const storedHNSWM = modelCompatibility?.stored_hnsw_m ?? 0;
    const currentHNSWM = normalizeHNSWM(value.hnswM);
    const hasLocalHNSWMismatch = storedHNSWM !== 0 && storedHNSWM !== currentHNSWM;

    const mismatchKind = embeddingIdentityMismatchKind(modelCompatibility, {
        providerType: value.embeddingProvider.type,
        dimensions: value.dimensions,
        modelName: currentModelName,
    });
    let localMismatchReason = '';
    switch (mismatchKind) {
    case 'provider':
        localMismatchReason = intl.formatMessage(
            {defaultMessage: 'provider changed: stored={stored}, current={current}'},
            {stored: modelCompatibility?.stored_provider_type ?? '', current: value.embeddingProvider.type},
        );
        break;
    case 'dimensions':
        localMismatchReason = intl.formatMessage(
            {defaultMessage: 'dimension mismatch: stored={stored}, current={current}'},
            {stored: modelCompatibility?.stored_dimensions ?? 0, current: value.dimensions},
        );
        break;
    case 'model':
        localMismatchReason = intl.formatMessage(
            {defaultMessage: 'model changed: stored={stored}, current={current}'},
            {stored: modelCompatibility?.stored_model_name ?? '', current: currentModelName},
        );
        break;
    case null:
        break;
    default: {
        const exhaustive: never = mismatchKind;
        throw new Error(`unhandled embedding identity mismatch: ${exhaustive}`);
    }
    }
    const hasLocalModelMismatch = localMismatchReason !== '';

    if (!isBasicsLicensed) {
        return (
            <Panel
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
                                hnswM: HNSW_DEFAULTS.m,
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
                        <IndexStorageGroup>
                            <IndexStorageTitle>
                                <FormattedMessage defaultMessage='Index storage'/>
                            </IndexStorageTitle>
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
                            <IntItem
                                label={intl.formatMessage({defaultMessage: 'HNSW M'})}
                                placeholder={HNSW_DEFAULTS.m.toString()}
                                value={normalizeHNSWM(value.hnswM)}
                                onChange={(hnswM) => onChange({...value, hnswM})}
                                min={HNSW_DEFAULTS.min}
                                max={HNSW_DEFAULTS.max}
                                helptext={intl.formatMessage({defaultMessage: 'Graph connections per row. Lower uses less RAM and is slightly less accurate. Changing this rebuilds the vector index; it does not re-embed posts. Default 8.'})}
                            />
                        </IndexStorageGroup>

                        <ChunkingOptionsConfig
                            value={value}
                            onChange={onChange}
                        />

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
                        hasLocalHNSWMismatch={hasLocalHNSWMismatch}
                        isJobStale={isJobStale}
                        onReindexClick={handleReindexClick}
                        onCancelJob={handleCancelJob}
                        onCatchUpClick={handleCatchUpClick}
                        onRebuildVectorIndexClick={handleRebuildVectorIndexClick}
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
            <RebuildVectorIndexConfirmation
                show={showRebuildConfirmation}
                onConfirm={handleConfirmRebuildVectorIndex}
                onCancel={handleCancelRebuildVectorIndex}
            />
        </Panel>
    );
};

export default EmbeddingSearchPanel;
