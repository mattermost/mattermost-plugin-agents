// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';
import {AccountOutlineIcon, CheckIcon, EyeOutlineIcon, ShieldOutlineIcon} from '@mattermost/compass-icons/components';

import type {ClientError} from '@mattermost/client';

import {GlobalState} from '@mattermost/types/store';

import {doAskUserResponse, getProfilesByIds, type AskUserResponseAction} from '@/client';
import {isValidId} from '@/utils/ids';
import {Timestamp} from '@/mm_webapp';

import QuestionOptions, {FreeFormTextarea, QuestionOption, useOptionSelection} from '../question_options';
import LoadingSpinner from '../assets/loading_spinner';

// Post type of the question card the bot DMs to the target user. Mirrors the
// server-side constant in conversations/ask_another_user.go (contract C4).
export const AskUserPostType = 'custom_llm_ask_user';

// Contract C4 prop keys. The Go side defines its own constants — the strings
// are the contract. ask_user_conversation_id and ask_user_tool_use_id also
// exist on the post but the webapp never reads them: the server resolves them
// from the post at answer time.
const PROP_STATUS = 'ask_user_status';
const PROP_QUESTION = 'ask_user_question';
const PROP_CONTEXT = 'ask_user_context';
const PROP_OPTIONS = 'ask_user_options';
const PROP_MULTI_SELECT = 'ask_user_multi_select';
const PROP_ALLOW_FREE_FORM = 'ask_user_allow_free_form';
const PROP_REQUESTER_ID = 'ask_user_requester_id';
const PROP_TARGET_ID = 'ask_user_target_id';
const PROP_SOURCE_POST_ID = 'ask_user_source_post_id';
const PROP_ANSWERED_AT = 'ask_user_answered_at';
const PROP_ANSWER_PREVIEW = 'ask_user_answer_preview';

// Contract V2-C2 prop keys — all optional on the read side. A card without
// ask_user_requester_kind is a pre-v2 card and renders the exact v1 layout.
const PROP_REQUESTER_KIND = 'ask_user_requester_kind';
const PROP_REQUESTER_USERNAME = 'ask_user_requester_username';
const PROP_REQUESTER_DISPLAY_NAME = 'ask_user_requester_display_name';
const PROP_REQUESTER_POSITION = 'ask_user_requester_position';
const PROP_AGENT_DISPLAY_NAME = 'ask_user_agent_display_name';
const PROP_DESTINATION_TYPE = 'ask_user_destination_type';
const PROP_DESTINATION_CHANNEL_NAME = 'ask_user_destination_channel_display_name';
const PROP_DESTINATION_MEMBER_COUNT = 'ask_user_destination_member_count';
const PROP_DESTINATION_POLICY_ENFORCED = 'ask_user_destination_policy_enforced';

export type AskUserStatus = 'pending' | 'answered' | 'declined' | 'canceled';

// '' = the prop is absent or malformed (pre-v2 card / unknown destination).
export type AskUserRequesterKind = 'user' | 'bot' | 'unknown' | '';
export type AskUserDestinationType = 'dm' | 'gm' | 'channel' | '';

export interface AskUserCardData {
    status: AskUserStatus;
    question: string;
    context: string; // '' when absent
    options: QuestionOption[]; // [] = free-form only
    multiSelect: boolean;
    allowFreeForm: boolean;
    requesterId: string; // '' = no attribution row
    targetId: string;
    sourcePostId: string; // '' = no permalink
    answeredAt: number; // ms; 0 when absent
    answerPreview: string;

    // v2 (V2-C2) — every field degrades to its zero value on pre-v2 cards.
    requesterKind: AskUserRequesterKind; // '' = pre-v2 card, render v1 layout
    requesterUsername: string;
    requesterDisplayName: string;
    requesterPosition: string;
    agentDisplayName: string;
    destinationType: AskUserDestinationType;
    destinationChannelName: string;
    destinationMemberCount: number; // <= 0 = unknown
    destinationPolicyEnforced: boolean;
}

