// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useSelector} from 'react-redux';
import styled from 'styled-components';

import {GlobalState} from '@mattermost/types/store';

import {doPostbackSummary, doRegenerate, doStopGenerating} from '@/client';
import {PluginWebSocketMessage} from '@/types';
import {useSelectNotAIPost} from '@/hooks';
import {useConversation, invalidateConversation} from '@/hooks/use_conversation';
import {PostMessagePreview} from '@/mm_webapp';

import {isValidId} from '@/utils/ids';

import {ServerToolUse} from '@/types/conversation';

import PostText from '../post_text';
import {SearchSources, parseSearchSources} from '../search_sources';
import ToolApprovalSet from '../tool_approval_set';
import {ToolApprovalStage, ToolCall, ToolCallStatus} from '../tool_types';
import {Annotation} from '../citations/types';

import ServerToolSet from './server_tool_set';

import {
    Round,
    buildRoundsFromTurns,
    computeRenderedRounds,
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
    server_tool?: string
}

interface LLMBotPostProps {
    post: any;
    websocketRegister?: (postID: string, listenerID: string, handler: (msg: PluginWebSocketMessage<PostUpdateWebsocketMessage>) => void) => void;
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
    const intl = useIntl();
    const selectPost = useSelectNotAIPost();

    // Post props are free-form JSON; a conversation_id that is not a
    // well-formed id is treated as absent so no request is built from it and
    // the component falls back to its no-conversation rendering path.
    const rawConversationId: unknown = props.post.props?.conversation_id;
    const conversationId: string | undefined = isValidId(rawConversationId) ? rawConversationId : undefined; // eslint-disable-line no-undefined
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
    const [serverTools, setServerTools] = useState<ServerToolUse[]>([]);
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
    const liveRef = useRef({message, toolCalls, reasoningSummary, annotations, serverTools});
    liveRef.current = {message, toolCalls, reasoningSummary, annotations, serverTools};

    // Sync message from post.message changes (e.g. after post update)
    useEffect(() => {
        if (props.post.message !== '' && props.post.message !== message) {
            setMessage(props.post.message);
            setPrecontent(false);
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
        setServerTools((prev: ServerToolUse[]) => (prev.length === 0 ? prev : []));
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

        props.websocketRegister(props.post.id, listenerID, (msg: PluginWebSocketMessage<PostUpdateWebsocketMessage>) => {
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
                                serverTools: live.serverTools,
                            },
                        ]);
                        setMessage('');
                        setToolCalls([]);
                        setReasoningSummary('');
                        setIsReasoningLoading(false);
                        setAnnotations([]);
                        setServerTools([]);
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

            if (data.control === 'server_tool' && data.server_tool) {
                // Cumulative provider-executed tool activity for the round;
                // each event replaces the prior snapshot.
                try {
                    const parsedServerTools = JSON.parse(data.server_tool) as ServerToolUse[];
                    setServerTools(parsedServerTools);
                    setPrecontent(false);
                } catch {
                    setError(intl.formatMessage({defaultMessage: 'Error parsing server tool data'}));
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
                setServerTools([]);
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
                setServerTools([]);
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
            annotations.length > 0 ||
            serverTools.length > 0;
        if (!hasContent) {
            return null;
        }
        return {
            id: LIVE_ROUND_ID,
            text: message,
            toolCalls,
            reasoning: {summary: reasoningSummary, signature: ''},
            annotations,
            serverTools,
        };
    }, [message, toolCalls, reasoningSummary, annotations, serverTools]);

    const renderedRounds = useMemo(() => computeRenderedRounds({
        regenerating,
        hasConversation: Boolean(conversationId),
        persistedRounds: stablePersisted,
        liveRounds,
        generating,
        pendingRefetch,
        currentRound,
    }), [regenerating, conversationId, stablePersisted, liveRounds, generating, pendingRefetch, currentRound]);

    const regnerate = () => {
        setMessage('');
        setGenerating(false);
        setPrecontent(true);
        setStopped(false);
        setReasoningSummary('');
        setIsReasoningLoading(false);
        setAnnotations([]);
        setToolCalls([]);
        setServerTools([]);
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

    // Parsed defensively: search_results is a free-form post prop, so a
    // malformed value yields an empty list instead of throwing during render.
    const searchSources = useMemo(
        () => parseSearchSources(props.post.props?.[SearchResultsPropKey]),
        [props.post.props],
    );

    const isReasoningCollapsed = (roundId: string): boolean => !expandedReasoning[roundId];
    const toggleReasoning = (roundId: string, collapsed: boolean) => {
        setExpandedReasoning((prev) => ({...prev, [roundId]: !collapsed}));
    };

    // Tool-only posts leave post.message empty, so precontent stays true on
    // remount. Hide Working... once rounds exist, except while generation is
    // actually resuming (continue/start still set precontent on purpose).
    const showWorking =
        (precontent && (generating || renderedRounds.length === 0)) ||
        (conversationLoading && !generating && renderedRounds.length === 0);

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
            {showWorking && (
                <MinimalReasoningContainer>
                    <SpinnerWrapper><LoadingSpinner/></SpinnerWrapper>
                    <span>
                        <FormattedMessage defaultMessage='Working...'/>
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
            {searchSources.length > 0 && (
                <SearchSources
                    sources={searchSources}
                />
            )}
            { showPostbackButton &&
            <PostSummaryHelpMessage data-testid='llm-bot-post-summary-help'>
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
            {round.serverTools.length > 0 && (
                <ServerToolSet serverTools={round.serverTools}/>
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
