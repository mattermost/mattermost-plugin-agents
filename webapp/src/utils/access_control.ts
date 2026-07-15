// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useEffect, useState} from 'react';

import {getABACStatus} from '@/client/access_control';
import type {AccessControlCELEditorComponent, AccessControlTableEditorComponent} from '@/types/access_control_editors';

// The host webapp exports the access-control editors on window.Components as
// React.lazy components (contract §6.1). Older webapps lack the exports: all
// ABAC UI must then be hidden.

export type AccessControlEditors = {
    TableEditor: AccessControlTableEditorComponent;
    CELEditor: AccessControlCELEditorComponent;
};

type WindowComponents = {
    AccessControlTableEditor?: AccessControlTableEditorComponent;
    AccessControlCELEditor?: AccessControlCELEditorComponent;
};

// isValidMattermostId mirrors the server's model.IsValidId: exactly 26
// alphanumeric characters, any case (the server checks unicode
// letters/numbers over byte length; for the ASCII ids that reach us the two
// are equivalent, and minted ids are always lowercase base-36). Resources
// with hand-crafted legacy IDs (e.g. a service id set via a raw config PUT
// before server-side minting) fail this and can never carry an access
// policy — the PDP short-circuits them to no_policy — so the policy UI must
// not offer authoring for them. Anything the server WOULD accept must pass
// here, or the UI would hide the editor for a policy-addressable resource.
export function isValidMattermostId(id: string): boolean {
    return (/^[a-zA-Z0-9]{26}$/).test(id);
}

// getAccessControlEditors feature-detects the host webapp's editor exports.
// Returns null on older webapps (hide all ABAC UI).
export function getAccessControlEditors(): AccessControlEditors | null {
    const components = (window as unknown as {Components?: WindowComponents}).Components;
    if (!components?.AccessControlTableEditor || !components?.AccessControlCELEditor) {
        return null;
    }
    return {
        TableEditor: components.AccessControlTableEditor,
        CELEditor: components.AccessControlCELEditor,
    };
}

// Module-level promise cache so the surfaces consuming useABACSupport (agent
// access tab, service panel, MCP panel) share one status request.
let statusPromise: Promise<boolean> | null = null;

function fetchAvailability(): Promise<boolean> {
    if (!statusPromise) {
        statusPromise = getABACStatus().
            then((status) => status.available).
            catch(() => {
                // Server unreachable / non-200 → unsupported; do not cache the
                // failure so a later mount can retry.
                statusPromise = null;
                return false;
            });
    }
    return statusPromise;
}

// resetABACSupportCacheForTesting clears the module-level status cache.
export function resetABACSupportCacheForTesting() {
    statusPromise = null;
}

export type ABACSupport = {
    supported: boolean;
    loading: boolean;
};

// useABACSupport reports whether the ABAC UI should render: host editors
// present AND the server reports the ABAC engine available.
export function useABACSupport(): ABACSupport {
    const editorsPresent = getAccessControlEditors() !== null;
    const [available, setAvailable] = useState<boolean | null>(null);

    useEffect(() => {
        if (!editorsPresent) {
            return () => { /* nothing to cancel */ };
        }
        let cancelled = false;
        fetchAvailability().then((result) => {
            if (!cancelled) {
                setAvailable(result);
            }
        });
        return () => {
            cancelled = true;
        };
    }, [editorsPresent]);

    if (!editorsPresent) {
        return {supported: false, loading: false};
    }
    return {supported: available === true, loading: available === null};
}
