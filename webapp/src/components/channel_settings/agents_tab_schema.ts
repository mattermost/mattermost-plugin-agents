// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {ComponentType} from 'react';
import {IntlShape} from 'react-intl';
import {Store, UnknownAction} from 'redux';

import {ClientError} from '@mattermost/client';
import {Channel} from '@mattermost/types/channels';
import {GlobalState} from '@mattermost/types/store';

import {
    ChannelAutoReplyMode,
    ChannelAutoReplySettings,
    getChannelAutoReply,
    getChannelInstructions,
    updateChannelAutoReply,
    updateChannelInstructions,
} from '@/client';
import {LLMBot, fetchAndStoreBots, filterBotsByChannelAccess} from '@/bots';
import {
    PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES,
    PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES,
    userHasChannelPermission,
} from '@/utils/permissions';
import manifest from '@/manifest';
import {licenseAllows} from '@/license';

import {AutoReplyAgentPicker} from './autoreply_agent_picker';
import {
    normalizeChannelAutoReply,
    setChannelAutoReplyDraft,
    setChannelAutoReplySaveError,
} from './autoreply_state';
import {ChannelContextPostsSection} from './channel_context_posts_section';
import {ChannelInstructionsSetting} from './channel_instructions_setting';
import {setChannelInstructionsDraft, setChannelInstructionsSaveError} from './channel_instructions_state';

export type WebappStore = Store<GlobalState, UnknownAction>;

// Local structural mirrors of the host's channel-settings schema types
// (mattermost master webapp/channels/src/types/plugins/channel_settings.ts and
// plugins/settings_schema/types.ts); @mattermost/types 11.4.0 predates them.
export type ChannelSettingsValues = {[name: string]: string};
type RadioOption = {value: string; text: string; helpText?: string};
type RadioSetting = {name: string; type: 'radio'; title?: string; helpText?: string; default: string; options: RadioOption[]};
type CustomSetting = {name: string; type: 'custom'; component: ComponentType<{informChange: (name: string, value: string) => void}>};
type SettingsSection = {title: string; settings: Array<RadioSetting | CustomSetting>};
type CustomSection = {title: string; component: ComponentType};
export type ChannelAgentsTabRegistration = {
    uiName: string;
    icon: string;
    shouldRender: (state: GlobalState, channel: Channel) => boolean;
    sections: Array<SettingsSection | CustomSection>;
    loadValues: (channel: Channel) => Promise<ChannelSettingsValues>;
    onSave: (values: ChannelSettingsValues, channel: Channel) => Promise<void>;
};

// The plugin's redux slice is registered dynamically, so GlobalState carries
// no typed entry for it; narrow through a keyed record of the one field read
// here instead of `any`.
type PluginStateSlice = {bots?: LLMBot[] | null};

export function botsFromState(state: GlobalState): LLMBot[] | null {
    const slice = (state as unknown as Record<string, PluginStateSlice | undefined>)['plugins-' + manifest.id];
    return slice?.bots ?? null;
}

// Synchronous and cheap: the host evaluates this inside a selector on every
// relevant store change while the channel menu or settings modal is rendered.
// It is also the only gate on tab visibility; because it requires the same
// manage-properties permission that gates the built-in Info tab, the plugin
// tab never expands Channel Settings menu-item visibility beyond core.
export const shouldRenderChannelAgentsTab = (state: GlobalState, channel: Channel): boolean => {
    if (channel.type !== 'O' && channel.type !== 'P') {
        return false;
    }

    // Auto-reply and channel agent context are available at Enterprise
    // Advanced; below it stored settings are inactive and stay clearable
    // through the REST API.
    if (!licenseAllows(state, 'channel_auto_reply') && !licenseAllows(state, 'channel_context')) {
        return false;
    }
    const permission = channel.type === 'P' ? PERMISSION_MANAGE_PRIVATE_CHANNEL_PROPERTIES : PERMISSION_MANAGE_PUBLIC_CHANNEL_PROPERTIES;
    if (!userHasChannelPermission(state, channel.team_id, channel.id, permission)) {
        return false;
    }
    const bots = botsFromState(state);
    if (!bots) {
        // Only cold before the init warm-up lands: websocket invalidations
        // refetch in place instead of clearing, so the cache never returns to
        // null once populated. The warm-up and useBotlist's lazy refetch fill
        // it in.
        return false;
    }
    return filterBotsByChannelAccess(bots, channel.id).length > 0;
};

// Shared by one registration's loadValues and onSave: the values last handed
// to the host by loadValues or a successful save, i.e. the host's own
// baseline. onSave compares against it rather than against drafts that
// websocket re-syncs update, so an unrelated save never writes a stale value
// back over a remote change. values is null until hydration succeeds.
export type AgentsTabBaseline = {values: ChannelSettingsValues | null};

