// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {act, fireEvent, render, screen, waitFor, within} from '@testing-library/react';
import {Provider} from 'react-redux';
import {combineReducers, createStore} from 'redux';
import {ClientError} from '@mattermost/client';

import {
    endProject,
    getChannelMemberProfiles,
    getProfilesByIds,
    getProject,
    getProjectChecklist,
    listChannelArtifacts,
    transferProjectLead,
    updateProjectSchedule,
    type ChannelArtifactEntry,
} from '@/client';
import manifest from '@/manifest';
import {pluginReducer} from '@/redux';
import type {Project, ProjectChecklist} from '@/types/projects';

import ProjectRHS from './project_rhs';

jest.mock('@/client', () => ({
    endProject: jest.fn(),
    getChannelMemberProfiles: jest.fn(),
    getProfilesByIds: jest.fn(),
    getProject: jest.fn(),
    getProjectChecklist: jest.fn(),
    listChannelArtifacts: jest.fn(),
    transferProjectLead: jest.fn(),
    updateProjectSchedule: jest.fn(),
}));

jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    const format = (descriptor: {defaultMessage: string}, values?: Record<string, unknown>) => descriptor.defaultMessage.replace(/\{(\w+)\}/g, (_match, key) => String(values?.[key] ?? ''));
    return {
        ...actual,
        useIntl: () => ({formatMessage: format}),
        FormattedMessage: ({defaultMessage, values}: {defaultMessage: string; values?: Record<string, unknown>}) => <>{format({defaultMessage}, values)}</>,
    };
});

jest.mock('@mattermost/compass-ui/components/select', () => ({
    Select: ({label, options, value, onChange, disabled}: {label: string; options: Array<{value: string; label: string}>; value: string; onChange: (value: string) => void; disabled?: boolean}) => (
        <select
            aria-label={label}
            value={value}
            disabled={disabled}
            onChange={(e) => onChange(e.target.value)}
        >
            <option value=''>{'-'}</option>
            {options.map((option) => (
                <option
                    key={option.value}
                    value={option.value}
                >
                    {option.label}
                </option>
            ))}
        </select>
    ),
}));

jest.mock('../channel_artifacts/viewers/artifact_viewer', () => ({
    ArtifactViewer: (props: {entry: {id: string; format?: string}; presentation: string; startedByClick: boolean; canEdit: boolean; canDelete: boolean; onClose: () => void; onPopOut?: () => void}) => (
        <div
            data-testid='mock-artifact-viewer'
            data-presentation={props.presentation}
            data-format={props.entry.format}
            data-started-by-click={String(props.startedByClick)}
            data-can-edit={String(props.canEdit)}
            data-can-delete={String(props.canDelete)}
        >
            <button onClick={props.onClose}>{'back'}</button>
            {props.onPopOut && <button onClick={props.onPopOut}>{'pop out'}</button>}
        </div>
    ),
    openArtifact: (entry: {id: string}, options: {presentation: string; onClose: () => void}) => (
        <div
            data-testid='mock-artifact-fullscreen'
            data-presentation={options.presentation}
            data-id={entry.id}
        >
            <button onClick={options.onClose}>{'close fullscreen'}</button>
        </div>
    ),
}));

const CHANNEL_ID = 'projectchannel01234567890';
const BOARD_CHANNEL_ID = 'boardchannel0123456789012';
const CHARTER_ID = 'charterartifact012345678';

function makeProject(overrides: Partial<Project> = {}): Project {
    return {
        channel_id: CHANNEL_ID,
        team_id: 'team1',
        agent_id: 'agent1',
        agent_bot_user_id: 'bot1',
        agent_display_name: 'Matty',
        agent_username: 'matty',
        state: 'active',
        lead_user_id: 'lead1',
        reply_mode: 'ambient',
        setup_root_post_id: '',
        board_channel_id: BOARD_CHANNEL_ID,
        playbook_run_id: 'run1',
        charter_artifact_id: CHARTER_ID,
        wrap_up_artifact_id: '',
        status_note: 'Login revamp is on track.',
        status_update_at: 1,
        checkin_cron: '',
        checkin_timezone: '',
        next_checkin_at: 0,
        last_checkin_at: 0,
        last_checkin_post_id: '',
        created_by: 'lead1',
        created_from: 'channel_modal',
        create_at: 1,
        update_at: 10,
        ended_by: '',
        ended_at: 0,
        ended_without_wrap_up: false,
        ...overrides,
    };
}

