// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';
import {MagnifyIcon} from '@mattermost/compass-icons/components';

import {GlobalState} from '@mattermost/types/store';

import {UserProfile} from '@mattermost/types/users';

import AutomationConfigView from './automation_config_view';

import AutomationRow from './automation_row';

import FilterTabs, {OwnershipFilter} from './filter_tabs';

import NewAutomationAgentMenu from './new_automation_agent_menu';

import {
    createAutomation,
    deleteAutomation as deleteAutomationAPI,
    getAgents,
    getAutomations,
    getProfilesByIds,
    updateAutomation,
} from '@/client';
import {UserAgent} from '@/types/agents';
import {
    Automation,
    AutomationUpdate,
    getAIPromptAction,
    getTriggerType,
} from '@/types/automations';

function triggerLabelForAutomation(
    automation: Automation,
    formatMessage: (descriptor: {defaultMessage: string}, values?: Record<string, string>) => string,
): string {
    const triggerType = getTriggerType(automation.trigger);
    switch (triggerType) {
    case 'message_posted':
        return formatMessage({defaultMessage: 'When a message is posted'});
    case 'membership_changed':
        return formatMessage({defaultMessage: 'When someone joins a channel'});
    case 'channel_created':
        return formatMessage({defaultMessage: 'When a new channel is created'});
    case 'user_joined_team':
        return formatMessage({defaultMessage: 'When someone joins a team'});
    case 'schedule': {
        const interval = automation.trigger.schedule?.interval || '';
        if (interval === '1h') {
            return formatMessage({defaultMessage: 'Hourly'});
        }
        if (interval === '24h') {
            return formatMessage({defaultMessage: 'Daily'});
        }
        if (interval === '168h') {
            return formatMessage({defaultMessage: 'Weekly'});
        }
        return formatMessage(
            {defaultMessage: 'Every {interval}'},
            {interval},
        );
    }
    default:
        return '';
    }
}

type ViewMode = 'create' | 'edit';

function buildNewAutomation(agent: UserAgent, name: string, createdBy: string): Automation {
    return {
        id: '',
        name,
        enabled: true,
        trigger: {
            schedule: {
                channel_id: '',
                interval: '24h',
            },
        },
        actions: [
            {
                id: 'run-agent',
                ai_prompt: {
                    prompt: '',
                    provider_type: 'agent',
                    provider_id: agent.id,
                },
            },
        ],
        created_at: 0,
        updated_at: 0,
        created_by: createdBy,
    };
}

