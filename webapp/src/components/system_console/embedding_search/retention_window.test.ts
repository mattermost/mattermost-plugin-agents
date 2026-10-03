// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {retentionWindowTightened, retentionWindowWidened} from './retention_window';

describe('retention window comparison', () => {
    it('missing stored does not nag', () => {
        expect(retentionWindowWidened(730)).toBe(false);
        expect(retentionWindowTightened(730)).toBe(false);
    });

    it('365 to 730 is a widen', () => {
        expect(retentionWindowWidened(730, 365)).toBe(true);
        expect(retentionWindowTightened(730, 365)).toBe(false);
    });

    it('365 to all posts is a widen', () => {
        expect(retentionWindowWidened(0, 365)).toBe(true);
        expect(retentionWindowTightened(0, 365)).toBe(false);
    });

    it('730 to 365 is a tighten', () => {
        expect(retentionWindowWidened(365, 730)).toBe(false);
        expect(retentionWindowTightened(365, 730)).toBe(true);
    });

    it('all posts to 365 is a tighten', () => {
        expect(retentionWindowWidened(365, 0)).toBe(false);
        expect(retentionWindowTightened(365, 0)).toBe(true);
    });

    it('same window is neither', () => {
        expect(retentionWindowWidened(365, 365)).toBe(false);
        expect(retentionWindowTightened(365, 365)).toBe(false);
    });
});
