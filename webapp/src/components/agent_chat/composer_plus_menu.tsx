// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

/**
 * ComposerPlusMenu: the agent chat composer's "+" button and its menu.
 *
 *   Upload files    opens the composer's file picker (only when uploads work)
 *   Tools        >  the agent's tool providers, toggled as in the tools popover
 *   Custom prompts > the user's prompts; a pick replaces the composer text
 *   AI Rewrite   >  the host composer's rewrite actions on the composer text
 *
 * Prop contract. The menu owns none of the composer's state, textarea or
 * host Textbox; it reads and writes the text only through these callbacks:
 *
 *   bot            The chat's agent. Without one, Tools and AI Rewrite are left out.
 *   channelId      The channel the composer posts to (custom prompts render for it).
 *   rootId         The thread root, '' for a new chat. Tools are offered only for
 *                  a new chat (as the RHS and Agents header popover); AI Rewrite
 *                  sends it so the thread is context.
 *   uploadEnabled  Whether files can attach (the composer's canAttach); false
 *                  leaves "Upload files" out.
 *   onUploadFiles  Opens the file picker. Called synchronously inside the click,
 *                  so the browser treats it as user-initiated.
 *   getText        The composer's current text (read when an action runs).
 *   setText        Replaces the composer's text (prompts, rewrite, discard). The
 *                  composer should also put focus back in its text box.
 *   disabled       Disables the "+" button (e.g. while a message is sending).
 *   onError        Shows a user-facing, already translated error in the composer.
 *   onRewritingChange  Optional: true while a rewrite runs, so the composer can
 *                  show it (and, like the host, hold edits until it lands).
 */

import React, {useCallback, useEffect, useId, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import PaperclipIcon from '@mattermost/compass-icons/components/paperclip';
import PlusIcon from '@mattermost/compass-icons/components/plus';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenu, PopoverMenuGroup} from '@mattermost/compass-ui/components/popover-menu';

import type {LLMBot} from '@/bots';
import {useAgentsRoute} from '@/components/agents/use_agents_route';
import Dropdown from '@/components/dropdown';
import {useMenuTransition} from '@/hooks/use_menu_transition';
import type {CustomPrompt} from '@/types';

import {focusFirstItem, handleMenuNavigation} from './composer_menu_keyboard';
import {menuIcon} from './composer_menu_parts';
import ComposerPromptsSubmenu from './composer_prompts_submenu';
import ComposerRewriteSubmenu from './composer_rewrite_submenu';
import ComposerToolsSubmenu, {showComposerTools} from './composer_tools_submenu';
import {useInsertCustomPrompt, useManageCustomPrompts} from './use_composer_custom_prompts';
import {useComposerRewrite, useRewriteAvailability} from './use_composer_rewrite';

export interface ComposerPlusMenuProps {
    bot?: LLMBot | null;
    channelId: string;
    rootId?: string;
    uploadEnabled: boolean;
    onUploadFiles: () => void;
    getText: () => string;
    setText: (text: string) => void;
    disabled?: boolean;
    onError: (message: string) => void;
    onRewritingChange?: (rewriting: boolean) => void;
}

type Submenu = 'tools' | 'prompts' | 'rewrite';

const RETURN_FOCUS_OFF = {returnFocus: false};

// The composer's "+" fill (as the old attach button): no compass token for the 8%
// center-channel wash on a neutral round IconButton.
const PLUS_FILL = 'rgba(var(--center-channel-color-rgb), 0.08)';

