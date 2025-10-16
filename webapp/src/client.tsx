// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Client4 as Client4Class, ClientError} from '@mattermost/client';
import {ChannelWithTeamData} from '@mattermost/types/channels';

import {NotPagedTeamSearchOpts, Team} from '@mattermost/types/teams';

import manifest from './manifest';

const Client4 = new Client4Class();

export function setSiteURL(siteURL: string) {
    Client4.setUrl(siteURL);
}

function baseRoute(): string {
    return `${Client4.url}/plugins/${manifest.id}`;
}

function postRoute(postid: string): string {
    return `${baseRoute()}/post/${postid}`;
}

function channelRoute(channelid: string): string {
    return `${baseRoute()}/channel/${channelid}`;
}

export async function doReaction(postid: string) {
    const url = `${postRoute(postid)}/react`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doThreadAnalysis(postid: string, analysisType: string, botUsername: string) {
    const url = `${postRoute(postid)}/analyze?botUsername=${botUsername}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({
            analysis_type: analysisType,
        }),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doTranscribe(postid: string, fileID: string) {
    const url = `${postRoute(postid)}/transcribe/file/${fileID}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doSummarizeTranscription(postid: string) {
    const url = `${postRoute(postid)}/summarize_transcription`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doStopGenerating(postid: string) {
    const url = `${postRoute(postid)}/stop`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doRegenerate(postid: string) {
    const url = `${postRoute(postid)}/regenerate`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doToolCall(postid: string, toolIDs: string[]) {
    const url = `${postRoute(postid)}/tool_call`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({
            accepted_tool_ids: toolIDs,
        }),
    }));

    if (response.ok) {
        return;
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function doPostbackSummary(postid: string) {
    const url = `${postRoute(postid)}/postback_summary`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function viewMyChannel(channelID: string) {
    return Client4.viewMyChannel(channelID);
}

export async function getAIDirectChannel(currentUserId: string) {
    const botUser = await Client4.getUserByUsername('ai');
    const dm = await Client4.createDirectChannel([currentUserId, botUser.id]);
    return dm.id;
}

export async function getBotDirectChannel(currentUserId: string, botUserID: string) {
    const dm = await Client4.createDirectChannel([currentUserId, botUserID]);
    return dm.id;
}

export async function getAIThreads() {
    const url = `${baseRoute()}/ai_threads`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function getAIBots() {
    const url = `${baseRoute()}/ai_bots`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function createPost(post: any) {
    const created = await Client4.createPost(post);
    return created;
}

export async function updateRead(userId: string, teamId: string, selectedPostId: string, timestamp: number) {
    Client4.updateThreadReadForUser(userId, teamId, selectedPostId, timestamp);
}

export function getProfilePictureUrl(userId: string, lastIconUpdate: number) {
    return Client4.getProfilePictureUrl(userId, lastIconUpdate);
}

export async function getBotProfilePictureUrl(username: string) {
    const user = await Client4.getUserByUsername(username);
    if (!user || user.id === '') {
        return '';
    }
    return getProfilePictureUrl(user.id, user.last_picture_update);
}

export async function doRunSearch(query: string, teamId: string, channelId: string, botUsername?: string) {
    const url = `${baseRoute()}/search/run${botUsername ? `?botUsername=${botUsername}` : ''}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({
            query,
            teamId,
            channelId,
        }),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function setUserProfilePictureByUsername(username: string, file: File) {
    const user = await Client4.getUserByUsername(username);
    if (!user || user.id === '') {
        return;
    }
    await setUserProfilePicture(user.id, file);
}

export async function setUserProfilePicture(userId: string, file: File) {
    await Client4.uploadProfileImage(userId, file);
}

export async function getAutocompleteAllUsers(name: string) {
    return Client4.autocompleteUsers(name, '', '');
}

export async function getProfilesByIds(userIds: string[]) {
    if (userIds.length === 0) {
        return [];
    }
    return Client4.getProfilesByIds(userIds);
}

export async function searchAllChannels(term: string): Promise<ChannelWithTeamData[]> {
    return Client4.searchAllChannels(term, {
        nonAdminSearch: false,
        public: true,
        private: true,
        include_deleted: false,
        deleted: false,
    }) as Promise<ChannelWithTeamData[]>; // With these paremeters we should always get ChannelWithTeamData[]
}

export async function getChannelById(channelId: string): Promise<ChannelWithTeamData> {
    const channel = await Client4.getChannel(channelId);
    const team = await Client4.getTeam(channel.team_id);
    return {
        ...channel,
        team_name: team.display_name,
        team_display_name: team.display_name,
        team_update_at: team.update_at,
    };
}

export async function getTeamsByIds(teamIds: string[]) {
    if (teamIds.length === 0) {
        return [];
    }
    return Promise.all(teamIds.map((id) => Client4.getTeam(id)));
}

export async function searchTeams(term: string): Promise<Team[]> {
    const opts: NotPagedTeamSearchOpts = {};

    // Types are messed up
    return Client4.searchTeams(term, opts) as unknown as Promise<Team[]>;
}

export function getTeamIconUrl(teamId: string, lastTeamIconUpdate: number) {
    return Client4.getTeamIconUrl(teamId, lastTeamIconUpdate);
}

export function getPost(postId: string) {
    return Client4.getPost(postId);
}

export async function doReindexPosts() {
    const url = `${baseRoute()}/admin/reindex`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function getReindexStatus() {
    const url = `${baseRoute()}/admin/reindex/status`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function cancelReindex() {
    const url = `${baseRoute()}/admin/reindex/cancel`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}

export async function getMCPTools() {
    const url = `${baseRoute()}/admin/mcp/tools`;
    const response = await fetch(url, Client4.getOptions({
        method: 'GET',
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}
export async function getChannelInterval(
    channelID: string,
    startTime: number,
    endTime: number,
    presetPrompt: string,
    prompt?: string,
    botUsername?: string,
) {
    const url = `${channelRoute(channelID)}/interval${botUsername ? `?botUsername=${botUsername}` : ''}`;
    const response = await fetch(url, Client4.getOptions({
        method: 'POST',
        body: JSON.stringify({
            start_time: startTime,
            end_time: endTime,
            preset_prompt: presetPrompt,
            prompt: prompt || '',
        }),
    }));

    if (response.ok) {
        return response.json();
    }

    throw new ClientError(Client4.url, {
        message: '',
        status_code: response.status,
        url,
    });
}
