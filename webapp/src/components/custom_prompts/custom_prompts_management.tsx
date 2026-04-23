// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect, useCallback, useMemo} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector, useDispatch} from 'react-redux';

import {CloseIcon, PinOutlineIcon, PinIcon, PlusIcon, MagnifyIcon, ArrowLeftIcon} from '@mattermost/compass-icons/components';

import {getCustomPrompts, getPinnedPromptIds, getShowCustomPromptsModal} from '@/selectors';
import {fetchCustomPrompts, fetchPinnedPromptIds, ShowCustomPromptsModalHandler} from '@/redux';
import {createCustomPrompt, updateCustomPrompt, deleteCustomPrompt, setCustomPromptPin} from '@/client';

import ConfirmationDialog from '../confirmation_dialog';
import {AnimatedModalShell, MODAL_SHEET_CLASS} from '@/components/animated_modal_shell';

import CustomPromptForm from './custom_prompt_form';

const ModalContainer = styled.div`
    background-color: var(--center-channel-bg);
    border-radius: 12px;
    overflow: hidden;
    clip-path: inset(0 round 12px);
    width: 768px;
    height: 80vh;
    display: flex;
    flex-direction: column;
    box-shadow: 0px 8px 24px rgba(0, 0, 0, 0.12);
`;

const ModalHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 12px;
    padding: 24px 32px 16px;
`;

const ModalHeaderLeading = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    flex: 1;
    min-width: 0;
`;

const ModalTitle = styled.h2`
    font-family: 'Metropolis', sans-serif;
    font-weight: 600;
    font-size: 22px;
    line-height: 28px;
    color: var(--center-channel-color);
    margin: 0;
`;

const ModalIconButton = styled.button`
    background: none;
    border: none;
    cursor: pointer;
    padding: 10px;
    border-radius: 4px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
    display: flex;
    align-items: center;
    justify-content: center;
    flex-shrink: 0;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.08);
    }
`;

const CloseButton = ModalIconButton;

const BackButton = styled(ModalIconButton)`
    margin-left: -12px;
`;

const ModalBody = styled.div<{$stickyFormFooter?: boolean}>`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    overflow-y: ${({$stickyFormFooter}) => ($stickyFormFooter ? 'hidden' : 'auto')};
    background-color: var(--center-channel-bg);
    border-radius: 0 0 12px 12px;
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
    padding: 16px 32px;
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
    padding: 0 32px 16px;
`;

const PromptRowContainer = styled.div`
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 4px;
    background: var(--center-channel-bg);
`;

const PromptRowHeader = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 12px 16px;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

