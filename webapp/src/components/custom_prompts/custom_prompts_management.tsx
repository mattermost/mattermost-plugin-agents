// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect, useCallback} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector, useDispatch} from 'react-redux';

import {CloseIcon, PinOutlineIcon, PinIcon, ChevronDownIcon, ChevronUpIcon, PlusIcon, MagnifyIcon} from '@mattermost/compass-icons/components';

import {getCustomPrompts, getPinnedPromptIds, getShowCustomPromptsModal} from '@/selectors';
import {fetchCustomPrompts, fetchPinnedPromptIds, ShowCustomPromptsModalHandler} from '@/redux';
import {createCustomPrompt, updateCustomPrompt, setCustomPromptPin} from '@/client';

import CustomPromptForm from './custom_prompt_form';

const ModalOverlay = styled.div`
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    background-color: rgba(0, 0, 0, 0.64);
    display: flex;
    align-items: center;
    justify-content: center;
    z-index: 2000;
`;

const ModalContainer = styled.div`
    background-color: var(--center-channel-bg);
    border-radius: 12px;
    width: 768px;
    max-height: 80vh;
    display: flex;
    flex-direction: column;
    box-shadow: 0px 8px 24px rgba(0, 0, 0, 0.12);
`;

const ModalHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 24px 32px;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const ModalTitle = styled.h2`
    font-family: 'Metropolis', sans-serif;
    font-weight: 600;
    font-size: 22px;
    line-height: 28px;
    color: var(--center-channel-color);
    margin: 0;
`;

const CloseButton = styled.button`
    background: none;
    border: none;
    cursor: pointer;
    padding: 10px;
    border-radius: 4px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    display: flex;
    align-items: center;
    justify-content: center;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const ModalBody = styled.div`
    display: flex;
    flex-direction: column;
    overflow-y: auto;
    flex: 1;
`;

const TabBar = styled.div`
    display: flex;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    padding: 0 32px;
`;

const Tab = styled.button<{$active: boolean}>`
    background: none;
    border: none;
    border-bottom: 2px solid ${({$active}) => ($active ? 'var(--button-bg)' : 'transparent')};
    padding: 12px 16px;
    font-size: 14px;
    font-weight: 600;
    color: ${({$active}) => ($active ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.64)')};
    cursor: pointer;

    &:hover {
        color: var(--button-bg);
    }
`;

const ToolbarRow = styled.div`
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 16px 20px;
`;

const SearchContainer = styled.div`
    flex: 1;
    position: relative;
    display: flex;
    align-items: center;
`;

const SearchIconWrapper = styled.div`
    position: absolute;
    left: 10px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    display: flex;
    align-items: center;
`;

const SearchInput = styled.input`
    width: 100%;
    padding: 8px 12px 8px 34px;
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.16);
    border-radius: 4px;
    background-color: var(--center-channel-bg);
    color: var(--center-channel-color);
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    line-height: 20px;
    outline: none;

    &:focus {
        border-color: var(--button-bg);
        box-shadow: 0 0 0 1px var(--button-bg);
    }
`;

const CreateNewButton = styled.button`
    display: flex;
    align-items: center;
    gap: 4px;
    background: none;
    color: var(--button-bg);
    border: none;
    border-radius: 4px;
    padding: 8px 16px;
    font-weight: 600;
    font-size: 14px;
    cursor: pointer;
    white-space: nowrap;
    font-family: 'Open Sans', sans-serif;

    &:hover {
        background: rgba(var(--button-bg-rgb), 0.08);
    }
`;

const PromptList = styled.div`
    display: flex;
    flex-direction: column;
    gap: 16px;
    padding: 0 16px 16px;
`;

const PromptRowContainer = styled.div`
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 4px;
    background: var(--center-channel-bg);
`;

const PromptRowHeader = styled.div<{$expanded: boolean}>`
    display: flex;
    align-items: center;
    padding: 12px 16px;
    cursor: pointer;
    background: ${({$expanded}) => ($expanded ? 'rgba(var(--center-channel-color-rgb), 0.04)' : 'none')};

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

