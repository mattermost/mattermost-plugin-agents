// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {renderHook} from '@testing-library/react';
import {Provider} from 'react-redux';
import {createStore} from 'redux';

import {useIsMultiLLMLicensed} from './license';

// Render the hook against a minimal Redux store with a license whose
// SkuShortName matches the named tier and dev-mode toggles off, so the
// result reflects only the license tier under test.
function renderWithLicense(skuShortName: string | null) {
    const license: Record<string, string> = {};
    if (skuShortName !== null) {
        license.SkuShortName = skuShortName;
    }

    const state = {
        entities: {
            general: {
                license,
                config: {
                    EnableTesting: 'false',
                    EnableDeveloper: 'false',
                },
            },
        },
    };
    const store = createStore((s = state) => s);

    const wrapper = ({children}: {children: React.ReactNode}) => (
        <Provider store={store}>{children}</Provider>
    );

    return renderHook(() => useIsMultiLLMLicensed(), {wrapper});
}

describe('useIsMultiLLMLicensed', () => {
    // MM-69186: Mattermost Professional advertises "Bring-Your-Own & Multi-LLM
    // Integration" and "Interactive AI Bot Support" on the Pro pricing page,
    // so a Pro license must clear this gate (which controls the Agents page
    // and the "Add an AI Bot" System Console action).
    const cases: Array<{name: string; sku: string | null; want: boolean}> = [
        {name: 'no SkuShortName', sku: null, want: false},
        {name: 'professional', sku: 'professional', want: true},
        {name: 'entry', sku: 'entry', want: true},
        {name: 'enterprise', sku: 'enterprise', want: true},
        {name: 'advanced (Enterprise Advanced)', sku: 'advanced', want: true},
    ];

    test.each(cases)('returns $want for $name', ({sku, want}) => {
        const {result} = renderWithLicense(sku);
        expect(result.current).toBe(want);
    });
});
