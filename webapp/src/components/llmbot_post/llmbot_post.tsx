// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import {useSelector} from 'react-redux';
import styled from 'styled-components';

import {WebSocketMessage} from '@mattermost/client';
import {GlobalState} from '@mattermost/types/store';

import {doPostbackSummary, doRegenerate, doStopGenerating} from '@/client';
import {useSelectNotAIPost} from '@/hooks';
import {useConversation, invalidateConversation} from '@/hooks/use_conversation';
import {PostMessagePreview} from '@/mm_webapp';

import PostText from '../post_text';
import {SearchSources} from '../search_sources';
import ToolApprovalSet from '../tool_approval_set';
import {ToolApprovalStage, ToolCall, ToolCallStatus} from '../tool_types';
import {Annotation} from '../citations/types';

import {
    Round,
    buildRoundsFromTurns,
    deriveApprovalStageForPost,
} from './turn_content_utils';
import {ReasoningDisplay, LoadingSpinner, MinimalReasoningContainer} from './reasoning_display';
import {ControlsBarComponent} from './controls_bar';
import {extractPermalinkData} from './permalink_data';

const SearchResultsPropKey = 'search_results';

// Sentinel id for the in-progress streaming round; persisted rounds use turn ids.
const LIVE_ROUND_ID = 'live';

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

// ToolRunner emits one tool_call event per round with pending statuses, then one
// with terminal statuses after execution. The terminal one is the round boundary.
function isResolvedToolCallEvent(toolCalls: ToolCall[]): boolean {
    if (toolCalls.length === 0) {
        return false;
    }
    return toolCalls.every((tc) =>
        tc.status === ToolCallStatus.Success ||
        tc.status === ToolCallStatus.Error ||
        tc.status === ToolCallStatus.AutoApproved ||
        tc.status === ToolCallStatus.Rejected,
    );
}

