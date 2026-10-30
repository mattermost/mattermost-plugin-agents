// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect} from 'react';
import {useIntl} from 'react-intl';
import HammerIcon from '@mattermost/compass-icons/components/hammer';

import type {LLMBot} from '@/bots';
import {useToolProviders} from '@/components/rhs/tool_provider_popover';

import ComposerSubmenu from './composer_submenu';
import ToolProviderMenuBody from './tool_provider_menu_body';
import {useComposerToolPreferences} from './use_composer_tool_preferences';

// The composer menu shows Tools in a new chat and in an existing thread, for an agent that does not
// use service account auth (those have no per-user connections or preferences).
export function showComposerTools(bot: LLMBot | null | undefined): bot is LLMBot {
    return Boolean(bot) && bot?.useServiceAccountAuth !== true;
}

interface Props {
    bot: LLMBot;
    open: boolean;
    focusOnOpen: boolean;
    onOpen: (focus: boolean) => void;
    onClose: () => void;
    onError: (message: string) => void;
}

// The tools popover's providers as checkable menu rows, through the same
// useToolProviders data and actions.
const ComposerToolsSubmenu = ({bot, open, focusOnOpen, onOpen, onClose, onError}: Props) => {
    const intl = useIntl();
    const prefs = useComposerToolPreferences(bot.id);
    const providers = useToolProviders({
        disabledServers: prefs.disabledServers,
        onDisabledServersChange: prefs.setDisabledServers,
        enabledMCPTools: bot.enabledMCPTools,
        autoEnableNewMCPTools: bot.autoEnableNewMCPTools,
    });

    // As the popover does when it opens.
    const {fetchServers} = providers;
    useEffect(() => {
        fetchServers();
    }, [fetchServers]);

    const label = intl.formatMessage({defaultMessage: 'Tools'});

    return (
        <ComposerSubmenu
            label={label}
            icon={<HammerIcon/>}
            menuLabel={label}
            open={open}
            focusOnOpen={focusOnOpen}
            onOpen={onOpen}
            onClose={onClose}
            data-testid='composer-plus-menu-tools'
        >
            <ToolProviderMenuBody
                status={prefs.status}
                onRetry={prefs.reload}
                disabledServers={prefs.disabledServers}
                providers={providers}
                onError={onError}
                refresh={false}
            />
        </ComposerSubmenu>
    );
};

export default ComposerToolsSubmenu;
