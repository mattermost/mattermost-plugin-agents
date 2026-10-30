// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {UnknownAction} from 'redux';

export interface PluginRegistry {
    registerPostTypeComponent(typeName: string, component: React.ElementType)

    // Add more if needed from https://developers.mattermost.com/extend/plugins/webapp/reference
}

// Global type definitions
declare global {
    interface Window {
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

            // Published host API; absent on older hosts. Returns the Redux action that opens the
            // modal: the caller must dispatch it.
            openUserSettings?: (dialogProps: {isContentProductSettings: boolean}) => UnknownAction;
        };
    }
}
