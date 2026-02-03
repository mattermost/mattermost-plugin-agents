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
import {PostMessagePreview} from '@/mm_webapp';

import {SearchSources} from '../search_sources';
import PostText from '../post_text';
import ToolApprovalSet from '../tool_approval_set';
import {Annotation} from '../citations/types';

import {ReasoningDisplay, LoadingSpinner, MinimalReasoningContainer} from './reasoning_display';
import {ControlsBarComponent} from './controls_bar';
import {extractPermalinkData} from './permalink_data';

// Types
export interface PostUpdateWebsocketMessage {
    post_id: string
    next?: string
    control?: string
    tool_call?: string
    reasoning?: string
    annotations?: string
}

export enum ToolCallStatus {
    Pending = 0,
    Accepted = 1,
    Rejected = 2,
    Error = 3,
    Success = 4
}

export interface ToolCall {
    id: string;
    name: string;
    description: string;
    arguments: any;
    result?: string;
    status: ToolCallStatus;
}

interface LLMBotPostProps {
    post: any;
    websocketRegister?: (postID: string, listenerID: string, handler: (msg: WebSocketMessage<any>) => void) => void;
    websocketUnregister?: (postID: string, listenerID: string) => void;
}

const SearchResultsPropKey = 'search_results';

export const LLMBotPost = (props: LLMBotPostProps) => {
    const selectPost = useSelectNotAIPost();
    const [message, setMessage] = useState(props.post.message);

    // Generating is true while we are receiving new content from the websocket
    const [generating, setGenerating] = useState(false);

    // State for tool calls - initialize from persisted tool calls if available
    const [toolCalls, setToolCalls] = useState<ToolCall[]>(() => {
        const toolCallsJson = props.post.props?.pending_tool_call;
        if (toolCallsJson) {
            try {
                return JSON.parse(toolCallsJson);
            } catch (error) {
                return [];
            }
        }
        return [];
    });

    // State for annotations/citations - initialize from persisted annotations if available
    const [annotations, setAnnotations] = useState<Annotation[]>(() => {
        const persistedAnnotations = props.post.props?.annotations || '';
        if (persistedAnnotations) {
            try {
                return JSON.parse(persistedAnnotations);
            } catch (error) {
                return [];
            }
        }
        return [];
    });

    // Precontent is true when we're waiting for the first content to arrive
    // Initialize to true if post is empty AND has no reasoning AND no tool calls AND no annotations (fresh post)
    const persistedReasoning = props.post.props?.reasoning_summary || '';
    const [precontent, setPrecontent] = useState(
        props.post.message === '' &&
        persistedReasoning === '' &&
        toolCalls.length === 0 &&
        annotations.length === 0,
    );

    // Stopped is a flag that is used to prevent the websocket from updating the message after the user has stopped the generation
    // Needs a ref because of the useEffect closure.
    const [stopped, setStopped] = useState(false);
    const stoppedRef = useRef(stopped);
    stoppedRef.current = stopped;

    const [error, setError] = useState('');

    // State for reasoning summary display
    // Use the same persistedReasoning from above
    const [reasoningSummary, setReasoningSummary] = useState(persistedReasoning);
    const [showReasoning, setShowReasoning] = useState(persistedReasoning !== '');
    const [isReasoningCollapsed, setIsReasoningCollapsed] = useState(true);
    const [isReasoningLoading, setIsReasoningLoading] = useState(false);

    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const rootPost = useSelector<GlobalState, any>((state) => state.entities.posts.posts[props.post.root_id]);

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

            // Initialize annotations from persisted data
            const persistedAnnotations = props.post.props?.annotations || '';
            let parsedAnnotations: Annotation[] = [];
            if (persistedAnnotations) {
                try {
                    parsedAnnotations = JSON.parse(persistedAnnotations);
                    setAnnotations(parsedAnnotations);
                } catch (error) {
                    setAnnotations([]);
                }
            } else {
                setAnnotations([]);
            }

            // Initialize tool calls from persisted data
            const toolCallsJson = props.post.props?.pending_tool_call;
            let parsedToolCalls: ToolCall[] = [];
            if (toolCallsJson) {
                try {
                    parsedToolCalls = JSON.parse(toolCallsJson);
                    setToolCalls(parsedToolCalls);
                } catch (error) {
                    setToolCalls([]);
                }
            } else {
                setToolCalls([]);
            }

            // Set precontent if this is a fresh empty post (no content, no reasoning, no tool calls, no annotations)
            // Otherwise reset to false (historical posts or posts with any content)
            setPrecontent(
                props.post.message === '' &&
                persistedReasoning === '' &&
                parsedToolCalls.length === 0 &&
                parsedAnnotations.length === 0,
            );

            previousPostIdRef.current = props.post.id;
        }
    }, [props.post.id, props.post.props?.reasoning_summary, props.post.props?.annotations, props.post.props?.pending_tool_call, props.post.message]);

    // Update tool calls from props when available
    useEffect(() => {
        const toolCallsJson = props.post.props?.pending_tool_call;
        if (toolCallsJson) {
            try {
                const parsedToolCalls = JSON.parse(toolCallsJson);
                setToolCalls(parsedToolCalls);
            } catch (error) {
                // Log error for debugging
                setError('Error parsing tool calls');
            }
        }
    }, [props.post.props?.pending_tool_call]);

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

                    // Explicitly set generating to false to prevent blinking cursor during reasoning
                    setGenerating(false);
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
                        setPrecontent(false); // Clear "Starting..." when tool calls arrive
                    } catch (error) {
                        // Handle error silently
                        setError('Error parsing tool call data');
                    }
                    return;
                }

                // Handle annotation events from the websocket
                if (data.control === 'annotations' && data.annotations) {
                    try {
                        const parsedAnnotations = JSON.parse(data.annotations);
                        setAnnotations(parsedAnnotations);
                        setPrecontent(false); // Clear "Starting..." when annotations arrive
                    } catch (error) {
                        // Handle error silently
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

    // Consider both generating and reasoning loading states for determining if generation is in progress
    const isGenerationInProgress = generating || isReasoningLoading;

    const showRegenerate = !isGenerationInProgress && requesterIsCurrentUser && !isNoShowRegen;
    const showPostbackButton = !isGenerationInProgress && requesterIsCurrentUser && isTranscriptionResult;
    const showStopGeneratingButton = isGenerationInProgress && requesterIsCurrentUser;
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
                    <SpinnerWrapper><LoadingSpinner/></SpinnerWrapper>
                    <span>
                        <FormattedMessage defaultMessage='Starting...'/>
                    </span>
                </MinimalReasoningContainer>
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

