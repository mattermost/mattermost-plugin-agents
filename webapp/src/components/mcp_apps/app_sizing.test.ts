// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {clampAppHeight} from './app_sizing';

describe('clampAppHeight', () => {
    const cases: Array<{name: string; reported: number; viewport: number; want: number}> = [
        {name: 'below min', reported: 100, viewport: 1000, want: 160},
        {name: 'in range', reported: 400, viewport: 1000, want: 400},
        {name: 'above max', reported: 900, viewport: 1000, want: 700},
        {name: 'tiny viewport floors at min', reported: 900, viewport: 200, want: 160},
        {name: 'rounds fractional', reported: 400.6, viewport: 1000, want: 401},
    ];

    test.each(cases)('$name', ({reported, viewport, want}) => {
        expect(clampAppHeight(reported, viewport)).toBe(want);
    });
});
