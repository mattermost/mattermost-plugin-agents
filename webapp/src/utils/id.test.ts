// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {generateId} from './id';

const UUID_V4_RE = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

describe('generateId', () => {
    const originalRandomUUID = crypto.randomUUID;

    afterEach(() => {
        (crypto as any).randomUUID = originalRandomUUID;
    });

    it('uses crypto.randomUUID when available (secure contexts)', () => {
        (crypto as any).randomUUID = jest.fn(() => 'aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee');
        expect(generateId()).toBe('aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee');
        expect(crypto.randomUUID).toHaveBeenCalled();
    });

    it('falls back to getRandomValues in insecure contexts (issue #554)', () => {
        // Insecure contexts (plain-HTTP LAN deployments) have no randomUUID.
        delete (crypto as any).randomUUID;
        const id = generateId();
        expect(id).toMatch(UUID_V4_RE);
    });

    it('fallback produces unique well-formed ids', () => {
        delete (crypto as any).randomUUID;
        const ids = new Set(Array.from({length: 100}, () => generateId()));
        expect(ids.size).toBe(100);
        for (const id of ids) {
            expect(id).toMatch(UUID_V4_RE);
        }
    });
});
