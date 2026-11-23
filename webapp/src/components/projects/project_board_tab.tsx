// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';
import {useSelector} from 'react-redux';
import styled from 'styled-components';

import type {GlobalState} from '@mattermost/types/store';

import type {Project} from '@/types/projects';

import {useElementWidth} from './pane/use_element_width';
import ProjectErrorBoundary from './project_error_boundary';
import ProjectLink from './project_link';
import {boardChannelPath} from './project_links';

const MIN_BOARDS_VERSION = 1;

// Narrower than this, the board uses its compact layout.
const COMPACT_BELOW_WIDTH = 640;

const BoardLink = ({project}: {project: Project}) => {
    const path = useSelector((state: GlobalState) => boardChannelPath(state, project));
    if (!path) {
        return null;
    }
    return (
        <ProjectLink
            path={path}
            testId='project-board-link'
        >
            <FormattedMessage defaultMessage='Open the board channel'/>
        </ProjectLink>
    );
};

const BoardUnavailable = ({project, missing}: {project: Project; missing: boolean}) => (
    <Message data-testid='project-board-fallback'>
        {missing ? (
            <FormattedMessage defaultMessage='The board view needs the Boards plugin, or a newer version of it. The board is still available in its own channel.'/>
        ) : (
            <FormattedMessage defaultMessage='The board could not be shown here. It is still available in its own channel.'/>
        )}
        <BoardLink project={project}/>
    </Message>
);

const ProjectBoardTab = ({project}: {project: Project}) => {
    const [hostRef, width] = useElementWidth<HTMLDivElement>();

    if (!project.board_channel_id) {
        return (
            <Message data-testid='project-board-empty'>
                <FormattedMessage defaultMessage='No board linked yet — ask your project agent to create one.'/>
            </Message>
        );
    }

    const boards = window.MattermostBoardsV2;
    if (!boards || !(boards.version >= MIN_BOARDS_VERSION)) {
        return (
            <BoardUnavailable
                project={project}
                missing={true}
            />
        );
    }

    const EmbeddedBoard = boards.EmbeddedBoard;
    return (
        <BoardHost
            ref={hostRef}
            data-testid='project-board-embed'
        >
            <ProjectErrorBoundary
                fallback={(
                    <BoardUnavailable
                        project={project}
                        missing={false}
                    />
                )}
            >
                <EmbeddedBoard
                    channelId={project.board_channel_id}
                    compact={width === 0 || width < COMPACT_BELOW_WIDTH}
                />
            </ProjectErrorBoundary>
        </BoardHost>
    );
};

export default ProjectBoardTab;

const BoardHost = styled.div`
    display: flex;
    flex: 1;
    flex-direction: column;
    min-height: 0;
    overflow: auto;
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
