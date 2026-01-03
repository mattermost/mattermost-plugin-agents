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
    sendUserId: false,
    outputTokenLimit: 0,
    useResponsesAPI: false,
    region: '',
    awsAccessKeyID: '',
    awsSecretAccessKey: '',
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

    const addNewService = (e: React.MouseEvent<HTMLButtonElement>) => {
        e.preventDefault();
        const id = crypto.randomUUID();
        if (props.services.length === 0) {
            props.onChange([{
                ...firstNewService,
                id,
            }]);
        } else {
            props.onChange([...props.services, {
                ...defaultNewService,
                id,
            }]);
        }
    };

    const onChange = (newService: LLMService) => {
        props.onChange(props.services.map((b) => (b.id === newService.id ? newService : b)));
    };

    const onDelete = (id: string) => {
        // Check if any bot is using this service
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

        props.onChange(props.services.filter((b) => b.id !== id));
    };

    return (
        <>
            <ServicesList>
                {props.services.map((service) => (
                    <Service
                        key={service.id}
                        service={service}
                        onChange={onChange}
                        onDelete={() => onDelete(service.id)}
                    />
                ))}
            </ServicesList>
            <TertiaryButton onClick={addNewService} >
                <PlusAIServiceIcon/>
                <FormattedMessage defaultMessage='Add an AI Service'/>
            </TertiaryButton>
            {showErrorDialog && (
                <ConfirmationDialog
                    title={<FormattedMessage defaultMessage='Cannot Delete Service'/>}
                    message={errorMessage}
                    confirmButtonText={<FormattedMessage defaultMessage='OK'/>}
                    onConfirm={() => setShowErrorDialog(false)}
                    onCancel={() => setShowErrorDialog(false)}
                />
            )}
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