function makeEntry(id: string, overrides: Partial<ChannelArtifactEntry> = {}): ChannelArtifactEntry {
    return {
        id,
        channel_id: CHANNEL_ID,
        title: `Artifact ${id}`,
        version: 1,
        sha256: 'abc',
        size: 1,
        source_post_id: 'post',
        source_file_id: 'file',
        source_bot_user_id: 'bot1',
        source_agent_display_name: 'Matty',
        created_by: 'bot1',
        create_at: 1,
        updated_by: 'bot1',
        update_at: 1,
        available: true,
        ...overrides,
    } as ChannelArtifactEntry;
}

const checklist: ProjectChecklist = {available: true, run_id: 'run1', name: 'Launch', status: 'InProgress', completed_tasks: 7, total_tasks: 12, url: '/playbooks/runs/run1'};

function renderRHS({project = makeProject() as Project | null, projectChannelsEnabled = true, channelArtifactsEnabled = true} = {}) {
    const entities = {
        channels: {currentChannelId: CHANNEL_ID, channels: {[CHANNEL_ID]: {id: CHANNEL_ID, type: 'O'}}},
        users: {currentUserId: 'me', profiles: {lead1: {id: 'lead1', username: 'lena'}}},
        teams: {currentTeamId: 'team1', teams: {team1: {id: 'team1', name: 'engineering'}}},
    };
    const store = createStore(combineReducers({
        entities: (state: typeof entities = entities) => state,
        ['plugins-' + manifest.id]: pluginReducer,
    }));
    store.dispatch({type: 'SET_PROJECT_CHANNELS_ENABLED', projectChannelsEnabled});
    store.dispatch({type: 'SET_CHANNEL_ARTIFACTS_ENABLED', channelArtifactsEnabled});
    if (project) {
        store.dispatch({type: 'PROJECT_RECEIVED', project});
    }
    const view = render(
        <Provider store={store}>
            <ProjectRHS/>
        </Provider>,
    );
    return {store, ...view};
}

const ChecklistBoard = ({channelId, compact}: {channelId: string; compact?: boolean}) => (
    <div
        data-testid='boards-embed'
        data-channel-id={channelId}
        data-compact={String(compact)}
    />
);

beforeEach(() => {
    jest.resetAllMocks();
    (getProjectChecklist as jest.Mock).mockResolvedValue(checklist);
    (listChannelArtifacts as jest.Mock).mockResolvedValue({
        entries: [makeEntry('other'), makeEntry(CHARTER_ID, {title: 'Project charter', format: 'markdown'})],
        can_add: true,
        can_edit: true,
        can_delete: false,
        max_per_channel: 50,
    });
    (getProfilesByIds as jest.Mock).mockResolvedValue([]);
    delete window.MattermostBoardsV2;
});

afterEach(() => {
    delete window.MattermostBoardsV2;
});

describe('ProjectRHS header', () => {
    test('shows the agent, state, lead, status note and checklist progress', async () => {
        renderRHS();

        expect(screen.getByTestId('project-agent-name').textContent).toContain('Matty');
        expect(screen.getByTestId('project-state-badge').textContent).toContain('Active');
        expect(screen.getByTestId('project-lead').textContent).toContain('Lead: @lena');
        expect(screen.getByTestId('project-status-note').textContent).toContain('Login revamp is on track.');
        expect((await screen.findByTestId('project-checklist-count')).textContent).toContain('7/12 done');
        expect(screen.getByTestId('project-checklist-link').getAttribute('href')).toBe('/playbooks/runs/run1');
    });

    test.each([
        {name: 'Playbooks is unreachable', setup: () => (getProjectChecklist as jest.Mock).mockRejectedValue(new Error('down'))},
        {name: 'the server reports the run as unavailable', setup: () => (getProjectChecklist as jest.Mock).mockResolvedValue({...checklist, available: false})},
    ])('falls back to the checklist link when $name', async ({setup}) => {
        setup();
        renderRHS();

        const fallback = await screen.findByTestId('project-checklist-unavailable');
        expect(within(fallback).getByTestId('project-checklist-link').getAttribute('href')).toBe('/playbooks/runs/run1');
        expect(screen.queryByTestId('project-checklist-count')).toBeNull();
    });

    test('says so when no checklist is linked', () => {
        renderRHS({project: makeProject({playbook_run_id: ''})});

        expect(screen.getByTestId('project-checklist-none')).not.toBeNull();
        expect(getProjectChecklist).not.toHaveBeenCalled();
    });

    test('a pushed project update changes the state badge and the checklist is read again', async () => {
        const {store} = renderRHS();
        await screen.findByTestId('project-checklist-count');

        act(() => {
            store.dispatch({type: 'PROJECT_RECEIVED', project: makeProject({state: 'ended', update_at: 20, status_note: 'Wrapped up.'})});
        });

        expect(screen.getByTestId('project-state-badge').textContent).toContain('Ended');
        expect(screen.getByTestId('project-status-note').textContent).toContain('Wrapped up.');
        await waitFor(() => expect(getProjectChecklist).toHaveBeenCalledTimes(2));
    });
});