function stringProp(value: unknown): string {
    return typeof value === 'string' ? value : '';
}

// Returns null when the props are missing or malformed so the component can
// fall back to rendering post.message (the server-side plain-text fallback).
export function parseAskUserProps(props: Record<string, unknown> | undefined): AskUserCardData | null {
    if (!props) {
        return null;
    }

    const status = props[PROP_STATUS];
    if (status !== 'pending' && status !== 'answered' && status !== 'declined' && status !== 'canceled') {
        return null;
    }

    const question = props[PROP_QUESTION];
    if (typeof question !== 'string' || question === '') {
        return null;
    }

    // Without the target id the card cannot gate interactivity.
    const targetId = props[PROP_TARGET_ID];
    if (typeof targetId !== 'string' || targetId === '') {
        return null;
    }

    const rawOptions = props[PROP_OPTIONS];
    const options: QuestionOption[] = [];
    if (rawOptions != null) {
        if (!Array.isArray(rawOptions)) {
            return null;
        }
        const seenLabels = new Set<string>();
        for (const opt of rawOptions) {
            if (opt == null || typeof opt !== 'object' || Array.isArray(opt)) {
                return null;
            }
            const optObj = opt as {[key: string]: unknown};
            if (typeof optObj.label !== 'string' || optObj.label === '') {
                return null;
            }

            // Labels key the option rows and the selection state; duplicates
            // would double-toggle. The server validates uniqueness, but props
            // are untrusted.
            if (seenLabels.has(optObj.label)) {
                return null;
            }
            seenLabels.add(optObj.label);
            const option: QuestionOption = {label: optObj.label};
            if (typeof optObj.description === 'string') {
                option.description = optObj.description;
            }
            options.push(option);
        }
    }

    // Mirror the server pointer semantics: an absent key means allowed, an
    // explicit false disables.
    const allowFreeForm = props[PROP_ALLOW_FREE_FORM] !== false;
    if (options.length === 0 && !allowFreeForm) {
        // Nothing to interact with; server validation makes this unreachable,
        // but props are untrusted.
        return null;
    }

    const rawAnsweredAt = props[PROP_ANSWERED_AT];
    const answeredAt = typeof rawAnsweredAt === 'number' && Number.isFinite(rawAnsweredAt) && rawAnsweredAt > 0 ? rawAnsweredAt : 0;

    // v2 props (V2-C2). Every one is optional and a malformed value is treated
    // as absent — a bad v2 prop must never break an otherwise valid card.
    const rawKind = props[PROP_REQUESTER_KIND];
    const requesterKind: AskUserRequesterKind =
        rawKind === 'user' || rawKind === 'bot' || rawKind === 'unknown' ? rawKind : '';

    const rawDestinationType = props[PROP_DESTINATION_TYPE];
    const destinationType: AskUserDestinationType =
        rawDestinationType === 'dm' || rawDestinationType === 'gm' || rawDestinationType === 'channel' ? rawDestinationType : '';

    const rawMemberCount = props[PROP_DESTINATION_MEMBER_COUNT];
    const destinationMemberCount =
        typeof rawMemberCount === 'number' && Number.isFinite(rawMemberCount) && rawMemberCount > 0 ? rawMemberCount : 0;

    return {
        status,
        question,
        context: stringProp(props[PROP_CONTEXT]),
        options,
        multiSelect: props[PROP_MULTI_SELECT] === true,
        allowFreeForm,
        requesterId: stringProp(props[PROP_REQUESTER_ID]),
        targetId,
        sourcePostId: stringProp(props[PROP_SOURCE_POST_ID]),
        answeredAt,
        answerPreview: stringProp(props[PROP_ANSWER_PREVIEW]),
        requesterKind,
        requesterUsername: stringProp(props[PROP_REQUESTER_USERNAME]),
        requesterDisplayName: stringProp(props[PROP_REQUESTER_DISPLAY_NAME]),
        requesterPosition: stringProp(props[PROP_REQUESTER_POSITION]),
        agentDisplayName: stringProp(props[PROP_AGENT_DISPLAY_NAME]),
        destinationType,
        destinationChannelName: stringProp(props[PROP_DESTINATION_CHANNEL_NAME]),
        destinationMemberCount,
        destinationPolicyEnforced: props[PROP_DESTINATION_POLICY_ENFORCED] === true,
    };
}

