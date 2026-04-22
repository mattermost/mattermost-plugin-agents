// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import {useSelector} from 'react-redux';
import styled from 'styled-components';

import {WebSocketMessage} from '@mattermost/client';
import {GlobalState} from '@mattermost/types/store';

import {doPostbackSummary, doRegenerate, doStopGenerating} from '@/client';
import {useSelectNotAIPost} from '@/hooks';
import {useConversation, useTurnForPost, invalidateConversation} from '@/hooks/use_conversation';
import {PostMessagePreview} from '@/mm_webapp';

import PostText from '../post_text';
import {SearchSources} from '../search_sources';
import ToolApprovalSet from '../tool_approval_set';
import {ToolApprovalStage, ToolCall} from '../tool_types';
import {Annotation} from '../citations/types';

import {
    extractToolCallsForPost,
    extractReasoningFromTurn,
    extractAnnotationsFromTurn,
    deriveApprovalStageForPost,
    hasAutoApprovedToolsForPost,
} from './turn_content_utils';
import {ReasoningDisplay, LoadingSpinner, MinimalReasoningContainer} from './reasoning_display';
import {ControlsBarComponent} from './controls_bar';
import {extractPermalinkData} from './permalink_data';

const SearchResultsPropKey = 'search_results';

// Types
export interface PostUpdateWebsocketMessage {
    post_id: string
    next?: string
    control?: string
    tool_call?: string
    reasoning?: string
    annotations?: string
}

interface LLMBotPostProps {
    post: any;
    websocketRegister?: (postID: string, listenerID: string, handler: (msg: WebSocketMessage<any>) => void) => void;
    websocketUnregister?: (postID: string, listenerID: string) => void;
}

