// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useMemo, useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage, useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';
import {CheckIcon} from '@mattermost/compass-icons/components';

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

export type AskUserStatus = 'pending' | 'answered' | 'declined';

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
    if (status !== 'pending' && status !== 'answered' && status !== 'declined') {
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
        for (const opt of rawOptions) {
            if (opt == null || typeof opt !== 'object' || Array.isArray(opt)) {
                return null;
            }
            const optObj = opt as {[key: string]: unknown};
            if (typeof optObj.label !== 'string' || optObj.label === '') {
                return null;
            }
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
    };
}

// Mirrors the server's answer-preview rule (contract C4) so the local
// resolved snapshot matches what the patched props will show.
function buildAnswerPreview(selected: string[], freeForm: string): string {
    return [...selected, freeForm.trim()].filter((s) => s !== '').join(', ').slice(0, 200);
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
    const requesterId = data?.requesterId ?? '';
    const requesterUsername = useSelector<GlobalState, string | undefined>(
        (state) => state.entities.users.profiles[requesterId]?.username,
    );
    const dispatch = useDispatch();

    useEffect(() => {
        if (!requesterId || requesterUsername) {
            return;
        }
        getProfilesByIds([requesterId]).then((profiles) => {
            const profilesById = profiles.reduce<Record<string, unknown>>((acc, p) => {
                acc[p.id] = p;
                return acc;
            }, {});
            dispatch({type: 'RECEIVED_PROFILES', data: profilesById});
        }).catch(() => {
            // Attribution is best-effort; the row simply stays hidden.
        });
    }, [requesterId, requesterUsername, dispatch]);

    const selection = useOptionSelection(data?.multiSelect ?? false);

    // Free-form-only questions (no options) use a standalone textarea instead
    // of the shared option selector.
    const [freeFormOnlyText, setFreeFormOnlyText] = useState('');
    const [submitState, setSubmitState] = useState<SubmitState>({phase: 'idle'});

    if (!data) {
        return <FallbackMessage>{post.message}</FallbackMessage>;
    }

    const canRespond = data.status === 'pending' && currentUserId === data.targetId;
    const interactive = canRespond && (submitState.phase === 'idle' || (submitState.phase === 'error' && !submitState.conflict));

    const submit = async (action: AskUserResponseAction, selected: string[], freeForm: string) => {
        if (!interactive) {
            return;
        }
        setSubmitState({phase: 'submitting', action});
        try {
            await doAskUserResponse(post.id, {action, selected, free_form: freeForm});
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
    // post on answer/decline and the post-edit event delivers new props. The
    // local snapshot only bridges the gap until that event lands.
    let resolution: 'answered' | 'declined' | null = null;
    let preview = '';
    let resolvedAt = 0;
    if (data.status === 'answered') {
        resolution = 'answered';
        preview = data.answerPreview;
        resolvedAt = data.answeredAt;
    } else if (data.status === 'declined') {
        resolution = 'declined';
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

    const renderPending = () => (
        <>
            {hasOptions ? (
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
                            placeholder={formatMessage({
                                id: 'ai.ask_user.free_form_placeholder',
                                defaultMessage: 'Type your answer…',
                            })}
                            disabled={!interactive}
                            onChange={(e) => setFreeFormOnlyText(e.target.value)}
                        />
                    </FreeFormOnlyRow>
                )
            )}
            {submitState.phase === 'error' && (
                <ErrorLine>
                    {submitState.conflict ? (
                        <FormattedMessage
                            id='ai.ask_user.no_longer_active'
                            defaultMessage='This question is no longer active.'
                        />
                    ) : (
                        <FormattedMessage
                            id='ai.ask_user.submit_failed'
                            defaultMessage='Failed to submit your response. Please try again.'
                        />
                    )}
                </ErrorLine>
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
                            disabled={!canSubmit}
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

    return (
        <Card>
            <QuestionText>{data.question}</QuestionText>
            {data.context !== '' && <ContextLine>{data.context}</ContextLine>}
            {requesterId !== '' && requesterUsername && (
                <AttributionRow>
                    <FormattedMessage
                        id='ai.ask_user.on_behalf_of'
                        defaultMessage='Asked on behalf of @{username}'
                        values={{username: requesterUsername}}
                    />
                </AttributionRow>
            )}
            {resolution === null ? renderPending() : renderResolved()}
            {isValidId(data.sourcePostId) && Boolean(siteURL) && (
                <ViewConversationLink href={`${siteURL}/_redirect/pl/${data.sourcePostId}`}>
                    <FormattedMessage
                        id='ai.ask_user.view_conversation'
                        defaultMessage='View conversation'
                    />
                </ViewConversationLink>
            )}
        </Card>
    );
};
