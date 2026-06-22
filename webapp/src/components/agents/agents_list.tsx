// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector} from 'react-redux';
import {PlusIcon, MagnifyIcon} from '@mattermost/compass-icons/components';
//eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip} from 'react-bootstrap';

import {GlobalState} from '@mattermost/types/store';

import {getAgents, getServices, deleteAgent as deleteAgentAPI} from '@/client';
import {userHasSystemPermission} from '@/utils/permissions';
import {PrimaryButton} from '@/components/assets/buttons';
import {UserAgent, ServiceInfo} from '@/types/agents';
import {useIsMultiLLMLicensed} from '@/license';

import AgentRow from './agent_row';
import DeleteAgentDialog from './delete_agent_dialog';
import AgentConfigView from './agent_config_view';

type Tab = 'all' | 'yours';

// Keep in sync with api.FreeTierAgentLimit (api/api_agents.go).
const FREE_TIER_AGENT_LIMIT = 1;

const AgentsList = () => {
    const intl = useIntl();
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const hasManageOthersAgent = useSelector((state: GlobalState) =>
        userHasSystemPermission(state, currentUserId, 'manage_others_agent'));
    const hasManageOwnAgent = useSelector((state: GlobalState) =>
        userHasSystemPermission(state, currentUserId, 'manage_own_agent'));
    const hasManageSystem = useSelector((state: GlobalState) =>
        userHasSystemPermission(state, currentUserId, 'manage_system'));
    const userCanCreateAgent = hasManageOwnAgent || hasManageSystem;
    const multiLLMLicensed = useIsMultiLLMLicensed();

    const [agents, setAgents] = useState<UserAgent[]>([]);
    const [services, setServices] = useState<ServiceInfo[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [servicesError, setServicesError] = useState<string | null>(null);
    const [deleteInFlight, setDeleteInFlight] = useState(false);
    const [activeTab, setActiveTab] = useState<Tab>('all');
    const [deletingAgent, setDeletingAgent] = useState<UserAgent | null>(null);
    const [searchQuery, setSearchQuery] = useState('');
    const [viewOpen, setViewOpen] = useState(false);
    const [viewMode, setViewMode] = useState<'create' | 'edit'>('create');
    const [editingAgent, setEditingAgent] = useState<UserAgent | null>(null);
    const [activeAgentCount, setActiveAgentCount] = useState<number | null>(null);

    const serverAgentCount = activeAgentCount ?? agents.length;
    const createQuotaReached = !multiLLMLicensed && serverAgentCount >= FREE_TIER_AGENT_LIMIT;
    const createButtonDisabled = loading || createQuotaReached;

    const fetchAgents = useCallback(async () => {
        try {
            setLoading(true);
            setError(null);
            setServicesError(null);
            const agentResult = await getAgents();
            setAgents(agentResult.agents || []);
            setActiveAgentCount(agentResult.activeAgentCount ?? null);
            try {
                const serviceResult = await getServices();
                setServices(serviceResult || []);
            } catch {
                setServicesError(intl.formatMessage({defaultMessage: 'Failed to load AI services. Using the last loaded list.'}));
            }
        } catch (e: any) {
            setError(intl.formatMessage({defaultMessage: 'Failed to load agents.'}));
        } finally {
            setLoading(false);
        }
    }, [intl]);

    useEffect(() => {
        fetchAgents();
    }, [fetchAgents]);

    const handleEdit = useCallback((agent: UserAgent) => {
        setEditingAgent(agent);
        setViewMode('edit');
        setViewOpen(true);
    }, []);

    const handleDeleteRequest = useCallback((agent: UserAgent) => {
        setDeletingAgent(agent);
    }, []);

    const handleDeleteConfirm = useCallback(async () => {
        if (!deletingAgent || deleteInFlight) {
            return;
        }
        setDeleteInFlight(true);
        try {
            await deleteAgentAPI(deletingAgent.id);
            const nextAgents = agents.filter((a) => a.id !== deletingAgent.id);
            setAgents(nextAgents);
            if (nextAgents.length === 0) {
                fetchAgents();
            }
        } catch (e: any) {
            setError(intl.formatMessage({defaultMessage: 'Failed to delete agent.'}));
        } finally {
            setDeleteInFlight(false);
            setDeletingAgent(null);
        }
    }, [agents, deletingAgent, deleteInFlight, fetchAgents, intl]);

    const handleDeleteCancel = useCallback(() => {
        setDeletingAgent(null);
    }, []);

    const handleCreateAgent = useCallback(() => {
        if (createButtonDisabled) {
            return;
        }
        setEditingAgent(null);
        setViewMode('create');
        setViewOpen(true);
    }, [createButtonDisabled]);

    const handleViewBack = useCallback(() => {
        setViewOpen(false);
        setEditingAgent(null);
    }, []);

    const handleViewSaved = useCallback(() => {
        setViewOpen(false);
        setEditingAgent(null);
        fetchAgents();
    }, [fetchAgents]);

    // Filter agents based on active tab and search query
    const userCanManageAgent = useCallback((a: UserAgent) => {
        const isOwner = a.creatorID === currentUserId || (a.adminUserIDs?.includes(currentUserId) ?? false);
        if (isOwner || hasManageOthersAgent) {
            return true;
        }

        // Migrated legacy bots have no creator; system admins had full control via System Console.
        return Boolean(!a.creatorID && hasManageSystem);
    }, [currentUserId, hasManageOthersAgent, hasManageSystem]);

    const filteredAgents = agents.filter((a) => {
        if (activeTab === 'yours' && a.creatorID !== currentUserId) {
            return false;
        }
        if (searchQuery.trim()) {
            const query = searchQuery.toLowerCase();
            return a.displayName.toLowerCase().includes(query) || a.name.toLowerCase().includes(query);
        }
        return true;
    });

    if (viewOpen) {
        return (
            <AgentConfigView
                mode={viewMode}
                {...(editingAgent ? {agent: editingAgent} : {})}
                services={services}
                onBack={handleViewBack}
                onSaved={handleViewSaved}
            />
        );
    }

    return (
        <Container>
            <Header>
                <TitleRow>
                    <Title>
                        <FormattedMessage defaultMessage='Agents'/>
                    </Title>
                    <Subtitle>
                        <FormattedMessage defaultMessage='Here are the agents you have access to'/>
                    </Subtitle>
                </TitleRow>
                {userCanCreateAgent && (
                    createQuotaReached ? (
                        <OverlayTrigger
                            placement='bottom'
                            overlay={
                                <Tooltip id='create-agent-quota-tooltip'>
                                    <FormattedMessage defaultMessage='Multiple self-service agents require a qualifying Mattermost plan'/>
                                </Tooltip>
                            }
                        >
                            {/* Wrapper receives hover events; a disabled button does not fire them itself. */}
                            <CreateButtonWrapper>
                                <CreateButton
                                    onClick={handleCreateAgent}
                                    disabled={true}
                                >
                                    <PlusIcon size={16}/>
                                    <FormattedMessage defaultMessage='Create agent'/>
                                </CreateButton>
                            </CreateButtonWrapper>
                        </OverlayTrigger>
                    ) : (
                        <CreateButton
                            onClick={handleCreateAgent}
                            disabled={createButtonDisabled}
                        >
                            <PlusIcon size={16}/>
                            <FormattedMessage defaultMessage='Create agent'/>
                        </CreateButton>
                    )
                )}
            </Header>

            <TabBar>
                <TabButton
                    $active={activeTab === 'all'}
                    onClick={() => setActiveTab('all')}
                >
                    <FormattedMessage defaultMessage='All agents'/>
                </TabButton>
                <TabButton
                    $active={activeTab === 'yours'}
                    onClick={() => setActiveTab('yours')}
                >
                    <FormattedMessage defaultMessage='Your agents'/>
                </TabButton>
            </TabBar>

            <SearchContainer>
                <SearchInputWrapper>
                    <SearchIconWrapper>
                        <MagnifyIcon size={18}/>
                    </SearchIconWrapper>
                    <SearchInput
                        type='text'
                        placeholder={intl.formatMessage({defaultMessage: 'Search agents...'})}
                        value={searchQuery}
                        onChange={(e) => setSearchQuery(e.target.value)}
                    />
                </SearchInputWrapper>
            </SearchContainer>

            {loading && (
                <LoadingContainer>
                    <FormattedMessage defaultMessage='Loading agents...'/>
                </LoadingContainer>
            )}

            {error && (
                <ErrorContainer>{error}</ErrorContainer>
            )}

            {servicesError && !error && (
                <ServicesWarningBanner>{servicesError}</ServicesWarningBanner>
            )}

            {!loading && !error && filteredAgents.length === 0 && searchQuery.trim() && (
                <NoResultsMessage>
                    <FormattedMessage
                        defaultMessage='No agents match "{query}"'
                        values={{query: searchQuery}}
                    />
                </NoResultsMessage>
            )}

            {!loading && !error && filteredAgents.length === 0 && !searchQuery.trim() && (
                <EmptyState>
                    {activeTab === 'yours' ? (
                        <FormattedMessage defaultMessage="You haven't created any agents yet."/>
                    ) : (
                        <FormattedMessage defaultMessage='No agents have been created yet.'/>
                    )}
                </EmptyState>
            )}

            {!loading && !error && filteredAgents.length > 0 && (
                <AgentListContainer>
                    {filteredAgents.map((agent) => (
                        <AgentRow
                            key={agent.id}
                            agent={agent}
                            services={services}
                            canManage={userCanManageAgent(agent)}
                            onEdit={handleEdit}
                            onDelete={handleDeleteRequest}
                        />
                    ))}
                </AgentListContainer>
            )}

            {deletingAgent && (
                <DeleteAgentDialog
                    agentName={deletingAgent.displayName}
                    confirmPending={deleteInFlight}
                    onConfirm={handleDeleteConfirm}
                    onCancel={handleDeleteCancel}
                />
            )}

            <Footer>
                <FormattedMessage defaultMessage='AI services are third party services. Mattermost is not responsible for output.'/>
            </Footer>
        </Container>
    );
};

// --- Styled Components ---

const Container = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    gap: 0;
    overflow-y: auto;
`;

const Header = styled.div`
    display: flex;
    flex-direction: row;
    justify-content: space-between;
    align-items: center;
    padding: 48px 0 24px;
`;

const TitleRow = styled.div`
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 12px;
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

const CreateButtonWrapper = styled.div`
    display: inline-flex;
    flex-shrink: 0;
`;

const CreateButton = styled(PrimaryButton)`
    gap: 8px;
    flex-shrink: 0;
`;

const TabBar = styled.div`
    display: flex;
    flex-direction: row;
    gap: 4px;
    padding-bottom: 16px;
`;

const TabButton = styled.button<{$active: boolean}>`
    padding: 4px 10px;
    border: none;
    border-radius: 4px;
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    cursor: pointer;
    background: ${(p) => (p.$active ? 'rgba(var(--button-bg-rgb, 28, 88, 217), 0.08)' : 'transparent')};
    color: ${(p) => (p.$active ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.64)')};

    &:hover {
        background: ${(p) => (p.$active ? 'rgba(var(--button-bg-rgb, 28, 88, 217), 0.08)' : 'rgba(var(--center-channel-color-rgb), 0.08)')};
    }
`;

const SearchContainer = styled.div`
    padding: 0 0 16px 0;
`;

const SearchInputWrapper = styled.div`
    position: relative;
    width: 100%;
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

const AgentListContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: 12px;
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

const ServicesWarningBanner = styled.div`
    display: flex;
    align-items: center;
    padding: 10px 12px;
    margin-bottom: 8px;
    background: rgba(var(--away-indicator-rgb, 255, 188, 66), 0.12);
    border-radius: 4px;
    border: 1px solid rgba(var(--away-indicator-rgb, 255, 188, 66), 0.35);
    color: rgba(var(--center-channel-color-rgb), 0.88);
    font-size: 14px;
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

const Footer = styled.div`
    padding: 24px 0;
    font-family: 'Open Sans', sans-serif;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

export default AgentsList;
