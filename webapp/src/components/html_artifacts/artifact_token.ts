// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

const NONCE_BYTES = 16;

// Identifies one host load of an artifact frame: the server only serves the
// artifact for a well-formed nonce and binds the bridge token to it, so every
// load (mount, Reload, Retry) must use a new one. 16 random bytes, base64url
// without padding: 22 characters.
export function newArtifactNonce(): string {
    const bytes = crypto.getRandomValues(new Uint8Array(NONCE_BYTES));
    const binary = String.fromCharCode(...bytes);
    return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/[=]+$/, '');
}
