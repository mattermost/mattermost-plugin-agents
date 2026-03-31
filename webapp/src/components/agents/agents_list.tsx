// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector} from 'react-redux';
import {PlusIcon} from '@mattermost/compass-icons/components';

import {GlobalState} from '@mattermost/types/store';

import {getAgents, deleteAgent as deleteAgentAPI} from '@/client';
import {PrimaryButton} from '@/components/assets/buttons';
import {UserAgent} from '@/types/agents';

import AgentRow from './agent_row';
import DeleteAgentDialog from './delete_agent_dialog';

type Tab = 'all' | 'yours';

const AgentsList = () => {
    const intl = useIntl();
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);

    const [agents, setAgents] = useState<UserAgent[]>([]);
    const [loading, setLoading] = useState(true);
    const [error, setError] = useState<string | null>(null);
    const [activeTab, setActiveTab] = useState<Tab>('all');
    const [deletingAgent, setDeletingAgent] = useState<UserAgent | null>(null);

    const fetchAgents = useCallback(async () => {
        try {
            setLoading(true);
            setError(null);
            const result = await getAgents();
            setAgents(result || []);
        } catch (e: any) {
            setError(intl.formatMessage({defaultMessage: 'Failed to load agents.'}));
        } finally {
            setLoading(false);
        }
    }, [intl]);

    useEffect(() => {
        fetchAgents();
    }, [fetchAgents]);

    const handleEdit = useCallback((_agent: UserAgent) => {
        // Phase 5 will implement the edit modal.
    }, []);

    const handleDeleteRequest = useCallback((agent: UserAgent) => {
        setDeletingAgent(agent);
    }, []);

    const handleDeleteConfirm = useCallback(async () => {
        if (!deletingAgent) {
            return;
        }
        try {
            await deleteAgentAPI(deletingAgent.id);
            setAgents((prev) => prev.filter((a) => a.id !== deletingAgent.id));
        } catch (e: any) {
            setError(intl.formatMessage({defaultMessage: 'Failed to delete agent.'}));
        } finally {
            setDeletingAgent(null);
        }
    }, [deletingAgent, intl]);

    const handleDeleteCancel = useCallback(() => {
        setDeletingAgent(null);
    }, []);

    const handleCreateAgent = useCallback(() => {
        // Phase 5 will implement the create modal.
    }, []);

    // Filter agents based on active tab
    const filteredAgents = activeTab === 'yours'
        ? agents.filter((a) => a.creatorId === currentUserId)
        : agents;

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
                <CreateButton onClick={handleCreateAgent}>
                    <PlusIcon size={16}/>
                    <FormattedMessage defaultMessage='Create agent'/>
                </CreateButton>
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

            {loading && (
                <LoadingContainer>
                    <FormattedMessage defaultMessage='Loading agents...'/>
                </LoadingContainer>
            )}

            {error && (
                <ErrorContainer>{error}</ErrorContainer>
            )}

            {!loading && !error && filteredAgents.length === 0 && (
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
                            isOwner={agent.creatorId === currentUserId || (agent.adminUserIds?.includes(currentUserId) ?? false)}
                            onEdit={handleEdit}
                            onDelete={handleDeleteRequest}
                        />
                    ))}
                </AgentListContainer>
            )}

            {deletingAgent && (
                <DeleteAgentDialog
                    agentName={deletingAgent.displayName}
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
    gap: 0;
`;

const Header = styled.div`
    display: flex;
    flex-direction: row;
    justify-content: space-between;
    align-items: center;
    padding: 24px 0;
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
    background: ${(p) => p.$active ? 'rgba(var(--button-bg-rgb, 28, 88, 217), 0.08)' : 'transparent'};
    color: ${(p) => p.$active ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.64)'};

    &:hover {
        background: ${(p) => p.$active ? 'rgba(var(--button-bg-rgb, 28, 88, 217), 0.08)' : 'rgba(var(--center-channel-color-rgb), 0.08)'};
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
