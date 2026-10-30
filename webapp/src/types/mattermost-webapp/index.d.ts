// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {UnknownAction} from 'redux';

import type {Channel} from '@mattermost/types/channels';

// Props the host passes a registerChannelViewComponent component.
export interface ChannelViewComponentProps {
    channel: Channel;
    channelId: string;
    teamId: string;
    focusedPostId?: string;
}

export interface PluginRegistry {
    registerPostTypeComponent(typeName: string, component: React.ElementType)

    // Replaces the center of the channels whose matcher returns true (the post list and composer;
    // the host keeps the channel header). Absent on servers before it shipped. The matcher runs on
    // every store update while a channel is open. Returns a registration id.
    registerChannelViewComponent?(registration: {
        matcher: (state: any, channel: Channel) => boolean;
        component: React.ComponentType<ChannelViewComponentProps>;
    }): string;

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