const ComposerPlusMenu = ({bot, channelId, rootId = '', uploadEnabled, onUploadFiles, getText, setText, disabled, onError, onRewritingChange}: ComposerPlusMenuProps) => {
    const intl = useIntl();
    const {toPrompts} = useAgentsRoute();
    const [open, setOpen] = useState(false);
    const menu = useMenuTransition(open);
    const [submenu, setSubmenu] = useState<{id: Submenu; focus: boolean} | null>(null);

    const setMenuOpen = useCallback((next: boolean) => {
        setOpen(next);
        if (!next) {
            setSubmenu(null);
        }
    }, []);

    // Focus goes back to "+" on Escape and after an item runs, but not after
    // a click outside (it stays where the user clicked), so the Dropdown's own
    // return-focus is off. An item that changes the text moves focus on to the
    // text box through setText.
    const baseId = `composer-plus-menu-${useId().replace(/:/g, '')}`;
    const triggerId = `${baseId}-button`;
    const menuId = `${baseId}-menu`;
    const finish = useCallback(() => {
        document.getElementById(triggerId)?.focus();
        setMenuOpen(false);
    }, [triggerId, setMenuOpen]);

    // Requests that outlive the menu (a prompt render, a rewrite) live here,
    // not in the submenus, which unmount with it.
    const promptError = useCallback(() => {
        onError(intl.formatMessage({defaultMessage: 'Couldn\'t insert the custom prompt. Try again.'}));
    }, [onError, intl]);
    const {insert: insertPrompt} = useInsertCustomPrompt({bot, channelId, setText, onError: promptError});
    const manageCustomPrompts = useManageCustomPrompts(toPrompts);

    const rewriteError = useCallback((serverMessage: string) => {
        onError(serverMessage || intl.formatMessage({defaultMessage: 'Couldn\'t rewrite your message. Try again.'}));
    }, [onError, intl]);

    // Checked on the first open, not on every composer mount.
    const [everOpened, setEverOpened] = useState(false);
    useEffect(() => {
        if (open) {
            setEverOpened(true);
        }
    }, [open]);
    const availability = useRewriteAvailability(Boolean(bot) && everOpened);
    const rewriter = useComposerRewrite({agentId: bot?.id ?? '', rootId, getText, setText, onError: rewriteError});

    useEffect(() => {
        onRewritingChange?.(rewriter.processing);
    }, [rewriter.processing, onRewritingChange]);

    const showTools = showComposerTools(bot, rootId);
    const showRewrite = Boolean(bot) && availability !== 'unavailable';

    const submenuProps = (id: Submenu) => ({
        open: submenu?.id === id,
        focusOnOpen: submenu?.id === id && submenu.focus,
        onOpen: (focus: boolean) => setSubmenu({id, focus}),
        onClose: () => setSubmenu(null),
    });

    const label = intl.formatMessage({defaultMessage: 'Add to message'});

    // The Dropdown's focus manager moves focus in when it mounts; a reopen
    // during the exit transition keeps it mounted, so move focus here too.
    useEffect(() => {
        if (!open) {
            return () => null;
        }
        const frame = requestAnimationFrame(() => {
            const el = document.getElementById(menuId);
            if (el && !el.contains(document.activeElement)) {
                focusFirstItem(el);
            }
        });
        return () => cancelAnimationFrame(frame);
    }, [open, menuId]);

    const handleMenuKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
        if (e.key === 'Escape') {
            e.preventDefault();
            e.stopPropagation();
            finish();
            return;
        }
        handleMenuNavigation(e);
    };

    const handleTriggerKeyDown = (e: React.KeyboardEvent<HTMLButtonElement>) => {
        if ((e.key === 'ArrowDown' || e.key === 'ArrowUp') && !open) {
            e.preventDefault();
            setMenuOpen(true);
        }
    };

    return (
        <Dropdown
            isOpen={menu.rendered}
            onOpenChange={setMenuOpen}
            placement='top-start'
            focusManager={RETURN_FOCUS_OFF}
            tooltip={label}
            target={(
                <PlusButton
                    id={triggerId}
                    size='small'
                    rounded={true}
                    icon={<Icon glyph={<PlusIcon/>}/>}
                    aria-label={label}
                    aria-haspopup='menu'
                    aria-expanded={open}
                    {...(rewriter.processing ? {'aria-busy': true} : {})}
                    active={open}
                    disabled={disabled}
                    data-testid='composer-plus-menu-button'
                    onClick={() => setMenuOpen(!open)}
                    onKeyDown={handleTriggerKeyDown}
                />
            )}
        >
            <PopoverMenu
                id={menuId}
                open={open}
                onExited={menu.onExited}
                role='menu'
                aria-label={label}
                onKeyDown={handleMenuKeyDown}
            >
                <PopoverMenuGroup>
                    {uploadEnabled && (
                        <MenuItem
                            role='menuitem'
                            label={intl.formatMessage({defaultMessage: 'Upload files'})}
                            leadingElement={true}
                            leadingVisual={menuIcon(<PaperclipIcon/>)}
                            data-testid='composer-plus-menu-upload'
                            onMouseEnter={() => setSubmenu(null)}
                            onClick={() => {
                                onUploadFiles();
                                finish();
                            }}
                        />
                    )}
                    {showTools && (
                        <ComposerToolsSubmenu
                            bot={bot}
                            onError={onError}
                            {...submenuProps('tools')}
                        />
                    )}
                    <ComposerPromptsSubmenu
                        {...submenuProps('prompts')}
                        onSelect={(prompt: CustomPrompt) => {
                            finish();
                            insertPrompt(prompt);
                        }}
                        onManage={() => {
                            finish();
                            manageCustomPrompts();
                        }}
                    />
                    {showRewrite && (
                        <ComposerRewriteSubmenu
                            availability={availability}
                            rewriter={rewriter}
                            text={getText()}
                            onDone={finish}
                            {...submenuProps('rewrite')}
                        />
                    )}
                </PopoverMenuGroup>
            </PopoverMenu>
        </Dropdown>
    );
};

// The composer's round, faintly filled 32px "+" (a small IconButton), placed by
// the composer's leading slot.
const PlusButton = styled(IconButton)`
    &&:not(:disabled):not(:hover):not(:active):not([aria-expanded='true']) {
        background: ${PLUS_FILL};
    }
`;

export default ComposerPlusMenu;
