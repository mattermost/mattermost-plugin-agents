// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

import {doLoopInAgent} from '@/client';

const Hint = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: 13px;
    line-height: 18px;
`;

const ErrorMessage = styled.div`
    color: rgba(var(--error-text-color-rgb), 1);
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

interface Props {
    post: {
        id: string;
        message: string;
        props?: {
            bot_username?: string;
            bot_display_name?: string;
            target_post_id?: string;
        };
    };
}

type LoopInStatus = 'idle' | 'pending' | 'done' | 'error';

export const AgentMentionReminderPost = ({post}: Props) => {
    const botUsername = post.props?.bot_username ?? '';
    const botDisplayName = post.props?.bot_display_name?.trim() || botUsername;
    const targetPostId = post.props?.target_post_id ?? post.id;

    const [status, setStatus] = useState<LoopInStatus>('idle');
    const pending = status === 'pending';

    const onClick = async (event: React.MouseEvent<HTMLAnchorElement>) => {
        event.preventDefault();
        if (pending || status === 'done' || !botUsername || !targetPostId) {
            return;
        }
        setStatus('pending');
        try {
            await doLoopInAgent(targetPostId, botUsername);
            setStatus('done');
        } catch (err) {
            console.error('Failed to loop in agent:', err); // eslint-disable-line no-console
            setStatus('error');
        }
    };

    if (!botUsername) {
        return (
            <Hint>{post.message}</Hint>
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
                defaultMessage='To respond to an agent you must @mention them. <link>click here to loop in @{botDisplayName}</link>'
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
            {status === 'error' && (
                <ErrorMessage>
                    <FormattedMessage
                        id='agents.agent_mention_reminder_error'
                        defaultMessage='Failed to loop in @{botDisplayName}. Please try again.'
                        values={{botDisplayName}}
                    />
                </ErrorMessage>
            )}
        </Hint>
    );
};
