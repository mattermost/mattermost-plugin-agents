// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage, useIntl} from 'react-intl';
import {useDispatch, useSelector} from 'react-redux';
import styled from 'styled-components';

import type {GlobalState} from '@mattermost/types/store';

import {Button} from '@mattermost/compass-ui/components/button';
import {Spinner} from '@mattermost/compass-ui/components/spinner';
import {Tabs} from '@mattermost/compass-ui/components/tabs';

import {getProjectRHSTab} from '@/selectors';
import type {ProjectTab} from '@/types/projects';

import ProjectArtifactsTab from './project_artifacts_tab';
import ProjectBoardTab from './project_board_tab';
import ProjectHeader from './project_header';
import {setProjectRHSTab} from './project_state';
import {useProject} from './use_project';

export const ProjectRHSTitle = () => (
    <FormattedMessage defaultMessage='Project'/>
);

const ProjectRHS = () => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const channelId = useSelector<GlobalState, string>((state) => state.entities.channels.currentChannelId);
    const tab = useSelector((state: GlobalState) => getProjectRHSTab(state as never));
    const lookup = useProject(channelId);

    switch (lookup.status) {
    case 'disabled':
    case 'missing':
        return (
            <Message data-testid='project-rhs-not-project'>
                <FormattedMessage defaultMessage='This channel is not a project.'/>
            </Message>
        );
    case 'loading':
        return (
            <Message data-testid='project-rhs-loading'>
                <Spinner size='24'/>
            </Message>
        );
    case 'error':
        return (
            <Message data-testid='project-rhs-error'>
                <FormattedMessage defaultMessage='Could not load the project.'/>
                <Button
                    size='small'
                    emphasis='tertiary'
                    onClick={lookup.reload}
                >
                    <FormattedMessage defaultMessage='Try again'/>
                </Button>
            </Message>
        );
    default:
        break;
    }

    const project = lookup.project;
    return (
        <Container data-testid='project-rhs'>
            <ProjectHeader project={project}/>
            <TabBar
                appearance='underlined'
                activeKey={tab}
                onChange={(key) => dispatch(setProjectRHSTab(key as ProjectTab))}
                aria-label={intl.formatMessage({defaultMessage: 'Project views'})}
                tabs={[
                    {key: 'board', label: <FormattedMessage defaultMessage='Board'/>, buttonProps: {'data-testid': 'project-tab-board'}},
                    {key: 'artifacts', label: <FormattedMessage defaultMessage='Artifacts'/>, buttonProps: {'data-testid': 'project-tab-artifacts'}},
                ]}
            />
            <Panel data-testid='project-rhs-panel'>
                {tab === 'artifacts' ? (
                    <ProjectArtifactsTab
                        key={project.channel_id}
                        project={project}
                    />
                ) : (
                    <ProjectBoardTab
                        key={project.channel_id}
                        project={project}
                    />
                )}
            </Panel>
        </Container>
    );
};

export default ProjectRHS;

const Container = styled.div`
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
`;

const TabBar = styled(Tabs)`
    flex: none;
    padding: 0 var(--spacing-l, 16px);
`;

const Panel = styled.div`
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
