// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';

import AutoFixIcon from '@mattermost/compass-icons/components/auto-fix';
import CogOutlineIcon from '@mattermost/compass-icons/components/cog-outline';
import CreationOutlineIcon from '@mattermost/compass-icons/components/creation-outline';
import EmoticonOutlineIcon from '@mattermost/compass-icons/components/emoticon-outline';
import FormatLetterCaseIcon from '@mattermost/compass-icons/components/format-letter-case';
import PaperclipIcon from '@mattermost/compass-icons/components/paperclip';
import PlusIcon from '@mattermost/compass-icons/components/plus';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenu, PopoverMenuScroll} from '@mattermost/compass-ui/components/popover-menu';

import {getAgentsBridgeStatus, renderCustomPrompt, rewriteMessage, RewriteAction} from '@/client';
import Dropdown from '@/components/dropdown';
import {IconMenuItem, LoadingRow, menuIcon, StatusRow} from '@/components/menu/menu_parts';
import {focusFirstItem, handleMenuNavigation} from '@/components/menu/menu_keyboard';
import Submenu from '@/components/menu/submenu';
import {useMenuTransition} from '@/hooks/use_menu_transition';
import {fetchCustomPrompts, fetchPinnedPromptIds, ShowCustomPromptsModalHandler} from '@/redux';
import {getCustomPrompts, getPinnedPromptIds, isCustomPromptsLoaded, isPinnedPromptIdsLoaded} from '@/selectors';

import {attributesCanBeSet, clickHostControl, HOST_ATTRIBUTES_BUTTON, HOST_EMOJI_BUTTON, HOST_FILE_UPLOAD_BUTTON} from './agent_composer_host';
import {readComposerText, writeComposerText} from './agent_composer_text';

const SKILL_LIST_MAX_HEIGHT = 'calc(8 * var(--line-height-100))';

interface AgentComposerMenuProps {
    rootRef: React.RefObject<HTMLElement | null>;
    formattingOpen: boolean;
    onToggleFormatting: () => void;
    channelId: string;
    botId: string;
    busy: boolean;
    onBusy: (busy: boolean) => void;
    onError: (message: string) => void;
    onStatus: (message: string) => void;
}

type OpenSubmenu = 'skills' | 'rewrite' | null;

