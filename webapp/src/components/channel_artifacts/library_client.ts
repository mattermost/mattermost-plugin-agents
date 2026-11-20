// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {
    channelArtifactDownloadURL,
    createChannelArtifact,
    deleteChannelArtifact,
    deleteChannelArtifactVersion,
    getChannelArtifact,
    getChannelArtifactContent,
    listChannelArtifactVersions,
    listChannelArtifacts,
    patchChannelArtifact,
    rollbackChannelArtifact,
    type ChannelArtifactEntry,
    type ChannelArtifactFormat,
    type ChannelArtifactList,
    type ChannelArtifactVersionRecord,
} from '@/client';

export type ChannelLibraryWrite = {
    channelId: string;

    // The file to register: an HTML or Markdown attachment of an agent's post
    // in the channel. postId is optional; the file's own post is used.
    fileId: string;
    postId?: string;
    title?: string;

    // Adds a version to this artifact instead of creating one.
    replaceId?: string;

    // Defaults to the replaced artifact's format, or the file's.
    format?: ChannelArtifactFormat;
};

/**
 * What the webapp needs from a channel's artifact library. Everything that
 * lists, opens or changes artifacts goes through this interface, so a
 * different artifacts backend can replace the plugin's own by calling
 * setChannelLibraryClient. Failures reject with a ClientError whose
 * status_code follows HTTP: 404 not found, 403 forbidden, 409 limit reached
 * or (message `duplicate:<id>`) identical content, 410 source gone, 413 too
 * large, 422 ineligible file.
 */
export interface ChannelLibraryClient {
    list(channelId: string): Promise<ChannelArtifactList>;
    get(id: string): Promise<ChannelArtifactEntry>;

    // Retained versions, newest first.
    versions(id: string): Promise<ChannelArtifactVersionRecord[]>;

    // Source text of a text format (Markdown); version 0 or omitted is current.
    readText(id: string, version?: number): Promise<string>;
    write(request: ChannelLibraryWrite): Promise<ChannelArtifactEntry>;

    // Removes the entry and its versions; the original message and file stay.
    remove(id: string): Promise<void>;
    rename(id: string, title: string): Promise<ChannelArtifactEntry>;
    rollback(id: string, version: number): Promise<ChannelArtifactEntry>;
    removeVersion(id: string, version: number): Promise<ChannelArtifactEntry>;
    downloadURL(id: string, version?: number): string;
}

// Each method resolves the plugin's client functions when it is called.
export const pluginChannelLibraryClient: ChannelLibraryClient = {
    list: (channelId) => listChannelArtifacts(channelId),
    get: (id) => getChannelArtifact(id),
    versions: async (id) => {
        const resp = await listChannelArtifactVersions(id);
        return (resp.versions || []).slice().sort((a, b) => b.version - a.version);
    },
    readText: (id, version) => getChannelArtifactContent(id, version),
    write: ({channelId, fileId, postId = '', title, replaceId, format}) => createChannelArtifact({
        channel_id: channelId,
        post_id: postId,
        file_id: fileId,
        ...(title ? {title} : {}),
        ...(replaceId ? {replace_id: replaceId} : {}),
        ...(format ? {format} : {}),
    }),
    remove: (id) => deleteChannelArtifact(id),
    rename: (id, title) => patchChannelArtifact(id, {title}),
    rollback: (id, version) => rollbackChannelArtifact(id, version),
    removeVersion: (id, version) => deleteChannelArtifactVersion(id, version),
    downloadURL: (id, version) => channelArtifactDownloadURL(id, version),
};

let current: ChannelLibraryClient = pluginChannelLibraryClient;

export function getChannelLibraryClient(): ChannelLibraryClient {
    return current;
}

// Installs another backend, and returns the one it replaced.
export function setChannelLibraryClient(client: ChannelLibraryClient): ChannelLibraryClient {
    const previous = current;
    current = client;
    return previous;
}

export function entryFormat(entry: Pick<ChannelArtifactEntry, 'format'>): ChannelArtifactFormat {
    return entry.format ?? 'html';
}
