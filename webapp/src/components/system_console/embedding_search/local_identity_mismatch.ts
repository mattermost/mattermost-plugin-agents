// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export type StoredEmbeddingIdentity = {
    stored_provider_type?: string;
    stored_dimensions?: number;
    stored_model_name?: string;
};

export type CurrentEmbeddingIdentity = {
    providerType: string;
    dimensions: number;
    modelName: string;
};

export type EmbeddingIdentityMismatchKind = 'provider' | 'dimensions' | 'model';

// Mirrors indexer.CheckModelCompatibility field order: provider, dimensions, model.
// Missing stored fields are not a mismatch (upgrade / partial ModelInfo).
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

    const storedModelName = stored.stored_model_name ?? '';
    if (storedModelName && current.modelName && current.modelName !== storedModelName) {
        return 'model';
    }

    return null;
}