describe('ProjectRHS gating and lookup', () => {
    test('shows no project UI when project channels are off', () => {
        renderRHS({projectChannelsEnabled: false});

        expect(screen.getByTestId('project-rhs-not-project')).not.toBeNull();
        expect(screen.queryByTestId('project-rhs')).toBeNull();
        expect(getProject).not.toHaveBeenCalled();
    });

    test('asks the server for a project the store does not know, and shows it', async () => {
        (getProject as jest.Mock).mockResolvedValue(makeProject());
        renderRHS({project: null});

        expect(screen.getByTestId('project-rhs-loading')).not.toBeNull();
        expect(await screen.findByTestId('project-rhs')).not.toBeNull();
        expect(getProject).toHaveBeenCalledWith(CHANNEL_ID);
    });

    test('a 404 means the channel is not a project, and is not asked about again', async () => {
        (getProject as jest.Mock).mockRejectedValue(new ClientError('http://localhost', {message: 'not found', status_code: 404, url: 'u'}));
        renderRHS({project: null});

        expect(await screen.findByTestId('project-rhs-not-project')).not.toBeNull();
        expect(getProject).toHaveBeenCalledTimes(1);
    });

    test('another failure offers a retry', async () => {
        (getProject as jest.Mock).mockRejectedValueOnce(new Error('boom')).mockResolvedValueOnce(makeProject());
        renderRHS({project: null});

        fireEvent.click(await screen.findByText('Try again'));

        expect(await screen.findByTestId('project-rhs')).not.toBeNull();
    });
});

describe('ProjectRHS Board tab', () => {
    test('embeds the Boards v2 board of the project, compact', () => {
        window.MattermostBoardsV2 = {version: 1, EmbeddedBoard: ChecklistBoard};
        renderRHS();

        const embed = screen.getByTestId('boards-embed');
        expect(embed.getAttribute('data-channel-id')).toBe(BOARD_CHANNEL_ID);
        expect(embed.getAttribute('data-compact')).toBe('true');
    });

    test.each([
        {name: 'Boards v2 is not installed', boards: null},
        {name: 'Boards v2 is older than version 1', boards: {version: 0, EmbeddedBoard: ChecklistBoard}},
    ])('links to the board channel when $name', ({boards}) => {
        if (boards) {
            window.MattermostBoardsV2 = boards;
        }
        renderRHS();

        expect(screen.getByTestId('project-board-fallback')).not.toBeNull();
        expect(screen.queryByTestId('boards-embed')).toBeNull();
        expect(screen.getByTestId('project-board-link').getAttribute('href')).toBe(`/engineering/channels/${BOARD_CHANNEL_ID}`);
    });

    test('says no board is linked yet, even when Boards v2 is installed', () => {
        window.MattermostBoardsV2 = {version: 1, EmbeddedBoard: ChecklistBoard};
        renderRHS({project: makeProject({board_channel_id: ''})});

        expect(screen.getByTestId('project-board-empty').textContent).toContain('No board linked yet — ask your project agent to create one.');
        expect(screen.queryByTestId('boards-embed')).toBeNull();
    });

    test('a board that throws leaves the rest of the sidebar working', () => {
        const consoleError = jest.spyOn(console, 'error').mockImplementation(jest.fn());
        const Broken = () => {
            throw new Error('render failed');
        };
        window.MattermostBoardsV2 = {version: 1, EmbeddedBoard: Broken};
        renderRHS();

        expect(screen.getByTestId('project-board-fallback')).not.toBeNull();
        expect(screen.getByTestId('project-header')).not.toBeNull();
        consoleError.mockRestore();
    });
});

