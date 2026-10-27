// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useDispatch} from 'react-redux';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import SettingsOutlineIcon from '@mattermost/compass-icons/components/settings-outline';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

// The host lays the Channels header out as [Settings][account menu] in one container, and the
// account menu button carries its own 8px start margin. A product's headerRightComponent is a
// sibling of that container in a flex row that also has an 8px gap, so the button would sit 16px
// from the avatar instead of 8px. Pull it back by the gap (the host sets the value, there is
// no way to configure it from a plugin).
const SettingsButton = styled(IconButton)`
    margin-inline-end: calc(-1 * var(--spacing-xs));
`;

// The host's global header renders its own Settings button only for the Channels product
// and gives other products just their `headerRightComponent`, so the Agents product
// supplies one that opens the same user settings modal through the published plugin API.
// `openUserSettings` returns a Redux action (the host's `openModal(...)`), it does not open
// anything by itself, so the result has to be dispatched. Hosts without it simply don't get
// the button.
const AgentsHeaderSettings = () => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const openUserSettings = window.WebappUtils?.openUserSettings;

    if (!openUserSettings) {
        return null;
    }

    return (
        <SettingsButton
            size='small'
            // eslint-disable-next-line react/style-prop-object -- compass IconButton `style` is a variant prop, not CSS
            style='inverted'
            icon={<Icon glyph={<SettingsOutlineIcon/>}/>}
            aria-label={intl.formatMessage({defaultMessage: 'Settings'})}
            aria-haspopup='dialog'
            onClick={() => dispatch(openUserSettings({isContentProductSettings: true}))}
        />
    );
};

export default AgentsHeaderSettings;