const AutomationsList = () => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const profiles = useSelector((state: GlobalState) => state.entities.users.profiles);
    const [activeTab, setActiveTab] = useState<OwnershipFilter>('all');
    const [searchQuery, setSearchQuery] = useState('');
    const [automations, setAutomations] = useState<Automation[]>([]);
    const [agentsById, setAgentsById] = useState<Map<string, UserAgent>>(new Map());
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [viewMode, setViewMode] = useState<ViewMode>('edit');
    const [editingAutomation, setEditingAutomation] = useState<Automation | null>(null);

    const fetchAutomations = useCallback(async () => {
        try {
            setLoading(true);
            setError(null);
            const [result, agentsResult] = await Promise.all([
                getAutomations(),
                getAgents().catch(() => ({agents: [] as UserAgent[]})),
            ]);
            setAutomations(result);
            setAgentsById(new Map(agentsResult.agents.map((agent) => [agent.id, agent])));
        } catch {
            setError(intl.formatMessage({defaultMessage: 'Failed to load automations.'}));
        } finally {
            setLoading(false);
        }
    }, [intl]);

    useEffect(() => {
        fetchAutomations();
    }, [fetchAutomations]);

    useEffect(() => {
        const missingIds = [...new Set(
            automations.
                map((item) => item.created_by).
                filter((id): id is string => Boolean(id) && !profiles[id]),
        )];
        if (missingIds.length === 0) {
            return () => {
                // No profiles to fetch
            };
        }

        let cancelled = false;
        getProfilesByIds(missingIds).
            then((fetched) => {
                if (cancelled || fetched.length === 0) {
                    return;
                }
                const profilesById = fetched.reduce<Record<string, UserProfile>>((acc, profile) => {
                    acc[profile.id] = profile;
                    return acc;
                }, {});
                dispatch({
                    type: 'RECEIVED_PROFILES',
                    data: profilesById,
                });
            }).
            catch(() => {
                // Leave unresolved creators blank rather than showing user ids.
            });

        return () => {
            cancelled = true;
        };
    }, [automations, profiles, dispatch]);

    const creatorUsername = useCallback((createdBy: string) => {
        if (!createdBy) {
            return '';
        }
        if (createdBy === currentUserId) {
            return intl.formatMessage({defaultMessage: 'You'});
        }
        return profiles[createdBy]?.username || '';
    }, [currentUserId, intl, profiles]);

    const filteredAutomations = useMemo(() => {
        return automations.filter((item) => {
            if (activeTab === 'yours' && item.created_by !== currentUserId) {
                return false;
            }
            if (searchQuery.trim()) {
                const query = searchQuery.toLowerCase();
                const agentId = getAIPromptAction(item)?.provider_id || '';
                const agentName = agentsById.get(agentId)?.displayName || '';
                const creatorName = creatorUsername(item.created_by);
                return item.name.toLowerCase().includes(query) ||
                    agentName.toLowerCase().includes(query) ||
                    creatorName.toLowerCase().includes(query) ||
                    triggerLabelForAutomation(item, intl.formatMessage).toLowerCase().includes(query);
            }
            return true;
        });
    }, [automations, activeTab, searchQuery, intl, agentsById, currentUserId, creatorUsername]);

    const handleToggle = useCallback(async (id: string, enabled: boolean) => {
        const existing = automations.find((a) => a.id === id);
        if (!existing) {
            return;
        }
        try {
            const updated = await updateAutomation(id, {
                name: existing.name,
                enabled,
                trigger: existing.trigger,
                actions: existing.actions,
            });
            setAutomations((prev) => prev.map((item) => (
                item.id === id ? updated : item
            )));
        } catch {
            setError(intl.formatMessage({defaultMessage: 'Failed to update automation.'}));
        }
    }, [automations, intl]);

    const handleEdit = useCallback((item: Automation) => {
        setViewMode('edit');
        setEditingAutomation(item);
    }, []);

    const handleDelete = useCallback(async (item: Automation) => {
        try {
            await deleteAutomationAPI(item.id);
            setAutomations((prev) => prev.filter((a) => a.id !== item.id));
        } catch {
            setError(intl.formatMessage({defaultMessage: 'Failed to delete automation.'}));
        }
    }, [intl]);

    const handleCreate = useCallback((agent: UserAgent) => {
        setViewMode('create');
        setEditingAutomation(buildNewAutomation(
            agent,
            intl.formatMessage({defaultMessage: 'New automation'}),
            currentUserId,
        ));
    }, [intl, currentUserId]);

    const handleViewBack = useCallback(() => {
        setEditingAutomation(null);
    }, []);

    const handleViewSaved = useCallback(async (update: AutomationUpdate) => {
        if (!editingAutomation) {
            return;
        }
        try {
            if (viewMode === 'create') {
                const created = await createAutomation(update);
                setAutomations((prev) => [...prev, created]);
            } else {
                const updated = await updateAutomation(editingAutomation.id, update);
                setAutomations((prev) => prev.map((item) => (
                    item.id === editingAutomation.id ? updated : item
                )));
            }
            setEditingAutomation(null);
        } catch {
            setError(intl.formatMessage(
                viewMode === 'create' ? {defaultMessage: 'Failed to create automation.'} : {defaultMessage: 'Failed to save automation.'},
            ));
        }
    }, [editingAutomation, intl, viewMode]);

    if (editingAutomation) {
        return (
            <AutomationConfigView
                automation={editingAutomation}
                onBack={handleViewBack}
                onSaved={handleViewSaved}
            />
        );
    }

    return (
        <Container>
            <Header>
                <Title>
                    <FormattedMessage defaultMessage='Automations'/>
                </Title>
                <Subtitle>
                    <FormattedMessage defaultMessage='Scheduled and event-driven automations for your channels'/>
                </Subtitle>
            </Header>

            <Toolbar>
                <FilterTabs
                    value={activeTab}
                    onChange={setActiveTab}
                />

                <SearchInputWrapper>
                    <SearchIconWrapper>
                        <MagnifyIcon size={18}/>
                    </SearchIconWrapper>
                    <SearchInput
                        type='text'
                        placeholder={intl.formatMessage({defaultMessage: 'Search automations'})}
                        value={searchQuery}
                        onChange={(e) => setSearchQuery(e.target.value)}
                    />
                </SearchInputWrapper>

                <NewAutomationAgentMenu
                    onSelect={handleCreate}
                    disabled={loading}
                />
            </Toolbar>

            {loading && (
                <LoadingContainer>
                    <FormattedMessage defaultMessage='Loading automations...'/>
                </LoadingContainer>
            )}

            {error && (
                <ErrorContainer>{error}</ErrorContainer>
            )}

            {!loading && !error && filteredAutomations.length === 0 && searchQuery.trim() && (
                <NoResultsMessage>
                    <FormattedMessage
                        defaultMessage='No automations match "{query}"'
                        values={{query: searchQuery}}
                    />
                </NoResultsMessage>
            )}

            {!loading && !error && filteredAutomations.length === 0 && !searchQuery.trim() && (
                <EmptyState>
                    {activeTab === 'yours' ? (
                        <FormattedMessage defaultMessage="You haven't created any automations yet."/>
                    ) : (
                        <FormattedMessage defaultMessage='No automations have been created yet.'/>
                    )}
                </EmptyState>
            )}

            {!loading && !error && filteredAutomations.length > 0 && (
                <ListContainer>
                    {filteredAutomations.map((item) => {
                        const agentId = getAIPromptAction(item)?.provider_id || '';
                        const agent = agentsById.get(agentId);
                        return (
                            <AutomationRow
                                key={item.id}
                                item={item}
                                agentDisplayName={agent?.displayName}
                                agentUserId={agent?.botUserID}
                                creatorDisplayName={creatorUsername(item.created_by)}
                                triggerLabel={triggerLabelForAutomation(item, intl.formatMessage)}
                                onToggle={handleToggle}
                                onEdit={handleEdit}
                                onDelete={handleDelete}
                            />
                        );
                    })}
                </ListContainer>
            )}
        </Container>
    );
};

