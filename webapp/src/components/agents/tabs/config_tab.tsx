// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';

import {getServices} from '@/client';
import {ServiceInfo} from '@/types/agents';
import {ItemList, TextItem, SelectionItem, SelectionItemOption} from '@/components/system_console/item';
import AvatarItem from '@/components/system_console/avatar';

import {AgentDraft} from '../agent_config_modal';

type Props = {
    draft: AgentDraft;
    onChange: (updates: Partial<AgentDraft>) => void;
    onAvatarChange: (file: File | null) => void;
    botUserId?: string;  // provided in edit mode — used for avatar display
    errors?: Record<string, string>;
}

const ConfigTab = (props: Props) => {
    const {draft, onChange, onAvatarChange, errors = {}} = props;
    const intl = useIntl();
    const [services, setServices] = useState<ServiceInfo[]>([]);

    // Fetch available services on mount
    useEffect(() => {
        const load = async () => {
            try {
                const result = await getServices();
                setServices(result || []);
            } catch {
                // Services will be empty — the SelectionItem will show "Select a service"
            }
        };
        load();
    }, []);

    return (
        <FormContainer>
            <ItemList>
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Display name'})}
                    value={draft.displayName}
                    onChange={(e) => onChange({displayName: e.target.value})}
                    placeholder={intl.formatMessage({defaultMessage: 'e.g. Sales Assistant'})}
                />
                {errors.displayName && <FieldError>{errors.displayName}</FieldError>}
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Agent username'})}
                    value={draft.username}
                    maxLength={22}
                    onChange={(e) => onChange({username: e.target.value})}
                    helptext={intl.formatMessage({defaultMessage: 'Users will mention this name to interact with the agent. Must start with a letter and contain only lowercase letters, numbers, dots, hyphens, or underscores.'})}
                />
                {errors.username && <FieldError>{errors.username}</FieldError>}
                <AvatarItem
                    botusername={draft.username}
                    changedAvatar={(image: File) => onAvatarChange(image)}
                />
                <SelectionItem
                    label={intl.formatMessage({defaultMessage: 'AI Service'})}
                    value={draft.serviceId}
                    onChange={(e) => onChange({serviceId: e.target.value})}
                >
                    <SelectionItemOption value=''>
                        {intl.formatMessage({defaultMessage: 'Select a service'})}
                    </SelectionItemOption>
                    {draft.serviceId && !services.find((s) => s.id === draft.serviceId) && (
                        <SelectionItemOption
                            value={draft.serviceId}
                            disabled={true}
                        >
                            {intl.formatMessage({defaultMessage: 'Unknown service (deleted)'})}
                        </SelectionItemOption>
                    )}
                    {services.map((svc) => (
                        <SelectionItemOption
                            key={svc.id}
                            value={svc.id}
                        >
                            {svc.name || svc.type}
                        </SelectionItemOption>
                    ))}
                </SelectionItem>
                {errors.serviceId && <FieldError>{errors.serviceId}</FieldError>}
                <TextItem
                    label={intl.formatMessage({defaultMessage: 'Custom instructions'})}
                    placeholder={intl.formatMessage({defaultMessage: 'How would you like the agent to respond?'})}
                    multiline={true}
                    value={draft.customInstructions}
                    onChange={(e) => onChange({customInstructions: e.target.value})}
                />
            </ItemList>
        </FormContainer>
    );
};

const FieldError = styled.div`
    color: var(--dnd-indicator, #D24B4E);
    font-size: 12px;
    margin-top: -8px;
`;

const FormContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 24px;
`;

export default ConfigTab;