const AgentComposerMenu = ({
    rootRef,
    formattingOpen,
    onToggleFormatting,
    channelId,
    botId,
    busy,
    onBusy,
    onError,
    onStatus,
}: AgentComposerMenuProps) => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const [open, setOpen] = useState(false);
    const [submenu, setSubmenu] = useState<OpenSubmenu>(null);
    const [submenuFromKeyboard, setSubmenuFromKeyboard] = useState(false);
    const [attributesEnabled, setAttributesEnabled] = useState(false);
    const [rewriteAvailable, setRewriteAvailable] = useState<boolean | null>(null);
    const menuRef = useRef<HTMLDivElement>(null);
    const run = useRef(0);
    const {rendered, onExited} = useMenuTransition(open);

    const prompts = useSelector(getCustomPrompts);
    const pinnedIds = useSelector(getPinnedPromptIds);
    const promptsLoaded = useSelector(isCustomPromptsLoaded);
    const pinsLoaded = useSelector(isPinnedPromptIdsLoaded);

    const skills = useMemo(() => {
        const pinned = new Set(pinnedIds);
        return [...prompts].sort((a, b) => Number(pinned.has(b.id)) - Number(pinned.has(a.id)));
    }, [prompts, pinnedIds]);

    const rewriteLabels = useMemo(() => [
        {id: 'shorten' as const, label: intl.formatMessage({defaultMessage: 'Shorten'})},
        {id: 'elaborate' as const, label: intl.formatMessage({defaultMessage: 'Elaborate'})},
        {id: 'improve_writing' as const, label: intl.formatMessage({defaultMessage: 'Improve writing'})},
        {id: 'fix_spelling' as const, label: intl.formatMessage({defaultMessage: 'Fix spelling'})},
        {id: 'simplify' as const, label: intl.formatMessage({defaultMessage: 'Simplify'})},
        {id: 'summarize' as const, label: intl.formatMessage({defaultMessage: 'Summarize'})},
    ], [intl]);

    useEffect(() => {
        if (!open) {
            setSubmenu(null);
            return;
        }
        setAttributesEnabled(attributesCanBeSet(rootRef.current));
        if (!promptsLoaded) {
            dispatchFetch(dispatch, fetchCustomPrompts);
        }
        if (!pinsLoaded) {
            dispatchFetch(dispatch, fetchPinnedPromptIds);
        }
    }, [open, promptsLoaded, pinsLoaded, dispatch, rootRef]);

    useEffect(() => {
        if (!open || !botId) {
            return () => {
                // No status request to cancel.
            };
        }
        const controller = new AbortController();
        let cancelled = false;
        getAgentsBridgeStatus(controller.signal).then(
            (status) => {
                if (!cancelled) {
                    setRewriteAvailable(status.available);
                }
            },
            () => {
                if (!cancelled) {
                    setRewriteAvailable(null);
                }
            },
        );
        return () => {
            cancelled = true;
            controller.abort();
        };
    }, [open, botId]);

    useEffect(() => {
        if (!open) {
            return;
        }
        const menu = menuRef.current?.querySelector<HTMLElement>('[role="menu"]') ?? null;
        focusFirstItem(menu);
    }, [open]);

    const close = () => setOpen(false);

    const fail = (message: string) => {
        onStatus('');
        onError(message);
    };

    const insertSkill = async (promptId: string) => {
        const root = rootRef.current;
        if (!root || !channelId) {
            fail(intl.formatMessage({defaultMessage: 'Couldn\'t insert that skill. Try again.'}));
            return;
        }
        const id = ++run.current;
        onError('');
        onBusy(true);
        onStatus(intl.formatMessage({defaultMessage: 'Inserting skill…'}));
        close();
        try {
            const renderedPrompt = await renderCustomPrompt(promptId, channelId, botId || '');
            if (id !== run.current) {
                return;
            }
            if (!writeComposerText(root, renderedPrompt.rendered, false)) {
                fail(intl.formatMessage({defaultMessage: 'Couldn\'t insert that skill. Try again.'}));
                return;
            }
            onStatus('');
        } catch {
            if (id === run.current) {
                fail(intl.formatMessage({defaultMessage: 'Couldn\'t insert that skill. Try again.'}));
            }
        } finally {
            if (id === run.current) {
                onBusy(false);
            }
        }
    };

    const rewrite = async (action: RewriteAction) => {
        const root = rootRef.current;
        if (!root || !botId) {
            fail(intl.formatMessage({defaultMessage: 'Couldn\'t rewrite that. Try again.'}));
            return;
        }
        const current = readComposerText(root);
        if (!current.found || current.text.trim() === '') {
            fail(intl.formatMessage({defaultMessage: 'Write a message to rewrite.'}));
            close();
            return;
        }
        const id = ++run.current;
        onError('');
        onBusy(true);
        onStatus(intl.formatMessage({defaultMessage: 'Rewriting…'}));
        close();
        try {
            const next = await rewriteMessage(current.text, action, botId);
            if (id !== run.current) {
                return;
            }
            if (next.trim() === '' || !writeComposerText(root, next, current.selected)) {
                fail(intl.formatMessage({defaultMessage: 'Couldn\'t rewrite that. Try again.'}));
                return;
            }
            onStatus('');
        } catch {
            if (id === run.current) {
                fail(intl.formatMessage({defaultMessage: 'Couldn\'t rewrite that. Try again.'}));
            }
        } finally {
            if (id === run.current) {
                onBusy(false);
            }
        }
    };

    const addLabel = intl.formatMessage({defaultMessage: 'Add to message'});
    const showFormatting = intl.formatMessage({defaultMessage: 'Show formatting'});
    const hideFormatting = intl.formatMessage({defaultMessage: 'Hide formatting'});
    const formattingLabel = formattingOpen ? hideFormatting : showFormatting;

    return (
        <Dropdown
            isOpen={rendered}
            onOpenChange={setOpen}
            placement='top-start'
            tooltip={intl.formatMessage({defaultMessage: 'Add'})}
            target={(
                <IconButton
                    icon={<Icon glyph={<PlusIcon/>}/>}
                    aria-label={addLabel}
                    aria-expanded={open}
                    aria-haspopup='menu'
                    rounded={true}
                    size='small'
                    loading={busy}
                    data-testid='agent-composer-add'
                    onClick={() => setOpen((current) => !current)}
                    onKeyDown={(event) => {
                        if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
                            event.preventDefault();
                            setOpen(true);
                        }
                    }}
                />
            )}
        >
            <div ref={menuRef}>
                <PopoverMenu
                    open={open}
                    onExited={onExited}
                    role='menu'
                    aria-label={addLabel}
                    data-testid='agent-composer-menu'
                    onKeyDown={handleMenuNavigation}
                >
                    <IconMenuItem
                        icon={<PaperclipIcon/>}
                        label={intl.formatMessage({defaultMessage: 'Upload files'})}
                        onClick={() => {
                            if (!clickHostControl(rootRef.current, HOST_FILE_UPLOAD_BUTTON)) {
                                fail(intl.formatMessage({defaultMessage: 'Couldn\'t open file upload. Try again.'}));
                            }
                            close();
                        }}
                    />
                    <IconMenuItem
                        icon={<EmoticonOutlineIcon/>}
                        label={intl.formatMessage({defaultMessage: 'Add emoji'})}
                        onClick={() => {
                            if (!clickHostControl(rootRef.current, HOST_EMOJI_BUTTON)) {
                                fail(intl.formatMessage({defaultMessage: 'Couldn\'t open emoji. Try again.'}));
                            }
                            close();
                        }}
                    />
                    <IconMenuItem
                        icon={<PlusIcon/>}
                        label={intl.formatMessage({defaultMessage: 'Add attributes'})}
                        disabled={!attributesEnabled}
                        onClick={() => {
                            clickHostControl(rootRef.current, HOST_ATTRIBUTES_BUTTON);
                            close();
                        }}
                    />
                    <MenuItem
                        role='menuitemcheckbox'
                        aria-checked={formattingOpen}
                        label={formattingLabel}
                        leadingElement={true}
                        leadingVisual={menuIcon(<FormatLetterCaseIcon/>)}
                        onClick={() => {
                            onToggleFormatting();
                            close();
                        }}
                    />
                    <Submenu
                        label={intl.formatMessage({defaultMessage: 'Skills'})}
                        icon={<CreationOutlineIcon/>}
                        menuLabel={intl.formatMessage({defaultMessage: 'Skills'})}
                        open={submenu === 'skills'}
                        focusOnOpen={submenu === 'skills' && submenuFromKeyboard}
                        onOpen={(focus) => {
                            setSubmenu('skills');
                            setSubmenuFromKeyboard(focus);
                        }}
                        onClose={() => setSubmenu(null)}
                        data-testid='agent-composer-skills'
                    >
                        {!promptsLoaded && <LoadingRow label={intl.formatMessage({defaultMessage: 'Loading skills…'})}/>}
                        {promptsLoaded && skills.length === 0 && (
                            <StatusRow role='status'>{intl.formatMessage({defaultMessage: 'No skills yet'})}</StatusRow>
                        )}
                        {skills.length > 0 && (
                            <PopoverMenuScroll maxHeight={SKILL_LIST_MAX_HEIGHT}>
                                {skills.map((prompt) => (
                                    <IconMenuItem
                                        key={prompt.id}
                                        icon={<CreationOutlineIcon/>}
                                        label={prompt.name}
                                        onClick={() => insertSkill(prompt.id)}
                                    />
                                ))}
                            </PopoverMenuScroll>
                        )}
                        <IconMenuItem
                            icon={<CogOutlineIcon/>}
                            label={intl.formatMessage({defaultMessage: 'Manage skills'})}
                            onClick={() => {
                                dispatch({type: ShowCustomPromptsModalHandler, show: true});
                                close();
                            }}
                        />
                    </Submenu>
                    {botId && (
                        <Submenu
                            label={intl.formatMessage({defaultMessage: 'Rewrite'})}
                            icon={<AutoFixIcon/>}
                            menuLabel={intl.formatMessage({defaultMessage: 'Rewrite'})}
                            open={submenu === 'rewrite'}
                            focusOnOpen={submenu === 'rewrite' && submenuFromKeyboard}
                            onOpen={(focus) => {
                                setSubmenu('rewrite');
                                setSubmenuFromKeyboard(focus);
                            }}
                            onClose={() => setSubmenu(null)}
                            data-testid='agent-composer-rewrite'
                        >
                            {rewriteAvailable === false && (
                                <StatusRow role='status'>{intl.formatMessage({defaultMessage: 'Rewrite isn\'t available'})}</StatusRow>
                            )}
                            {rewriteAvailable !== false && rewriteLabels.map((action) => (
                                <IconMenuItem
                                    key={action.id}
                                    icon={<AutoFixIcon/>}
                                    label={action.label}
                                    onClick={() => rewrite(action.id)}
                                />
                            ))}
                        </Submenu>
                    )}
                </PopoverMenu>
            </div>
        </Dropdown>
    );
};

const dispatchFetch = (dispatch: ReturnType<typeof useDispatch>, fetch: () => unknown) => {
    try {
        const result = dispatch(fetch() as never);
        if (result && typeof (result as Promise<unknown>).then === 'function') {
            (result as Promise<unknown>).catch(() => { /* the menu shows the empty state */ });
        }
    } catch {
        // A store without thunk middleware cannot run the fetch.
    }
};

export default AgentComposerMenu;
