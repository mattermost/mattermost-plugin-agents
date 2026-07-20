// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {
    ChevronDownIcon,
    LightningBoltOutlineIcon,
    MagnifyIcon,
} from '@mattermost/compass-icons/components';

import {getAgents, getServices} from '@/client';
import {PrimaryButton} from '@/components/assets/buttons';
import {ServiceInfo, UserAgent} from '@/types/agents';

import Avatar from './avatar';

type Props = {
    onSelect: (agent: UserAgent) => void;
    disabled?: boolean;
};

function formatPickerMeta(
    agent: UserAgent,
    services: ServiceInfo[],
    formatMessage: ReturnType<typeof useIntl>['formatMessage'],
): string {
    const service = services.find((s) => s.id === agent.serviceID);
    const model = agent.model || service?.defaultModel || service?.name || '';
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

    if (model) {
        return `${model} • ${mcpLabel} • ${toolsLabel}`;
    }
    return `${mcpLabel} • ${toolsLabel}`;
}

const NewAutomationAgentMenu = ({onSelect, disabled = false}: Props) => {
    const intl = useIntl();
    const containerRef = useRef<HTMLDivElement>(null);
    const searchInputRef = useRef<HTMLInputElement>(null);
    const [open, setOpen] = useState(false);
    const [agents, setAgents] = useState<UserAgent[]>([]);
    const [services, setServices] = useState<ServiceInfo[]>([]);
    const [loading, setLoading] = useState(false);
    const [searchQuery, setSearchQuery] = useState('');

    useEffect(() => {
        if (!open) {
            return () => {
                // No outside-click listener while closed
            };
        }
        const handler = (e: MouseEvent) => {
            if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
                setOpen(false);
            }
        };
        document.addEventListener('mousedown', handler);
        return () => document.removeEventListener('mousedown', handler);
    }, [open]);

    useEffect(() => {
        if (!open) {
            return () => {
                // No outside-click listener while closed
            };
        }
        let cancelled = false;
        const load = async () => {
            setLoading(true);
            try {
                const [agentsResult, servicesResult] = await Promise.all([
                    getAgents(),
                    getServices().catch(() => [] as ServiceInfo[]),
                ]);
                if (!cancelled) {
                    setAgents(agentsResult.agents);
                    setServices(servicesResult);
                }
            } catch {
                if (!cancelled) {
                    setAgents([]);
                }
            } finally {
                if (!cancelled) {
                    setLoading(false);
                }
            }
        };
        load();
        return () => {
            cancelled = true;
        };
    }, [open]);

    useEffect(() => {
        if (open) {
            searchInputRef.current?.focus();
        } else {
            setSearchQuery('');
        }
    }, [open]);

    const filteredAgents = useMemo(() => {
        const query = searchQuery.trim().toLowerCase();
        if (!query) {
            return agents;
        }
        return agents.filter((agent) => (
            agent.displayName.toLowerCase().includes(query) ||
            agent.name.toLowerCase().includes(query) ||
            agent.customInstructions.toLowerCase().includes(query)
        ));
    }, [agents, searchQuery]);

    const handleToggle = useCallback(() => {
        if (disabled) {
            return;
        }
        setOpen((prev) => !prev);
    }, [disabled]);

    const handleSelect = useCallback((agent: UserAgent) => {
        setOpen(false);
        onSelect(agent);
    }, [onSelect]);

    const handleSearchChange = useCallback((e: React.ChangeEvent<HTMLInputElement>) => {
        setSearchQuery(e.target.value);
    }, []);

    return (
        <Container ref={containerRef}>
            <CreateButton
                type='button'
                onClick={handleToggle}
                disabled={disabled}
                aria-expanded={open}
                aria-haspopup='listbox'
            >
                <LightningBoltOutlineIcon size={16}/>
                <FormattedMessage defaultMessage='New automation'/>
                <ChevronDownIcon size={16}/>
            </CreateButton>
            {open && (
                <Dropdown role='listbox'>
                    <DropdownHeader>
                        <FormattedMessage defaultMessage='Choose an agent to run the automation'/>
                    </DropdownHeader>
                    <SearchWrapper>
                        <SearchIconWrapper>
                            <MagnifyIcon size={16}/>
                        </SearchIconWrapper>
                        <SearchInput
                            ref={searchInputRef}
                            type='text'
                            placeholder={intl.formatMessage({defaultMessage: 'Search agents'})}
                            value={searchQuery}
                            onChange={handleSearchChange}
                        />
                    </SearchWrapper>
                    <OptionsList>
                        {loading && (
                            <StatusMessage>
                                <FormattedMessage defaultMessage='Loading agents...'/>
                            </StatusMessage>
                        )}
                        {!loading && filteredAgents.length === 0 && (
                            <StatusMessage>
                                {searchQuery.trim() ? (
                                    <FormattedMessage defaultMessage='No agents match your search'/>
                                ) : (
                                    <FormattedMessage defaultMessage='No agents available'/>
                                )}
                            </StatusMessage>
                        )}
                        {!loading && filteredAgents.map((agent) => (
                            <AgentOption
                                key={agent.id}
                                agent={agent}
                                meta={formatPickerMeta(agent, services, intl.formatMessage)}
                                onSelect={handleSelect}
                            />
                        ))}
                    </OptionsList>
                </Dropdown>
            )}
        </Container>
    );
};

