// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import {useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {listChannelArtifacts} from '@/client';
import {getChannelArtifactsEnabled} from '@/selectors';

/** Live library size for the Channel Info menu trailing badge. */
const ChannelArtifactsCountBadge = () => {
    const enabled = useSelector(getChannelArtifactsEnabled);
    const channelId = useSelector((state: GlobalState) => state.entities.channels.currentChannelId);
    const [count, setCount] = useState<number | null>(null);

    useEffect(() => {
        if (!enabled || !channelId) {
            setCount(null);
            return undefined; // eslint-disable-line no-undefined
        }
        let cancelled = false;
        listChannelArtifacts(channelId).then((resp) => {
            if (!cancelled) {
                setCount((resp.entries || []).length);
            }
        }, () => {
            if (!cancelled) {
                setCount(null);
            }
        });
        return () => {
            cancelled = true;
        };
    }, [enabled, channelId]);

    if (count === null) {
        return null;
    }
    return <>{count}</>;
};

export default ChannelArtifactsCountBadge;
