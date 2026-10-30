// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';
import AiSummarizeIcon from '@mattermost/compass-icons/components/ai-summarize';
import AutoFixIcon from '@mattermost/compass-icons/components/auto-fix';
import CloseIcon from '@mattermost/compass-icons/components/close';
import CreationOutlineIcon from '@mattermost/compass-icons/components/creation-outline';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';
import RefreshIcon from '@mattermost/compass-icons/components/refresh';
import SpellcheckIcon from '@mattermost/compass-icons/components/spellcheck';
import TextLongIcon from '@mattermost/compass-icons/components/text-long';
import TextShortIcon from '@mattermost/compass-icons/components/text-short';

import {MenuItem} from '@mattermost/compass-ui/components/menu-item';
import {PopoverMenuDivider, PopoverMenuGroup} from '@mattermost/compass-ui/components/popover-menu';
import {TextInput} from '@mattermost/compass-ui/components/text-input';

import type {RewriteAction} from '@/client';

import ComposerSubmenu from './composer_submenu';
import {LoadingRow, menuIcon} from './composer_menu_parts';
import type {ComposerRewrite, RewriteAvailability} from './use_composer_rewrite';

// The host composer's rewrite actions (advanced_text_editor/rewrite_menu.tsx
// menuItems), in its order and with its labels and icons. Labels resolve at
// render (formatMessage literals, so i18n extraction sees them).
export const REWRITE_ACTIONS: Array<{action: Exclude<RewriteAction, 'custom'>; icon: React.ReactNode}> = [
    {action: 'shorten', icon: <TextShortIcon/>},
    {action: 'elaborate', icon: <TextLongIcon/>},
    {action: 'improve_writing', icon: <AutoFixIcon/>},
    {action: 'fix_spelling', icon: <SpellcheckIcon/>},
    {action: 'simplify', icon: <CreationOutlineIcon/>},
    {action: 'summarize', icon: <AiSummarizeIcon/>},
];

const useRewriteActionLabel = () => {
    const intl = useIntl();
    return (action: Exclude<RewriteAction, 'custom'>): string => {
        switch (action) {
        case 'shorten':
            return intl.formatMessage({defaultMessage: 'Shorten'});
        case 'elaborate':
            return intl.formatMessage({defaultMessage: 'Elaborate'});
        case 'improve_writing':
            return intl.formatMessage({defaultMessage: 'Improve writing'});
        case 'fix_spelling':
            return intl.formatMessage({defaultMessage: 'Fix spelling and grammar'});
        case 'simplify':
            return intl.formatMessage({defaultMessage: 'Simplify'});
        default:
            return intl.formatMessage({defaultMessage: 'Summarize'});
        }
    };
};

interface Props {
    availability: RewriteAvailability;
    rewriter: ComposerRewrite;
    text: string;
    open: boolean;
    focusOnOpen: boolean;
    onOpen: (focus: boolean) => void;
    onClose: () => void;

    // Closes the whole "+" menu once a rewrite starts.
    onDone: () => void;
}

const ComposerRewriteSubmenu = ({availability, rewriter, text, open, focusOnOpen, onOpen, onClose, onDone}: Props) => {
    const intl = useIntl();
    const actionLabel = useRewriteActionLabel();
    const [prompt, setPrompt] = useState('');
    const label = intl.formatMessage({defaultMessage: 'AI Rewrite'});
    const hasText = text.trim().length > 0;

    let secondaryLabel: string | undefined;
    if (rewriter.processing) {
        secondaryLabel = intl.formatMessage({defaultMessage: 'Rewriting…'});
    } else if (availability === 'error') {
        secondaryLabel = intl.formatMessage({defaultMessage: 'Unavailable'});
    }

    let placeholder = intl.formatMessage({defaultMessage: 'Ask AI to edit message...'});
    if (!hasText) {
        placeholder = intl.formatMessage({defaultMessage: 'Create a new message...'});
    } else if (rewriter.canDiscard) {
        placeholder = intl.formatMessage({defaultMessage: 'What would you like AI to do next?'});
    }

    const start = (action: RewriteAction, customPrompt?: string) => {
        rewriter.rewrite(action, customPrompt);
        setPrompt('');
        onDone();
    };

    const handlePromptKeyDown = (e: React.KeyboardEvent<HTMLInputElement>) => {
        if (e.key !== 'Enter' || e.nativeEvent.isComposing) {
            return;
        }
        e.preventDefault();
        if (prompt.trim()) {
            start('custom', prompt.trim());
        }
    };

    return (
        <ComposerSubmenu
            label={label}
            icon={<PencilOutlineIcon/>}
            secondaryLabel={secondaryLabel}
            disabled={availability !== 'available' && !rewriter.processing}
            menuLabel={label}
            open={open}
            focusOnOpen={focusOnOpen}
            onOpen={onOpen}
            onClose={onClose}
            data-testid='composer-plus-menu-rewrite'
        >
            {rewriter.processing ? (
                <>
                    <LoadingRow label={intl.formatMessage({defaultMessage: 'Rewriting…'})}/>
                    <MenuItem
                        role='menuitem'
                        label={intl.formatMessage({defaultMessage: 'Stop generating'})}
                        leadingElement={true}
                        leadingVisual={menuIcon(<CloseIcon/>)}
                        onClick={rewriter.stop}
                    />
                </>
            ) : (
                <>
                    <PromptField>
                        <TextInput
                            size='small'
                            value={prompt}
                            placeholder={placeholder}
                            aria-label={intl.formatMessage({defaultMessage: 'Ask AI to edit message'})}
                            onChange={(e) => setPrompt(e.target.value)}
                            onKeyDown={handlePromptKeyDown}
                        />
                    </PromptField>
                    <PopoverMenuGroup aria-label={intl.formatMessage({defaultMessage: 'Rewrite actions'})}>
                        {REWRITE_ACTIONS.map((item) => (
                            <MenuItem
                                key={item.action}
                                role='menuitem'
                                label={actionLabel(item.action)}
                                leadingElement={true}
                                leadingVisual={menuIcon(item.icon)}
                                disabled={!hasText}
                                onClick={() => start(item.action)}
                            />
                        ))}
                    </PopoverMenuGroup>
                    {rewriter.canDiscard && (
                        <>
                            <PopoverMenuDivider/>
                            <PopoverMenuGroup>
                                <MenuItem
                                    role='menuitem'
                                    label={intl.formatMessage({defaultMessage: 'Discard'})}
                                    leadingElement={true}
                                    leadingVisual={menuIcon(<CloseIcon/>)}
                                    onClick={() => {
                                        rewriter.discard();
                                        onDone();
                                    }}
                                />
                                <MenuItem
                                    role='menuitem'
                                    label={intl.formatMessage({defaultMessage: 'Regenerate'})}
                                    leadingElement={true}
                                    leadingVisual={menuIcon(<RefreshIcon/>)}
                                    onClick={() => {
                                        rewriter.regenerate();
                                        onDone();
                                    }}
                                />
                            </PopoverMenuGroup>
                        </>
                    )}
                </>
            )}
        </ComposerSubmenu>
    );
};

// The host's rewrite menu header: the instruction field above the actions.
const PromptField = styled.div`
    padding: var(--spacing-xxs) var(--spacing-s) var(--spacing-xs);
`;

export default ComposerRewriteSubmenu;