// Mirrors the server's answer-preview rule (askUserAnswerPreview in
// conversations/ask_another_user.go) so the local resolved snapshot matches
// what the patched props will show: selected labels joined with ', ', then
// ' — ' and the free-form text when both are present, truncated to 200 runes.
// Exported for tests.
export function buildAnswerPreview(selected: string[], freeForm: string): string {
    let preview = selected.join(', ');
    const trimmedFreeForm = freeForm.trim();
    if (trimmedFreeForm !== '') {
        preview = preview === '' ? trimmedFreeForm : `${preview} — ${trimmedFreeForm}`;
    }

    // Rune-safe truncation: Array.from splits by code points, matching Go's
    // []rune semantics for astral characters.
    return Array.from(preview).slice(0, 200).join('');
}

const Card = styled.div`
    position: relative;
    display: flex;
    flex-direction: column;
    gap: 12px;
    padding: 16px 16px 16px 12px;
    margin: 8px 0 12px;
    overflow: hidden;
    background: var(--center-channel-bg);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 4px;
    box-shadow: 0 2px 3px rgba(0, 0, 0, 0.08);

    &::before {
        content: '';
        position: absolute;
        top: 0;
        left: 0;
        bottom: 0;
        width: 3px;
        background: var(--button-bg);
    }
`;

const QuestionText = styled.div`
    padding-left: 12px;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: var(--center-channel-color);
    word-break: break-word;
`;

const ContextLine = styled.div`
    padding-left: 12px;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    word-break: break-word;
`;

const AttributionRow = styled.div`
    padding-left: 12px;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

// F4a (V2-C7): visually contained region for MODEL-authored text (question,
// context, option labels/descriptions, free-form input). System chrome must
// never render inside it, so injected text cannot forge system lines.
const AIContentSection = styled.div`
    display: flex;
    flex-direction: column;
    gap: 12px;
    margin-left: 12px;
    padding: 12px;
    background: rgba(var(--center-channel-color-rgb), 0.04);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
    border-radius: 4px;
`;

const AIContentLabel = styled.div`
    font-size: 10px;
    font-weight: 600;
    line-height: 16px;
    text-transform: uppercase;
    letter-spacing: 0.02em;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

// Inside the AI region the section supplies the left inset, so the v1 12px
// question/context padding is dropped.
const AIQuestionText = styled(QuestionText)`
    padding-left: 0;
`;

const AIContextLine = styled(ContextLine)`
    padding-left: 0;
`;

// SYSTEM-authored context (destination disclosure, requester identity,
// access-policy note) below the model region and above the footer buttons.
const SystemContextSection = styled.div`
    display: flex;
    flex-direction: column;
    gap: 4px;
    margin-left: 12px;
    padding: 8px 0 0;
    border-top: 1px solid rgba(var(--center-channel-color-rgb), 0.08);
`;

const SystemContextLine = styled.div`
    display: flex;
    align-items: flex-start;
    gap: 6px;
    font-size: 12px;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
`;

const SystemContextIcon = styled.span`
    display: flex;
    align-items: center;
    flex-shrink: 0;
    height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.56);
`;

// Icon-less detail lines (requester identity) indent by the icon + gap width
// (14px + 6px) so their text column aligns with the icon-prefixed lines.
const SystemContextDetailLine = styled(SystemContextLine)`
    padding-left: 20px;
`;

// v2 attribution line above the model region; same line treatment as the
// system context block but with the card's 12px left inset.
const AttributionLine = styled(SystemContextLine)`
    margin-left: 12px;
`;

const ErrorLine = styled.div`
    color: var(--error-text);
    font-size: 12px;
    padding-left: 12px;
`;

const FreeFormOnlyRow = styled.div`
    display: flex;
`;

