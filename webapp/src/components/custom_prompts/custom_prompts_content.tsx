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
import {SearchInput} from '@mattermost/compass-ui/components/search-input';
import {SectionNotice} from '@mattermost/compass-ui/components/section-notice';
import {Tabs} from '@mattermost/compass-ui/components/tabs';
import {WithTooltip} from '@mattermost/shared/components/tooltip';

import {getCustomPrompts, getPinnedPromptIds} from '@/selectors';
import {fetchCustomPrompts, fetchPinnedPromptIds} from '@/redux';
import {createCustomPrompt, updateCustomPrompt, deleteCustomPrompt, setCustomPromptPin} from '@/client';

import ConfirmationDialog from '../confirmation_dialog';
import {UnderlineTab, UnderlineTabs} from '@/components/underline_tabs';

import CustomPromptForm from './custom_prompt_form';

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

// Page layout mirrors the All agents list: the host column owns the horizontal
// padding, tabs sit above a full-width search, and the list has no side margins.
const PageTabs = styled(Tabs)`
    margin-bottom: var(--spacing-l);
    flex-shrink: 0;
`;

const PageSearchRow = styled.div`
    padding: 0 0 var(--spacing-l);
    flex-shrink: 0;
`;

const PagePromptEmptyState = styled(EmptyState)`
    padding-top: var(--spacing-xxl);
`;

const ToolbarRow = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-m);
    padding: var(--spacing-l) var(--spacing-xxxl);
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

const PromptList = styled.div<{$page?: boolean}>`
    display: flex;
    flex-direction: column;
    margin: ${({$page}) => ($page ? '0' : '0 var(--spacing-xxxl) var(--spacing-l)')};
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const PromptRowContainer = styled.div`
    border-bottom: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    background: var(--center-channel-bg);

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

const PromptRowHeader = styled.div`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs);
    padding-right: var(--spacing-l);
`;

const PromptRowMain = styled.div`
    flex: 1;
    min-width: 0;
    padding: var(--spacing-l) 0 var(--spacing-l) var(--spacing-l);
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
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-100);
    color: var(--center-channel-color);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const PromptDescription = styled.div`
    font-size: var(--font-size-75);
    line-height: var(--line-height-75);
    color: rgba(var(--center-channel-color-rgb), 0.56);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
`;

const PinButton = styled(IconButton)`
    flex-shrink: 0;
`;

const ErrorBanner = styled(SectionNotice)<{$page?: boolean}>`
    flex-shrink: 0;
    margin: ${({$page}) => ($page ? '0 0 var(--spacing-xs)' : 'var(--spacing-xs) var(--spacing-xxxl) 0')};
