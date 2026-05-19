// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {Store, UnknownAction} from 'redux';
import styled from 'styled-components';
import {FormattedMessage, createIntl} from 'react-intl';

import {WebSocketMessage} from '@mattermost/client';
import {GlobalState} from '@mattermost/types/store';
import {CodeTagsIcon} from '@mattermost/compass-icons/components';

//@ts-ignore it exists
import aiIcon from '../../assets/bot_icon.png';

import manifest from '@/manifest';

import {LLMBotPost} from './components/llmbot_post/llmbot_post';
import PostMenu from './components/post_menu';
import IconThreadSummarization from './components/assets/icon_thread_summarization';
import IconReactForMe from './components/assets/icon_react_for_me';
import RHS from './components/rhs/rhs';
import CustomPromptsDropdown from './components/custom_prompts/custom_prompts_dropdown';
import CustomPromptsManagement from './components/custom_prompts/custom_prompts_management';
import Config from './components/system_console/config';
import {setSiteURL, doReaction, doRunSearch, doThreadAnalysis, getAIDirectChannel} from './client';

import {setOpenRHSAction} from './redux_actions';
import PostEventListener from './websocket';
import {BotsHandler, setupRedux} from './redux';
import UnreadsSummarize from './components/unreads_summarize';
import {PostbackPost} from './components/postback_post';
import {isRHSCompatable} from './mm_webapp';
import SearchButton from './components/search_button';
import AskChannelButton from './components/ask_channel_button';
import {doSelectPost} from './hooks';
import {invalidateConversation} from './hooks/use_conversation';
import {notifyMCPConnectionUpdated, MCPConnectionEvent} from './hooks/use_mcp_connection_events';
import {handleAskChannelCommand, handleSummarizeChannelCommand} from './commands';
import SearchHints from './components/search_hints';
import {useBotlist} from './bots';
import {shouldSuppressBotNotification} from './notifications';
import AgentsTour from './components/tutorial/agents_tour';
import AgentsPage, {AGENTS_ROUTE} from './components/agents/agents_page';
import IconAI from './components/assets/icon_ai';
import {isEnterpriseLicensedOrDevelopment} from './license';

type WebappStore = Store<GlobalState, UnknownAction>

function getAgentsProductLabel(store: WebappStore): string {
    const state = store.getState() as any;
    const locale = state.entities?.i18n?.locale ?? 'en';
    let messages: Record<string, string>;
    try {
        // eslint-disable-next-line global-require, import/no-dynamic-require
        messages = require(`./i18n/${locale}.json`);
    } catch {
        // eslint-disable-next-line global-require
        messages = require('./i18n/en.json');
    }
    const intl = createIntl({
        locale,
        messages,
        defaultLocale: 'en',
    });
    return intl.formatMessage({defaultMessage: 'Agents'});
}

const IconAIContainer = styled.img`
	border-radius: 50%;
    width: 24px;
    height: 24px;
`;

// Product switcher: in the global header, inherit the same muted header text color as the Channels glyph
// (see Mattermost ProductBranding). In the dropdown, match string product icons (ProductMenuItem uses --button-bg).
const ProductSwitcherIconWrapper = styled.span`
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 24px;
    min-width: 24px;
    height: 24px;
    flex-shrink: 0;
    color: inherit;

    .product-switcher-menu & {
        color: var(--button-bg);
    }

    svg {
        width: 18px;
        height: 18px;
    }
`;

const ProductSwitcherIconAI = (props: {className?: string}) => (
    <ProductSwitcherIconWrapper className={props.className}>
        <IconAI/>
    </ProductSwitcherIconWrapper>
);

const RHSTitleContainer = styled.span`
    display: flex;
	gap: 8px;
    align-items: center;
	margin-left: 8px;
`;

const RHSTitle = () => {
    return (
        <RHSTitleContainer>
            <IconAIContainer src={aiIcon}/>
            <FormattedMessage defaultMessage='Agents'/>
        </RHSTitleContainer>
    );
};

const ChannelHeaderIcon = () => {
    const {bots} = useBotlist();

    // Only show icon if user has access to at least one bot
    if (!bots || bots.length === 0) {
        return null;
    }

    return <IconAIContainer src={aiIcon}/>;
};

