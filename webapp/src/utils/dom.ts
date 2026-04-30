// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/**
 * Returns the element to portal floating UI (e.g. react-select menus,
 * dropdown popovers) into.
 *
 * Prefer `#root` so portaled content inherits the Mattermost theme CSS
 * variables scoped there, same as the main webapp. Falls back to
 * `document.body` if `#root` is not present (should only happen in
 * unusual test setups). Returns `null` in non-browser environments (SSR).
 */
export function getPortalTarget(): HTMLElement | null {
    if (typeof document === 'undefined') {
        return null;
    }
    return document.getElementById('root') ?? document.body;
}