export const LLMBotPost = (props: LLMBotPostProps) => {
    const selectPost = useSelectNotAIPost();

    const conversationId: string | undefined = props.post.props?.conversation_id;
    const {conversation, loading: conversationLoading, error: conversationError} = useConversation(conversationId);

    // Meeting summarization posts have no conversation entity yet; fall back to
    // the legacy llm_requester_user_id prop.
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

    const [message, setMessage] = useState(props.post.message);
    const [generating, setGenerating] = useState(false);
    const [toolCalls, setToolCalls] = useState<ToolCall[]>([]);
    const [annotations, setAnnotations] = useState<Annotation[]>([]);
    const [precontent, setPrecontent] = useState(props.post.message === '');
    const [error, setError] = useState('');

    // Stopped is a flag that is used to prevent the websocket from updating the message after the user has stopped the generation.
    // Needs a ref because of the useEffect closure.
    const [stopped, setStopped] = useState(false);
    const stoppedRef = useRef(stopped);
    stoppedRef.current = stopped;

    const [reasoningSummary, setReasoningSummary] = useState('');
    const [isReasoningLoading, setIsReasoningLoading] = useState(false);

    const [expandedReasoning, setExpandedReasoning] = useState<Record<string, boolean>>({});

    // Rounds completed during this stream, before turns land via refetch.
    const [liveRounds, setLiveRounds] = useState<Round[]>([]);

    const [pendingRefetch, setPendingRefetch] = useState(false);

    // Suppresses persistedRounds while regenerating so the prior generation
    // doesn't render alongside the new stream.
    const [regenerating, setRegenerating] = useState(false);

    // Lets the WebSocket handler snapshot the live round without re-subscribing.
    const liveRef = useRef({message, toolCalls, reasoningSummary, annotations});
    liveRef.current = {message, toolCalls, reasoningSummary, annotations};

    // Sync message from post.message changes (e.g. after post update)
    useEffect(() => {
        if (props.post.message !== '' && props.post.message !== message) {
            setMessage(props.post.message);
        }
    }, [props.post.message]);

    const persistedRounds: Round[] = useMemo(() => {
        if (!conversation) {
            return [];
        }
        return buildRoundsFromTurns(conversation, props.post.id);
    }, [conversation, props.post.id]);

    // Keep prior rounds visible during the refetch window after invalidate.
    const lastPersistedRef = useRef<Round[]>([]);
    useEffect(() => {
        if (conversation) {
            lastPersistedRef.current = persistedRounds;
        }
    }, [conversation, persistedRounds]);
    const stablePersisted = conversation ? persistedRounds : lastPersistedRef.current;

    // Once the refetch lands, clear local state for completed rounds so we
    // don't double-render. useLayoutEffect prevents a duplicated frame.
    useLayoutEffect(() => {
        if (!pendingRefetch || !conversation) {
            return;
        }

        setLiveRounds((prev: Round[]) => (prev.length === 0 ? prev : []));
        setToolCalls((prev: ToolCall[]) => (prev.length === 0 ? prev : []));
        setAnnotations((prev: Annotation[]) => (prev.length === 0 ? prev : []));
        setMessage((prev: string) => (prev === '' ? prev : ''));
        setReasoningSummary((prev: string) => (prev === '' ? prev : ''));
        setIsReasoningLoading(false);
        setRegenerating(false);
        setPendingRefetch(false);
    }, [conversation, pendingRefetch]);

    useEffect(() => {
        if (!props.websocketRegister || !props.websocketUnregister) {
            return undefined; // eslint-disable-line no-undefined
        }

        const listenerID = Math.random().toString(36).substring(7);

        props.websocketRegister(props.post.id, listenerID, (msg: WebSocketMessage<PostUpdateWebsocketMessage>) => {
            const data = msg.data;

            if (data.post_id !== props.post.id) {
                return;
            }

            if (data.control === 'reasoning_summary' && data.reasoning) {
                // Don't clear generating: the `generating && currentRound`
                // gate in renderedRounds would hide the thinking block.
                setReasoningSummary(data.reasoning);
                setIsReasoningLoading(true);
                setPrecontent(false);
                return;
            }

            if (data.control === 'reasoning_summary_done' && data.reasoning) {
                setReasoningSummary(data.reasoning);
                setIsReasoningLoading(false);
                return;
            }

            if (data.control === 'tool_call' && data.tool_call) {
                try {
                    const parsedToolCalls = JSON.parse(data.tool_call) as ToolCall[];
                    if (isResolvedToolCallEvent(parsedToolCalls)) {
                        // Snapshot the round into liveRounds and reset for the next.
                        const live = liveRef.current;
                        setLiveRounds((prev) => [
                            ...prev,
                            {
                                id: `live-${prev.length}-${Date.now()}`,
                                text: live.message,
                                toolCalls: parsedToolCalls,
                                reasoning: {summary: live.reasoningSummary, signature: ''},
                                annotations: live.annotations,
                            },
                        ]);
                        setMessage('');
                        setToolCalls([]);
                        setReasoningSummary('');
                        setIsReasoningLoading(false);
                        setAnnotations([]);
                    } else {
                        setToolCalls(parsedToolCalls);
                    }
                    setPrecontent(false);
                } catch {
                    setError('Error parsing tool call data');
                }
                return;
            }

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

            if (typeof data.next === 'string' && !stoppedRef.current) {
                setGenerating(true);
                setPrecontent(false);
                setMessage(data.next);
                return;
            }

            if (data.control === 'end') {
                setGenerating(false);
                setPrecontent(false);
                setStopped(false);
                setIsReasoningLoading(false);
                setPendingRefetch(true);
                if (conversationId) {
                    invalidateConversation(conversationId);
                }
                return;
            }

            if (data.control === 'cancel') {
                setGenerating(false);
                setPrecontent(false);
                setStopped(false);
                setIsReasoningLoading(false);
                setRegenerating(false);
                return;
            }

            if (data.control === 'start') {
                setGenerating(true);
                setPrecontent(true);
                setStopped(false);
                setReasoningSummary('');
                setIsReasoningLoading(false);
                setToolCalls([]);
                setAnnotations([]);
                setLiveRounds([]);
                if (!message) {
                    setMessage('');
                }
                return;
            }

            if (data.control === 'continue') {
                // Tool-approval resume: prior round comes from refetched
                // persistedRounds, so reset all local state.
                setGenerating(true);
                setPrecontent(true);
                setStopped(false);
                setMessage('');
                setReasoningSummary('');
                setIsReasoningLoading(false);
                setAnnotations([]);
                setToolCalls([]);
                setLiveRounds([]);
                if (conversationId) {
                    invalidateConversation(conversationId);
                }
            }
        });

        return () => {
            if (props.websocketUnregister) {
                props.websocketUnregister(props.post.id, listenerID);
            }
        };
    }, [props.post.id, conversationId]);

    const currentRound: Round | null = useMemo(() => {
        const hasContent = message !== '' ||
            toolCalls.length > 0 ||
            reasoningSummary !== '' ||
            annotations.length > 0;
        if (!hasContent) {
            return null;
        }
        return {
            id: LIVE_ROUND_ID,
            text: message,
            toolCalls,
            reasoning: {summary: reasoningSummary, signature: ''},
            annotations,
        };
    }, [message, toolCalls, reasoningSummary, annotations]);

    const renderedRounds = useMemo(() => {
        if (regenerating) {
            // Suppress stablePersisted (still the pre-regen turn) but keep
            // liveRounds so multi-round regens don't visually empty between rounds.
            const out: Round[] = [...liveRounds];
            if (currentRound) {
                out.push(currentRound);
            }
            return out;
        }
        const out: Round[] = [...stablePersisted, ...liveRounds];
        if (generating && currentRound) {
            out.push(currentRound);
        }
        return out;
    }, [regenerating, stablePersisted, liveRounds, generating, currentRound]);

    const regnerate = () => {
        setMessage('');
        setGenerating(false);
        setPrecontent(true);
        setStopped(false);
        setReasoningSummary('');
        setIsReasoningLoading(false);
        setAnnotations([]);
        setToolCalls([]);
        setLiveRounds([]);
        setRegenerating(true);
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

    const isGenerationInProgress = generating || isReasoningLoading;

    const showRegenerate = isDM && !isGenerationInProgress && requesterIsCurrentUser && !isNoShowRegen;
    const showPostbackButton = !isGenerationInProgress && requesterIsCurrentUser && isTranscriptionResult;
    const showStopGeneratingButton = isGenerationInProgress && requesterIsCurrentUser;
    const hasContent = renderedRounds.length > 0;
    const showControlsBar = ((showRegenerate || showPostbackButton) && hasContent) || showStopGeneratingButton;

    // Only the post anchor (latest persisted round) gets a real approval stage;
    // live/locally-tracked rounds always render as 'done'.
    const anchorStage: ToolApprovalStage = conversation ? deriveApprovalStageForPost(conversation, props.post.id) : 'done';
    const lastPersistedIdx = stablePersisted.length - 1;
    const lastRenderedIdx = renderedRounds.length - 1;
    const stageForRound = (idx: number): ToolApprovalStage => {
        if (idx === lastPersistedIdx && idx === lastRenderedIdx) {
            return anchorStage;
        }
        return 'done';
    };

    const isReasoningCollapsed = (roundId: string): boolean => !expandedReasoning[roundId];
    const toggleReasoning = (roundId: string, collapsed: boolean) => {
        setExpandedReasoning((prev) => ({...prev, [roundId]: !collapsed}));
    };

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
            {(precontent || (conversationLoading && !generating && renderedRounds.length === 0)) && (
                <MinimalReasoningContainer>
                    <SpinnerWrapper><LoadingSpinner/></SpinnerWrapper>
                    <span>
                        <FormattedMessage defaultMessage='Starting...'/>
                    </span>
                </MinimalReasoningContainer>
            )}
            {renderedRounds.map((round, idx) => {
                const isLiveRound = round.id === LIVE_ROUND_ID;
                const showCursor = generating && isLiveRound && !precontent;
                const reasoningLoading = isLiveRound && isReasoningLoading;
                return (
                    <RoundView
                        key={round.id}
                        round={round}
                        postID={props.post.id}
                        conversationID={conversationId}
                        channelID={props.post.channel_id}
                        approvalStage={stageForRound(idx)}
                        canApprove={requesterIsCurrentUser}
                        canExpand={requesterIsCurrentUser}
                        showCursor={showCursor}
                        reasoningLoading={reasoningLoading}
                        reasoningCollapsed={isReasoningCollapsed(round.id)}
                        onToggleReasoning={(collapsed) => toggleReasoning(round.id, collapsed)}
                    />
                );
            })}
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

interface RoundViewProps {
    round: Round;
    postID: string;
    conversationID?: string;
    channelID: string;
    approvalStage: ToolApprovalStage;
    canApprove: boolean;
    canExpand: boolean;
    showCursor: boolean;
    reasoningLoading: boolean;
    reasoningCollapsed: boolean;
    onToggleReasoning: (collapsed: boolean) => void;
}

function RoundView(props: RoundViewProps) {
    const {round} = props;
    const showArguments = round.toolCalls.some((tc) => tc.arguments != null);
    const showResults = round.toolCalls.some((tc) => tc.result != null);
    return (
        <RoundContainer>
            {round.reasoning.summary !== '' && (
                <ReasoningDisplay
                    reasoningSummary={round.reasoning.summary}
                    isReasoningCollapsed={props.reasoningCollapsed}
                    isReasoningLoading={props.reasoningLoading}
                    onToggleCollapse={props.onToggleReasoning}
                />
            )}
            {round.text !== '' && (
                <PostText
                    message={round.text}
                    channelID={props.channelID}
                    postID={props.postID}
                    showCursor={props.showCursor}
                    annotations={round.annotations.length > 0 ? round.annotations : undefined} // eslint-disable-line no-undefined
                />
            )}
            {round.toolCalls.length > 0 && (
                <ToolApprovalSet
                    postID={props.postID}
                    conversationID={props.conversationID}
                    toolCalls={round.toolCalls}
                    approvalStage={props.approvalStage}
                    canApprove={props.canApprove}
                    canExpand={props.canExpand}
                    showArguments={showArguments}
                    showResults={showResults}
                />
            )}
        </RoundContainer>
    );
}

const PostBody = styled.div`
`;

const RoundContainer = styled.div`
    & + & {
        margin-top: 8px;
    }
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
