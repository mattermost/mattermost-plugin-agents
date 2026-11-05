// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useMemo, useRef, useState} from 'react';
import {defineMessages, useIntl, type MessageDescriptor} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';
import styled, {css, keyframes} from 'styled-components';

import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import {createPost, getProfilePictureUrl, renderCustomPrompt} from '@/client';
import type {LLMBot} from '@/bots';
import {AdvancedTextEditor} from '@/mm_webapp';
import {noMotionWhenReduced} from '@/components/llmbot_post/motion';
import {fetchCustomPrompts, fetchPinnedPromptIds} from '@/redux';
import {getCustomPrompts, getPinnedPromptIds, isCustomPromptsLoaded, isPinnedPromptIdsLoaded} from '@/selectors';
import type {CustomPrompt} from '@/types';

import {CHAT_CONTENT_MAX_WIDTH, HOST_COMPOSER_CONTAINER_CLASS, hostComposerInset} from './agent_chat_host_styles';
import StarterPrompts from './starter_prompts';

// Greeting pairs for the new-chat state. The titles are the prototype's
// EMPTY_CHAT_TITLES; each subtitle belongs to its title, so both are picked
// together by one index.
const greetingMessages = defineMessages({
    helpTodayTitle: {defaultMessage: 'How can I help today?'},
    helpTodaySubtitle: {defaultMessage: 'Ask a question, draft something, or hand off a task.'},
    getWorkDoneTitle: {defaultMessage: 'Let\'s get some work done'},
    getWorkDoneSubtitle: {defaultMessage: 'Tell me what you\'re working on and we\'ll start there.'},
    tackleTitle: {defaultMessage: 'What should we tackle?'},
    tackleSubtitle: {defaultMessage: 'Pick a project, a problem, or just a rough idea.'},
    readyTitle: {defaultMessage: 'Ready when you are'},
    readySubtitle: {defaultMessage: 'Start with a question or describe what you need.'},
    startTitle: {defaultMessage: 'Where should we start?'},
    startSubtitle: {defaultMessage: 'Describe the goal and we\'ll work through it together.'},
    onYourMindTitle: {defaultMessage: 'Got something on your mind?'},
    onYourMindSubtitle: {defaultMessage: 'Share a thought, a question, or something you\'re stuck on.'},
});

export const NEW_CHAT_GREETINGS: ReadonlyArray<{title: MessageDescriptor; subtitle: MessageDescriptor}> = [
    {title: greetingMessages.helpTodayTitle, subtitle: greetingMessages.helpTodaySubtitle},
    {title: greetingMessages.getWorkDoneTitle, subtitle: greetingMessages.getWorkDoneSubtitle},
    {title: greetingMessages.tackleTitle, subtitle: greetingMessages.tackleSubtitle},
    {title: greetingMessages.readyTitle, subtitle: greetingMessages.readySubtitle},
    {title: greetingMessages.startTitle, subtitle: greetingMessages.startSubtitle},
    {title: greetingMessages.onYourMindTitle, subtitle: greetingMessages.onYourMindSubtitle},
];

// Secondary text emphasis, as in the prototype (0.72 opacity over the text
// color). No compass token covers text emphasis levels.
const SUBTITLE_EMPHASIS = '72%';

// 'page': the product view's centered 800px column.
// 'panel': fills a narrow container (the RHS).
export type AgentNewChatLayout = 'page' | 'panel';

interface AgentNewChatProps {
    bot: LLMBot;
    channelId: string;
    onCreated: (rootId: string) => void;
    layout: AgentNewChatLayout;
}

// What the host composer's afterSubmit receives (actions/views/create_comment.tsx
// SubmitPostReturnType): the created post for a message, nothing for a slash command.
interface HostSubmitResult {
    created?: {id?: string};
}

