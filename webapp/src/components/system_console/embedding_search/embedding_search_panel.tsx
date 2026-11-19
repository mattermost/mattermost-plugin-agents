// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl, FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import {AdminPanel} from '@mattermost/compass-ui/components/admin-panel';

import {useIsLicensedFor} from '@/license';

import {LicenseChip} from '../enterprise_chip';
import {BooleanItem, ItemList, SelectionItem} from '../item';
import {FloatItem, IntItem} from '../number_items';

import {EmbeddingSearchConfig, HNSW_DEFAULTS, RECENCY_DEFAULTS, REINDEX_DEFAULTS, REINDEX_INDEX_STRATEGY, ReindexIndexStrategy, VECTOR_ELEMENT_TYPE, normalizeVectorElementType} from './types';
import {OpenAIProviderConfig, OpenAICompatibleProviderConfig} from './provider_configs';
import {ChunkingOptionsConfig} from './chunking_options';
import {ReindexSection} from './reindex_section';
import {ReindexConfirmation, RebuildVectorIndexConfirmation} from './reindex_confirmation';
import {useJobStatus} from './use_job_status';
import {embeddingIdentityMismatchKind} from './local_identity_mismatch';
import {retentionWindowTightened, retentionWindowWidened} from './retention_window';

const IndexStorageGroup = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxl);
`;

const IndexStorageTitle = styled.div`
    font-weight: var(--font-weight-semibold);
    font-size: var(--font-size-100);
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
    const semanticSearchLicensed = useIsLicensedFor('semantic_search');
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
    const currentRetentionDays = value.indexRetentionDays ?? 0;
    const storedRetentionDays = modelCompatibility?.stored_index_retention_days;
    const savedRetentionWiden = Boolean(modelCompatibility?.needs_catch_up);
    const formRetentionWidened = retentionWindowWidened(currentRetentionDays, storedRetentionDays);
    const hasUnsavedRetentionWiden = formRetentionWidened && !savedRetentionWiden;
    const hasLocalRetentionWiden = savedRetentionWiden || formRetentionWidened;
    const hasLocalRetentionTighten = retentionWindowTightened(currentRetentionDays, storedRetentionDays) &&
        !hasLocalRetentionWiden;

    const mismatchKind = embeddingIdentityMismatchKind(modelCompatibility, {
        providerType: value.embeddingProvider.type,
        dimensions: value.dimensions,
        modelName: currentModelName,
        vectorElementType: normalizeVectorElementType(value.vectorElementType),
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
    case 'vectorElementType':
        localMismatchReason = intl.formatMessage(
            {defaultMessage: 'vector element type changed: stored={stored}, current={current}'},
            {stored: modelCompatibility?.stored_vector_element_type ?? '', current: normalizeVectorElementType(value.vectorElementType)},
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

    return (
        <AdminPanel
            title={<FormattedMessage defaultMessage='Embedding Search'/>}
            showBeta={true}
            betaLabel={intl.formatMessage({defaultMessage: 'EXPERIMENTAL'})}
            subtitle={intl.formatMessage({defaultMessage: 'Experimental and subject to breaking changes that may require a full reindex.'})}
        >
            <ItemList>
                <BooleanItem
                    label={intl.formatMessage({defaultMessage: 'Enable Embedding Search'})}
                    value={isEnabled}
                    disableTrue={!semanticSearchLicensed}
                    extra={!semanticSearchLicensed && (
                        <LicenseChip capability='semantic_search'/>
                    )}
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
                                vectorElementType: VECTOR_ELEMENT_TYPE.vector,
                                indexRetentionDays: 0,
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
                    helpText={intl.formatMessage({defaultMessage: 'Turn on embedding-based semantic search.'})}
                />

                {isEnabled &&
                <SelectionItem
                    label={intl.formatMessage({defaultMessage: 'Vector Store Type'})}
                    value={value.vectorStore.type}
                    onChange={(type) => onChange({
                        ...value,
                        vectorStore: {...value.vectorStore, type},
                    })}
                    options={[{value: 'pgvector', label: 'PostgreSQL pgvector'}]}
                />
                }

                {isEnabled &&
                <SelectionItem
                    label={intl.formatMessage({defaultMessage: 'Embedding Provider Type'})}
                    value={value.embeddingProvider.type}
                    onChange={(newType) => {
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
                    options={[
                        {value: 'openai', label: 'OpenAI'},
                        {value: 'openai-compatible', label: 'OpenAI-compatible API'},
                    ]}
                />
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
                                helptext={intl.formatMessage({defaultMessage: 'Vector embedding dimensions (commonly 768, 1024, or 1536, depending on the model).'})}
                            />
                            <IntItem
                                label={intl.formatMessage({defaultMessage: 'HNSW M'})}
                                placeholder={HNSW_DEFAULTS.m.toString()}
                                value={normalizeHNSWM(value.hnswM)}
                                onChange={(hnswM) => onChange({...value, hnswM})}
                                min={HNSW_DEFAULTS.min}
                                max={HNSW_DEFAULTS.max}
                                helptext={intl.formatMessage({defaultMessage: 'Graph connections per row (default 8). Lower uses less RAM, slightly less accuracy. Requires Rebuild vector index, not re-embedding.'})}
                            />
                            <SelectionItem
                                label={intl.formatMessage({defaultMessage: 'Vector precision'})}
                                value={normalizeVectorElementType(value.vectorElementType)}
                                onChange={(vectorElementType) => onChange({
                                    ...value,
                                    vectorElementType: normalizeVectorElementType(vectorElementType),
                                })}
                                helptext={intl.formatMessage({defaultMessage: 'Half precision saves RAM and disk (default: standard). Changing drops the index; run Full Reindex.'})}
                                options={[
                                    {value: VECTOR_ELEMENT_TYPE.vector, label: intl.formatMessage({defaultMessage: 'Standard (vector)'})},
                                    {value: VECTOR_ELEMENT_TYPE.halfvec, label: intl.formatMessage({defaultMessage: 'Half precision (halfvec)'})},
                                ]}
                            />
                            <IntItem
                                label={intl.formatMessage({defaultMessage: 'Index posts from the last N days'})}
                                placeholder='0'
                                value={value.indexRetentionDays ?? 0}
                                onChange={(indexRetentionDays) => onChange({...value, indexRetentionDays})}
                                min={0}
                                helptext={intl.formatMessage({defaultMessage: '0 indexes all posts; positive values limit how far back to index. Raise and run Catch Up to add older posts; lowering does not delete indexed posts.'})}
                            />
                        </IndexStorageGroup>

                        <ChunkingOptionsConfig
                            value={value}
                            onChange={onChange}
                        />

                        <BooleanItem
                            label={intl.formatMessage({defaultMessage: 'Recency Bias'})}
                            value={value.recencyBiasEnabled ?? false}
                            onChange={(recencyBiasEnabled) => onChange({...value, recencyBiasEnabled})}
                            helpText={intl.formatMessage({defaultMessage: 'Boosts newer messages in search ranking without changing relevance filtering.'})}
                        />

                        {value.recencyBiasEnabled && (
                            <>
                                <FloatItem
                                    label={intl.formatMessage({defaultMessage: 'Recency Half-Life (days)'})}
                                    placeholder={RECENCY_DEFAULTS.halfLifeDays.toString()}
                                    value={normalizeRecencyValue(value.recencyHalfLifeDays, RECENCY_DEFAULTS.halfLifeDays)}
                                    onChange={(recencyHalfLifeDays) => onChange({...value, recencyHalfLifeDays})}
                                    min={0.1}
                                    helptext={intl.formatMessage({defaultMessage: 'Days until a message loses half its recency boost. Lower values favor newer messages.'})}
                                />

                                <FloatItem
                                    label={intl.formatMessage({defaultMessage: 'Recency Floor'})}
                                    placeholder={RECENCY_DEFAULTS.floor.toString()}
                                    value={normalizeRecencyValue(value.recencyFloor, RECENCY_DEFAULTS.floor, 1)}
                                    onChange={(recencyFloor) => onChange({...value, recencyFloor})}
                                    min={0.01}
                                    max={1}
                                    helptext={intl.formatMessage({defaultMessage: 'Minimum score multiplier for old messages (0–1). Higher preserves older relevant hits; 1 disables recency bias.'})}
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
                            helptext={intl.formatMessage({defaultMessage: 'Concurrent bulk reindex workers. Higher is faster but adds DB and provider load; lower if you hit rate limits.'})}
                        />

                        <IntItem
                            label={intl.formatMessage({defaultMessage: 'Reindex Batch Size'})}
                            placeholder={REINDEX_DEFAULTS.batchSize.toString()}
                            value={normalizeReindexValue(value.reindexBatchSize, REINDEX_DEFAULTS.batchSize, REINDEX_DEFAULTS.maxBatchSize)}
                            onChange={(reindexBatchSize) => onChange({...value, reindexBatchSize})}
                            min={1}
                            max={REINDEX_DEFAULTS.maxBatchSize}
                            helptext={intl.formatMessage({defaultMessage: 'Posts fetched and embedded per bulk reindex batch.'})}
                        />

                        <SelectionItem
                            label={intl.formatMessage({defaultMessage: 'Reindex Index Strategy'})}
                            value={normalizeReindexIndexStrategy(value.reindexIndexStrategy)}
                            onChange={(strategy) => onChange({...value, reindexIndexStrategy: normalizeReindexIndexStrategy(strategy)})}
                            helptext={intl.formatMessage({defaultMessage: 'Dropping and rebuilding after bulk load is much faster on large databases, but semantic search is unavailable until it finishes.'})}
                            options={[
                                {value: REINDEX_INDEX_STRATEGY.maintain, label: intl.formatMessage({defaultMessage: 'Maintain index during reindex (default)'})},
                                {value: REINDEX_INDEX_STRATEGY.defer, label: intl.formatMessage({defaultMessage: 'Drop and rebuild index after reindex (faster for large databases)'})},
                            ]}
                        />
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
                        hasLocalRetentionWiden={hasLocalRetentionWiden}
                        hasUnsavedRetentionWiden={hasUnsavedRetentionWiden}
                        hasLocalRetentionTighten={hasLocalRetentionTighten}
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
        </AdminPanel>
    );
};

export default EmbeddingSearchPanel;