const Container = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    overflow-y: auto;

    scrollbar-width: thin;
    scrollbar-color: transparent transparent;

    &::-webkit-scrollbar {
        width: 8px;
    }

    &::-webkit-scrollbar-thumb {
        border-radius: 4px;
        background-color: transparent;
    }

    &:hover {
        scrollbar-color: rgba(var(--center-channel-color-rgb), 0.24) transparent;
    }

    &:hover::-webkit-scrollbar-thumb {
        background-color: rgba(var(--center-channel-color-rgb), 0.24);
    }
`;

const Header = styled.div`
    display: flex;
    flex-direction: column;
    gap: 4px;
    padding: 48px 0 24px;
`;

const Title = styled.h1`
    font-family: 'Metropolis', sans-serif;
    font-size: 22px;
    font-weight: 600;
    line-height: 28px;
    color: var(--center-channel-color);
    margin: 0;
`;

const Subtitle = styled.p`
    font-family: 'Open Sans', sans-serif;
    font-size: 12px;
    font-weight: 400;
    line-height: 20px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
    margin: 0;
`;

const Toolbar = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 16px;
    padding-bottom: 16px;
`;

const SearchInputWrapper = styled.div`
    position: relative;
    flex: 1;
    min-width: 0;
    height: 40px;
`;

const SearchIconWrapper = styled.div`
    position: absolute;
    top: 50%;
    left: 12px;
    transform: translateY(-50%);
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    pointer-events: none;
`;

const SearchInput = styled.input`
    width: 100%;
    height: 40px;
    padding: 0 12px 0 38px;
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

const ListContainer = styled.div`
    display: flex;
    flex-direction: column;
`;

const LoadingContainer = styled.div`
    display: flex;
    justify-content: center;
    padding: 40px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const ErrorContainer = styled.div`
    display: flex;
    align-items: center;
    padding: 10px 12px;
    background: rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.08);
    border-radius: 4px;
    border: 1px solid rgba(var(--dnd-indicator-rgb, 210, 75, 78), 0.3);
    color: var(--dnd-indicator, #D24B4E);
`;

const NoResultsMessage = styled.div`
    padding: 24px;
    text-align: center;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 14px;
`;

const EmptyState = styled.div`
    display: flex;
    justify-content: center;
    padding: 60px 20px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 14px;
`;

export default AutomationsList;
