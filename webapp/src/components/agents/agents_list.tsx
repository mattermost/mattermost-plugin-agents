// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector} from 'react-redux';
import {PlusIcon} from '@mattermost/compass-icons/components';
//eslint-disable-next-line import/no-unresolved -- react-bootstrap is external
import {OverlayTrigger, Tooltip} from 'react-bootstrap';

import {GlobalState} from '@mattermost/types/store';

import {Button} from '@mattermost/compass-ui/components/button';
import {EmptyState} from '@mattermost/compass-ui/components/empty-state';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {SearchInput} from '@mattermost/compass-ui/components/search-input';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {Tabs} from '@mattermost/compass-ui/components/tabs';

import {getAgents, getServices, deleteAgent as deleteAgentAPI} from '@/client';
import {userHasSystemPermission} from '@/utils/permissions';
import {UserAgent, ServiceInfo} from '@/types/agents';
import {LicenseLevel, useAgentLimit, useLicenseLevel, useLicenseLevelName} from '@/license';

import AgentRow from './agent_row';
import DeleteAgentDialog from './delete_agent_dialog';
import AgentConfigView from './agent_config_view';

type Tab = 'all' | 'yours';

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

    // Mirrors api.canConfigureAgentServices. Users without these permissions
    // browse read-only; requesting /services would 403 and wrongly flag every agent.
    const canViewServices = hasManageOwnAgent || hasManageOthersAgent || hasManageSystem;
    const agentLimit = useAgentLimit();
    const licenseLevel = useLicenseLevel();
    const licenseLevelName = useLicenseLevelName();

    const [agents, setAgents] = useState<UserAgent[]>([]);
    const [services, setServices] = useState<ServiceInfo[]>([]);
    const [servicesLoaded, setServicesLoaded] = useState(false);
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
    const createQuotaReached = agentLimit !== null && serverAgentCount >= agentLimit;
    const createButtonDisabled = loading || createQuotaReached;
    const nextAgentPlanName = licenseLevelName(
        licenseLevel < LicenseLevel.Professional ? LicenseLevel.Professional : LicenseLevel.Enterprise,
    );
    const createQuotaMessage = agentLimit === null ? '' : intl.formatMessage(
        {defaultMessage: 'Your current plan allows {count, plural, one {# agent} other {# agents}}. Additional agents are available on {plan} plans and above.'},
        {count: agentLimit, plan: nextAgentPlanName},
    );

    const fetchAgents = useCallback(async () => {
        try {
            setLoading(true);
            setError(null);
            setServicesError(null);
            const agentResult = await getAgents();
            setAgents(agentResult.agents || []);
            setActiveAgentCount(agentResult.activeAgentCount ?? null);
            if (canViewServices) {
                try {
                    const serviceResult = await getServices();
                    setServices(serviceResult || []);
                    setServicesLoaded(true);
                } catch {
                    setServicesError(intl.formatMessage({defaultMessage: 'Failed to load AI services. Using the last loaded list.'}));
                }
            }
        } catch (e: any) {
            setError(intl.formatMessage({defaultMessage: 'Failed to load agents.'}));
        } finally {
            setLoading(false);
        }
    }, [intl, canViewServices]);

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
            <ConfigViewFrame>
                <ContentColumn $fillHeight={true}>
                    <AgentConfigView
                        mode={viewMode}
                        {...(editingAgent ? {agent: editingAgent} : {})}
                        services={services}
                        onBack={handleViewBack}
                        onSaved={handleViewSaved}
                    />
                </ContentColumn>
            </ConfigViewFrame>
        );
    }

    return (
        <Container>
            <FixedChrome>
                <ContentColumn>
                    <Header>
                        <TitleRow>
                            <Title>
                                <FormattedMessage defaultMessage='Agents'/>
                            </Title>
                            <Subtitle>
                                <FormattedMessage defaultMessage='Agents are AI assistants in your workspace. Mention @username in a channel or direct message to chat with one.'/>
                            </Subtitle>
                        </TitleRow>
                        {userCanCreateAgent && (
                            createQuotaReached ? (
                                <OverlayTrigger
                                    placement='bottom'
                                    overlay={
                                        <Tooltip id='create-agent-quota-tooltip'>
                                            {createQuotaMessage}
                                        </Tooltip>
                                    }
                                >
                                    {/* Wrapper receives hover events; a disabled button does not fire them itself. */}
                                    <CreateButtonWrapper>
                                        <CreateButton
                                            emphasis='primary'
                                            leadingIcon={<Icon glyph={<PlusIcon/>}/>}
                                            onClick={handleCreateAgent}
                                            disabled={true}
                                        >
                                            <FormattedMessage defaultMessage='Create agent'/>
                                        </CreateButton>
                                    </CreateButtonWrapper>
                                </OverlayTrigger>
                            ) : (
                                <CreateButton
                                    emphasis='primary'
                                    leadingIcon={<Icon glyph={<PlusIcon/>}/>}
                                    onClick={handleCreateAgent}
                                    disabled={createButtonDisabled}
                                >
                                    <FormattedMessage defaultMessage='Create agent'/>
                                </CreateButton>
                            )
                        )}
                    </Header>

                    <TabBar
                        tabs={[
                            {key: 'all', label: <FormattedMessage defaultMessage='All agents'/>},
                            {key: 'yours', label: <FormattedMessage defaultMessage='Your agents'/>},
                        ]}
                        activeKey={activeTab}
                        onChange={(key) => setActiveTab(key as Tab)}
                    />

                    <SearchContainer>
                        <SearchInput
                            placeholder={intl.formatMessage({defaultMessage: 'Search agents...'})}
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                            onClear={() => setSearchQuery('')}
                            clearLabel={intl.formatMessage({defaultMessage: 'Clear search'})}
                        />
                    </SearchContainer>
                </ContentColumn>
            </FixedChrome>

            <ListViewport data-testid='agents-list-viewport'>
                <ListContent>
                    {loading && (
                        <LoadingContainer>
                            <Spinner size='20'/>
                            <FormattedMessage defaultMessage='Loading agents...'/>
                        </LoadingContainer>
                    )}

                    {error && (
                        <Notice
                            type='danger'
                            title={error}
                        />
                    )}

                    {servicesError && !error && (
                        <Notice
                            type='warning'
                            title={servicesError}
                        />
                    )}

                    {!loading && !error && filteredAgents.length === 0 && searchQuery.trim() && (
                        <AgentsEmptyState
                            title={
                                <FormattedMessage
                                    defaultMessage='No agents match "{query}"'
                                    values={{query: searchQuery}}
                                />
                            }
                        />
                    )}

                    {!loading && !error && filteredAgents.length === 0 && !searchQuery.trim() && (
                        <AgentsEmptyState
                            title={activeTab === 'yours' ? (
                                <FormattedMessage defaultMessage="You haven't created any agents yet."/>
                            ) : (
                                <FormattedMessage defaultMessage='No agents have been created yet.'/>
                            )}
                        />
                    )}

                    {!loading && !error && filteredAgents.length > 0 && (
                        <AgentListContainer>
                            {filteredAgents.map((agent) => (
                                <AgentRow
                                    key={agent.id}
                                    agent={agent}
                                    services={services}
                                    servicesLoaded={servicesLoaded}
                                    canManage={userCanManageAgent(agent)}
                                    onEdit={handleEdit}
                                    onDelete={handleDeleteRequest}
                                />
                            ))}
                        </AgentListContainer>
                    )}
                </ListContent>
            </ListViewport>

            {deletingAgent && (
                <DeleteAgentDialog
                    agentName={deletingAgent.displayName}
                    confirmPending={deleteInFlight}
                    onConfirm={handleDeleteConfirm}
                    onCancel={handleDeleteCancel}
                />
            )}

            <FixedChrome>
                <ContentColumn>
                    <Footer>
                        <FormattedMessage defaultMessage='AI services are third party services. Mattermost is not responsible for output.'/>
                    </Footer>
                </ContentColumn>
            </FixedChrome>
        </Container>
    );
};

