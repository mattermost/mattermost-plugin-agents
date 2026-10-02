// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {PlusIcon} from '@mattermost/compass-icons/components';
import React from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {EmptyState} from '@mattermost/compass-ui/components/empty-state';
import {Icon} from '@mattermost/compass-ui/components/icon';

import SparklesGraphic from 'src/components/assets/sparkles_graphic';

type Props = {
    onAddServicePressed: () => void;
};

const NoServicesPage = (props: Props) => {
    const intl = useIntl();
    return (
        <Card>
            <EmptyState
                illustration={{children: <SparklesGraphic/>, 'aria-label': ''}}
                title={intl.formatMessage({defaultMessage: 'No AI services added yet'})}
                description={intl.formatMessage({defaultMessage: 'To get started with Agents, add an AI service'})}
                action={{
                    children: intl.formatMessage({defaultMessage: 'Add an AI Service'}),
                    leadingIcon: <Icon glyph={<PlusIcon/>}/>,
                    onClick: props.onAddServicePressed,
                }}
            />
        </Card>
    );
};

const Card = styled.div`
	padding: var(--spacing-xxxl) var(--spacing-xxxl) 56px;
	border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
	border-radius: var(--radius-s);
	background: var(--center-channel-bg);
	box-shadow: var(--elevation-1);
`;

export default NoServicesPage;
