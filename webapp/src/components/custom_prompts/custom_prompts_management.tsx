// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect, useCallback, useRef} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector, useDispatch} from 'react-redux';

import {CloseIcon, PinOutlineIcon, PinIcon, ChevronDownIcon, ChevronUpIcon, PlusIcon, MagnifyIcon, ArrowLeftIcon} from '@mattermost/compass-icons/components';

import {getCustomPrompts, getPinnedPromptIds, getShowCustomPromptsModal} from '@/selectors';
import {fetchCustomPrompts, fetchPinnedPromptIds, ShowCustomPromptsModalHandler} from '@/redux';
import {createCustomPrompt, updateCustomPrompt, deleteCustomPrompt, setCustomPromptPin} from '@/client';

import ConfirmationDialog from '../confirmation_dialog';

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
    overflow: hidden;
    clip-path: inset(0 round 12px);
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
    padding: 24px 32px 16px;
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
    background-color: var(--center-channel-bg);
    border-radius: 0 0 12px 12px;
`;

const TabBar = styled.div`
    display: flex;
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    padding: 0 24px;
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
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
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

const ErrorBanner = styled.div`
    display: flex;
    align-items: center;
    padding: 12px 20px;
    margin: 8px 16px 0;
    background: rgba(var(--error-text-color-rgb, 210, 75, 78), 0.08);
    color: var(--error-text);
    border-radius: 4px;
    font-size: 14px;
    line-height: 20px;
`;

