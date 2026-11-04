// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {viewVersionAfterRefresh} from './html_artifact_fullscreen';

describe('viewVersionAfterRefresh', () => {
    test.each([
        {name: 'follows the new head', current: 2, previousHead: 2, head: 3, listed: [1, 2, 3], want: 3},
        {name: 'keeps an older version the viewer chose', current: 1, previousHead: 2, head: 3, listed: [1, 2, 3], want: 1},
        {name: 'falls back when the viewed version was pruned', current: 1, previousHead: 2, head: 4, listed: [3, 4], want: 4},
    ])('$name', ({current, previousHead, head, listed, want}) => {
        expect(viewVersionAfterRefresh(current, previousHead, head, listed)).toBe(want);
    });
});