describe('ProjectRHS Artifacts tab', () => {
    const openArtifactsTab = async () => {
        fireEvent.click(screen.getByTestId('project-tab-artifacts'));
        return screen.findByTestId('project-artifacts-list');
    };

    test('lists the charter first and opens an artifact in the inline viewer', async () => {
        renderRHS();
        const list = await openArtifactsTab();

        const rows = within(list).getAllByRole('button');
        expect(rows[0].getAttribute('data-testid')).toBe(`project-artifact-${CHARTER_ID}`);
        expect(rows[0].textContent).toContain('Charter');
        expect(listChannelArtifacts).toHaveBeenCalledWith(CHANNEL_ID);

        fireEvent.click(rows[0]);

        const viewer = screen.getByTestId('mock-artifact-viewer');
        expect(viewer.getAttribute('data-presentation')).toBe('inline');
        expect(viewer.getAttribute('data-format')).toBe('markdown');
        expect(viewer.getAttribute('data-started-by-click')).toBe('true');
        expect(viewer.getAttribute('data-can-edit')).toBe('true');
        expect(viewer.getAttribute('data-can-delete')).toBe('false');
    });

    test('pops an artifact out to fullscreen and returns to the inline viewer', async () => {
        renderRHS();
        const list = await openArtifactsTab();
        fireEvent.click(within(list).getByTestId('project-artifact-other'));

        fireEvent.click(screen.getByText('pop out'));
        expect(screen.getByTestId('mock-artifact-fullscreen').getAttribute('data-presentation')).toBe('fullscreen');
        expect(screen.queryByTestId('mock-artifact-viewer')).toBeNull();

        fireEvent.click(screen.getByText('close fullscreen'));
        expect(screen.getByTestId('mock-artifact-viewer')).not.toBeNull();
    });

    test('Back returns to the list', async () => {
        renderRHS();
        const list = await openArtifactsTab();
        fireEvent.click(within(list).getByTestId('project-artifact-other'));

        fireEvent.click(screen.getByText('back'));

        expect(screen.getByTestId('project-artifacts-list')).not.toBeNull();
    });

    test('the tab is remembered when the sidebar is reopened', async () => {
        const {store, unmount} = renderRHS();
        await openArtifactsTab();
        unmount();

        render(
            <Provider store={store}>
                <ProjectRHS/>
            </Provider>,
        );

        expect(await screen.findByTestId('project-artifacts-list')).not.toBeNull();
    });

    test('says so when the project has no artifacts yet', async () => {
        (listChannelArtifacts as jest.Mock).mockResolvedValue({entries: [], can_add: true, can_edit: true, can_delete: true, max_per_channel: 50});
        renderRHS();
        fireEvent.click(screen.getByTestId('project-tab-artifacts'));

        expect(await screen.findByTestId('project-artifacts-empty')).not.toBeNull();
    });

    test('says so when channel artifacts are turned off', () => {
        renderRHS({channelArtifactsEnabled: false});
        fireEvent.click(screen.getByTestId('project-tab-artifacts'));

        expect(screen.getByTestId('project-artifacts-disabled')).not.toBeNull();
        expect(listChannelArtifacts).not.toHaveBeenCalled();
    });
});

