// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type React from 'react';

import type {Channel} from '@mattermost/types/channels';
import type {GlobalState} from '@mattermost/types/store';

export const MAX_CHANNEL_INSTRUCTIONS = 16384;
export const MAX_CHANNEL_KNOWLEDGE_FILES = 10;

export type ChannelKnowledgeFile = {
    id: string;
    name: string;
    mimeType: string;
    size: number;
};

export type ChannelContextState = {
    customInstructions: string;
    files: ChannelKnowledgeFile[];
};

export type ChannelContextUpdate = {
    customInstructions: string;
    fileIDs: string[];
};

export type ChannelSettingsTabHandlers = {
    save: () => Promise<void>;
    reset: () => void;
};

export type ChannelSettingsTabBodyProps = {
    channel: Channel;
    setUnsaved: (unsaved: boolean) => void;
    registerHandlers: (handlers: ChannelSettingsTabHandlers | null) => void;
};

export type ChannelSettingsTabRegistration = {
    uiName: string;
    icon?: string;
    shouldRender?: (state: GlobalState, channel: Channel) => boolean;
    component: React.ComponentType<ChannelSettingsTabBodyProps>;
};
