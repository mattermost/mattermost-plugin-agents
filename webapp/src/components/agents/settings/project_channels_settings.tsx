// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';

import {Tag} from '@mattermost/compass-ui/components/tag';

import {BooleanItem, SelectionItem, TextItem} from '@/components/system_console/item';
import {PluginConfig} from '@/components/system_console/plugin_config_types';

type Props = {
    value: PluginConfig;
    updateConfig: (updates: Partial<PluginConfig>) => void;
};

// The settings of project channels: the switch, and the model new projects' agents start with.
const ProjectChannelsSettings = ({value, updateConfig}: Props) => {
    const intl = useIntl();
    const services = value.services ?? [];
    const serviceID = value.projectDefaultServiceID ?? '';
    const serviceMissing = Boolean(serviceID) && !services.some((service) => service.id === serviceID);

    return (
        <>
            <BooleanItem
                label={
                    <Horizontal>
                        <FormattedMessage defaultMessage='Enable project channels'/>
                        <Tag
                            type='info'
                            label={<FormattedMessage defaultMessage='EXPERIMENTAL'/>}
                        />
                    </Horizontal>
                }
                value={Boolean(value.enableProjectChannels)}
                onChange={(to) => {
                    updateConfig({enableProjectChannels: to});
                }}
                helpText={intl.formatMessage({defaultMessage: 'Adds a Project option to the Create a new channel dialog. Each project channel is led by its own agent, which keeps a board, a checklist and artifacts next to the channel and works only inside the project. Requires a default service below, ambient auto-replies, channel mention tool calling, and HTML and channel artifacts.'})}
            />
            {Boolean(value.enableProjectChannels) && (
                <>
                    <SelectionItem
                        label={intl.formatMessage({defaultMessage: 'Project agent service'})}
                        value={serviceID}
                        placeholder={intl.formatMessage({defaultMessage: 'Choose a service'})}
                        error={serviceMissing ? intl.formatMessage({defaultMessage: 'The selected service no longer exists. Project channels stay off until you choose another.'}) : ''}
                        onChange={(projectDefaultServiceID) => {
                            updateConfig({projectDefaultServiceID});
                        }}
                        options={services.map((service) => ({value: service.id, label: service.name || service.id}))}
                        helptext={intl.formatMessage({defaultMessage: 'The service that powers the agent of every new project. Required.'})}
                    />
                    <TextItem
                        label={intl.formatMessage({defaultMessage: 'Project agent model'})}
                        value={value.projectDefaultModel ?? ''}
                        onChange={(e) => updateConfig({projectDefaultModel: e.target.value})}
                        helptext={intl.formatMessage({defaultMessage: 'Optional. Leave empty to use the service\'s default model.'})}
                    />
                </>
            )}
        </>
    );
};

export default ProjectChannelsSettings;

const Horizontal = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: var(--spacing-xs, 8px);
`;
