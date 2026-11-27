// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {useChannelLibrary} from './library';

/** Live library size for the Channel Info menu trailing badge. */
const ChannelArtifactsCountBadge = () => {
    const channelId = useSelector((state: GlobalState) => state.entities.channels.currentChannelId);
    const library = useChannelLibrary(channelId);

    if (library.status !== 'ready') {
        return null;
    }
    return <>{library.entries.length}</>;
};

export default ChannelArtifactsCountBadge;
