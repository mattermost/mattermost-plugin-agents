// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export interface PluginRegistry {
    registerPostTypeComponent(typeName: string, component: React.ElementType)

    // Add more if needed from https://developers.mattermost.com/extend/plugins/webapp/reference
}

type HostMenuItemProps = {
    labels: React.ReactElement;
    leadingElement?: React.ReactNode;
    onClick?: (event?: React.SyntheticEvent) => void;
    disabled?: boolean;
}

type HostMenuComponents = {
    Item?: React.ComponentType<HostMenuItemProps>;
    Separator?: React.ComponentType<Record<string, never>>;
}

// Global type definitions
declare global {
    interface Window {
        Components?: {
            Menu?: HostMenuComponents;
        };
        WebappUtils?: {
            sendWebSocketMessage: (msg: {
                action: string;
                seq: number;
                data: {
                    data: string;
                    [key: string]: any;
                };
            }) => void;
            browserHistory?: {
                push: (path: string) => void;
            };
        };
    }
}
