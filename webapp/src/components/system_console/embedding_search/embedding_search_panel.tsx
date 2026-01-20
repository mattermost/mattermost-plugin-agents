// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl, FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import {useIsBasicsLicensed} from '@/license';

import {Pill} from '../../pill';
import EnterpriseChip from '../enterprise_chip';
import Panel from '../panel';
import {ItemList, SelectionItem, SelectionItemOption} from '../item';
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

    const {
        jobStatus,
        statusMessage,
        showReindexConfirmation,
        healthCheckResult,
        healthCheckLoading,
        modelCompatibility,
        isJobStale,
        incrementalStats,
        handleReindexClick,
        handleConfirmReindex,
        handleCancelReindex,
        handleCancelJob,
        handleCatchUpClick,
        handleHealthCheck,
        handleResetStaleJob,
        handleResetIncrementalStats,
    } = useJobStatus();

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
                <SelectionItem
                    label={intl.formatMessage({defaultMessage: 'Type'})}
                    value={value.type}
                    onChange={(e) => {
                        const newType = e.target.value;
                        if (newType === '') {
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
                        } else if (value.type === '') {
                            // Set defaults when enabling
                            onChange({
                                type: newType,
                                vectorStore: {type: 'pgvector', parameters: {}},
                                embeddingProvider: {type: 'openai', parameters: {embeddingModel: '', apiKey: ''}},
                                parameters: {},
                                dimensions: 0,
                                chunkingOptions: {
                                    chunkSize: 1000,
                                    chunkOverlap: 200,
                                    minChunkSize: 0.75,
                                    chunkingStrategy: 'sentences',
                                },
                            });
                        } else {
                            onChange({...value, type: newType});
                        }
                    }}
                >
                    <SelectionItemOption value=''>{'Disabled'}</SelectionItemOption>
                    <SelectionItemOption value='composite'>{'Composite'}</SelectionItemOption>
                </SelectionItem>

                {value.type && value.type !== '' &&
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

                {value.type && value.type !== '' &&
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

                {value.type && value.type !== '' && value.embeddingProvider.type === 'openai' && (
                    <OpenAIProviderConfig
                        value={value.embeddingProvider}
                        onChange={(config) => onChange({...value, embeddingProvider: config})}
                    />
                )}

                {value.type && value.type !== '' && value.embeddingProvider.type === 'openai-compatible' && (
                    <OpenAICompatibleProviderConfig
                        value={value.embeddingProvider}
                        onChange={(config) => onChange({...value, embeddingProvider: config})}
                    />
                )}

                {value.type === 'composite' && (
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
                            min={0}
                            helptext={intl.formatMessage({defaultMessage: 'The number of dimensions for the vector embeddings. Common values are 768, 1024, or 1536 depending on the model.'})}
                        />

                        <ChunkingOptionsConfig
                            value={value}
                            onChange={onChange}
                        />
                    </>
                )}

                {value.type && value.type !== '' && (
                    <ReindexSection
                        jobStatus={jobStatus}
                        statusMessage={statusMessage}
                        healthCheckResult={healthCheckResult}
                        healthCheckLoading={healthCheckLoading}
                        modelCompatibility={modelCompatibility}
                        isJobStale={isJobStale}
                        incrementalStats={incrementalStats}
                        onReindexClick={handleReindexClick}
                        onCancelJob={handleCancelJob}
                        onCatchUpClick={handleCatchUpClick}
                        onHealthCheck={handleHealthCheck}
                        onResetStaleJob={handleResetStaleJob}
                        onResetIncrementalStats={handleResetIncrementalStats}
                    />
                )}
            </ItemList>

            <ReindexConfirmation
                show={showReindexConfirmation}
                onConfirm={handleConfirmReindex}
                onCancel={handleCancelReindex}
            />
        </Panel>
    );
};

export default EmbeddingSearchPanel;