// The new-chat state: the agent's avatar, a greeting, and the host's message
// composer (AdvancedTextEditor), centered until the first post creates the
// thread. The composer posts a root post to the agent's DM, exactly as the
// Channels DM composer does (drafts, uploads, slash commands, AI actions
// included), and its afterSubmit opens the new thread.
const AgentNewChat = ({bot, channelId, onCreated, layout}: AgentNewChatProps) => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const prompts = useSelector(getCustomPrompts);
    const pinnedIds = useSelector(getPinnedPromptIds);
    const promptsLoaded = useSelector(isCustomPromptsLoaded);
    const pinsLoaded = useSelector(isPinnedPromptIdsLoaded);
    const [runningId, setRunningId] = useState<string | null>(null);
    const [starterError, setStarterError] = useState<string | null>(null);

    // Set synchronously, so two clicks in the same tick (before a re-render disables the buttons)
    // cannot both start a run. runningId is only for the disabled look.
    const inFlight = useRef(false);

    // Whether this chat already has its first message (from the composer or a starter), and whether
    // the view is still showing. A starter that finishes after either must not start a second chat
    // or pull the user away from where they went.
    const firstMessageCreated = useRef(false);
    const mounted = useRef(true);
    useEffect(() => {
        mounted.current = true;
        return () => {
            mounted.current = false;
        };
    }, []);

    // The lists are fetched once and kept fresh by the prompts page and menu, so a new chat only
    // fetches what has not loaded yet (a failed fetch leaves it unloaded, and is not retried here).
    useEffect(() => {
        if (!promptsLoaded) {
            dispatch(fetchCustomPrompts() as any);
        }
    }, [dispatch, promptsLoaded]);

    useEffect(() => {
        if (!pinsLoaded) {
            dispatch(fetchPinnedPromptIds() as any);
        }
    }, [dispatch, pinsLoaded]);

    const pinned = useMemo(() => pinnedIds.
        map((id) => prompts.find((p) => p.id === id)).
        filter((p): p is CustomPrompt => Boolean(p)), [prompts, pinnedIds]);

    // The host composer has no way to take a draft from outside, so a starter runs the prompt:
    // it posts the rendered text as the chat's first message.
    const runStarter = useCallback(async (prompt: CustomPrompt) => {
        if (inFlight.current) {
            return;
        }
        inFlight.current = true;
        setRunningId(prompt.id);
        setStarterError(null);

        const release = () => {
            inFlight.current = false;
            if (mounted.current) {
                setRunningId(null);
            }
        };

        try {
            const {rendered} = await renderCustomPrompt(prompt.id, channelId, bot.id);
            if (firstMessageCreated.current || !mounted.current) {
                release();
                return;
            }
            const created = await createPost({channel_id: channelId, message: rendered, root_id: ''});
            if (created?.id) {
                const chatAlreadyOpened = firstMessageCreated.current || !mounted.current;
                firstMessageCreated.current = true;
                if (!chatAlreadyOpened) {
                    onCreated(created.id);
                }
                return;
            }
        } catch (e) {
            console.error('Failed to run custom prompt:', e); // eslint-disable-line no-console
        }
        release();
        if (mounted.current) {
            setStarterError(intl.formatMessage({defaultMessage: 'Couldn\'t run that prompt. Please try again.'}));
        }
    }, [channelId, bot.id, onCreated, intl]);

    const afterSubmit = useCallback((result: HostSubmitResult) => {
        if (result?.created?.id) {
            firstMessageCreated.current = true;
            onCreated(result.created.id);
        }
    }, [onCreated]);

    // One index per mount picks title and subtitle together, so they always
    // match and re-renders don't change them.
    const [greeting] = useState(() => NEW_CHAT_GREETINGS[Math.floor(Math.random() * NEW_CHAT_GREETINGS.length)]);

    return (
        <EmptyChat
            $layout={layout}
            data-testid='agent-chat-new-chat'
        >
            <EmptyIntro>
                <UserAvatar
                    src={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
                    alt={bot.displayName}
                    name={bot.displayName}
                    size='64'
                />
                <Greeting>
                    <EmptyTitle $layout={layout}>{intl.formatMessage(greeting.title)}</EmptyTitle>
                    <EmptySubtitle>{intl.formatMessage(greeting.subtitle)}</EmptySubtitle>
                </Greeting>
            </EmptyIntro>
            {pinned.length > 0 && (
                <StarterPrompts
                    prompts={pinned}
                    disabled={runningId !== null}
                    error={starterError}
                    onPick={runStarter}
                />
            )}
            {AdvancedTextEditor && (
                <Composer
                    className={HOST_COMPOSER_CONTAINER_CLASS}
                    data-testid='agent-chat-new-composer'

                    // The host composer has no disabled prop. While a starter renders and posts, the
                    // composer cannot be typed in or submitted, so the two cannot both create the first post.
                    inert={runningId !== null}
                    $busy={runningId !== null}
                >
                    {/* In the RHS, RHS_COMMENT makes the host treat it as the RHS composer (focus, ids, suggestion list). */}
                    <AdvancedTextEditor
                        location={layout === 'page' ? 'CENTER' : 'RHS_COMMENT'}
                        channelId={channelId}
                        rootId=''
                        placeholder={intl.formatMessage({defaultMessage: 'Message {name}'}, {name: bot.displayName})}
                        afterSubmit={afterSubmit}
                    />
                </Composer>
            )}
        </EmptyChat>
    );
};

