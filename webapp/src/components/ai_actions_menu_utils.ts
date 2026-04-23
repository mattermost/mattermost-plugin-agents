// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {ComponentType, ReactElement, ReactNode, SyntheticEvent} from 'react';

export type HostMenuItemProps = {
    labels: ReactElement;
    leadingElement?: ReactNode;
    onClick?: (event?: SyntheticEvent) => void;
    disabled?: boolean;
};

export type HostMenuComponents = {
    Item?: ComponentType<HostMenuItemProps>;
    Separator?: ComponentType<Record<string, never>>;
};

export function getHostMenuComponents(): HostMenuComponents | undefined {
    return window.Components?.Menu;
}

export function dismissLegacyMenu() {
    document.getElementById('backdropForMenuComponent')?.click();
}