// --- Styled Components ---

const CONTENT_MAX_WIDTH = '960px';
const CONTENT_HORIZONTAL_PADDING = 'var(--spacing-xxxl)';

const ContentColumn = styled.div<{$fillHeight?: boolean}>`
    width: 100%;
    max-width: ${CONTENT_MAX_WIDTH};
    margin: 0 auto;
    padding: 0 ${CONTENT_HORIZONTAL_PADDING};

    ${({$fillHeight}) => $fillHeight && `
        display: flex;
        flex-direction: column;
        flex: 1;
        min-height: 0;
        height: 100%;
    `}
`;

const FixedChrome = styled.div`
    flex-shrink: 0;
    width: 100%;
`;

const ConfigViewFrame = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    height: 100%;
    width: 100%;
    overflow: hidden;
    align-items: stretch;
`;

const Container = styled.div`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    height: 100%;
    overflow: hidden;
`;

const listScrollbarStyles = `
    scrollbar-width: thin;
    scrollbar-color: transparent transparent;

    &::-webkit-scrollbar {
        width: var(--spacing-xs);
    }

    &::-webkit-scrollbar-thumb {
        border-radius: var(--radius-s);
        background-color: transparent;
    }

    &:hover {
        scrollbar-color: rgba(var(--center-channel-color-rgb), 0.24) transparent;
    }

    &:hover::-webkit-scrollbar-thumb {
        background-color: rgba(var(--center-channel-color-rgb), 0.24);
    }
