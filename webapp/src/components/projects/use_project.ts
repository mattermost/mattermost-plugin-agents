// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';
import {useDispatch, useSelector} from 'react-redux';
import {ClientError} from '@mattermost/client';

import type {GlobalState} from '@mattermost/types/store';

import {getProject as fetchProject, getProjectChecklist} from '@/client';
import {getProject, getProjectChannelsEnabled, isKnownNotProject} from '@/selectors';
import type {Project, ProjectChecklist} from '@/types/projects';

import {markNotProject, receiveProject} from './project_state';

export type ProjectLookup =
    {status: 'disabled' | 'loading' | 'missing' | 'error'; project?: undefined; reload: () => void} |
    {status: 'ready'; project: Project; reload: () => void};

// The project of a channel. The store answers when it can; otherwise the server is asked once, and
// a 404 is remembered so channels that are not projects are not asked about again.
export function useProject(channelId: string): ProjectLookup {
    const dispatch = useDispatch();
    const enabled = useSelector((state: GlobalState) => getProjectChannelsEnabled(state as never));
    const project = useSelector((state: GlobalState) => getProject(state as never, channelId));
    const missing = useSelector((state: GlobalState) => isKnownNotProject(state as never, channelId));
    const [failedFor, setFailedFor] = useState('');
    const requested = useRef('');
    const latest = useRef(channelId);
    latest.current = channelId;

    const load = useCallback(() => {
        if (!channelId) {
            return;
        }
        requested.current = channelId;
        fetchProject(channelId).then(
            (loaded) => {
                dispatch(receiveProject(loaded));
            },
            (error: unknown) => {
                if (error instanceof ClientError && error.status_code === 404) {
                    dispatch(markNotProject(channelId));
                    return;
                }
                if (latest.current === channelId) {
                    setFailedFor(channelId);
                }
            },
        );
    }, [channelId, dispatch]);

    useEffect(() => {
        if (!enabled || !channelId || project || missing || requested.current === channelId) {
            return;
        }
        load();
    }, [enabled, channelId, project, missing, load]);

    const reload = useCallback(() => {
        setFailedFor('');
        load();
    }, [load]);

    if (!enabled) {
        return {status: 'disabled', reload};
    }
    if (project) {
        return {status: 'ready', project, reload};
    }
    if (missing) {
        return {status: 'missing', reload};
    }
    return {status: failedFor === channelId ? 'error' : 'loading', reload};
}

export type ChecklistLookup = {status: 'none' | 'loading' | 'ready' | 'unavailable'; checklist?: ProjectChecklist};

type ChecklistResult = {channelId: string; runId: string; checklist: ProjectChecklist | null};

// The linked checklist's progress. It is read again whenever the project changes, which is when
// the server broadcasts a link or status update; the previous numbers stay up meanwhile. A failure
// or an unreachable Playbooks leaves only the link.
export function useProjectChecklist(project: Project): ChecklistLookup {
    const [result, setResult] = useState<ChecklistResult | null>(null);
    const {channel_id: channelId, playbook_run_id: runId, update_at: updateAt} = project;

    useEffect(() => {
        if (!runId) {
            return undefined; // eslint-disable-line no-undefined
        }
        let current = true;
        getProjectChecklist(channelId).then(
            (checklist) => {
                if (current) {
                    setResult({channelId, runId, checklist});
                }
            },
            () => {
                if (current) {
                    setResult({channelId, runId, checklist: null});
                }
            },
        );
        return () => {
            current = false;
        };
    }, [channelId, runId, updateAt]);

    if (!runId) {
        return {status: 'none'};
    }
    if (!result || result.channelId !== channelId || result.runId !== runId) {
        return {status: 'loading'};
    }
    if (!result.checklist?.available) {
        return {status: 'unavailable'};
    }
    return {status: 'ready', checklist: result.checklist};
}
