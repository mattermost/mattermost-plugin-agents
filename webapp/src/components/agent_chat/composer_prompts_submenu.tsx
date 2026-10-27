// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import CodeTagsIcon from '@mattermost/compass-icons/components/code-tags';
import CogOutlineIcon from '@mattermost/compass-icons/components/cog-outline';
import PinOutlineIcon from '@mattermost/compass-icons/components/pin-outline';
import RefreshIcon from '@mattermost/compass-icons/components/refresh';

import {MenuGroupHeading} from '@mattermost/compass-ui/components/menu-group-heading';
import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenuDivider, PopoverMenuGroup} from '@mattermost/compass-ui/components/popover-menu';

import type {CustomPrompt} from '@/types';

import ComposerSubmenu from './composer_submenu';
import {ErrorRow, LoadingRow, StatusRow, menuIcon} from './composer_menu_parts';
import {useComposerCustomPrompts} from './use_composer_custom_prompts';

interface Props {
    open: boolean;
    focusOnOpen: boolean;
    onOpen: (focus: boolean) => void;
    onClose: () => void;
    onSelect: (prompt: CustomPrompt) => void;
    onManage: () => void;
}

// The host composer's "Custom prompts" AI action (CustomPromptsDropdown) as a
// submenu: the user's prompts, pinned first, then "Manage prompts".
const ComposerPromptsSubmenu = ({open, focusOnOpen, onOpen, onClose, onSelect, onManage}: Props) => {
    const intl = useIntl();
    const {status, pinned, others, reload} = useComposerCustomPrompts();
    const label = intl.formatMessage({defaultMessage: 'Custom prompts'});
    const hasPrompts = pinned.length + others.length > 0;

    const promptItem = (prompt: CustomPrompt, isPinned: boolean) => (
        <MenuItem
            key={prompt.id}
            role='menuitem'
            label={prompt.name}
            leadingElement={isPinned}
            {...(isPinned ? {leadingVisual: menuIcon(<PinOutlineIcon/>)} : {})}
            onClick={() => onSelect(prompt)}
        />
    );

    let list: React.ReactNode = null;
    if (status === 'error') {
        list = (
            <>
                <ErrorRow role='alert'>
                    {intl.formatMessage({defaultMessage: 'Couldn\'t load custom prompts.'})}
                </ErrorRow>
                <MenuItem
                    role='menuitem'
                    label={intl.formatMessage({defaultMessage: 'Try again'})}
                    leadingElement={true}
                    leadingVisual={menuIcon(<RefreshIcon/>)}
                    onClick={reload}
                />
            </>
        );
    } else if (status === 'loading' && !hasPrompts) {
        list = <LoadingRow label={intl.formatMessage({defaultMessage: 'Loading custom prompts…'})}/>;
    } else if (hasPrompts) {
        list = (
            <>
                {pinned.length > 0 && (
                    <PopoverMenuGroup aria-label={intl.formatMessage({defaultMessage: 'Pinned'})}>
                        <MenuGroupHeading
                            aria-hidden={true}
                            label={intl.formatMessage({defaultMessage: 'Pinned'})}
                        />
                        {pinned.map((p) => promptItem(p, true))}
                    </PopoverMenuGroup>
                )}
                {pinned.length > 0 && others.length > 0 && <PopoverMenuDivider/>}
                {others.length > 0 && (
                    <PopoverMenuGroup aria-label={label}>
                        {others.map((p) => promptItem(p, false))}
                    </PopoverMenuGroup>
                )}
            </>
        );
    } else {
        list = (
            <StatusRow role='status'>
                {intl.formatMessage({defaultMessage: 'No custom prompts yet'})}
            </StatusRow>
        );
    }

    return (
        <ComposerSubmenu
            label={label}
            icon={<CodeTagsIcon/>}
            menuLabel={label}
            open={open}
            focusOnOpen={focusOnOpen}
            onOpen={onOpen}
            onClose={onClose}
            data-testid='composer-plus-menu-prompts'
        >
            {list}
            <PopoverMenuDivider/>
            <MenuItem
                role='menuitem'
                label={intl.formatMessage({defaultMessage: 'Manage prompts'})}
                leadingElement={true}
                leadingVisual={menuIcon(<CogOutlineIcon/>)}
                onClick={onManage}
            />
        </ComposerSubmenu>
    );
};

export default ComposerPromptsSubmenu;
