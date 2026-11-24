// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';

import ConfirmationDialog from '../../confirmation_dialog';

interface ReindexConfirmationProps {
    show: boolean;
    onConfirm: () => void;
    onCancel: () => void;
    embeddingProviderType: string;
}

export const ReindexConfirmation = ({show, onConfirm, onCancel, embeddingProviderType}: ReindexConfirmationProps) => {
    return (
        <ConfirmationDialog
            show={show}
            title={<FormattedMessage defaultMessage='Confirm Reindexing'/>}
            message={
                <>
                    <p>
                        <FormattedMessage defaultMessage='Are you sure you want to reindex all posts?'/>
                    </p>
                    <p>
                        <FormattedMessage defaultMessage='This clears the index and rebuilds it from scratch:'/>
                    </p>
                    <ul>
                        <li><FormattedMessage defaultMessage='Re-embeds all existing posts'/></li>
                        <li><FormattedMessage defaultMessage='Takes significant time on large workspaces'/></li>
                        <li><FormattedMessage defaultMessage='Increases database load while running'/></li>
                        {(embeddingProviderType === 'openai' || embeddingProviderType === 'openai-compatible') && (
                            <li><FormattedMessage defaultMessage='Incurs embedding provider API costs'/></li>
                        )}
                    </ul>
                </>
            }
            confirmButtonText={<FormattedMessage defaultMessage='Reindex'/>}
            onConfirm={onConfirm}
            onCancel={onCancel}
        />
    );
};

interface RebuildVectorIndexConfirmationProps {
    show: boolean;
    onConfirm: () => void;
    onCancel: () => void;
}

export const RebuildVectorIndexConfirmation = ({show, onConfirm, onCancel}: RebuildVectorIndexConfirmationProps) => {
    return (
        <ConfirmationDialog
            show={show}
            title={<FormattedMessage defaultMessage='Confirm vector index rebuild'/>}
            message={
                <>
                    <p>
                        <FormattedMessage defaultMessage='Rebuild the vector index with the current HNSW M setting? This does not re-embed posts.'/>
                    </p>
                    <p>
                        <FormattedMessage defaultMessage='Semantic search is unavailable until the rebuild finishes. On large databases this can take a while.'/>
                    </p>
                </>
            }
            confirmButtonText={<FormattedMessage defaultMessage='Rebuild vector index'/>}
            onConfirm={onConfirm}
            onCancel={onCancel}
        />
    );
};