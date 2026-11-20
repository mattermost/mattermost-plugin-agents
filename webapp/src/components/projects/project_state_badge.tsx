// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';

import {Tag} from '@mattermost/compass-ui/components/tag';

import type {ProjectState} from '@/types/projects';

type Props = {
    state: ProjectState;
};

const ProjectStateBadge = ({state}: Props) => {
    switch (state) {
    case 'active':
        return (
            <Tag
                type='success'
                label={<FormattedMessage defaultMessage='Active'/>}
                data-testid='project-state-badge'
            />
        );
    case 'ending':
        return (
            <Tag
                type='warning'
                label={<FormattedMessage defaultMessage='Ending'/>}
                data-testid='project-state-badge'
            />
        );
    default:
        return (
            <Tag
                type='default'
                label={<FormattedMessage defaultMessage='Ended'/>}
                data-testid='project-state-badge'
            />
        );
    }
};

export default ProjectStateBadge;
