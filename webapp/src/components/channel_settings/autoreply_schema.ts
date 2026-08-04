// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ComponentType} from 'react';
import {IntlShape} from 'react-intl';
import {Store, UnknownAction} from 'redux';

import {ClientError} from '@mattermost/client';
import {Channel} from '@mattermost/types/channels';
import {GlobalState} from '@mattermost/types/store';

import {ChannelAutoReplyMode, ChannelAutoReplySettings, getChannelAutoReply, updateChannelAutoReply} from '@/client';
import {LLMBot, fetchAndStoreBots, filterBotsByChannelAccess} from '@/bots';
import {
    PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES,
    PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES,
    userHasChannelPermission,
} from '@/utils/permissions';
import manifest from '@/manifest';

import {AutoReplyAgentPicker} from './autoreply_agent_picker';
import {
    normalizeChannelAutoReply,
    setChannelAutoReplyDraft,
    setChannelAutoReplySaveError,
} from './autoreply_state';

export type WebappStore = Store<GlobalState, UnknownAction>;

// Local structural mirrors of the host's channel-settings schema types
// (mattermost master webapp/channels/src/types/plugins/channel_settings.ts and
// plugins/settings_schema/types.ts); @mattermost/types 11.4.0 predates them.
export type ChannelSettingsValues = {[name: string]: string};
type RadioOption = {value: string; text: string; helpText?: string};
type RadioSetting = {name: string; type: 'radio'; title?: string; helpText?: string; default: string; options: RadioOption[]};
type CustomSetting = {name: string; type: 'custom'; component: ComponentType<{informChange: (name: string, value: string) => void}>};
type SettingsSection = {title: string; settings: Array<RadioSetting | CustomSetting>};
export type ChannelAutoReplyTabRegistration = {
    uiName: string;
    icon: string;
    shouldRender: (state: GlobalState, channel: Channel) => boolean;
    sections: SettingsSection[];
    loadValues: (channel: Channel) => Promise<ChannelSettingsValues>;
    onSave: (values: ChannelSettingsValues, channel: Channel) => Promise<void>;
};

function botsFromState(state: GlobalState): LLMBot[] | null {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    return (state as any)['plugins-' + manifest.id]?.bots ?? null;
}

// Synchronous and cheap: the host evaluates this inside a selector on every
// relevant store change while the channel menu or settings modal is rendered.
// It is also the only gate on tab visibility; because it requires the same
// manage-properties permission that gates the built-in Info tab, the plugin
// tab never expands Channel Settings menu-item visibility beyond core.
export const shouldRenderChannelAutoReplyTab = (state: GlobalState, channel: Channel): boolean => {
    if (channel.type !== 'O' && channel.type !== 'P') {
        return false;
    }
    const permission = channel.type === 'P' ? PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES : PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES;
    if (!userHasChannelPermission(state, channel.team_id, channel.id, permission)) {
        return false;
    }
    const bots = botsFromState(state);
    if (!bots) {
        // Cache cold or invalidated; the init warm-up and the lazy refetch in
        // useBotlist self-heal it.
        return false;
    }
    return filterBotsByChannelAccess(bots, channel.id).length > 0;
};

export const makeLoadValues = (store: WebappStore) => async (channel: Channel): Promise<ChannelSettingsValues> => {
    let raw: ChannelAutoReplySettings;
    try {
        raw = await getChannelAutoReply(channel.id);
    } catch (e) {
        // A failed GET (e.g. 403 from the default-agent middleware) must not
        // leave a previous channel's draft visible: clear it so the picker
        // renders its load-failure message, then let the host fall back to
        // schema defaults.
        setChannelAutoReplyDraft(null);
        throw e;
    }
    let bots = botsFromState(store.getState());
    if (!bots) {
        bots = await fetchAndStoreBots(store.dispatch).catch(() => null);
    }
    const saved = normalizeChannelAutoReply(raw, bots ?? [], channel.id);
    setChannelAutoReplyDraft({channelId: channel.id, saved, saveError: null});
    return {mode: saved.mode, bot_id: saved.bot_id};
};

export const makeOnSave = () => async (values: ChannelSettingsValues, channel: Channel): Promise<void> => {
    const mode: ChannelAutoReplyMode = values.mode === 'root_posts' || values.mode === 'threads' ? values.mode : 'off';
    const botId = mode === 'off' ? '' : (values.bot_id ?? '');
    if (mode !== 'off' && !botId) {
        setChannelAutoReplySaveError('no_agent');
        throw new Error('no agent selected for channel auto-reply');
    }
    try {
        await updateChannelAutoReply(channel.id, {bot_id: botId, mode});
    } catch (e) {
        // The endpoints answer with a bare status code (no JSON error body),
        // so error handling keys off the status only.
        const status = e instanceof ClientError ? e.status_code : 0;
        setChannelAutoReplySaveError(status === 403 ? 'forbidden' : 'generic');

        // The host swallows the rejection and keeps the tab dirty; the picker
        // is the plugin-rendered element that displays the recorded error.
        throw e;
    }
    setChannelAutoReplyDraft({channelId: channel.id, saved: {bot_id: botId, mode}, saveError: null});
};

// Builds the registration passed to registry.registerChannelSettingsTab. Must
// be called exactly once at init: the host re-runs hydration whenever the
// schema object reference changes, which would destroy in-progress user edits.
export function makeChannelAutoReplySchema(store: WebappStore, intl: IntlShape): ChannelAutoReplyTabRegistration {
    return {
        uiName: intl.formatMessage({defaultMessage: 'Agents'}),
        icon: 'icon-creation-outline',
        shouldRender: shouldRenderChannelAutoReplyTab,
        sections: [{
            title: intl.formatMessage({defaultMessage: 'Automatic replies'}),
            settings: [
                {
                    name: 'mode',
                    type: 'radio',
                    title: intl.formatMessage({defaultMessage: 'Auto-reply mode'}),
                    helpText: intl.formatMessage({defaultMessage: 'An automatic reply behaves exactly as if the author had @-mentioned the agent.'}),
                    default: 'off',
                    options: [
                        {
                            value: 'off',
                            text: intl.formatMessage({defaultMessage: 'Off'}),
                            helpText: intl.formatMessage({defaultMessage: 'The agent replies only when @-mentioned.'}),
                        },
                        {
                            value: 'root_posts',
                            text: intl.formatMessage({defaultMessage: 'Top-level posts only'}),
                            helpText: intl.formatMessage({defaultMessage: 'The agent automatically replies to new top-level posts, starting a thread.'}),
                        },
                        {
                            value: 'threads',
                            text: intl.formatMessage({defaultMessage: 'Threads too'}),
                            helpText: intl.formatMessage({defaultMessage: 'The agent also automatically replies to replies in threads.'}),
                        },
                    ],
                },
                {

                    // No title/helpText/default: the host ignores title and
                    // helpText for custom settings (the picker renders its own
                    // label), drops falsy defaults, and loadValues always
                    // supplies bot_id.
                    name: 'bot_id',
                    type: 'custom',
                    component: AutoReplyAgentPicker,
                },
            ],
        }],
        loadValues: makeLoadValues(store),
        onSave: makeOnSave(),
    };
}
