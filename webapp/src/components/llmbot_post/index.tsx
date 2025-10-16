// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useRef, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import {useSelector} from 'react-redux';

import {WebSocketMessage} from '@mattermost/client';
import {GlobalState} from '@mattermost/types/store';

import {doPostbackSummary, doRegenerate, doStopGenerating} from '@/client';
import {useSelectNotAIPost} from '@/hooks';
import {PostMessagePreview} from '@/mm_webapp';

import {SearchSources} from '../search_sources';
import PostText from '../post_text';
import ToolApprovalSet from '../tool_approval_set';

import {
    LLMBotPostProps,
    PostUpdateWebsocketMessage,
    ToolCall,
    ToolCallStatus,
} from './types';
import {
    PostBody,
    PostSummaryHelpMessage,
    MinimalReasoningContainer,
    LoadingSpinner,
} from './styles';
import {ReasoningDisplay} from './reasoning_display';
import {ControlsBarComponent} from './controls_bar';
import {extractPermalinkData} from './utils';

// Re-export types for external use
export type {
    PostUpdateWebsocketMessage,
    ToolCall,
};

// Re-export enum (needs regular export, not type-only export)
export {ToolCallStatus};

const SearchResultsPropKey = 'search_results';

export const LLMBotPost = (props: LLMBotPostProps) => {
    const selectPost = useSelectNotAIPost();
    const [message, setMessage] = useState(props.post.message);

    // Generating is true while we are receiving new content from the websocket
    const [generating, setGenerating] = useState(false);

    // Precontent is true when we're waiting for the first content to arrive
    // Initialize to true if post is empty AND has no reasoning (fresh post), false otherwise (historical post)
    const persistedReasoning = props.post.props?.reasoning_summary || '';
    const [precontent, setPrecontent] = useState(props.post.message === '' && persistedReasoning === '');

    // Stopped is a flag that is used to prevent the websocket from updating the message after the user has stopped the generation
    // Needs a ref because of the useEffect closure.
    const [stopped, setStopped] = useState(false);
    const stoppedRef = useRef(stopped);
    stoppedRef.current = stopped;

    // State for tool calls
    const [toolCalls, setToolCalls] = useState<ToolCall[]>([]);
    const [error, setError] = useState('');

    // State for reasoning summary display
    // Use the same persistedReasoning from above
    const [reasoningSummary, setReasoningSummary] = useState(persistedReasoning);
    const [showReasoning, setShowReasoning] = useState(persistedReasoning !== '');
    const [isReasoningCollapsed, setIsReasoningCollapsed] = useState(true);
    const [isReasoningLoading, setIsReasoningLoading] = useState(false);

    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const rootPost = useSelector<GlobalState, any>((state) => state.entities.posts.posts[props.post.root_id]);

    // Get tool calls from post props
    const toolCallsJson = props.post.props?.pending_tool_call;

    // Initialize reasoning from persisted data when navigating to different posts
    const previousPostIdRef = useRef(props.post.id);
    useEffect(() => {
        if (previousPostIdRef.current !== props.post.id) {
            const persistedReasoning = props.post.props?.reasoning_summary || '';
            if (persistedReasoning) {
                setReasoningSummary(persistedReasoning);
                setShowReasoning(true);
                setIsReasoningCollapsed(true);
                setIsReasoningLoading(false);
            } else {
                // Reset reasoning state for posts without reasoning
                setReasoningSummary('');
                setShowReasoning(false);
                setIsReasoningCollapsed(true);
                setIsReasoningLoading(false);
            }

            // Set precontent if this is a fresh empty post (no content and no reasoning)
            // Otherwise reset to false (historical posts)
            setPrecontent(props.post.message === '' && persistedReasoning === '');

            previousPostIdRef.current = props.post.id;
        }
    }, [props.post.id, props.post.props?.reasoning_summary, props.post.message]);

    // Update tool calls from props when available
    useEffect(() => {
        if (toolCallsJson) {
            try {
                const parsedToolCalls = JSON.parse(toolCallsJson);
                setToolCalls(parsedToolCalls);
            } catch (error) {
                // Log error for debugging
                setError('Error parsing tool calls');
            }
        }
    }, [toolCallsJson]);

    useEffect(() => {
        if (props.post.message !== '' && props.post.message !== message) {
            setMessage(props.post.message);
        }
    }, [props.post.message]);

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
                    // Replace entire reasoning with accumulated text from backend
                    setReasoningSummary(data.reasoning);
                    setShowReasoning(true);
                    setIsReasoningLoading(true);

                    // Don't set generating=true here - only set it when actual content starts streaming
                    setPrecontent(false); // Clear "Starting..." when reasoning begins
                    return;
                }

                if (data.control === 'reasoning_summary_done' && data.reasoning) {
                    // Final reasoning text
                    setReasoningSummary(data.reasoning);
                    setIsReasoningLoading(false);

                    // Don't change collapsed state - preserve user's choice
                    return;
                }

                // Handle tool call events from the websocket event
                if (data.control === 'tool_call' && data.tool_call) {
                    try {
                        const parsedToolCalls = JSON.parse(data.tool_call);
                        setToolCalls(parsedToolCalls);
                    } catch (error) {
                        // Handle error silently
                        setError('Error parsing tool call data');
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
    }, [props.post.id]);

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

    const requesterIsCurrentUser = (props.post.props?.llm_requester_user_id === currentUserId);
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

    const showRegenerate = !generating && requesterIsCurrentUser && !isNoShowRegen;
    const showPostbackButton = !generating && requesterIsCurrentUser && isTranscriptionResult;
    const showStopGeneratingButton = generating && requesterIsCurrentUser;
    const hasContent = message !== '' || reasoningSummary !== '';
    const showControlsBar = ((showRegenerate || showPostbackButton) && hasContent) || showStopGeneratingButton;

    return (
        <PostBody
            data-testid='llm-bot-post'
        >
            {error && <div className='error'>{error}</div>}
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
            {precontent && (
                <MinimalReasoningContainer>
                    <LoadingSpinner/>
                    <span>
                        <FormattedMessage defaultMessage='Starting...'/>
                    </span>
                </MinimalReasoningContainer>
            )}
            <PostText
                message={message}
                channelID={props.post.channel_id}
                postID={props.post.id}
                showCursor={generating}
            />
            {props.post.props?.[SearchResultsPropKey] && (
                <SearchSources
                    sources={JSON.parse(props.post.props[SearchResultsPropKey])}
                />
            )}
            {toolCalls && toolCalls.length > 0 && (
                <ToolApprovalSet
                    postID={props.post.id}
                    toolCalls={toolCalls}
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

