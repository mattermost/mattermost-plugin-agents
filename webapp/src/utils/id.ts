// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/**
 * Returns a RFC 4122 version 4 UUID.
 *
 * `crypto.randomUUID()` only exists in secure contexts (HTTPS or
 * localhost). Self-hosted Mattermost servers reached over plain HTTP on a
 * LAN are insecure contexts, where calling it throws
 * `TypeError: crypto.randomUUID is not a function` — which silently broke
 * every "add" button in the system console (issue #554). Fall back to
 * building the UUID from `crypto.getRandomValues()`, which is available in
 * insecure contexts too.
 */
export function generateId(): string {
    if (typeof crypto.randomUUID === 'function') {
        return crypto.randomUUID();
    }
    const bytes = crypto.getRandomValues(new Uint8Array(16));
    bytes[6] = (bytes[6] & 0x0f) | 0x40; // version 4
    bytes[8] = (bytes[8] & 0x3f) | 0x80; // RFC 4122 variant
    const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
    return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
