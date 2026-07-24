// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/** Resource _meta.ui.prefersBorder defaults to bordered when omitted. */
export function prefersAppBorder(prefersBorder: boolean | undefined): boolean {
    return prefersBorder !== false;
}
