// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {ErrorMessage} from '@mattermost/compass-ui/components/error-message';

import {doDisableAgentMentionReminder, doLoopInAgent} from '@/client';
import {isValidId} from '@/utils/ids';

const Hint = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
`;

const Actions = styled.div`
    display: flex;
    gap: var(--spacing-2, 8px);
    margin-top: var(--spacing-1, 4px);
`;

const DismissButton = styled.button<{$pending: boolean}>`
    background: transparent;
    border: none;
    padding: 0;
    color: rgba(var(--link-color-rgb), 1);
    cursor: ${(props) => (props.$pending ? 'progress' : 'pointer')};
    pointer-events: ${(props) => (props.$pending ? 'none' : 'auto')};
    opacity: ${(props) => (props.$pending ? 0.6 : 1)};
    text-decoration: underline;

    &:hover {
        text-decoration: none;
    }
`;

const LoopInError = styled(ErrorMessage)`
    margin-top: 4px;
`;

const LoopInLink = styled.a<{$pending: boolean}>`
    color: rgba(var(--link-color-rgb), 1);
    cursor: ${(props) => (props.$pending ? 'progress' : 'pointer')};
    pointer-events: ${(props) => (props.$pending ? 'none' : 'auto')};
    opacity: ${(props) => (props.$pending ? 0.6 : 1)};
    text-decoration: underline;

    &:hover {
        text-decoration: none;
    }
`;

// Only the server sets this type.
const EphemeralPostType = 'system_ephemeral';

function stringProp(value: unknown): string {
    return typeof value === 'string' ? value : '';
}

interface Props {
    post: {
        id: string;
        type?: string;
        message: string;
        props?: {
            bot_user_id?: unknown;
            bot_username?: unknown;
            bot_display_name?: unknown;
            target_post_id?: unknown;
        };
    };
}

type LoopInStatus = 'idle' | 'pending' | 'done' | 'error';
type DismissStatus = 'idle' | 'pending' | 'done' | 'error';

export const AgentMentionReminderPost = ({post}: Props) => {
    const botUserId = stringProp(post.props?.bot_user_id);
    const botUsername = stringProp(post.props?.bot_username);
    const botDisplayName = stringProp(post.props?.bot_display_name).trim() || botUsername;
    const targetPostId = post.props?.target_post_id ?? post.id;
    const loopInPostId = isValidId(targetPostId) ? targetPostId : '';
    const canLoopIn = post.type === EphemeralPostType && isValidId(botUserId) && loopInPostId !== '';

    const [status, setStatus] = useState<LoopInStatus>('idle');
    const [dismissStatus, setDismissStatus] = useState<DismissStatus>('idle');
    const pending = status === 'pending';
    const dismissPending = dismissStatus === 'pending';

    const onClick = async (event: React.MouseEvent<HTMLAnchorElement>) => {
        event.preventDefault();
        if (pending || status === 'done' || !canLoopIn) {
            return;
        }
        setStatus('pending');
        try {
            await doLoopInAgent(loopInPostId, botUserId);
            setStatus('done');
        } catch (err) {
            console.error('Failed to loop in agent:', err); // eslint-disable-line no-console
            setStatus('error');
        }
    };

    const onDismiss = async () => {
        if (dismissPending || dismissStatus === 'done') {
            return;
        }
        setDismissStatus('pending');
        try {
            await doDisableAgentMentionReminder();
            setDismissStatus('done');
        } catch (err) {
            console.error('Failed to disable agent mention reminder:', err); // eslint-disable-line no-console
            setDismissStatus('error');
        }
    };

    if (!canLoopIn) {
        return (
            <Hint>{post.message}</Hint>
        );
    }

    if (dismissStatus === 'done') {
        return (
            <Hint>
                <FormattedMessage
                    id='agents.agent_mention_reminder_dismissed'
                    defaultMessage="You won't see this reminder again."
                />
            </Hint>
        );
    }

    if (status === 'done') {
        return (
            <Hint>
                <FormattedMessage
                    id='agents.agent_mention_reminder_done'
                    defaultMessage='Looped in @{botDisplayName}.'
                    values={{botDisplayName}}
                />
            </Hint>
        );
    }

    return (
        <Hint>
            <FormattedMessage
                id='agents.agent_mention_reminder_body'
                defaultMessage='To respond to an agent you must @mention them. <link>Click here to loop in @{botDisplayName}</link>'
                values={{
                    botDisplayName,
                    link: (chunks: React.ReactNode) => (
                        <LoopInLink
                            href='#'
                            onClick={onClick}
                            $pending={pending}
                            aria-disabled={pending}
                        >
                            {chunks}
                        </LoopInLink>
                    ),
                }}
            />
            <Actions>
                <DismissButton
                    type='button'
                    onClick={onDismiss}
                    $pending={dismissPending}
                    aria-disabled={dismissPending}
                >
                    <FormattedMessage
                        id='agents.agent_mention_reminder_dismiss'
                        defaultMessage="Don't show again"
                    />
                </DismissButton>
            </Actions>
            {status === 'error' && (
                <LoopInError
                    message={
                        <FormattedMessage
                            id='agents.agent_mention_reminder_error'
                            defaultMessage='Failed to loop in @{botDisplayName}. Please try again.'
                            values={{botDisplayName}}
                        />
                    }
                />
            )}
            {dismissStatus === 'error' && (
                <LoopInError
                    message={
                        <FormattedMessage
                            id='agents.agent_mention_reminder_dismiss_error'
                            defaultMessage='Failed to update your preference. Please try again.'
                        />
                    }
                />
            )}
        </Hint>
    );
};
