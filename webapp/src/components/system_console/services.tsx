// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {PlusIcon} from '@mattermost/compass-icons/components';
import {FormattedMessage, useIntl} from 'react-intl';

import {TertiaryButton} from '../assets/buttons';
import ConfirmationDialog from '../confirmation_dialog';

import Service, {LLMService} from './service';
import {LLMBotConfig} from './bot';

const defaultNewService: LLMService = {
    id: '',
    name: '',
    type: 'openai',
    apiKey: '',
    apiURL: '',
    orgId: '',
    defaultModel: '',
    tokenLimit: 0,
    streamingTimeoutSeconds: 0,
    outputTokenLimit: 0,
    useResponsesAPI: true,
    region: '',
    awsAccessKeyID: '',
    awsSecretAccessKey: '',
    vertexProjectID: '',
    vertexProjectNumber: '',
    vertexAuthCredentials: '',
    fallbackServiceID: '',
};

export const firstNewService = {
    ...defaultNewService,
    name: 'OpenAI Service',
};

type Props = {
    services: LLMService[]
    bots: LLMBotConfig[]
    onChange: (services: LLMService[]) => void
}

const Services = (props: Props) => {
    const intl = useIntl();
    const [showErrorDialog, setShowErrorDialog] = useState(false);
    const [errorMessage, setErrorMessage] = useState('');

    // No id is assigned client-side: the backend mints the stable ID on save
    // (normalizeAdminConfig); policy authoring needs a persisted id.
    const addNewService = (e: React.MouseEvent<HTMLButtonElement>) => {
        e.preventDefault();
        if (props.services.length === 0) {
            props.onChange([{...firstNewService}]);
        } else {
            props.onChange([...props.services, {...defaultNewService}]);
        }
    };

    // Entries added this session have no id yet, so services are addressed by
    // index rather than by id.
    const onChange = (index: number, newService: LLMService) => {
        props.onChange(props.services.map((s, i) => (i === index ? newService : s)));
    };

    const onDelete = (index: number) => {
        const id = props.services[index].id;

        // Only persisted services can be referenced by bots or as fallbacks.
        if (id) {
            const botsUsingService = props.bots.filter((bot) => bot.serviceID === id);

            if (botsUsingService.length > 0) {
                const botNames = botsUsingService.map((bot) => bot.displayName).join(', ');
                const message = intl.formatMessage(
                    {defaultMessage: 'Cannot delete this service because it is being used by the following bot(s): {botNames}'},
                    {botNames},
                );
                setErrorMessage(message);
                setShowErrorDialog(true);
                return;
            }
        }

        // Drop the service and clear any remaining service's fallback link to it,
        // so deletion never leaves a dangling fallbackServiceID behind.
        const remaining = props.services.
            filter((_, i) => i !== index).
            map((s) => (id && s.fallbackServiceID === id ? {...s, fallbackServiceID: ''} : s));
        props.onChange(remaining);
    };

    return (
        <>
            <ServicesList>
                {props.services.map((service, index) => (
                    <Service
                        key={service.id || `unsaved-${index}`}
                        service={service}
                        services={props.services}
                        onChange={(updated) => onChange(index, updated)}
                        onDelete={() => onDelete(index)}
                    />
                ))}
            </ServicesList>
            <TertiaryButton onClick={addNewService} >
                <PlusAIServiceIcon/>
                <FormattedMessage defaultMessage='Add an AI Service'/>
            </TertiaryButton>
            <ConfirmationDialog
                show={showErrorDialog}
                title={<FormattedMessage defaultMessage='Cannot Delete Service'/>}
                message={errorMessage}
                confirmButtonText={<FormattedMessage defaultMessage='OK'/>}
                onConfirm={() => setShowErrorDialog(false)}
                onCancel={() => setShowErrorDialog(false)}
            />
        </>
    );
};

const PlusAIServiceIcon = styled(PlusIcon)`
	width: 18px;
	height: 18px;
	margin-right: 8px;
`;

const ServicesList = styled.div`
	display: flex;
	flex-direction: column;
	gap: 12px;

	padding-bottom: 24px;
`;

export default Services;