const BackButton = styled.button`
    background: none;
    border: none;
    cursor: pointer;
    padding: 4px;
    border-radius: 4px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    display: flex;
    align-items: center;
    justify-content: center;
    margin-right: 8px;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const ConfirmationOverlay = styled.div`
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 3000;
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
    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null);
    const [error, setError] = useState('');
    const expandedRowRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        if (expandedId && expandedRowRef.current) {
            setTimeout(() => {
                expandedRowRef.current?.scrollIntoView({behavior: 'smooth', block: 'nearest'});
            }, 0);
        }
    }, [expandedId]);

    useEffect(() => {
        if (show) {
            setError('');
            dispatch(fetchCustomPrompts() as any);
            dispatch(fetchPinnedPromptIds() as any);
        }
    }, [show, dispatch]);

    const handleClose = useCallback(() => {
        dispatch({type: ShowCustomPromptsModalHandler, show: false});
        setShowCreateForm(false);
        setExpandedId(null);
        setSearchQuery('');
        setDeleteConfirmId(null);
    }, [dispatch]);

    const handleTogglePin = useCallback(async (promptId: string) => {
        const isPinned = pinnedIds.includes(promptId);
        try {
            await setCustomPromptPin(promptId, !isPinned);
            dispatch(fetchPinnedPromptIds() as any);
        } catch (e) {
            console.error('Failed to toggle pin:', e); // eslint-disable-line no-console
            setError(intl.formatMessage({defaultMessage: 'Failed to update pin. Please try again.'}));
        }
    }, [pinnedIds, dispatch, intl]);

    const handleCreate = useCallback(async (data: {name: string; description: string; template: string; is_shared: boolean}) => {
        try {
            await createCustomPrompt(data);
            dispatch(fetchCustomPrompts() as any);
            setShowCreateForm(false);
        } catch (e) {
            console.error('Failed to create prompt:', e); // eslint-disable-line no-console
            setError(intl.formatMessage({defaultMessage: 'Failed to create prompt. Please try again.'}));
        }
    }, [dispatch, intl]);

    const handleUpdate = useCallback(async (id: string, data: {name: string; description: string; template: string; is_shared: boolean}) => {
        try {
            await updateCustomPrompt(id, data);
            dispatch(fetchCustomPrompts() as any);
            setExpandedId(null);
        } catch (e) {
            console.error('Failed to update prompt:', e); // eslint-disable-line no-console
            setError(intl.formatMessage({defaultMessage: 'Failed to update prompt. Please try again.'}));
        }
    }, [dispatch, intl]);

    const handleDelete = useCallback(async (id: string) => {
        try {
            await deleteCustomPrompt(id);
            dispatch(fetchCustomPrompts() as any);
            dispatch(fetchPinnedPromptIds() as any);
            setExpandedId(null);
            setDeleteConfirmId(null);
        } catch (e) {
            console.error('Failed to delete prompt:', e); // eslint-disable-line no-console
            setError(intl.formatMessage({defaultMessage: 'Failed to delete prompt. Please try again.'}));
            setDeleteConfirmId(null);
        }
    }, [dispatch, intl]);

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
            <ModalContainer
                onClick={handleModalClick}
                role='dialog'
                aria-modal='true'
                aria-label={intl.formatMessage({defaultMessage: 'Custom Prompts'})}
            >
                <ModalHeader>
                    {showCreateForm && (
                        <BackButton
                            onClick={() => setShowCreateForm(false)}
                            aria-label={intl.formatMessage({defaultMessage: 'Back to prompts'})}
                        >
                            <ArrowLeftIcon size={20}/>
                        </BackButton>
                    )}
                    <ModalTitle>
                        {showCreateForm ? (
                            <FormattedMessage defaultMessage='New Prompt'/>
                        ) : (
                            <FormattedMessage defaultMessage='Custom Prompts'/>
                        )}
                    </ModalTitle>
                    <CloseButton
                        onClick={handleClose}
                        aria-label={intl.formatMessage({defaultMessage: 'Close'})}
                    >
                        <CloseIcon size={20}/>
                    </CloseButton>
                </ModalHeader>
                {showCreateForm ? (
                    <ModalBody>
                        {error && <ErrorBanner>{error}</ErrorBanner>}
                        <CustomPromptForm
                            onSave={handleCreate}
                            onDiscard={() => setShowCreateForm(false)}
                        />
                    </ModalBody>
                ) : (
                    <>
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
                                    aria-label={intl.formatMessage({defaultMessage: 'Search prompts'})}
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
                            {error && <ErrorBanner>{error}</ErrorBanner>}
                            <PromptList>
                                {filteredPrompts.map((prompt) => {
                                    const isPinned = pinnedIds.includes(prompt.id);
                                    const isExpanded = expandedId === prompt.id;
                                    const isOwner = prompt.creator_id === currentUserId;

                                    return (
                                        <PromptRowContainer
                                            key={prompt.id}
                                            ref={isExpanded ? expandedRowRef : null}
                                        >
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
                                                    aria-label={isPinned ?
                                                        intl.formatMessage({defaultMessage: 'Unpin prompt'}) :
                                                        intl.formatMessage({defaultMessage: 'Pin prompt'})
                                                    }
                                                >
                                                    {isPinned ? <PinIcon size={18}/> : <PinOutlineIcon size={18}/>}
                                                </PinButton>
                                                <ChevronButton
                                                    aria-label={isExpanded ?
                                                        intl.formatMessage({defaultMessage: 'Collapse prompt'}) :
                                                        intl.formatMessage({defaultMessage: 'Expand prompt'})
                                                    }
                                                >
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
                                                        {...(isOwner ? {onDelete: () => setDeleteConfirmId(prompt.id)} : {})}
                                                    />
                                                </ExpandedContent>
                                            )}
                                        </PromptRowContainer>
                                    );
                                })}
                                {filteredPrompts.length === 0 && (
                                    <EmptyState>
                                        <FormattedMessage defaultMessage='No prompts found'/>
                                    </EmptyState>
                                )}
                            </PromptList>
                        </ModalBody>
                    </>
                )}
            </ModalContainer>
            {deleteConfirmId && (
                <ConfirmationOverlay>
                    <ConfirmationDialog
                        title={<FormattedMessage defaultMessage='Delete prompt'/>}
                        message={<FormattedMessage defaultMessage='Are you sure you want to delete this prompt? This action cannot be undone.'/>}
                        confirmButtonText={<FormattedMessage defaultMessage='Delete'/>}
                        onConfirm={() => handleDelete(deleteConfirmId)}
                        onCancel={() => setDeleteConfirmId(null)}
                        isDestructive={true}
                    />
                </ConfirmationOverlay>
            )}
        </ModalOverlay>
    );
};

export default CustomPromptsManagement;