const PromptInfo = styled.div`
    flex: 1;
    min-width: 0;
`;

const PromptName = styled.div`
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
`;

const PromptDescription = styled.div`
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const PinButton = styled.button<{$pinned: boolean}>`
    background: none;
    border: none;
    cursor: pointer;
    padding: 4px;
    border-radius: 4px;
    display: flex;
    align-items: center;
    justify-content: center;
    color: ${({$pinned}) => ($pinned ? 'var(--button-bg)' : 'rgba(var(--center-channel-color-rgb), 0.56)')};
    margin-right: 8px;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
        color: var(--button-bg);
    }
`;

const ChevronButton = styled.button`
    background: none;
    border: none;
    cursor: pointer;
    padding: 4px;
    border-radius: 4px;
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(var(--center-channel-color-rgb), 0.56);

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const ExpandedContent = styled.div`
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const EmptyState = styled.div`
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    padding: 48px 32px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
    font-size: 14px;
    line-height: 20px;
`;

const NewPromptContainer = styled.div`
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const NewPromptHeader = styled.div`
    display: flex;
    align-items: center;
    padding: 12px 32px;
    font-size: 14px;
    font-weight: 600;
    color: var(--center-channel-color);
    background: rgba(var(--center-channel-color-rgb), 0.04);
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const CustomPromptsManagement = () => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const show = useSelector(getShowCustomPromptsModal);
    const prompts = useSelector(getCustomPrompts);
    const pinnedIds = useSelector(getPinnedPromptIds);
    const currentUserId = useSelector((state: any) => state.entities.users.currentUserId);

    const [activeTab, setActiveTab] = useState<'all' | 'yours'>('all');
    const [searchQuery, setSearchQuery] = useState('');
    const [expandedId, setExpandedId] = useState<string | null>(null);
    const [showCreateForm, setShowCreateForm] = useState(false);

    useEffect(() => {
        if (show) {
            dispatch(fetchCustomPrompts() as any);
            dispatch(fetchPinnedPromptIds() as any);
        }
    }, [show, dispatch]);

    const handleClose = useCallback(() => {
        dispatch({type: ShowCustomPromptsModalHandler, show: false});
        setShowCreateForm(false);
        setExpandedId(null);
        setSearchQuery('');
    }, [dispatch]);

    const handleTogglePin = useCallback(async (promptId: string) => {
        const isPinned = pinnedIds.includes(promptId);
        try {
            await setCustomPromptPin(promptId, !isPinned);
            dispatch(fetchPinnedPromptIds() as any);
        } catch {
            // Handle error silently
        }
    }, [pinnedIds, dispatch]);

    const handleCreate = useCallback(async (data: {name: string; description: string; template: string; is_shared: boolean}) => {
        try {
            await createCustomPrompt(data);
            dispatch(fetchCustomPrompts() as any);
            setShowCreateForm(false);
        } catch {
            // Handle error silently
        }
    }, [dispatch]);

    const handleUpdate = useCallback(async (id: string, data: {name: string; description: string; template: string; is_shared: boolean}) => {
        try {
            await updateCustomPrompt(id, data);
            dispatch(fetchCustomPrompts() as any);
            setExpandedId(null);
        } catch {
            // Handle error silently
        }
    }, [dispatch]);

    const handleModalClick = (e: React.MouseEvent) => {
        e.stopPropagation();
    };

    if (!show) {
        return null;
    }

    const filteredPrompts = (prompts || []).filter((prompt) => {
        if (activeTab === 'yours' && prompt.creator_id !== currentUserId) {
            return false;
        }
        if (searchQuery) {
            const q = searchQuery.toLowerCase();
            return prompt.name.toLowerCase().includes(q) || prompt.description.toLowerCase().includes(q);
        }
        return true;
    });

    return (
        <ModalOverlay onClick={handleClose}>
            <ModalContainer onClick={handleModalClick}>
                <ModalHeader>
                    <ModalTitle>
                        <FormattedMessage defaultMessage='Custom Prompts'/>
                    </ModalTitle>
                    <CloseButton onClick={handleClose}>
                        <CloseIcon size={20}/>
                    </CloseButton>
                </ModalHeader>
                <TabBar>
                    <Tab
                        $active={activeTab === 'all'}
                        onClick={() => setActiveTab('all')}
                    >
                        <FormattedMessage defaultMessage='All Prompts'/>
                    </Tab>
                    <Tab
                        $active={activeTab === 'yours'}
                        onClick={() => setActiveTab('yours')}
                    >
                        <FormattedMessage defaultMessage='Your Prompts'/>
                    </Tab>
                </TabBar>
                <ToolbarRow>
                    <SearchContainer>
                        <SearchIconWrapper>
                            <MagnifyIcon size={16}/>
                        </SearchIconWrapper>
                        <SearchInput
                            value={searchQuery}
                            onChange={(e) => setSearchQuery(e.target.value)}
                            placeholder={intl.formatMessage({defaultMessage: 'Search prompts'})}
                        />
                    </SearchContainer>
                    <CreateNewButton
                        onClick={() => {
                            setShowCreateForm(true);
                            setExpandedId(null);
                        }}
                    >
                        <PlusIcon size={16}/>
                        <FormattedMessage defaultMessage='Create new'/>
                    </CreateNewButton>
                </ToolbarRow>
                <ModalBody>
                    {showCreateForm && (
                        <NewPromptContainer>
                            <NewPromptHeader>
                                <FormattedMessage defaultMessage='New Prompt'/>
                            </NewPromptHeader>
                            <CustomPromptForm
                                onSave={handleCreate}
                                onDiscard={() => setShowCreateForm(false)}
                            />
                        </NewPromptContainer>
                    )}
                    <PromptList>
                        {filteredPrompts.map((prompt) => {
                            const isPinned = pinnedIds.includes(prompt.id);
                            const isExpanded = expandedId === prompt.id;
                            const isOwner = prompt.creator_id === currentUserId;

                            return (
                                <PromptRowContainer key={prompt.id}>
                                    <PromptRowHeader
                                        $expanded={isExpanded}
                                        onClick={() => setExpandedId(isExpanded ? null : prompt.id)}
                                    >
                                        <PromptInfo>
                                            <PromptName>{prompt.name}</PromptName>
                                            {!isExpanded && prompt.description && (
                                                <PromptDescription>{prompt.description}</PromptDescription>
                                            )}
                                        </PromptInfo>
                                        <PinButton
                                            $pinned={isPinned}
                                            onClick={(e) => {
                                                e.stopPropagation();
                                                handleTogglePin(prompt.id);
                                            }}
                                        >
                                            {isPinned ? <PinIcon size={18}/> : <PinOutlineIcon size={18}/>}
                                        </PinButton>
                                        <ChevronButton>
                                            {isExpanded ? <ChevronUpIcon size={18}/> : <ChevronDownIcon size={18}/>}
                                        </ChevronButton>
                                    </PromptRowHeader>
                                    {isExpanded && (
                                        <ExpandedContent>
                                            <CustomPromptForm
                                                prompt={prompt}
                                                readOnly={!isOwner}
                                                onSave={(data) => handleUpdate(prompt.id, data)}
                                                onDiscard={() => setExpandedId(null)}
                                            />
                                        </ExpandedContent>
                                    )}
                                </PromptRowContainer>
                            );
                        })}
                        {filteredPrompts.length === 0 && !showCreateForm && (
                            <EmptyState>
                                <FormattedMessage defaultMessage='No prompts found'/>
                            </EmptyState>
                        )}
                    </PromptList>
                </ModalBody>
            </ModalContainer>
        </ModalOverlay>
    );
};

export default CustomPromptsManagement;
