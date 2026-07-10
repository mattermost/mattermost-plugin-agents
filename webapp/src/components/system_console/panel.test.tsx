// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render, screen} from '@testing-library/react';

import Panel from './panel';

describe('Panel', () => {
    test('applies an optional stable test id to the panel container', () => {
        const {rerender} = render(
            <Panel
                title='Web Search'
                subtitle='Search settings'
                testId='system-console-web-search-panel'
            >
                {'Panel content'}
            </Panel>,
        );

        expect(screen.getByTestId('system-console-web-search-panel').textContent).toContain('Panel content');

        rerender(
            <Panel
                title='Web Search'
                subtitle='Search settings'
            >
                {'Panel content'}
            </Panel>,
        );

        expect(screen.queryByTestId('system-console-web-search-panel')).toBeNull();
    });
});
