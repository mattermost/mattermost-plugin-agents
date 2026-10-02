// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect, useCallback, useMemo} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector, useDispatch} from 'react-redux';

import {PinOutlineIcon, PinIcon, PlusIcon} from '@mattermost/compass-icons/components';

import {Button} from '@mattermost/compass-ui/components/button';
import {EmptyState} from '@mattermost/compass-ui/components/empty-state';
import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {Modal} from '@mattermost/compass-ui/components/modal';
import {SearchInput} from '@mattermost/compass-ui/components/search-input';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {getCustomPrompts, getPinnedPromptIds, getShowCustomPromptsModal} from '@/selectors';
import {fetchCustomPrompts, fetchPinnedPromptIds, ShowCustomPromptsModalHandler} from '@/redux';
import {createCustomPrompt, updateCustomPrompt, deleteCustomPrompt, setCustomPromptPin} from '@/client';

import ConfirmationDialog from '../confirmation_dialog';
import {AnimatedModalShell, MODAL_SHEET_CLASS} from '@/components/animated_modal_shell';
import {UnderlineTab, UnderlineTabs} from '@/components/underline_tabs';

import CustomPromptForm from './custom_prompt_form';

const Sheet = styled.div`
    display: flex;
    max-width: calc(100vw - 32px);
`;

const PromptsModal = styled(Modal)`
    && {
        height: 80vh;
        max-width: 100%;
    }
`;

const SheetContent = styled.div`
    display: flex;
    flex-direction: column;
    height: 100%;
`;

const ModalBody = styled.div<{$stickyFormFooter?: boolean}>`
    display: flex;
    flex-direction: column;
    flex: 1;
    min-height: 0;
    overflow-y: ${({$stickyFormFooter}) => ($stickyFormFooter ? 'hidden' : 'auto')};
`;

const TabBar = styled(UnderlineTabs)`
    padding: 0 var(--spacing-xxxl);
`;

const ToolbarRow = styled.div`
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 16px 32px;
`;

const SearchContainer = styled.div`
    flex: 1;
    min-width: 0;

    /* SearchInput draws its own focus ring; suppress the host a11y ring on the inner input. */
    input.a11y--focused {
        box-shadow: none !important;
        outline: none !important;
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
    gap: var(--spacing-xs);
    padding-right: var(--spacing-l);

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

const PromptRowMain = styled.div`
    flex: 1;
    min-width: 0;
    padding: var(--spacing-m) 0 var(--spacing-m) var(--spacing-l);
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

const PinButton = styled(IconButton)`
    margin-right: 8px;
    flex-shrink: 0;
`;

const ErrorBanner = styled(SectionNotice)`
    flex-shrink: 0;
    margin: 8px 32px 0;
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
                <Sheet
                    className={MODAL_SHEET_CLASS}
                    onClick={handleModalClick}
                >
                    <PromptsModal
                        size='medium'
                        title={title}
                        onClose={handleClose}
                        closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
                        showBackButton={Boolean(showCreateForm || editingPrompt)}
                        onBack={handleFormBack}
                        backLabel={intl.formatMessage({defaultMessage: 'Back to prompts'})}
                        headerDivider={false}
                        bodyPadding='none'
                        scrollable={false}
                    >
                        <SheetContent>
                            {showCreateForm || editingPrompt ? (
                                <ModalBody
                                    $stickyFormFooter={Boolean(
                                        showCreateForm ||
                                (editingPrompt && editingPrompt.creator_id === currentUserId),
                                    )}
                                >
                                    {error && (
                                        <ErrorBanner
                                            type='danger'
                                            title={error}
                                        />
                                    )}
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
                                    <TabBar role='tablist'>
                                        <UnderlineTab
                                            type='button'
                                            role='tab'
                                            $active={activeTab === 'all'}
                                            aria-selected={activeTab === 'all'}
                                            tabIndex={activeTab === 'all' ? 0 : -1}
                                            onClick={() => setActiveTab('all')}
                                        >
                                            <FormattedMessage defaultMessage='All Prompts'/>
                                        </UnderlineTab>
                                        <UnderlineTab
                                            type='button'
                                            role='tab'
                                            $active={activeTab === 'yours'}
                                            aria-selected={activeTab === 'yours'}
                                            tabIndex={activeTab === 'yours' ? 0 : -1}
                                            onClick={() => setActiveTab('yours')}
                                        >
                                            <FormattedMessage defaultMessage='Your Prompts'/>
                                        </UnderlineTab>
                                    </TabBar>
                                    <ToolbarRow>
                                        <SearchContainer>
                                            <SearchInput
                                                value={searchQuery}
                                                onChange={(e) => setSearchQuery(e.target.value)}
                                                onClear={() => setSearchQuery('')}
                                                clearLabel={intl.formatMessage({defaultMessage: 'Clear search'})}
                                                placeholder={intl.formatMessage({defaultMessage: 'Search prompts'})}
                                                aria-label={intl.formatMessage({defaultMessage: 'Search prompts'})}
                                            />
                                        </SearchContainer>
                                        <Button
                                            emphasis='tertiary'
                                            leadingIcon={<Icon glyph={<PlusIcon/>}/>}
                                            onClick={() => {
                                                setShowCreateForm(true);
                                                setEditingPromptId(null);
                                            }}
                                        >
                                            <FormattedMessage defaultMessage='Create new'/>
                                        </Button>
                                    </ToolbarRow>
                                    <ModalBody>
                                        {error && (
                                            <ErrorBanner
                                                type='danger'
                                                title={error}
                                            />
                                        )}
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
                                                                size='small'
                                                                active={isPinned}
                                                                aria-pressed={isPinned}
                                                                icon={<Icon glyph={isPinned ? <PinIcon/> : <PinOutlineIcon/>}/>}
                                                                onClick={() => handleTogglePin(prompt.id)}
                                                                aria-label={isPinned ? intl.formatMessage({defaultMessage: 'Unpin prompt'}) : intl.formatMessage({defaultMessage: 'Pin prompt'})
                                                                }
                                                            />
                                                        </PromptRowHeader>
                                                    </PromptRowContainer>
                                                );
                                            })}
                                            {filteredPrompts.length === 0 && (
                                                <EmptyState title={<FormattedMessage defaultMessage='No prompts found'/>}/>
                                            )}
                                        </PromptList>
                                    </ModalBody>
                                </>
                            )}
                        </SheetContent>
                    </PromptsModal>
                </Sheet>
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
