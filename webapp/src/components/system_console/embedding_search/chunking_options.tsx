// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';

import {SelectionItem, SelectionItemOption} from '../item';
import {IntItem, FloatItem} from '../number_items';

import {ChunkingOptions, EmbeddingSearchConfig} from './types';

interface ChunkingOptionsProps {
    value: EmbeddingSearchConfig;
    onChange: (config: EmbeddingSearchConfig) => void;
}

export const ChunkingOptionsConfig = ({value, onChange}: ChunkingOptionsProps) => {
    const intl = useIntl();

    // Define default chunking options in one place to maintain consistency
    const defaultChunkingOptions = {
        chunkSize: 1000,
        chunkOverlap: 200,
        minChunkSize: 0.75,
        chunkingStrategy: 'sentences',
    };

    return (
        <>
            <SelectionItem
                label={intl.formatMessage({defaultMessage: 'Chunking Strategy'})}
                value={value.chunkingOptions?.chunkingStrategy || defaultChunkingOptions.chunkingStrategy}
                onChange={(e) => onChange({
                    ...value,
                    chunkingOptions: {
                        ...(value.chunkingOptions || defaultChunkingOptions),
                        chunkingStrategy: e.target.value,
                    } as ChunkingOptions,
                })}
                helptext={intl.formatMessage({defaultMessage: 'The strategy to use for splitting text into chunks.'})}
            >
                <SelectionItemOption value='sentences'>{'Sentences'}</SelectionItemOption>
                <SelectionItemOption value='paragraphs'>{'Paragraphs'}</SelectionItemOption>
                <SelectionItemOption value='fixed'>{'Fixed Size'}</SelectionItemOption>
            </SelectionItem>

            <IntItem
                label={intl.formatMessage({defaultMessage: 'Chunk Size'})}
                placeholder={defaultChunkingOptions.chunkSize.toString()}
                value={value.chunkingOptions?.chunkSize || defaultChunkingOptions.chunkSize}
                onChange={(chunkSize) => {
                    onChange({
                        ...value,
                        chunkingOptions: {
                            ...(value.chunkingOptions || defaultChunkingOptions),
                            chunkSize,
                        } as ChunkingOptions,
                    });
                }}
                min={1}
                helptext={intl.formatMessage({defaultMessage: 'Maximum size of each chunk in characters.'})}
            />

            <IntItem
                label={intl.formatMessage({defaultMessage: 'Chunk Overlap'})}
                placeholder={defaultChunkingOptions.chunkOverlap.toString()}
                value={value.chunkingOptions?.chunkOverlap || defaultChunkingOptions.chunkOverlap}
                onChange={(chunkOverlap) => {
                    onChange({
                        ...value,
                        chunkingOptions: {
                            ...(value.chunkingOptions || defaultChunkingOptions),
                            chunkOverlap,
                        } as ChunkingOptions,
                    });
                }}
                min={0}
                helptext={intl.formatMessage({defaultMessage: 'Number of characters to overlap between chunks.'})}
            />

            <FloatItem
                label={intl.formatMessage({defaultMessage: 'Minimum Chunk Size Ratio'})}
                placeholder={defaultChunkingOptions.minChunkSize.toString()}
                value={value.chunkingOptions?.minChunkSize || defaultChunkingOptions.minChunkSize}
                onChange={(minChunkSize) => {
                    onChange({
                        ...value,
                        chunkingOptions: {
                            ...(value.chunkingOptions || defaultChunkingOptions),
                            minChunkSize,
                        } as ChunkingOptions,
                    });
                }}
                min={0}
                max={1}
                helptext={intl.formatMessage({defaultMessage: 'Minimum chunk size as a fraction of the maximum size (0.0-1.0). Used for sentence and paragraph chunking.'})}
            />
        </>
    );
};
