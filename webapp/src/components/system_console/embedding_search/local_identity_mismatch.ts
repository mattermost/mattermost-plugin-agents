// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {normalizeVectorElementType} from './types';

export type StoredEmbeddingIdentity = {
    stored_provider_type?: string;
    stored_dimensions?: number;
    stored_model_name?: string;
    stored_vector_element_type?: string;
};

export type CurrentEmbeddingIdentity = {
    providerType: string;
    dimensions: number;
    modelName: string;
    vectorElementType: string;
};

export type EmbeddingIdentityMismatchKind = 'provider' | 'dimensions' | 'vectorElementType' | 'model';

// Mirrors indexer.CheckModelCompatibility field order: provider, dimensions,
// vector element type, model. Missing stored fields are not a mismatch
// (upgrade / partial ModelInfo). Empty stored vector element type on an
// existing index is treated as "vector" (pre-halfvec indexes).
export function embeddingIdentityMismatchKind(
    stored: StoredEmbeddingIdentity | null | undefined,
    current: CurrentEmbeddingIdentity,
): EmbeddingIdentityMismatchKind | null {
    if (!stored) {
        return null;
    }

    const storedProvider = stored.stored_provider_type ?? '';
    if (storedProvider && current.providerType && storedProvider !== current.providerType) {
        return 'provider';
    }

    const storedDimensions = stored.stored_dimensions ?? 0;
    if (storedDimensions > 0 && current.dimensions !== storedDimensions) {
        return 'dimensions';
    }

    // Same "no stored info" gate as CheckModelCompatibility: empty type is
    // only defaulted to vector when an index identity already exists.
    const hasStoredIdentity = storedDimensions > 0 || Boolean(stored.stored_model_name);
    if (hasStoredIdentity) {
        const storedElementType = normalizeVectorElementType(stored.stored_vector_element_type);
        const currentElementType = normalizeVectorElementType(current.vectorElementType);
        if (storedElementType !== currentElementType) {
            return 'vectorElementType';
        }
    }

    const storedModelName = stored.stored_model_name ?? '';
    if (storedModelName && current.modelName && current.modelName !== storedModelName) {
        return 'model';
    }

    return null;
}
