// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {generateId} from './id';

const MATTERMOST_ID_PATTERN = /^[ybndrfg8ejkmcpqxot1uwisza345h769]{26}$/;

describe('generateId', () => {
    it('returns 26 chars from the Mattermost base32 alphabet', () => {
        const id = generateId();
        expect(id).toHaveLength(26);
        expect(id).toMatch(MATTERMOST_ID_PATTERN);
    });

    it('returns distinct values across 1000 calls', () => {
        const ids = new Set<string>();
        for (let i = 0; i < 1000; i++) {
            ids.add(generateId());
        }
        expect(ids.size).toBe(1000);
    });
});
