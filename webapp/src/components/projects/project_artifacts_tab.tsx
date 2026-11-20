// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import {useSelector} from 'react-redux';
import styled from 'styled-components';

import type {GlobalState} from '@mattermost/types/store';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {Tag} from '@mattermost/compass-ui/components/tag';
import {FileCodeOutlineIcon, FileTextOutlineIcon} from '@mattermost/compass-icons/components';

import type {ChannelArtifactEntry} from '@/client';
import {getChannelArtifactsEnabled} from '@/selectors';
import type {Project} from '@/types/projects';

import FormatBadge from '../channel_artifacts/format_badge';
import {useChannelLibrary} from '../channel_artifacts/library';
import {ArtifactViewer, openArtifact} from '../channel_artifacts/viewers/artifact_viewer';

// The charter and the wrap-up come first; the rest keep the library's order.
function pinnedFirst(entries: ChannelArtifactEntry[], project: Project): ChannelArtifactEntry[] {
    const rank = (entry: ChannelArtifactEntry) => {
        if (entry.id === project.charter_artifact_id) {
            return 0;
        }
        return entry.id === project.wrap_up_artifact_id ? 1 : 2;
    };
    return entries.map((entry, index) => ({entry, index})).
        sort((a, b) => (rank(a.entry) - rank(b.entry)) || (a.index - b.index)).
        map(({entry}) => entry);
}

const ProjectArtifactsTab = ({project}: {project: Project}) => {
    const enabled = useSelector((state: GlobalState) => getChannelArtifactsEnabled(state as never));
    const library = useChannelLibrary(project.channel_id);
    const [selectedId, setSelectedId] = useState<string | null>(null);
    const [poppedOut, setPoppedOut] = useState(false);

    const entries = useMemo(() => pinnedFirst(library.entries, project), [library.entries, project]);
    const selected = entries.find((entry) => entry.id === selectedId) ?? null;

    if (!enabled) {
        return (
            <Message data-testid='project-artifacts-disabled'>
                <FormattedMessage defaultMessage='Channel artifacts are turned off. Ask an admin to enable HTML artifacts and Channel artifacts in the System Console.'/>
            </Message>
        );
    }
    if (library.status === 'loading') {
        return <Message><FormattedMessage defaultMessage='Loading…'/></Message>;
    }
    if (library.status === 'error' || library.status === 'unsupported') {
        return (
            <Message data-testid='project-artifacts-error'>
                <FormattedMessage defaultMessage='Could not load the project artifacts.'/>
            </Message>
        );
    }

    if (selected && poppedOut) {
        return openArtifact(selected, {
            presentation: 'fullscreen',
            canEdit: library.canEdit,
            canDelete: library.canDelete,
            startedByClick: true,
            onClose: () => setPoppedOut(false),
        });
    }

    if (selected) {
        return (
            <ViewerHost data-testid='project-artifact-viewer'>
                <ArtifactViewer
                    key={selected.id}
                    entry={selected}
                    presentation='inline'
                    canEdit={library.canEdit}
                    canDelete={library.canDelete}
                    startedByClick={true}
                    onClose={() => setSelectedId(null)}
                    onPopOut={() => setPoppedOut(true)}
                />
            </ViewerHost>
        );
    }

    if (entries.length === 0) {
        return (
            <Message data-testid='project-artifacts-empty'>
                <FormattedMessage defaultMessage='No artifacts yet. The charter and wrap-up the agent writes appear here.'/>
            </Message>
        );
    }

    return (
        <List data-testid='project-artifacts-list'>
            {entries.map((entry) => (
                <Row
                    key={entry.id}
                    type='button'
                    onClick={() => setSelectedId(entry.id)}
                    data-testid={`project-artifact-${entry.id}`}
                >
                    <Icon
                        size='16'
                        glyph={entry.format === 'markdown' ? <FileTextOutlineIcon/> : <FileCodeOutlineIcon/>}
                    />
                    <Meta>
                        <Title>{entry.title}</Title>
                        <Sub>{new Date(entry.update_at || entry.create_at).toLocaleString()}</Sub>
                    </Meta>
                    {entry.id === project.charter_artifact_id && (
                        <Tag
                            type='info'
                            label={<FormattedMessage defaultMessage='Charter'/>}
                        />
                    )}
                    {entry.id === project.wrap_up_artifact_id && (
                        <Tag
                            type='info'
                            label={<FormattedMessage defaultMessage='Wrap-up'/>}
                        />
                    )}
                    <FormatBadge entry={entry}/>
                </Row>
            ))}
        </List>
    );
};

export default ProjectArtifactsTab;

const ViewerHost = styled.div`
    display: flex;
    flex: 1;
    flex-direction: column;
    min-height: 0;
    padding: 0 var(--spacing-l, 16px) var(--spacing-m, 12px);
`;

const List = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs, 8px);
    padding: var(--spacing-m, 12px) var(--spacing-l, 16px);
    overflow: auto;
`;

const Row = styled.button`
    display: flex;
    align-items: center;
    gap: var(--spacing-xs, 8px);
    padding: 10px var(--spacing-s, 12px);
    border: 1px solid rgba(var(--center-channel-color-rgb), 0.12);
    border-radius: 8px;
    background: var(--center-channel-bg);
    color: var(--center-channel-color);
    cursor: pointer;
    text-align: left;

    &:hover {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

const Meta = styled.div`
    flex: 1;
    min-width: 0;
`;

const Title = styled.div`
    overflow: hidden;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const Sub = styled.div`
    color: rgba(var(--center-channel-color-rgb), 0.64);
    font-size: 12px;
    line-height: 16px;
`;

const Message = styled.div`
    padding: var(--spacing-xl, 24px) var(--spacing-m, 16px);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    line-height: 20px;
    text-align: center;
`;
