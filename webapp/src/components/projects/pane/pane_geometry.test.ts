// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {isChannelRoute} from './browser_location';
import {PANE_MIN_WIDTH, POST_COLUMN_MIN_WIDTH, clampPaneWidth, effectivePaneMode} from './pane_geometry';

describe('clampPaneWidth', () => {
    test.each([
        {name: 'keeps a width that fits', available: 1600, desired: 700, want: 700},
        {name: 'has no cap besides the messages column', available: 2400, desired: 1900, want: 1900},
        {name: 'leaves the messages their minimum', available: 1200, desired: 1100, want: 1200 - POST_COLUMN_MIN_WIDTH},
        {name: 'never goes below the pane minimum', available: 1600, desired: 100, want: PANE_MIN_WIDTH},
        {name: 'keeps the pane minimum when nothing fits', available: 500, desired: 480, want: PANE_MIN_WIDTH},
        {name: 'has no cap before the column is measured', available: 0, desired: 900, want: 900},
    ])('$name', ({available, desired, want}) => {
        expect(clampPaneWidth(available, desired)).toBe(want);
    });
});

describe('effectivePaneMode', () => {
    test.each([
        {name: 'split fits', chosen: 'split' as const, available: 745, viewport: 1280, want: 'split'},
        {name: 'split doesn\'t fit beside the messages', chosen: 'split' as const, available: 744, viewport: 1280, want: 'pane'},
        {name: 'phone layout', chosen: 'split' as const, available: 900, viewport: 767, want: 'pane'},
        {name: 'expanded stays expanded', chosen: 'pane' as const, available: 1600, viewport: 1600, want: 'pane'},
        {name: 'full window stays full window when narrow', chosen: 'fullscreen' as const, available: 600, viewport: 700, want: 'fullscreen'},
    ])('$name', ({chosen, available, viewport, want}) => {
        expect(effectivePaneMode(chosen, available, viewport)).toBe(want);
    });
});

describe('isChannelRoute', () => {
    const original = window.basename;
    afterEach(() => {
        window.basename = original;
    });

    test.each([
        {name: 'the channel view', path: '/eng/channels/launch', want: true},
        {name: 'a permalink in the channel', path: '/eng/channels/launch/abcdefghijklmnopqrstuvwxyz', want: true},
        {name: 'an encoded name', path: '/eng/channels/la%75nch', want: true},
        {name: 'another channel', path: '/eng/channels/other', want: false},
        {name: 'another team', path: '/ops/channels/launch', want: false},
        {name: 'Threads keeps the channel selected', path: '/eng/threads', want: false},
        {name: 'a thread in Threads', path: '/eng/threads/abcdefghijklmnopqrstuvwxyz', want: false},
        {name: 'a direct message route', path: '/eng/messages/@someone', want: false},
    ])('$name', ({path, want}) => {
        expect(isChannelRoute(path, 'eng', 'launch')).toBe(want);
    });

    test('ignores the server subpath', () => {
        window.basename = '/mm';
        expect(isChannelRoute('/mm/eng/channels/launch', 'eng', 'launch')).toBe(true);
    });
});