`;

const ListViewport = styled.div`
    flex: 1;
    min-height: 0;
    width: 100%;
    overflow-y: auto;
    overflow-x: hidden;
    overscroll-behavior: contain;
    ${listScrollbarStyles}
`;

const ListContent = styled.div`
    width: 100%;
    max-width: ${CONTENT_MAX_WIDTH};
    margin: 0 auto;
    padding: 0 ${CONTENT_HORIZONTAL_PADDING} var(--spacing-xs);
`;

const Header = styled.div`
    display: flex;
    flex-direction: row;
    justify-content: space-between;
    align-items: center;
    padding: var(--spacing-xxxxxl) 0 var(--spacing-xxl);
    flex-shrink: 0;
    gap: var(--spacing-l);
`;

const TitleRow = styled.div`
    display: flex;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--spacing-xxxs);
    min-width: 0;
    flex: 1;
`;

const Title = styled.h1`
    font-family: var(--font-family-heading, 'Metropolis', sans-serif);
    font-size: var(--font-size-500);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-500);
    color: var(--center-channel-color);
    margin: 0;
`;

const Subtitle = styled.p`
    font-family: var(--font-family-body, 'Open Sans', sans-serif);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.75);
    margin: 0;
`;

const CreateButtonWrapper = styled.div`
    display: inline-flex;
    flex-shrink: 0;
`;

const CreateButton = styled(Button)`
    flex-shrink: 0;
`;

const TabBar = styled(Tabs)`
    margin-bottom: var(--spacing-l);
    flex-shrink: 0;
`;

const SearchContainer = styled.div`
    padding: 0 0 var(--spacing-l) 0;
    flex-shrink: 0;
`;

const AgentListContainer = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-m);
`;

const AgentsEmptyState = styled(EmptyState)`
	padding-top: var(--spacing-xxl);
`;

const LoadingContainer = styled.div`
    display: flex;
    justify-content: center;
    align-items: center;
    gap: var(--spacing-xs);
    padding: var(--spacing-xxxxl);
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

const Notice = styled(SectionNotice)`
    margin-bottom: var(--spacing-xs);
`;

const Footer = styled.div`
    padding: var(--spacing-xxl) 0;
    font-family: var(--font-family-body, 'Open Sans', sans-serif);
    font-size: var(--font-size-75);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.75);
    flex-shrink: 0;
`;

export default AgentsList;