type AgentOptionProps = {
    agent: UserAgent;
    meta: string;
    onSelect: (agent: UserAgent) => void;
};

const AgentOption = ({agent, meta, onSelect}: AgentOptionProps) => {
    const handleClick = useCallback(() => {
        onSelect(agent);
    }, [agent, onSelect]);

    // TODO: For now, agents don't have a description, so we're using the custom instructions as a fallback.
    // This has to be changed before merging.
    return (
        <Option
            type='button'
            role='option'
            onClick={handleClick}
        >
            <Avatar userId={agent.botUserID ?? ''}/>
            <OptionText>
                <OptionName>{agent.displayName}</OptionName>
                {agent.customInstructions && (
                    <OptionDescription>{agent.customInstructions}</OptionDescription>
                )}
                <OptionMeta>{meta}</OptionMeta>
            </OptionText>
        </Option>
    );
};

const Container = styled.div`
    position: relative;
    flex-shrink: 0;
`;

const CreateButton = styled(PrimaryButton)`
    gap: 6px;
    height: 40px;
`;

const Dropdown = styled.div`
    position: absolute;
    top: calc(100% + 4px);
    right: 0;
    z-index: 20;
    display: flex;
    flex-direction: column;
    width: 360px;
    max-height: 420px;
    background: var(--center-channel-bg);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.12);
    overflow: hidden;
`;

const DropdownHeader = styled.div`
    padding: 12px 16px 8px;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const SearchWrapper = styled.div`
    position: relative;
    margin: 0 12px 8px;
`;

const SearchIconWrapper = styled.div`
    position: absolute;
    top: 50%;
    left: 10px;
    transform: translateY(-50%);
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    pointer-events: none;
`;

const SearchInput = styled.input`
    width: 100%;
    height: 32px;
    padding: 0 10px 0 32px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-size: 14px;

    &::placeholder {
        color: rgba(var(--center-channel-color-rgb), 0.56);
    }

    &:focus {
        outline: none;
        border-color: var(--button-bg);
        box-shadow: inset 0 0 0 1px var(--button-bg);
    }
`;

const OptionsList = styled.div`
    display: flex;
    flex-direction: column;
    overflow-y: auto;
    padding-bottom: 4px;
`;

const StatusMessage = styled.div`
    padding: 16px;
    text-align: center;
    font-size: 13px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const Option = styled.button`
    display: flex;
    align-items: flex-start;
    gap: 10px;
    width: 100%;
    padding: 10px 16px;
    border: none;
    background: transparent;
    cursor: pointer;
    text-align: left;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const OptionText = styled.div`
    display: flex;
    flex-direction: column;
    gap: 2px;
    min-width: 0;
    flex: 1;
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

const OptionDescription = styled.div`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    display: -webkit-box;
    -webkit-line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
`;

const OptionMeta = styled.div`
    font-size: 11px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

export default NewAutomationAgentMenu;
