// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useMemo} from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';

import {UserAgent} from '@/types/agents';
import {ChannelAccessLevel} from '@/components/system_console/bot';
import {BaseSelectOption, SingleSelect} from '@/components/select';

import Avatar from './avatar';

type AgentOption = BaseSelectOption & {
    agent: UserAgent;
};

type Props = {
    agents: UserAgent[];
    value: string;
    onChange: (agent: UserAgent) => void;
    loading?: boolean;
};

export function formatAgentMeta(agent: UserAgent, formatMessage: ReturnType<typeof useIntl>['formatMessage']): string {
    const tools = agent.enabledMCPTools ?? [];
    const mcpCount = new Set(tools.map((t) => t.server_origin)).size;

    const mcpLabel = formatMessage(
        {defaultMessage: '{count} MCP'},
        {count: mcpCount},
    );
    const toolsLabel = agent.autoEnableNewMCPTools ? formatMessage({defaultMessage: 'All tools'}) : formatMessage(
        {defaultMessage: '{count, plural, one {# tool} other {# tools}}'},
        {count: tools.length},
    );
    const channelsLabel = channelAccessLabel(agent, formatMessage);

    return `${mcpLabel} · ${toolsLabel} · ${channelsLabel}`;
}

function channelAccessLabel(agent: UserAgent, formatMessage: ReturnType<typeof useIntl>['formatMessage']): string {
    switch (agent.channelAccessLevel) {
    case ChannelAccessLevel.Allow: {
        const count = agent.channelIDs?.length ?? 0;
        return formatMessage(
            {defaultMessage: '{count, plural, one {# channel} other {# channels}}'},
            {count},
        );
    }
    case ChannelAccessLevel.None:
        return formatMessage({defaultMessage: 'No channels'});
    case ChannelAccessLevel.Block:
    case ChannelAccessLevel.All:
    default:
        return formatMessage({defaultMessage: 'All channels'});
    }
}

const AgentSelector = ({agents, value, onChange, loading = false}: Props) => {
    const intl = useIntl();

    const options = useMemo((): AgentOption[] => (
        agents.map((agent) => ({
            value: agent.id,
            label: agent.displayName,
            agent,
        }))
    ), [agents]);

    const selected = useMemo(
        () => options.find((option) => option.value === value) ?? null,
        [options, value],
    );

    const handleChange = useCallback((option: AgentOption | null) => {
        if (option) {
            onChange(option.agent);
        }
    }, [onChange]);

    const formatOptionLabel = useCallback((option: AgentOption) => (
        <OptionMain>
            <Avatar userId={option.agent.botUserID ?? ''}/>
            <OptionText>
                <OptionName>{option.agent.displayName}</OptionName>
                <OptionMeta>{formatAgentMeta(option.agent, intl.formatMessage)}</OptionMeta>
            </OptionText>
        </OptionMain>
    ), [intl.formatMessage]);

    return (
        <SingleSelect<AgentOption>
            value={selected}
            options={options}
            onChange={handleChange}
            formatOptionLabel={formatOptionLabel}
            placeholder={loading ?
                intl.formatMessage({defaultMessage: 'Loading agents...'}) :
                intl.formatMessage({defaultMessage: 'Select an agent'})
            }
            isDisabled={loading || agents.length === 0}
            isLoading={loading}
            aria-label={intl.formatMessage({defaultMessage: 'Agent'})}
        />
    );
};

const OptionMain = styled.div`
    display: flex;
    align-items: center;
    gap: 10px;
    min-width: 0;
`;

const OptionText = styled.div`
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
`;

const OptionName = styled.div`
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const OptionMeta = styled.div`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

export default AgentSelector;
