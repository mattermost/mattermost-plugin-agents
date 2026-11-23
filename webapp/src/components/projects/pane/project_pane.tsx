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

import type {ProjectTab} from '@/types/projects';

import ProjectArtifactsTab from '../project_artifacts_tab';
import ProjectBoardTab from '../project_board_tab';
import ProjectHeader from '../project_header';
import {useProject} from '../use_project';

import {closeProjectPane, getProjectPaneTab, selectedProjectTab, setProjectPaneTab, visibleProjectTabs} from './pane_state';
import ProjectChecklistTab from './project_checklist_tab';
import ProjectSetupTab from './project_setup_tab';

type Props = {
    channelId: string;

    // The host's layout controls, shown in the pane's top bar.
    controls?: React.ReactNode;

    // The window is too narrow to show the channel beside the pane.
    showMessagesHint?: boolean;
};

const TAB_LABELS: Record<ProjectTab, React.ReactNode> = {
    setup: <FormattedMessage defaultMessage='Setup'/>,
    board: <FormattedMessage defaultMessage='Board'/>,
    checklist: <FormattedMessage defaultMessage='Checklist'/>,
    artifacts: <FormattedMessage defaultMessage='Artifacts'/>,
};

const ProjectPane = ({channelId, controls, showMessagesHint}: Props) => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const chosen = useSelector((state: GlobalState) => getProjectPaneTab(state as never, channelId));
    const title = useSelector((state: GlobalState) => state.entities.channels.channels[channelId]?.display_name ?? '');
    const lookup = useProject(channelId);

    const topBar = (
        <TopBar>
            <Title data-testid='project-pane-title'>
                {title || <FormattedMessage defaultMessage='Project'/>}
            </Title>
            {controls && <Controls>{controls}</Controls>}
        </TopBar>
    );

    let body: React.ReactNode;
    switch (lookup.status) {
    case 'disabled':
    case 'missing':
        body = (
            <Message data-testid='project-pane-not-project'>
                <FormattedMessage defaultMessage='This channel is not a project.'/>
            </Message>
        );
        break;
    case 'loading':
        body = (
            <Message data-testid='project-pane-loading'>
                <Spinner size='24'/>
            </Message>
        );
        break;
    case 'error':
        body = (
            <Message data-testid='project-pane-error'>
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
        break;
    default:
        break;
    }
    if (body || lookup.status !== 'ready') {
        return (
            <Container data-testid='project-pane'>
                {topBar}
                {body}
            </Container>
        );
    }

    const project = lookup.project;
    const tabs = visibleProjectTabs(project, chosen);
    const tab = selectedProjectTab(project, chosen);
    const select = (next: ProjectTab) => dispatch(setProjectPaneTab(channelId, next));

    let panel: React.ReactNode;
    switch (tab) {
    case 'setup':
        panel = <ProjectSetupTab project={project}/>;
        break;
    case 'board':
        panel = <ProjectBoardTab project={project}/>;
        break;
    case 'checklist':
        panel = <ProjectChecklistTab project={project}/>;
        break;
    default:
        panel = <ProjectArtifactsTab project={project}/>;
    }

    return (
        <Container data-testid='project-pane'>
            {topBar}
            {showMessagesHint && (
                <Hint data-testid='project-pane-narrow-hint'>
                    <FormattedMessage defaultMessage='The channel is hidden while the window is this narrow.'/>
                    <Button
                        size='small'
                        emphasis='tertiary'
                        onClick={() => dispatch(closeProjectPane(channelId))}
                    >
                        <FormattedMessage defaultMessage='Show messages'/>
                    </Button>
                </Hint>
            )}
            <ProjectHeader
                project={project}
                onOpenChecklist={() => select('checklist')}
            />
            <TabBar
                appearance='underlined'
                activeKey={tab}
                onChange={(key) => select(key as ProjectTab)}
                aria-label={intl.formatMessage({defaultMessage: 'Project views'})}
                tabs={tabs.map((key) => ({key, label: TAB_LABELS[key], buttonProps: {'data-testid': `project-tab-${key}`}}))}
            />
            {chosen && chosen !== tab && (
                <Hint data-testid='project-pane-missing-tab'>
                    {chosen === 'board' && <FormattedMessage defaultMessage='This project has no board yet. Ask its agent if you want one.'/>}
                    {chosen === 'checklist' && <FormattedMessage defaultMessage='This project has no checklist yet. Ask its agent if you want one.'/>}
                    {chosen === 'setup' && <FormattedMessage defaultMessage='This project has no setup thread.'/>}
                </Hint>
            )}
            <Panel
                key={`${project.channel_id}-${tab}`}
                data-testid='project-pane-panel'
            >
                {panel}
            </Panel>
        </Container>
    );
};

export default ProjectPane;

const Container = styled.div`
    display: flex;
    flex: 1;
    flex-direction: column;
    height: 100%;
    min-height: 0;
`;

const TopBar = styled.div`
    display: flex;
    flex: none;
    align-items: center;
    gap: var(--spacing-xs, 8px);
    min-height: 62px;
    padding: 0 var(--spacing-s, 8px) 0 var(--spacing-l, 16px);
    border-bottom: var(--border-light);
`;

const Title = styled.h2`
    flex: 1;
    min-width: 0;
    margin: 0;
    overflow: hidden;
    color: var(--center-channel-color);
    font-size: 16px;
    font-weight: 600;
    line-height: 24px;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const Controls = styled.div`
    display: flex;
    flex: none;
    align-items: center;
    gap: var(--spacing-xxs, 4px);
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

const Hint = styled.div`
    display: flex;
    flex: none;
    align-items: center;
    justify-content: space-between;
    gap: var(--spacing-s, 8px);
    padding: var(--spacing-xs, 4px) var(--spacing-l, 16px);
    background: rgba(var(--center-channel-color-rgb), 0.04);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 12px;
    line-height: 16px;
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
