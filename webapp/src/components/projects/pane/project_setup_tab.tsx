// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';
import {useDispatch} from 'react-redux';
import styled from 'styled-components';

import {Button} from '@mattermost/compass-ui/components/button';

import {selectRegularPost} from '@/redux_actions';
import type {Project} from '@/types/projects';

import AgentChatThreadView, {isThreadViewerAvailable} from '../../agent_chat/agent_chat_thread_view';

// The project's setup thread as a chat with its agent, the same view the Agents page uses.
const ProjectSetupTab = ({project}: {project: Project}) => {
    const dispatch = useDispatch();
    const rootId = project.setup_root_post_id;

    if (!rootId || !isThreadViewerAvailable) {
        return (
            <Message data-testid='project-setup-unavailable'>
                <FormattedMessage defaultMessage='The setup conversation can’t be shown here.'/>
                {rootId && (
                    <Button
                        size='small'
                        emphasis='tertiary'
                        onClick={() => dispatch(selectRegularPost(rootId, project.channel_id))}
                    >
                        <FormattedMessage defaultMessage='Open thread'/>
                    </Button>
                )}
            </Message>
        );
    }

    return (
        <ChatHost data-testid='project-setup-tab'>
            <AgentChatThreadView
                layout='panel'
                testId='project-setup-thread'
                rootId={rootId}
                agentName={project.agent_display_name || project.agent_username}
                isThreadView={true}
            />
        </ChatHost>
    );
};

export default ProjectSetupTab;

const ChatHost = styled.div`
    display: flex;
    flex: 1;
    flex-direction: column;
    min-height: 0;
    overflow: hidden;
`;

const Message = styled.div`
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--spacing-s, 8px);
    padding: var(--spacing-xl, 24px) var(--spacing-m, 16px);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    line-height: 20px;
    text-align: center;
`;