export default class Plugin {
    postEventListener: PostEventListener = new PostEventListener();
    private store: WebappStore | null = null;
    // eslint-disable-next-line @typescript-eslint/no-unused-vars, @typescript-eslint/no-empty-function
    public async initialize(registry: any, store: WebappStore) {
        setupRedux(registry, store);
        this.store = store;

        let siteURL = store.getState().entities.general.config.SiteURL;

        // Site URL should always be set by this point if the workspace is to be properly functional, but fall back to the window.location.origin just in case
        if (!siteURL) {
            siteURL = window.location.origin;
        }
        setSiteURL(siteURL);

        registry.registerDesktopNotificationHook(this.blockFastBotNotifications.bind(this));

        registry.registerTranslations((locale: string) => {
            try {
                // eslint-disable-next-line global-require
                return require(`./i18n/${locale}.json`);
            } catch (e) {
                return {};
            }
        });

        let rhs: any = null;
        if (isRHSCompatable()) {
            rhs = registry.registerRightHandSidebarComponent(RHS, RHSTitle);
            setOpenRHSAction(rhs.showRHSPlugin);
        }

        let currentUserId = store.getState().entities.users.currentUserId;
        if (currentUserId) {
            getAIDirectChannel(currentUserId).then((botChannelId) => {
                store.dispatch({type: 'SET_AI_BOT_CHANNEL', botChannelId} as any);
            });
        }

        store.subscribe(() => {
            const state = store.getState();
            if (state && state.entities.users.currentUserId !== currentUserId) {
                currentUserId = state.entities.users.currentUserId;
                if (currentUserId) {
                    getAIDirectChannel(currentUserId).then((botChannelId) => {
                        store.dispatch({type: 'SET_AI_BOT_CHANNEL', botChannelId} as any);
                    });
                } else {
                    store.dispatch({type: 'SET_AI_BOT_CHANNEL', botChannelId: ''} as any);
                }
            }
        });

        // Handle all post-related websocket events with one handler
        registry.registerWebSocketEventHandler('custom_mattermost-ai_postupdate', this.postEventListener.handlePostUpdateWebsockets);
        registry.registerWebSocketEventHandler('custom_mattermost-ai_tool_call_status_updated', this.postEventListener.handlePostUpdateWebsockets);

        // Invalidate conversation cache when backend publishes conversation updates
        registry.registerWebSocketEventHandler(
            'custom_mattermost-ai_conversation_updated',
            (msg: WebSocketMessage<{conversation_id: string}>) => {
                invalidateConversation(msg.data.conversation_id);
            },
        );

        // MCP OAuth connect/disconnect: refresh cached tool lists in open UI.
        registry.registerWebSocketEventHandler(
            'custom_mattermost-ai_mcp_connection_updated',
            (msg: WebSocketMessage<MCPConnectionEvent>) => {
                notifyMCPConnectionUpdated(msg.data);
            },
        );

        const LLMBotPostWithWebsockets = (props: any) => {
            return (
                <LLMBotPost
                    {...props}
                    websocketRegister={this.postEventListener.registerPostUpdateListener}
                    websocketUnregister={this.postEventListener.unregisterPostUpdateListener}
                />
            );
        };

        const invalidateRuntimeBotsCache = () => {
            store.dispatch({
                type: BotsHandler,
                bots: null,
            } as any);
        };

        registry.registerWebSocketEventHandler('config_changed', invalidateRuntimeBotsCache);

        // Agent CRUD refreshes server-side bot cache but does not emit config_changed; mirror that invalidate so RHS dropdown refetches.
        registry.registerWebSocketEventHandler('custom_mattermost-ai_bots_invalidate', invalidateRuntimeBotsCache);

        registry.registerPostTypeComponent('custom_llmbot', LLMBotPostWithWebsockets);
        registry.registerPostTypeComponent('custom_llm_postback', PostbackPost);
        if (registry.registerPostActionComponent) {
            registry.registerPostActionComponent(PostMenu);
        } else {
            registry.registerPostDropdownMenuAction(<><span className='icon'><IconThreadSummarization/></span><FormattedMessage defaultMessage='Summarize Thread'/></>, (postId: string) => {
                const state = store.getState();
                const team = state.entities.teams.teams[state.entities.teams.currentTeamId];
                window.WebappUtils.browserHistory.push('/' + team.name + '/messages/@ai');
                doThreadAnalysis(postId, 'summarize_thread', '');
                if (rhs) {
                    store.dispatch(rhs.showRHSPlugin);
                }
            });
            registry.registerPostDropdownMenuAction(<><span className='icon'><IconReactForMe/></span><FormattedMessage defaultMessage='React for me'/></>, doReaction);
        }

        registry.registerAdminConsoleCustomSetting('Config', Config);
        const agentsProductLabel = getAgentsProductLabel(store);

        if (rhs) {
            registry.registerChannelHeaderButtonAction(<ChannelHeaderIcon/>, () => {
                store.dispatch(rhs.toggleRHSPlugin);
            },
            agentsProductLabel,
            agentsProductLabel,
            );
        }

        if (registry.registerNewMessagesSeparatorActionComponent) {
            registry.registerNewMessagesSeparatorActionComponent(UnreadsSummarize);
        }

        if (registry.registerChannelHeaderIcon) {
            registry.registerChannelHeaderIcon(AskChannelButton);
        }

        // Register slash commands
        if (rhs) {
            registry.registerSlashCommandWillBePostedHook((message: string, args: any) => {
                if ((message.startsWith('/ask-channel') || message.startsWith('/summarize-channel')) &&
                    !isEnterpriseLicensedOrDevelopment(store.getState())) {
                    return {
                        error: {
                            message: 'The /ask-channel and /summarize-channel commands are available on Enterprise plans.',
                        },
                    };
                }

                if (message.startsWith('/ask-channel')) {
                    const query = message.replace('/ask-channel', '').trim();
                    return handleAskChannelCommand(query, args, store, rhs);
                } else if (message.startsWith('/summarize-channel')) {
                    const commandParams = message.replace('/summarize-channel', '').trim();
                    return handleSummarizeChannelCommand(commandParams, args, store, rhs);
                }
                return {message, args};
            });
        }

        if (registry.registerRootComponent) {
            registry.registerRootComponent(AgentsTour);
            registry.registerRootComponent(CustomPromptsManagement);
        }

        // Register Agents as a product so it appears in the product switcher (grid icon).
        // registerProduct is an internal Mattermost API available on the plugin registry.
        if ((registry as any).registerProduct) {
            (registry as any).registerProduct(
                AGENTS_ROUTE,
                ProductSwitcherIconAI,
                agentsProductLabel,
                AGENTS_ROUTE,
                AgentsPage,
            );
        }

        if (registry.registerSearchComponents) {
            registry.registerSearchComponents({
                buttonComponent: SearchButton,
                suggestionsComponent: () => null,
                hintsComponent: SearchHints,
                action: async (searchTerms: string) => {
                    // Get the active bot from the state
                    const state = store.getState() as any;
                    const bots = state['plugins-' + manifest.id]?.bots || [];
                    const activeBotUsername = localStorage.getItem('defaultBot') || '';
                    const activeBot = bots.find((bot: any) => bot.username === activeBotUsername);

                    const result = await doRunSearch(
                        searchTerms,
                        '',
                        '',
                        activeBot?.username,
                    );
                    doSelectPost(result.postid, result.channelid, store.dispatch);
                    if (rhs) {
                        store.dispatch(rhs.showRHSPlugin);
                    }
                },
            });
        }

        // Register Custom Prompts AI action menu item
        if (registry.registerAIActionMenuItemComponent) {
            registry.registerAIActionMenuItemComponent({
                icon: <CodeTagsIcon size={18}/>,
                text: <FormattedMessage defaultMessage='Custom prompts'/>,
                sortOrder: 10,
                component: CustomPromptsDropdown,
            });
        }
    }

    private async blockFastBotNotifications(
        post: any,
        msgProps: any,
        channel: any,
        teamId: string,
        args: any,
    ): Promise<{args?: any; error?: string}> {
        const state = this.store?.getState();
        const parentPost = post?.root_id ? state?.entities.posts.posts[post.root_id] : null;
        const currentUserId = state?.entities.users.currentUserId;

        if (shouldSuppressBotNotification(post, {currentUserId, parentPost, now: Date.now()})) {
            return {args: {...args, notify: false}};
        }
        return {args};
    }
}

declare global {
    interface Window {
        registerPlugin(pluginId: string, plugin: Plugin): void
        WebappUtils?: any
    }
}

window.registerPlugin(manifest.id, new Plugin());
