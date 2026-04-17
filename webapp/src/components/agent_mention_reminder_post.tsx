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

export const AgentMentionReminderPost = ({post}: Props) => {
    const botUsername = post.props?.bot_username ?? '';
    const botDisplayName = post.props?.bot_display_name?.trim() || botUsername;
    const targetPostId = post.props?.target_post_id ?? post.id;

    const [pending, setPending] = useState(false);
    const [done, setDone] = useState(false);
    const [error, setError] = useState(false);

    const onClick = async (event: React.MouseEvent<HTMLAnchorElement>) => {
        event.preventDefault();
        if (pending || done || !botUsername || !targetPostId) {
            return;
        }
        setPending(true);
        setError(false);
        try {
            await doLoopInAgent(targetPostId, botUsername);
            setDone(true);
        } catch {
            setError(true);
        } finally {
            setPending(false);
        }
    };

    if (!botUsername) {
        return (
            <Hint>{post.message}</Hint>
        );
    }

    if (done) {
        return (
            <Hint>
                <FormattedMessage
                    defaultMessage='Looped in @{botUsername}.'
                    values={{botUsername}}
                />
            </Hint>
        );
    }

    return (
        <Hint>
            <FormattedMessage
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
            {error && (
                <ErrorMessage>
                    <FormattedMessage
                        defaultMessage='Failed to loop in @{botDisplayName}. Please try again.'
                        values={{botDisplayName}}
                    />
                </ErrorMessage>
            )}
        </Hint>
    );
};