export const makeLoadValues = (store: WebappStore, baseline: AgentsTabBaseline) => async (channel: Channel): Promise<ChannelSettingsValues> => {
    let raw: ChannelAutoReplySettings;
    let instructions: string;
    try {
        [raw, {instructions}] = await Promise.all([
            getChannelAutoReply(channel.id),
            getChannelInstructions(channel.id),
        ]);
    } catch (e) {
        // A failed GET must not leave a previous channel's drafts visible:
        // clear them so the custom settings render their load-failure
        // messages, then let the host fall back to schema defaults.
        baseline.values = null;
        setChannelAutoReplyDraft(null);
        setChannelInstructionsDraft(null);
        throw e;
    }
    let bots = botsFromState(store.getState());
    if (!bots) {
        bots = await fetchAndStoreBots(store.dispatch).catch(() => null);
    }

    // bots stays null when the fetch failed; normalization then preserves the
    // saved agent instead of clearing it like an empty agent list would.
    const saved = normalizeChannelAutoReply(raw, bots, channel.id);
    setChannelAutoReplyDraft({channelId: channel.id, saved, saveError: null});
    setChannelInstructionsDraft({channelId: channel.id, saved: instructions, saveError: null});
    baseline.values = {mode: saved.mode, bot_id: saved.bot_id, instructions};
    return baseline.values;
};

function saveErrorStatus(e: unknown): number {
    // The endpoints answer with a bare status code (no JSON error body), so
    // error handling keys off the status only.
    return e instanceof ClientError ? e.status_code ?? 0 : 0;
}

async function saveAutoReply(values: ChannelSettingsValues, channel: Channel, baseline: AgentsTabBaseline): Promise<void> {
    const mode: ChannelAutoReplyMode = values.mode === 'root_posts' || values.mode === 'threads' ? values.mode : 'off';
    const botId = mode === 'off' ? '' : (values.bot_id ?? '');
    const before = baseline.values;
    if (before && mode === before.mode && (mode === 'off' || botId === before.bot_id)) {
        return;
    }
    if (mode !== 'off' && !botId) {
        setChannelAutoReplySaveError('no_agent');
        throw new Error('no agent selected for channel auto-reply');
    }
    try {
        await updateChannelAutoReply(channel.id, {bot_id: botId, mode});
    } catch (e) {
        setChannelAutoReplySaveError(saveErrorStatus(e) === 403 ? 'forbidden' : 'generic');

        // The host swallows the rejection and keeps the tab dirty; the picker
        // is the plugin-rendered element that displays the recorded error.
        throw e;
    }
    setChannelAutoReplyDraft({channelId: channel.id, saved: {bot_id: botId, mode}, saveError: null});
    if (baseline.values) {
        baseline.values = {...baseline.values, mode, bot_id: values.bot_id ?? ''};
    }
}

async function saveInstructions(values: ChannelSettingsValues, channel: Channel, baseline: AgentsTabBaseline): Promise<void> {
    const instructions = values.instructions;

    // Absent when hydration failed and the host fell back to schema defaults.
    if (typeof instructions === 'undefined' || instructions === baseline.values?.instructions) {
        return;
    }
    let saved: string;
    try {
        ({instructions: saved} = await updateChannelInstructions(channel.id, instructions));
    } catch (e) {
        const status = saveErrorStatus(e);
        if (status === 403) {
            setChannelInstructionsSaveError('forbidden');
        } else if (status === 400 || status === 413) {
            setChannelInstructionsSaveError('invalid');
        } else {
            setChannelInstructionsSaveError('generic');
        }
        throw e;
    }
    setChannelInstructionsDraft({channelId: channel.id, saved, saveError: null});
    if (baseline.values) {
        baseline.values = {...baseline.values, instructions};
    }
}

// Saves only the parts the user changed, so editing the instructions never
// re-validates (or re-audits) an untouched auto-reply setting and vice versa.
// If one part fails after the other succeeded, the host keeps the tab dirty
// and a retry re-sends only the failed part. Without a baseline (hydration
// failed) the auto-reply values are always sent.
export const makeOnSave = (baseline: AgentsTabBaseline) => async (values: ChannelSettingsValues, channel: Channel): Promise<void> => {
    await saveAutoReply(values, channel, baseline);
    await saveInstructions(values, channel, baseline);
};

// Builds the registration passed to registry.registerChannelSettingsTab. Must
// be called exactly once at init: the host re-runs hydration whenever the
// schema object reference changes, which would destroy in-progress user edits.
export function makeChannelAgentsTabSchema(store: WebappStore, intl: IntlShape): ChannelAgentsTabRegistration {
    const baseline: AgentsTabBaseline = {values: null};
    return {
        uiName: intl.formatMessage({defaultMessage: 'Agents'}),
        icon: 'icon-creation-outline',
        shouldRender: shouldRenderChannelAgentsTab,
        sections: [
            {
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
            },
            {
                title: intl.formatMessage({defaultMessage: 'Channel instructions'}),
                settings: [
                    {
                        name: 'instructions',
                        type: 'custom',
                        component: ChannelInstructionsSetting,
                    },
                ],
            },
            {
                title: intl.formatMessage({defaultMessage: 'Pinned agent context'}),
                component: ChannelContextPostsSection,
            },
        ],
        loadValues: makeLoadValues(store, baseline),
        onSave: makeOnSave(baseline),
    };
}
