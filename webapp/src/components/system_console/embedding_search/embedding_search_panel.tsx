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

import {EmbeddingSearchConfig} from './types';
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
                title={
                    <Horizontal>
                        <FormattedMessage defaultMessage='Embedding Search'/>
                        <Pill><FormattedMessage defaultMessage='EXPERIMENTAL'/></Pill>
                    </Horizontal>
                }
                subtitle={''}
            >
                <EnterpriseChip
                    text={intl.formatMessage({defaultMessage: 'Embeddings search is available on Enterprise plans'})}
                    subtext={intl.formatMessage({defaultMessage: 'Embeddings search is available on Enterprise plans'})}
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
                                    minChunkSize: 0.75,
                                    chunkingStrategy: 'sentences',
                                },
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
                                    minChunkSize: 0.75,
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