`;

export interface CustomPromptsContentRenderProps {

    /** Header title for the current view: list, "New Prompt", or the prompt being edited. */
    title: React.ReactNode;

    /** True while the create or edit form is showing, i.e. a back affordance applies. */
    showBack: boolean;

    /** Returns from the create/edit form to the list. */
    onBack: () => void;

    /**
     * Page layout only, list view only: the "Create new" action, for the host to
     * place in its header. Null in the modal layout and while a form is showing.
     */
    createAction: React.ReactNode;

    /** The view itself; the host places it under its own chrome. */
    body: React.ReactNode;
}

interface CustomPromptsContentProps {

    /**
     * Whether the host is currently showing the content. Turning true clears the
     * error banner and (re)fetches prompts and pins; turning false resets
     * the view back to the list.
     */
    active: boolean;

    /** z-index of the delete confirmation, which must sit above the host's own layer. */
    confirmZIndex?: number;

    /**
     * 'modal' (default) keeps the modal's inset tabs/toolbar. 'page' drops the
     * inset padding (the host column provides it), uses compass Tabs, and hands
     * the Create action to the host via `createAction`.
     */
    layout?: 'modal' | 'page';

    /** Presentation contract: the host renders its own title/back chrome around `body`. */
    children: (props: CustomPromptsContentRenderProps) => React.ReactNode;
}

const CustomPromptsContent = ({active, confirmZIndex = 3000, layout = 'modal', children}: CustomPromptsContentProps) => {
    const isPage = layout === 'page';
    const intl = useIntl();
    const dispatch = useDispatch();
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
        if (active) {
            setError('');
            dispatch(fetchCustomPrompts() as any);
            dispatch(fetchPinnedPromptIds() as any);
            return;
        }
        setShowCreateForm(false);
        setEditingPromptId(null);
        setSearchQuery('');
        setDeleteConfirmId(null);
    }, [active, dispatch]);

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

    const openCreateForm = () => {
        setShowCreateForm(true);
        setEditingPromptId(null);
    };

    const createButton = (
        <Button
            emphasis={isPage ? 'primary' : 'tertiary'}
            leadingIcon={<Icon glyph={<PlusIcon/>}/>}
            onClick={openCreateForm}
        >
            <FormattedMessage defaultMessage='Create new'/>
        </Button>
    );

    const searchInput = (
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
    );

    const title = (() => {
        if (showCreateForm) {
            return <FormattedMessage defaultMessage='New Prompt'/>;
        }
        if (editingPrompt) {
            return editingPrompt.name;
        }
        return <FormattedMessage defaultMessage='Custom Prompts'/>;
    })();

    const body = (
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
                            $page={isPage}
                            type='danger'
                            title={error}
                        />
                    )}
                    {showCreateForm ? (
                        <CustomPromptForm
                            flush={isPage}
                            stickyFooter={true}
                            onSave={handleCreate}
                            onDiscard={handleFormBack}
                        />
                    ) : (
                        editingPrompt && (
                            <CustomPromptForm
                                flush={isPage}
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
                    {isPage ? (
                        <>
                            <PageTabs
                                tabs={[
                                    {key: 'all', label: <FormattedMessage defaultMessage='All Prompts'/>},
                                    {key: 'yours', label: <FormattedMessage defaultMessage='Your Prompts'/>},
                                ]}
                                activeKey={activeTab}
                                onChange={(key) => setActiveTab(key as 'all' | 'yours')}
                            />
                            <PageSearchRow>{searchInput}</PageSearchRow>
                        </>
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
                                {searchInput}
                                {createButton}
                            </ToolbarRow>
                        </>
                    )}
                    <ModalBody>
                        {error && (
                            <ErrorBanner
                                $page={isPage}
                                type='danger'
                                title={error}
                            />
                        )}
                        <PromptList $page={isPage}>
                            {filteredPrompts.map((prompt) => {
                                const isPinned = pinnedIds.includes(prompt.id);
                                let pinLabel = intl.formatMessage({defaultMessage: 'Pin prompt'});
                                if (isPinned) {
                                    pinLabel = intl.formatMessage({defaultMessage: 'Unpin prompt'});
                                }
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
                                            <WithTooltip title={pinLabel}>
                                                <PinButton
                                                    size='small'
                                                    active={isPinned}
                                                    aria-pressed={isPinned}
                                                    icon={<Icon glyph={isPinned ? <PinIcon/> : <PinOutlineIcon/>}/>}
                                                    onClick={() => handleTogglePin(prompt.id)}
                                                    aria-label={pinLabel}
                                                />
                                            </WithTooltip>
                                        </PromptRowHeader>
                                    </PromptRowContainer>
                                );
                            })}
                            {filteredPrompts.length === 0 && (
                                isPage ? (
                                    <PagePromptEmptyState title={<FormattedMessage defaultMessage='No prompts found'/>}/>
                                ) : (
                                    <EmptyState title={<FormattedMessage defaultMessage='No prompts found'/>}/>
                                )
                            )}
                        </PromptList>
                    </ModalBody>
                </>
            )}
        </SheetContent>
    );

    return (
        <>
            {children({
                title,
                showBack: Boolean(showCreateForm || editingPrompt),
                onBack: handleFormBack,
                createAction: isPage && !showCreateForm && !editingPrompt ? createButton : null,
                body,
            })}
            <ConfirmationDialog
                show={deleteConfirmId !== null}
                title={<FormattedMessage defaultMessage='Delete prompt'/>}
                message={<FormattedMessage defaultMessage='Are you sure you want to delete this prompt? This action cannot be undone.'/>}
                confirmButtonText={<FormattedMessage defaultMessage='Delete'/>}
                onConfirm={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                onCancel={() => setDeleteConfirmId(null)}
                isDestructive={true}
                managedAccessibility={true}
                zIndex={confirmZIndex}
            />
        </>
    );
};

export default CustomPromptsContent;
