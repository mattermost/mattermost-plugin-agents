// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Mirrors server-side model.NewId(): 16 random bytes, base32-encoded with
// Mattermost's alphabet, truncated to 26 chars. Passes model.IsValidId.
const ID_ALPHABET = 'ybndrfg8ejkmcpqxot1uwisza345h769';

export function generateId(): string {
    const bytes = new Uint8Array(16);
    crypto.getRandomValues(bytes);
    let bits = 0;
    let value = 0;
    let out = '';
    for (const byte of bytes) {
        value = (value << 8) | byte;
        bits += 8;
        while (bits >= 5) {
            out += ID_ALPHABET[(value >>> (bits - 5)) & 31];
            bits -= 5;
        }
    }
    if (bits > 0) {
        out += ID_ALPHABET[(value << (5 - bits)) & 31];
    }
    return out.slice(0, 26);
}