const Footer = styled.div`
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-left: 12px;
    padding-top: 4px;
`;

const SelectedCount = styled.div`
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const FooterButtons = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    margin-left: auto;
`;

const FooterButton = styled.button<{$primary: boolean}>`
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 6px;
    padding: 8px 16px;
    border: none;
    border-radius: 4px;
    font-size: 12px;
    font-weight: 600;
    line-height: 16px;
    cursor: pointer;
    background: ${(props) => (props.$primary ? 'var(--button-bg)' : 'rgba(var(--button-bg-rgb), 0.08)')};
    color: ${(props) => (props.$primary ? 'var(--button-color)' : 'var(--button-bg)')};

    &:hover:not(:disabled) {
        background: ${(props) => (props.$primary ? 'rgba(var(--button-bg-rgb), 0.88)' : 'rgba(var(--button-bg-rgb), 0.12)')};
    }

    &:disabled {
        cursor: default;
        opacity: 0.5;
    }
`;

const StatusLine = styled.div`
    display: flex;
    align-items: center;
    gap: 8px;
    padding-left: 12px;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.75);
`;

const AnsweredIcon = styled(CheckIcon)`
    color: var(--online-indicator);
`;

const DeclinedText = styled.div`
    padding-left: 12px;
    font-size: 13px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const AnswerPreview = styled.div`
    padding-left: 12px;
    font-size: 13px;
    font-weight: 400;
    line-height: 16px;
    color: rgba(var(--center-channel-color-rgb), 0.72);
    word-break: break-word;
`;

const ViewConversationLink = styled.a`
    padding-left: 12px;
    font-size: 12px;
    font-weight: 400;
    line-height: 16px;
    color: var(--link-color);
    width: fit-content;

    &:hover {
        text-decoration: underline;
    }
`;

const FallbackMessage = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const ProcessingSpinner = styled(LoadingSpinner)`
    width: 12px;
    height: 12px;