const PromptRowMain = styled.div`
    flex: 1;
    min-width: 0;
    cursor: pointer;

    &:focus-visible {
        outline: none;
        box-shadow: inset 0 0 0 2px var(--button-bg);
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
    flex-shrink: 0;
    padding: 12px 20px;
    margin: 8px 32px 0;
    background: rgba(var(--error-text-color-rgb, 210, 75, 78), 0.08);
    color: var(--error-text);
    border-radius: 4px;
    font-size: 14px;
    line-height: 20px;
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
    const [editingPromptId, setEditingPromptId] = useState<string | null>(null);
    const [showCreateForm, setShowCreateForm] = useState(false);
    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null);
    const [error, setError] = useState('');

    const editingPrompt = useMemo(() => {
        if (!editingPromptId || !prompts?.length) {
            return null;
        }
        return prompts.find((p) => p.id === editingPromptId) ?? null;
    }, [editingPromptId, prompts]);

    useEffect(() => {
        if (editingPromptId && prompts && !prompts.some((p) => p.id === editingPromptId)) {
            setEditingPromptId(null);
        }
    }, [editingPromptId, prompts]);

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
        setEditingPromptId(null);
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
            setEditingPromptId(null);
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
            setEditingPromptId(null);
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

    const handleFormBack = useCallback(() => {
        setShowCreateForm(false);
        setEditingPromptId(null);
    }, []);

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

    const title = (() => {
        if (showCreateForm) {
            return <FormattedMessage defaultMessage='New Prompt'/>;
        }
        if (editingPrompt) {
            return editingPrompt.name;
        }
        return <FormattedMessage defaultMessage='Custom Prompts'/>;
    })();

    return (
        <>
            <AnimatedModalShell
                show={show}
                onBackdropClick={handleClose}
                zIndex={2000}
            >
                <ModalContainer
                    className={MODAL_SHEET_CLASS}
                    onClick={handleModalClick}
                    role='dialog'
                    aria-modal='true'
                    aria-label={intl.formatMessage({defaultMessage: 'Custom Prompts'})}
                >
                    <ModalHeader>
                        <ModalHeaderLeading>
                            {(showCreateForm || editingPrompt) && (
                                <BackButton
                                    type='button'
                                    onClick={handleFormBack}
                                    aria-label={intl.formatMessage({defaultMessage: 'Back to prompts'})}
                                >
                                    <ArrowLeftIcon size={20}/>
                                </BackButton>
                            )}
                            <ModalTitle>{title}</ModalTitle>
                        </ModalHeaderLeading>
                        <CloseButton
                            type='button'
                            onClick={handleClose}
                            aria-label={intl.formatMessage({defaultMessage: 'Close'})}
                        >
                            <CloseIcon size={20}/>
                        </CloseButton>
                    </ModalHeader>
                    {showCreateForm || editingPrompt ? (
                        <ModalBody
                            $stickyFormFooter={Boolean(
                                showCreateForm ||
                                (editingPrompt && editingPrompt.creator_id === currentUserId),
                            )}
                        >
                            {error && <ErrorBanner>{error}</ErrorBanner>}
                            {showCreateForm ? (
                                <CustomPromptForm
                                    stickyFooter={true}
                                    onSave={handleCreate}
                                    onDiscard={handleFormBack}
                                />
                            ) : (
                                editingPrompt && (
                                    <CustomPromptForm
                                        stickyFooter={editingPrompt.creator_id === currentUserId}
                                        prompt={editingPrompt}
                                        readOnly={editingPrompt.creator_id !== currentUserId}
                                        onSave={(data) => handleUpdate(editingPrompt.id, data)}
                                        onDiscard={handleFormBack}
                                        {...(editingPrompt.creator_id === currentUserId ? {onDelete: () => setDeleteConfirmId(editingPrompt.id)} : {})}
                                    />
                                )
                            )}
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
                                        setEditingPromptId(null);
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
                                        const openPrompt = () => {
                                            setEditingPromptId(prompt.id);
                                            setShowCreateForm(false);
                                        };

                                        return (
                                            <PromptRowContainer key={prompt.id}>
                                                <PromptRowHeader>
                                                    <PromptRowMain
                                                        role='button'
                                                        tabIndex={0}
                                                        aria-label={intl.formatMessage(
                                                            {defaultMessage: 'Open prompt {name}'},
                                                            {name: prompt.name},
                                                        )}
                                                        onClick={openPrompt}
                                                        onKeyDown={(e) => {
                                                            if (e.key === 'Enter' || e.key === ' ') {
                                                                e.preventDefault();
                                                                openPrompt();
                                                            }
                                                        }}
                                                    >
                                                        <PromptInfo>
                                                            <PromptName>{prompt.name}</PromptName>
                                                            {prompt.description && (
                                                                <PromptDescription>{prompt.description}</PromptDescription>
                                                            )}
                                                        </PromptInfo>
                                                    </PromptRowMain>
                                                    <PinButton
                                                        type='button'
                                                        $pinned={isPinned}
                                                        onClick={(e) => {
                                                            e.stopPropagation();
                                                            handleTogglePin(prompt.id);
                                                        }}
                                                        aria-label={isPinned ? intl.formatMessage({defaultMessage: 'Unpin prompt'}) : intl.formatMessage({defaultMessage: 'Pin prompt'})
                                                        }
                                                    >
                                                        {isPinned ? <PinIcon size={18}/> : <PinOutlineIcon size={18}/>}
                                                    </PinButton>
                                                </PromptRowHeader>
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
            </AnimatedModalShell>
            <ConfirmationDialog
                show={deleteConfirmId !== null}
                title={<FormattedMessage defaultMessage='Delete prompt'/>}
                message={<FormattedMessage defaultMessage='Are you sure you want to delete this prompt? This action cannot be undone.'/>}
                confirmButtonText={<FormattedMessage defaultMessage='Delete'/>}
                onConfirm={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                onCancel={() => setDeleteConfirmId(null)}
                isDestructive={true}
                zIndex={3000}
            />
        </>
    );
};

export default CustomPromptsManagement;
