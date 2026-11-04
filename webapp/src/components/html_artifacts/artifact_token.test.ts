// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {newArtifactNonce} from './artifact_token';

describe('newArtifactNonce', () => {
    test('is 22 base64url characters and differs on every call', () => {
        const nonces = Array.from({length: 200}, () => newArtifactNonce());
        for (const nonce of nonces) {
            expect(nonce).toMatch(/^[A-Za-z0-9_-]{22}$/);
        }
        expect(new Set(nonces).size).toBe(nonces.length);
    });

    test.each([
        {name: 'all zero bytes', fill: 0x00, expected: 'AAAAAAAAAAAAAAAAAAAAAA'},
        {name: 'all 0xff bytes use the URL-safe alphabet', fill: 0xff, expected: '_____________________w'},
        {name: '0xfb bytes map 62 to "-"', fill: 0xfb, expected: '-_v7-_v7-_v7-_v7-_v7-w'},
    ])('encodes $name', ({fill, expected}) => {
        const spy = jest.spyOn(crypto, 'getRandomValues').mockImplementation(<T extends ArrayBufferView | null>(array: T): T => {
            (array as unknown as Uint8Array).fill(fill);
            return array;
        });
        try {
            expect(newArtifactNonce()).toBe(expected);
        } finally {
            spy.mockRestore();
        }
    });
});
