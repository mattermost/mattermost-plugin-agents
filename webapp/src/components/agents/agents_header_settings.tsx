// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import SettingsOutlineIcon from '@mattermost/compass-icons/components/settings-outline';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

// The host's global header renders its own Settings button only for the Channels product
// and gives other products just their `headerRightComponent`, so the Agents product
// supplies one that opens the same user settings modal through the published plugin API.
// Hosts without `openUserSettings` simply don't get the button.
const AgentsHeaderSettings = () => {
    const intl = useIntl();
    const openUserSettings = window.WebappUtils?.openUserSettings;

    if (!openUserSettings) {
        return null;
    }

    return (
        <IconButton
            size='small'
            // eslint-disable-next-line react/style-prop-object -- compass IconButton `style` is a variant prop, not CSS
            style='inverted'
            icon={<Icon glyph={<SettingsOutlineIcon/>}/>}
            aria-label={intl.formatMessage({defaultMessage: 'Settings'})}
            aria-haspopup='dialog'
            onClick={() => openUserSettings({isContentProductSettings: true})}
        />
    );
};

export default AgentsHeaderSettings;