export const LLMBotPost = (props: LLMBotPostProps) => {
    const selectPost = useSelectNotAIPost();

    // Conversation entity data
    const conversationId: string | undefined = props.post.props?.conversation_id;
    const {conversation, loading: conversationLoading, error: conversationError} = useConversation(conversationId);
    const turn = useTurnForPost(conversation, props.post.id);

    // Derive requester check from conversation entity. Meeting summarization
    // posts currently have no conversation entity; fall back to the legacy
    // llm_requester_user_id prop for those. Remove the fallback once meeting
    // flows produce conversation entities.
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const legacyRequester: string | undefined = props.post.props?.llm_requester_user_id;
    const requesterIsCurrentUser = Boolean(
        (conversation && conversation.user_id === currentUserId) ||
        (!conversationId && legacyRequester && legacyRequester === currentUserId),
    );

    const channel = useSelector<GlobalState, {type?: string} | undefined>(
        (state) => state.entities.channels.channels[props.post.channel_id],
    );
    const isDM = channel?.type === 'D';
    const rootPost = useSelector<GlobalState, any>((state) => state.entities.posts.posts[props.post.root_id]);

    // Local state for streaming
    const [message, setMessage] = useState(props.post.message);
    const [generating, setGenerating] = useState(false);
    const [toolCalls, setToolCalls] = useState<ToolCall[]>([]);
    const [toolApprovalStage, setToolApprovalStage] = useState<ToolApprovalStage>('call');
    const [isAutoApproved, setIsAutoApproved] = useState(false);
    const [annotations, setAnnotations] = useState<Annotation[]>([]);
    const [precontent, setPrecontent] = useState(props.post.message === '');
    const [error, setError] = useState('');

    // Stopped is a flag that is used to prevent the websocket from updating the message after the user has stopped the generation.
    // Needs a ref because of the useEffect closure.
    const [stopped, setStopped] = useState(false);
    const stoppedRef = useRef(stopped);
    stoppedRef.current = stopped;

    // State for reasoning summary display
    const [reasoningSummary, setReasoningSummary] = useState('');
    const [showReasoning, setShowReasoning] = useState(false);
    const [isReasoningCollapsed, setIsReasoningCollapsed] = useState(true);
    const [isReasoningLoading, setIsReasoningLoading] = useState(false);

    // Populate local state from turn data when not streaming.
    // This overwrites whatever was accumulated during streaming once
    // the finalized turn arrives from the conversation API.
    useEffect(() => {
        if (!turn || generating) {
            return;
        }

        // Tool calls — aggregate across every turn that belongs to this
        // post's response so multi-round tool use displays all calls.
        if (conversation) {
            const derived = extractToolCallsForPost(conversation, props.post.id);
            setToolCalls(derived);
            setToolApprovalStage(deriveApprovalStageForPost(conversation, props.post.id));
            setIsAutoApproved(hasAutoApprovedToolsForPost(conversation, props.post.id));
        }

        // Reasoning
        const reasoning = extractReasoningFromTurn(turn);
        if (reasoning.summary) {
            setReasoningSummary(reasoning.summary);
            setShowReasoning(true);
            setIsReasoningLoading(false);
        } else {
            setReasoningSummary('');
            setShowReasoning(false);
        }

        // Annotations
        const turnAnnotations = extractAnnotationsFromTurn(turn);
        setAnnotations(turnAnnotations);

        // Precontent should be false when we have turn data
        setPrecontent(false);
    }, [turn, generating, conversation, props.post.id]);

    // Sync message from post.message changes (e.g. after post update)
    useEffect(() => {
        if (props.post.message !== '' && props.post.message !== message) {
            setMessage(props.post.message);
        }
    }, [props.post.message]);

    // WebSocket handler for streaming -- largely unchanged
    useEffect(() => {
        if (props.websocketRegister && props.websocketUnregister) {
            const listenerID = Math.random().toString(36).substring(7);

            props.websocketRegister(props.post.id, listenerID, (msg: WebSocketMessage<PostUpdateWebsocketMessage>) => {
                const data = msg.data;

                // Ensure we're only processing events for this post
                if (data.post_id !== props.post.id) {
                    return;
                }

                // Handle reasoning summary events
                if (data.control === 'reasoning_summary' && data.reasoning) {
                    setReasoningSummary(data.reasoning);
                    setShowReasoning(true);
                    setIsReasoningLoading(true);
                    setGenerating(false);
                    setPrecontent(false);
                    return;
                }

                if (data.control === 'reasoning_summary_done' && data.reasoning) {
                    setReasoningSummary(data.reasoning);
                    setIsReasoningLoading(false);
                    return;
                }

                // Handle tool call events from the websocket event.
                // Each round emits its own event; merge by id so live display
                // shows every round's calls instead of only the last round's.
                if (data.control === 'tool_call' && data.tool_call) {
                    try {
                        const parsedToolCalls = JSON.parse(data.tool_call) as ToolCall[];
                        setToolCalls((prev) => {
                            const byID = new Map<string, number>();
                            const next = [...prev];
                            for (let i = 0; i < next.length; i++) {
                                byID.set(next[i].id, i);
                            }
                            for (const tc of parsedToolCalls) {
                                const idx = byID.get(tc.id);
                                if (idx === undefined) { // eslint-disable-line no-undefined
                                    byID.set(tc.id, next.length);
                                    next.push(tc);
                                } else {
                                    next[idx] = tc;
                                }
                            }
                            return next;
                        });
                        setPrecontent(false);
                    } catch {
                        setError('Error parsing tool call data');
                    }
                    return;
                }

                // Handle annotation events from the websocket
                if (data.control === 'annotations' && data.annotations) {
                    try {
                        const parsedAnnotations = JSON.parse(data.annotations);
                        setAnnotations(parsedAnnotations);
                        setPrecontent(false);
                    } catch {
                        setError('Error parsing annotation data');
                    }
                    return;
                }

                // Handle regular post updates
                if (data.next && !stoppedRef.current) {
                    setGenerating(true);
                    setPrecontent(false);
                    setMessage(data.next);
                } else if (data.control === 'end') {
                    setGenerating(false);
                    setPrecontent(false);
                    setStopped(false);
                    setIsReasoningLoading(false);

                    // Re-fetch the conversation to get finalized turn data
                    if (conversationId) {
                        invalidateConversation(conversationId);
                    }
                } else if (data.control === 'cancel') {
                    setGenerating(false);
                    setPrecontent(false);
                    setStopped(false);
                    setIsReasoningLoading(false);
                } else if (data.control === 'start') {
                    setGenerating(true);
                    setPrecontent(true);
                    setStopped(false);

                    // Clear reasoning when starting new generation
                    setReasoningSummary('');
                    setShowReasoning(false);
                    setIsReasoningCollapsed(true);
                    setIsReasoningLoading(false);

                    // Clear tool calls and annotations when starting new generation
                    setToolCalls([]);
                    setAnnotations([]);

                    if (!message) {
                        setMessage('');
                    }
                }
            });

            return () => {
                if (props.websocketUnregister) {
                    props.websocketUnregister(props.post.id, listenerID);
                }
            };
        }

        return () => {/* no cleanup */};
    }, [props.post.id, conversationId]);

    const regnerate = () => {
        setMessage('');
        setGenerating(false);
        setPrecontent(true);
        setStopped(false);

        // Clear reasoning summary when regenerating
        setReasoningSummary('');
        setShowReasoning(false);
        setIsReasoningCollapsed(true);
        setIsReasoningLoading(false);

        // Clear annotations/citations when regenerating
        setAnnotations([]);

        // Clear tool calls when regenerating
        setToolCalls([]);

        doRegenerate(props.post.id);
    };

    const stopGenerating = () => {
        setStopped(true);
        setGenerating(false);
        setIsReasoningLoading(false);
        doStopGenerating(props.post.id);
    };

    const postSummary = async () => {
        const result = await doPostbackSummary(props.post.id);
        selectPost(result.rootid, result.channelid);
    };

    // Privacy: the API handles filtering. For the requester, all data is present.
    // For non-requesters, `input` is null on redacted tool_use blocks and `content`
    // is absent on redacted tool_result blocks. We reflect this directly.
    const showToolArguments = toolCalls.length > 0 && toolCalls.some((tc) => tc.arguments != null);
    const showToolResults = toolCalls.length > 0 && toolCalls.some((tc) => tc.result != null);

    const isThreadSummaryPost = (props.post.props?.referenced_thread && props.post.props?.referenced_thread !== '');
    const isNoShowRegen = (props.post.props?.no_regen && props.post.props?.no_regen !== '');
    const isTranscriptionResult = rootPost?.props?.referenced_transcript_post_id && rootPost?.props?.referenced_transcript_post_id !== '';

    let permalinkView = null;
    if (PostMessagePreview) { // Ignore permalink if version does not export PostMessagePreview
        const permalinkData = extractPermalinkData(props.post);
        if (permalinkData !== null) {
            permalinkView = (
                <PostMessagePreview
                    data-testid='llm-bot-permalink'
                    metadata={permalinkData}
                />
            );
        }
    }

    // Consider both generating and reasoning loading states for determining if generation is in progress
    const isGenerationInProgress = generating || isReasoningLoading;

    const showRegenerate = isDM && !isGenerationInProgress && requesterIsCurrentUser && !isNoShowRegen;
    const showPostbackButton = !isGenerationInProgress && requesterIsCurrentUser && isTranscriptionResult;
    const showStopGeneratingButton = isGenerationInProgress && requesterIsCurrentUser;
    const hasContent = message !== '' || reasoningSummary !== '';
    const showControlsBar = ((showRegenerate || showPostbackButton) && hasContent) || showStopGeneratingButton;

    return (
        <PostBody
            data-testid='llm-bot-post'
        >
            {error && <div className='error'>{error}</div>}
            {conversationError && !generating && (
                <div className='error'>
                    <FormattedMessage defaultMessage='Failed to load conversation data'/>
                </div>
            )}
            {isThreadSummaryPost && permalinkView &&
            <>
                {permalinkView}
            </>
            }
            {showReasoning && (
                <ReasoningDisplay
                    reasoningSummary={reasoningSummary}
                    isReasoningCollapsed={isReasoningCollapsed}
                    isReasoningLoading={isReasoningLoading}
                    onToggleCollapse={setIsReasoningCollapsed}
                />
            )}
            {(precontent || (conversationLoading && !generating && !message)) && (
                <MinimalReasoningContainer>
                    <SpinnerWrapper><LoadingSpinner/></SpinnerWrapper>
                    <span>
                        <FormattedMessage defaultMessage='Starting...'/>
                    </span>
                </MinimalReasoningContainer>
            )}
            {toolCalls && toolCalls.length > 0 && (
                <ToolApprovalSet
                    postID={props.post.id}
                    conversationID={conversationId}
                    toolCalls={toolCalls}
                    approvalStage={toolApprovalStage}
                    canApprove={requesterIsCurrentUser}
                    canExpand={requesterIsCurrentUser}
                    showArguments={showToolArguments}
                    showResults={showToolResults}
                    isAutoApproved={isAutoApproved}
                />
            )}
            <PostText
                message={message}
                channelID={props.post.channel_id}
                postID={props.post.id}
                showCursor={generating && !precontent}
                annotations={annotations.length > 0 ? annotations : undefined} // eslint-disable-line no-undefined
            />
            {props.post.props?.[SearchResultsPropKey] && (
                <SearchSources
                    sources={JSON.parse(props.post.props[SearchResultsPropKey])}
                />
            )}
            { showPostbackButton &&
            <PostSummaryHelpMessage>
                <FormattedMessage defaultMessage='Would you like to post this summary to the original call thread? You can also ask Agents to make changes.'/>
            </PostSummaryHelpMessage>
            }
            { showControlsBar &&
            <ControlsBarComponent
                showStopGeneratingButton={showStopGeneratingButton}
                showPostbackButton={showPostbackButton}
                showRegenerate={showRegenerate}
                onStopGenerating={stopGenerating}
                onPostSummary={postSummary}
                onRegenerate={regnerate}
            />
            }
        </PostBody>
    );
};

// Styled components
const PostBody = styled.div`
`;

const SpinnerWrapper = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    width: 16px;
    height: 16px;
`;

const PostSummaryHelpMessage = styled.div`
    font-size: 14px;
    font-style: italic;
    font-weight: 400;
    line-height: 20px;
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    padding-top: 8px;
    padding-bottom: 8px;
    margin-top: 16px;
`;