`;

type SubmitState =
    | {phase: 'idle'}
    | {phase: 'submitting'; action: AskUserResponseAction}
    | {phase: 'resolved'; resolution: 'answered' | 'declined'; preview: string; at: number}
    | {phase: 'error'; conflict: boolean};

interface AskUserPostProps {
    post: {
        id: string;
        message: string;
        channel_id: string;
        user_id: string;
        props?: Record<string, unknown>;
    };
}

export const AskUserPost: React.FC<AskUserPostProps> = ({post}) => {
    const data = useMemo(() => parseAskUserProps(post.props), [post.props]);

    const {formatMessage} = useIntl();
    const currentUserId = useSelector<GlobalState, string>((state) => state.entities.users.currentUserId);
    const siteURL = useSelector<GlobalState, string | undefined>((state) => state.entities.general.config.SiteURL);

    // Pre-v2 cards attribute via the requester id + a client profile fetch.
    // v2 cards (requesterKind set) carry the username in props and never
    // depend on the profile store for attribution.
    const legacyRequesterId = (data?.requesterKind ?? '') === '' ? (data?.requesterId ?? '') : '';
    const legacyRequesterUsername = useSelector<GlobalState, string | undefined>(
        (state) => state.entities.users.profiles[legacyRequesterId]?.username,
    );

    // The card post's author is the bot. Its username must accompany every
    // answer/decline request (see doAskUserResponse); submission stays
    // disabled until the profile resolves.
    const botUserId = post.user_id;
    const botUsername = useSelector<GlobalState, string | undefined>(
        (state) => state.entities.users.profiles[botUserId]?.username,
    );
    const dispatch = useDispatch();

    useEffect(() => {
        const missing: string[] = [];
        if (legacyRequesterId && !legacyRequesterUsername) {
            missing.push(legacyRequesterId);
        }
        if (botUserId && !botUsername) {
            missing.push(botUserId);
        }
        if (missing.length === 0) {
            return;
        }
        getProfilesByIds(missing).then((profiles) => {
            const profilesById = profiles.reduce<Record<string, unknown>>((acc, p) => {
                acc[p.id] = p;
                return acc;
            }, {});
            dispatch({type: 'RECEIVED_PROFILES', data: profilesById});
        }).catch(() => {
            // Best-effort: the attribution row stays hidden and submission
            // stays disabled until the profiles land in redux some other way
            // (e.g. another consumer fetches them) or the card remounts and
            // the effect runs again.
        });
    }, [legacyRequesterId, legacyRequesterUsername, botUserId, botUsername, dispatch]);

    const selection = useOptionSelection(data?.multiSelect ?? false);

    // Free-form-only questions (no options) use a standalone textarea instead
    // of the shared option selector. The placeholder doubles as the accessible
    // name since the field has no visible label.
    const [freeFormOnlyText, setFreeFormOnlyText] = useState('');
    const freeFormOnlyPlaceholder = formatMessage({
        id: 'ai.ask_user.free_form_placeholder',
        defaultMessage: 'Type your answer…',
    });
    const [submitState, setSubmitState] = useState<SubmitState>({phase: 'idle'});

    if (!data) {
        return <FallbackMessage>{post.message}</FallbackMessage>;
    }

    const canRespond = data.status === 'pending' && currentUserId === data.targetId;
    const interactive = canRespond && (submitState.phase === 'idle' || (submitState.phase === 'error' && !submitState.conflict));

    const submit = async (action: AskUserResponseAction, selected: string[], freeForm: string) => {
        if (!interactive || !botUsername) {
            return;
        }
        setSubmitState({phase: 'submitting', action});
        try {
            await doAskUserResponse(post.id, botUsername, {action, selected, free_form: freeForm});
            setSubmitState({
                phase: 'resolved',
                resolution: action === 'answer' ? 'answered' : 'declined',
                preview: buildAnswerPreview(selected, freeForm),
                at: Date.now(),
            });
        } catch (err) {
            const conflict = (err as ClientError).status_code === 409;
            setSubmitState({phase: 'error', conflict});
        }
    };

    const hasOptions = data.options.length > 0;

    // Answer requires at least one valid choice. When free-form is selected
    // its text must be non-empty; otherwise a predefined option must be
    // selected. Free-form-only questions just need non-blank text.
    let canSubmit: boolean;
    if (!hasOptions) {
        canSubmit = freeFormOnlyText.trim() !== '';
    } else if (selection.freeFormSelected) {
        canSubmit = selection.customAnswered || selection.selections.length > 0;
    } else {
        canSubmit = selection.selections.length > 0;
    }

    const onAnswer = () => {
        if (hasOptions) {
            submit('answer', selection.selections, selection.customAnswered ? selection.trimmedCustom : '');
        } else {
            submit('answer', [], freeFormOnlyText.trim());
        }
    };

    const onDecline = () => {
        submit('decline', [], '');
    };

    const guardedToggleOption = (label: string) => {
        if (!interactive) {
            return;
        }
        selection.toggleOption(label);
    };

    const guardedToggleFreeForm = () => {
        if (!interactive) {
            return;
        }
        selection.toggleFreeForm();
    };

    // Props always win over the local snapshot: the server patches the card
    // post on answer/decline/cancel and the post-edit event delivers new
    // props. The local snapshot only bridges the gap until that event lands.
    let resolution: 'answered' | 'declined' | 'canceled' | null = null;
    let preview = '';
    let resolvedAt = 0;
    if (data.status === 'answered') {
        resolution = 'answered';
        preview = data.answerPreview;
        resolvedAt = data.answeredAt;
    } else if (data.status === 'declined') {
        resolution = 'declined';
    } else if (data.status === 'canceled') {
        resolution = 'canceled';
    } else if (submitState.phase === 'resolved') {
        resolution = submitState.resolution;
        preview = submitState.preview;
        resolvedAt = submitState.at;
    }

    const renderResolved = () => {
        if (resolution === 'declined') {
            return (
                <DeclinedText>
                    <FormattedMessage
                        id='ai.ask_user.declined'
                        defaultMessage='You declined to answer'
                    />
                </DeclinedText>
            );
        }
        if (resolution === 'canceled') {
            // Neutral terminal state (V2-C6): no preview, no timestamp.
            return (
                <StatusLine>
                    <FormattedMessage
                        id='ai.ask_user.no_longer_needed'
                        defaultMessage='This question is no longer needed.'
                    />
                </StatusLine>
            );
        }
        return (
            <>
                <StatusLine>
                    <AnsweredIcon size={16}/>
                    <FormattedMessage
                        id='ai.ask_user.answered'
                        defaultMessage='Answered'
                    />
                    {resolvedAt > 0 && Timestamp && (
                        <Timestamp
                            value={resolvedAt}
                            units={['now', 'minute', 'hour', 'day', 'week']}
                            useTime={false}
                        />
                    )}
                </StatusLine>
                {preview !== '' && <AnswerPreview>{preview}</AnswerPreview>}
            </>
        );
    };

    // MODEL-authored answer inputs (option labels/descriptions, free-form
    // field). In the v2 layout these render inside the AI content region.
    const renderAnswerInputs = () => (
        hasOptions ? (
            <QuestionOptions
                options={data.options}
                multiSelect={data.multiSelect}
                allowFreeForm={data.allowFreeForm}
                interactive={interactive}
                multilineFreeForm={true}
                selections={selection.selections}
                freeFormSelected={selection.freeFormSelected}
                customText={selection.customText}
                onToggleOption={guardedToggleOption}
                onToggleFreeForm={guardedToggleFreeForm}
                onChangeCustomText={selection.setCustomText}
            />
        ) : (
            canRespond && (
                <FreeFormOnlyRow>
                    <FreeFormTextarea
                        rows={3}
                        value={freeFormOnlyText}
                        placeholder={freeFormOnlyPlaceholder}
                        aria-label={freeFormOnlyPlaceholder}
                        disabled={!interactive}
                        onChange={(e) => setFreeFormOnlyText(e.target.value)}
                    />
                </FreeFormOnlyRow>
            )
        )
    );

    // SYSTEM-authored pending chrome: submit status/error lines + footer
    // buttons. In the v2 layout these render outside the AI content region.
    const renderPendingChrome = () => (
        <>
            {submitState.phase === 'error' && !submitState.conflict && (
                <ErrorLine>
                    <FormattedMessage
                        id='ai.ask_user.submit_failed'
                        defaultMessage='Failed to submit your response. Please try again.'
                    />
                </ErrorLine>
            )}
            {submitState.phase === 'error' && submitState.conflict && (

                // A 409 means the question was resolved elsewhere (an answer
                // race or an initiator cancel). Neutral line, not an error;
                // the patched props settle the final rendering (V2-C4).
                <StatusLine>
                    <FormattedMessage
                        id='ai.ask_user.no_longer_needed'
                        defaultMessage='This question is no longer needed.'
                    />
                </StatusLine>
            )}
            {submitState.phase === 'submitting' && (
                <StatusLine>
                    <ProcessingSpinner/>
                    <FormattedMessage
                        id='ai.ask_user.submitting'
                        defaultMessage='Submitting…'
                    />
                </StatusLine>
            )}
            {interactive && (
                <Footer>
                    {data.multiSelect && (
                        <SelectedCount>
                            <FormattedMessage
                                id='ai.question.selected_count'
                                defaultMessage='{count, plural, =0 {None selected} one {# selected} other {# selected}}'
                                values={{count: selection.selectedCount}}
                            />
                        </SelectedCount>
                    )}
                    <FooterButtons>
                        <FooterButton
                            type='button'
                            $primary={false}
                            disabled={!botUsername}
                            onClick={onDecline}
                        >
                            <FormattedMessage
                                id='ai.ask_user.decline'
                                defaultMessage='Decline'
                            />
                        </FooterButton>
                        <FooterButton
                            type='button'
                            $primary={true}
                            disabled={!canSubmit || !botUsername}
                            onClick={onAnswer}
                        >
                            <FormattedMessage
                                id='ai.ask_user.answer'
                                defaultMessage='Answer'
                            />
                        </FooterButton>
                    </FooterButtons>
                </Footer>
            )}
        </>
    );

    const renderPending = () => (
        <>
            {renderAnswerInputs()}
            {renderPendingChrome()}
        </>
    );

    const permalink = isValidId(data.sourcePostId) && Boolean(siteURL) && (
        <ViewConversationLink href={`${siteURL}/_redirect/pl/${data.sourcePostId}`}>
            <FormattedMessage
                id='ai.ask_user.view_conversation'
                defaultMessage='View conversation'
            />
        </ViewConversationLink>
    );

    // Pre-v2 card: render the exact v1 layout (V2-C2 backward-compat rule).
    if (data.requesterKind === '') {
        return (
            <Card>
                <QuestionText>{data.question}</QuestionText>
                {data.context !== '' && <ContextLine>{data.context}</ContextLine>}
                {legacyRequesterId !== '' && legacyRequesterUsername && (
                    <AttributionRow>
                        <FormattedMessage
                            id='ai.ask_user.on_behalf_of'
                            defaultMessage='Asked on behalf of @{username}'
                            values={{username: legacyRequesterUsername}}
                        />
                    </AttributionRow>
                )}
                {resolution === null ? renderPending() : renderResolved()}
                {permalink}
            </Card>
        );
    }

    // F4c attribution — always from props, never from model text. An empty
    // name value (malformed props) omits the row rather than rendering a
    // broken sentence.
    const renderAttribution = () => {
        if (data.requesterKind === 'user' && data.requesterUsername !== '') {
            return (
                <AttributionLine>
                    <SystemContextIcon><AccountOutlineIcon size={14}/></SystemContextIcon>
                    <span>
                        <FormattedMessage
                            id='ai.ask_user.on_behalf_of'
                            defaultMessage='Asked on behalf of @{username}'
                            values={{username: data.requesterUsername}}
                        />
                    </span>
                </AttributionLine>
            );
        }
        if (data.requesterKind === 'bot' && data.agentDisplayName !== '') {
            return (
                <AttributionLine>
                    <SystemContextIcon><AccountOutlineIcon size={14}/></SystemContextIcon>
                    <span>
                        <FormattedMessage
                            id='ai.ask_user.asked_by_agent_unattended'
                            defaultMessage='Asked by the {agentName} agent running unattended (no human requester)'
                            values={{agentName: data.agentDisplayName}}
                        />
                    </span>
                </AttributionLine>
            );
        }
        if (data.requesterKind === 'unknown' && data.agentDisplayName !== '') {
            return (
                <AttributionLine>
                    <SystemContextIcon><AccountOutlineIcon size={14}/></SystemContextIcon>
                    <span>
                        <FormattedMessage
                            id='ai.ask_user.asked_by_agent_unknown_requester'
                            defaultMessage='Asked via the {agentName} agent (requester identity unavailable)'
                            values={{agentName: data.agentDisplayName}}
                        />
                    </span>
                </AttributionLine>
            );
        }
        return null;
    };

    // Destination disclosure — the full V2-C2 rendering matrix. Every lookup
    // failure at dispatch degraded toward the broader audience claim, so the
    // fallbacks here go in the same direction.
    const renderDestinationMessage = () => {
        if (data.destinationType === 'dm') {
            if (data.requesterKind === 'user' && data.requesterUsername !== '') {
                return (
                    <FormattedMessage
                        id='ai.ask_user.destination_dm'
                        defaultMessage='Your answer will be shared with @{username}.'
                        values={{username: data.requesterUsername}}
                    />
                );
            }
            if (data.requesterKind === 'bot' && data.agentDisplayName !== '') {
                return (
                    <FormattedMessage
                        id='ai.ask_user.destination_dm_agent'
                        defaultMessage='Your answer will be shared with the {agentName} agent.'
                        values={{agentName: data.agentDisplayName}}
                    />
                );
            }
            return (
                <FormattedMessage
                    id='ai.ask_user.destination_dm_unknown'
                    defaultMessage='Your answer will be shared with the person who asked the agent.'
                />
            );
        }
        if (data.destinationType === 'channel') {
            if (data.destinationChannelName !== '' && data.destinationMemberCount > 0) {
                return (
                    <FormattedMessage
                        id='ai.ask_user.destination_channel'
                        defaultMessage='Your answer may be shared with the {count, plural, one {# member} other {# members}} of ~{channelName}.'
                        values={{count: data.destinationMemberCount, channelName: data.destinationChannelName}}
                    />
                );
            }
            if (data.destinationChannelName !== '') {
                return (
                    <FormattedMessage
                        id='ai.ask_user.destination_channel_no_count'
                        defaultMessage='Your answer may be shared with the members of ~{channelName}.'
                        values={{channelName: data.destinationChannelName}}
                    />
                );
            }
            return (
                <FormattedMessage
                    id='ai.ask_user.destination_channel_unknown'
                    defaultMessage='Your answer may be shared in the channel where the agent was asked.'
                />
            );
        }
        if (data.destinationType === 'gm') {
            if (data.destinationMemberCount > 0) {
                return (
                    <FormattedMessage
                        id='ai.ask_user.destination_gm'
                        defaultMessage='Your answer may be shared with the {count, plural, one {# member} other {# members}} of a group message.'
                        values={{count: data.destinationMemberCount}}
                    />
                );
            }
            return (
                <FormattedMessage
                    id='ai.ask_user.destination_gm_no_count'
                    defaultMessage='Your answer may be shared with the members of a group message.'
                />
            );
        }
        return null;
    };

    const destinationMessage = renderDestinationMessage();

    // Identity detail (kind=user only): non-empty parts joined with ' · ' in
    // code — pure formatting, no i18n message (V2-C2).
    const identityDetail = data.requesterKind === 'user' ?
        [data.requesterDisplayName, data.requesterPosition].filter((part) => part !== '').join(' · ') :
        '';

    // The policy flag can only come from the same channel read that produced
    // the display name; anything unavailable means the line is omitted —
    // never render an empty or "no restrictions" statement (V2-C2).
    const showPolicyLine = data.destinationType === 'channel' &&
        data.destinationPolicyEnforced &&
        data.destinationChannelName !== '';

    const hasSystemContext = destinationMessage !== null || identityDetail !== '' || showPolicyLine;

    return (
        <Card>
            {renderAttribution()}
            <AIContentSection data-testid='ask-user-ai-content'>
                <AIContentLabel>
                    <FormattedMessage
                        id='ai.ask_user.agent_generated_label'
                        defaultMessage='AI-generated content'
                    />
                </AIContentLabel>
                <AIQuestionText>{data.question}</AIQuestionText>
                {data.context !== '' && <AIContextLine>{data.context}</AIContextLine>}
                {resolution === null && renderAnswerInputs()}
            </AIContentSection>
            {hasSystemContext && (
                <SystemContextSection>
                    {destinationMessage !== null && (
                        <SystemContextLine>
                            <SystemContextIcon><EyeOutlineIcon size={14}/></SystemContextIcon>
                            <span>{destinationMessage}</span>
                        </SystemContextLine>
                    )}
                    {identityDetail !== '' && (
                        <SystemContextDetailLine>{identityDetail}</SystemContextDetailLine>
                    )}
                    {showPolicyLine && (
                        <SystemContextLine>
                            <SystemContextIcon><ShieldOutlineIcon size={14}/></SystemContextIcon>
                            <span>
                                <FormattedMessage
                                    id='ai.ask_user.channel_policy_enforced'
                                    defaultMessage='Access to ~{channelName} is restricted by an attribute-based access policy.'
                                    values={{channelName: data.destinationChannelName}}
                                />
                            </span>
                        </SystemContextLine>
                    )}
                </SystemContextSection>
            )}
            {resolution === null ? renderPendingChrome() : renderResolved()}
            {permalink}
        </Card>
    );
};