const pageLayout = css`
    gap: var(--spacing-xxl);
    max-width: ${CHAT_CONTENT_MAX_WIDTH};
    margin-inline: auto;
    padding: var(--spacing-xxxxl);

    /* Sit slightly above true center, as in the prototype. */
    --empty-chat-lift: calc(var(--spacing-xxxxxl) + var(--spacing-l) + var(--spacing-xxl));
`;

// The RHS is short as well as narrow: a smaller lift, and 24px padding on all
// sides (the RHS thread composer uses the same side padding, so they line up).
const panelLayout = css`
    gap: var(--spacing-xl);
    padding: var(--spacing-xxl);
    --empty-chat-lift: calc(2 * var(--spacing-xxl));
`;

// The content fades in and rises 24px into its lifted position. The element's own transform holds
// the end state, so the animation only fills backwards.
const emptyChatEnter = keyframes`
    from {
        opacity: 0;
        transform: translateY(calc(var(--spacing-xxl) - var(--empty-chat-lift)));
    }

    to {
        opacity: 1;
        transform: translateY(calc(-1 * var(--empty-chat-lift)));
    }
`;

const EmptyChat = styled.div<{$layout: AgentNewChatLayout}>`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    width: 100%;
    min-height: 0;

    ${({$layout}) => ($layout === 'page' ? pageLayout : panelLayout)}
    transform: translateY(calc(-1 * var(--empty-chat-lift)));
    animation: ${emptyChatEnter} var(--duration-moderate) var(--ease-entrance) backwards;

    ${noMotionWhenReduced}
`;

// The host composer fills the column; the column's own padding is its inset, so the host's is
// removed. The host wrapper's own background and radius stay.
const Composer = styled.div<{$busy: boolean}>`
    && {
        width: 100%;
    }

    opacity: ${({$busy}) => ($busy ? 0.56 : 1)};
    transition: opacity var(--duration-quick) var(--ease-transition);

    ${hostComposerInset('0')}
`;

const EmptyIntro = styled.div`
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--spacing-m);
    text-align: center;
`;

const Greeting = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
`;

// The product view's title is larger (36px); the narrow RHS keeps the smaller one.
const EmptyTitle = styled.h2<{$layout: AgentNewChatLayout}>`
    margin: 0;
    font-family: var(--font-family-heading);
    font-size: ${({$layout}) => ($layout === 'page' ? 'var(--font-size-900)' : 'var(--font-size-600)')};
    font-weight: var(--font-weight-semibold);
    line-height: ${({$layout}) => ($layout === 'page' ? 'var(--line-height-900)' : 'var(--line-height-600)')};
    letter-spacing: ${({$layout}) => ($layout === 'page' ? '-0.015em' : 'normal')};
    color: var(--center-channel-color);
`;

const EmptySubtitle = styled.p`
    margin: 0;
    font-family: var(--font-family-body);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-200);
    color: color-mix(in srgb, var(--center-channel-color) ${SUBTITLE_EMPHASIS}, transparent);
    text-wrap: balance;
`;

export default AgentNewChat;
