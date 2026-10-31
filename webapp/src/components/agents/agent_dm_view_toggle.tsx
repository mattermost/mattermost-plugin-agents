// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';
import CreationOutlineIcon from '@mattermost/compass-icons/components/creation-outline';

import {GlobalState} from '@mattermost/types/store';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {isCurrentAgentDMShownAs, setAgentDMRawView} from '@/agent_dm_view';

// Channel header icon (registerChannelHeaderIcon): in an agent DM the user switched to plain messages,
// the way back to the agent chat view. Renders nothing anywhere else. The host gives bot DMs no
// channel menu, so this is where the switch back lives.
const AgentDMViewToggle = () => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const channelId = useSelector<GlobalState, string>((state) => state.entities.channels.currentChannelId);
    const shownAsMessages = useSelector<GlobalState, boolean>((state) => isCurrentAgentDMShownAs(state, true));

    if (!shownAsMessages) {
        return null;
    }

    const label = intl.formatMessage({defaultMessage: 'View as agent chat'});
    return (
        <WithTooltip title={label}>
            <IconButton
                size='small'
                icon={<Icon glyph={<CreationOutlineIcon/>}/>}
                aria-label={label}
                data-testid='agent-dm-view-as-chat'
                onClick={() => dispatch(setAgentDMRawView(channelId, false))}
            />
        </WithTooltip>
    );
};

export default AgentDMViewToggle;