describe('ProjectRHS actions', () => {
    test('End project asks for confirmation, then ends the project and shows the new state', async () => {
        (endProject as jest.Mock).mockResolvedValue(makeProject({state: 'ending', update_at: 30}));
        renderRHS();

        fireEvent.click(screen.getByTestId('project-action-end'));
        expect(endProject).not.toHaveBeenCalled();
        const dialog = await screen.findByRole('dialog');
        fireEvent.click(within(dialog).getByRole('button', {name: 'End project'}));

        await waitFor(() => expect(endProject).toHaveBeenCalledWith(CHANNEL_ID));
        await waitFor(() => expect(screen.getByTestId('project-state-badge').textContent).toContain('Ending'));
        expect((screen.getByTestId('project-action-end') as HTMLButtonElement).disabled).toBe(true);
    });

    test('cancelling the confirmation does not end the project', async () => {
        renderRHS();

        fireEvent.click(screen.getByTestId('project-action-end'));
        const dialog = await screen.findByRole('dialog');
        fireEvent.click(within(dialog).getByRole('button', {name: 'Cancel'}));

        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
        expect(endProject).not.toHaveBeenCalled();
    });

    test('shows the server\'s reason when ending fails and keeps the project active', async () => {
        (endProject as jest.Mock).mockRejectedValue(new Error('You do not have permission to archive this channel.'));
        renderRHS();

        fireEvent.click(screen.getByTestId('project-action-end'));
        const dialog = await screen.findByRole('dialog');
        fireEvent.click(within(dialog).getByRole('button', {name: 'End project'}));

        expect(await within(dialog).findByText('You do not have permission to archive this channel.')).not.toBeNull();
        expect(screen.getByTestId('project-state-badge').textContent).toContain('Active');
    });

    test('an ended project offers no actions', () => {
        renderRHS({project: makeProject({state: 'ended'})});

        expect((screen.getByTestId('project-action-end') as HTMLButtonElement).disabled).toBe(true);
        expect((screen.getByTestId('project-action-schedule') as HTMLButtonElement).disabled).toBe(true);
        expect((screen.getByTestId('project-action-transfer') as HTMLButtonElement).disabled).toBe(true);
    });

    test('Transfer lead offers eligible members only and sends the chosen one', async () => {
        const member = (id: string, extra: Record<string, unknown> = {}) => ({id, username: id, roles: 'system_user', is_bot: false, delete_at: 0, ...extra});
        (getChannelMemberProfiles as jest.Mock).mockResolvedValue([
            member('lead1'),
            member('newlead'),
            member('somebot', {is_bot: true}),
            member('aguest', {roles: 'system_guest'}),
            member('gone', {delete_at: 5}),
        ]);
        (transferProjectLead as jest.Mock).mockResolvedValue(makeProject({lead_user_id: 'newlead', update_at: 40}));
        renderRHS();

        fireEvent.click(screen.getByTestId('project-action-transfer'));
        const select = await screen.findByLabelText('New lead');
        await waitFor(() => expect(within(select).getAllByRole('option').map((o) => o.textContent)).toEqual(['-', '@newlead']));
        expect((screen.getByTestId('project-transfer-lead-confirm') as HTMLButtonElement).disabled).toBe(true);

        fireEvent.change(select, {target: {value: 'newlead'}});
        fireEvent.click(screen.getByTestId('project-transfer-lead-confirm'));

        await waitFor(() => expect(transferProjectLead).toHaveBeenCalledWith(CHANNEL_ID, 'newlead'));
        await waitFor(() => expect(screen.queryByTestId('project-transfer-lead-confirm')).toBeNull());
    });

    test('Check-in schedule sends the weekday cron with the time zone, and off clears it', async () => {
        (updateProjectSchedule as jest.Mock).mockResolvedValue(makeProject({checkin_cron: '30 10 * * 1-5', checkin_timezone: 'Europe/Berlin', update_at: 50}));
        renderRHS();

        fireEvent.click(screen.getByTestId('project-action-schedule'));
        fireEvent.change(await screen.findByLabelText('Check in'), {target: {value: 'weekdays'}});
        fireEvent.change(screen.getByLabelText('Time'), {target: {value: '10:30'}});
        fireEvent.change(screen.getByLabelText('Time zone'), {target: {value: 'Europe/Berlin'}});
        fireEvent.click(screen.getByTestId('project-schedule-save'));

        await waitFor(() => expect(updateProjectSchedule).toHaveBeenCalledWith(CHANNEL_ID, '30 10 * * 1-5', 'Europe/Berlin'));
        await waitFor(() => expect(screen.queryByTestId('project-schedule-save')).toBeNull());

        (updateProjectSchedule as jest.Mock).mockResolvedValue(makeProject({checkin_cron: '', update_at: 60}));
        fireEvent.click(screen.getByTestId('project-action-schedule'));
        expect(((await screen.findByLabelText('Check in')) as HTMLSelectElement).value).toBe('weekdays');
        fireEvent.change(screen.getByLabelText('Check in'), {target: {value: 'off'}});
        fireEvent.click(screen.getByTestId('project-schedule-save'));

        await waitFor(() => expect(updateProjectSchedule).toHaveBeenLastCalledWith(CHANNEL_ID, '', ''));
    });
});
