// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {embeddingIdentityMismatchKind} from './local_identity_mismatch';

describe('embeddingIdentityMismatchKind', () => {
    const current = {
        providerType: 'openai',
        dimensions: 1536,
        modelName: 'text-embedding-3-small',
        vectorElementType: 'vector',
    };

    it.each([
        {
            name: 'provider-only change',
            stored: {
                stored_provider_type: 'openai',
                stored_dimensions: 1536,
                stored_model_name: 'text-embedding-3-small',
            },
            current: {...current, providerType: 'anthropic'},
            want: 'provider' as const,
        },
        {
            name: 'dimension change',
            stored: {
                stored_provider_type: 'openai',
                stored_dimensions: 768,
                stored_model_name: 'text-embedding-3-small',
            },
            current,
            want: 'dimensions' as const,
        },
        {
            name: 'vector element type change',
            stored: {
                stored_provider_type: 'openai',
                stored_dimensions: 1536,
                stored_model_name: 'text-embedding-3-small',
                stored_vector_element_type: 'vector',
            },
            current: {...current, vectorElementType: 'halfvec'},
            want: 'vectorElementType' as const,
        },
        {
            name: 'model name change',
            stored: {
                stored_provider_type: 'openai',
                stored_dimensions: 1536,
                stored_model_name: 'text-embedding-ada-002',
            },
            current,
            want: 'model' as const,
        },
        {
            name: 'matching identity',
            stored: {
                stored_provider_type: 'openai',
                stored_dimensions: 1536,
                stored_model_name: 'text-embedding-3-small',
                stored_vector_element_type: 'vector',
            },
            current,
            want: null,
        },
        {
            name: 'missing stored fields are not a mismatch',
            stored: {},
            current,
            want: null,
        },
        {
            name: 'null stored is not a mismatch',
            stored: null,
            current,
            want: null,
        },
    ])('$name', ({stored, current: cur, want}) => {
        expect(embeddingIdentityMismatchKind(stored, cur)).toBe(want);
    });
});
