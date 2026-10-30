// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useEffect, useRef, useState} from 'react';
import {useSelector} from 'react-redux';

import type {FileInfo} from '@mattermost/types/files';
import {GlobalState} from '@mattermost/types/store';

import {FileUpload, FileUploadAbortedError, uploadFile} from '@/client';

// The host composer's per-post cap (Constants.MAX_UPLOAD_FILES); the server
// config does not expose it.
export const MAX_FILES_PER_POST = 10;

// 'too_large' never reaches the server: it is over the server's MaxFileSize.
export type AttachmentStatus = 'uploading' | 'done' | 'failed' | 'too_large';

export interface ComposerAttachment {
    clientId: string;
    file: File;
    status: AttachmentStatus;

    // 0-100 while uploading.
    progress: number;
    fileInfo?: FileInfo;
}

// A host-style client id (26 lowercase alphanumerics), echoed back by the
// server so an upload response maps to its file.
const newClientId = () => {
    let id = '';
    while (id.length < 26) {
        id += Math.random().toString(36).slice(2);
    }
    return id.slice(0, 26);
};

const EMPTY_CONFIG: Partial<Record<string, string>> = {};

const counts = (a: ComposerAttachment) => a.status !== 'too_large';

// File attachments for one composer: uploads each file to the channel as soon
// as it is added (picker, drop or paste), tracks progress, and lets the user
// cancel, remove or retry. Files over the server's MaxFileSize are kept as an
// error entry rather than dropped; files past the per-post cap are refused
// with `limitReached`. Uploads need the channel id, so nothing can be attached
// until it is known, nor when the server disables file attachments.
export const useComposerAttachments = (channelId: string) => {
    const config = useSelector<GlobalState, Partial<Record<string, string>>>((state) => state.entities.general?.config ?? EMPTY_CONFIG);
    const parsedMax = parseInt(config.MaxFileSize ?? '', 10);
    const maxFileSize = Number.isFinite(parsedMax) && parsedMax > 0 ? parsedMax : 0;
    const enabled = config.EnableFileAttachments !== 'false';
    const canAttach = enabled && Boolean(channelId);

    const [attachments, setAttachments] = useState<ComposerAttachment[]>([]);
    const [limitReached, setLimitReached] = useState(false);

    // The list's source of truth, so two adds in one tick count each other.
    const list = useRef<ComposerAttachment[]>([]);
    const uploads = useRef(new Map<string, FileUpload>());

    const mutate = useCallback((fn: (prev: ComposerAttachment[]) => ComposerAttachment[]) => {
        list.current = fn(list.current);
        setAttachments(list.current);
    }, []);

    const patch = useCallback((clientId: string, change: Partial<ComposerAttachment>) => {
        mutate((prev) => prev.map((a) => (a.clientId === clientId ? {...a, ...change} : a)));
    }, [mutate]);

    const start = useCallback((clientId: string, file: File) => {
        const upload = uploadFile(file, channelId, clientId, (progress) => {
            if (uploads.current.get(clientId) === upload) {
                patch(clientId, {progress});
            }
        });
        uploads.current.set(clientId, upload);
        upload.promise.then((fileInfo) => {
            if (uploads.current.get(clientId) !== upload) {
                return;
            }
            uploads.current.delete(clientId);
            patch(clientId, {status: 'done', progress: 100, fileInfo});
        }).catch((error: unknown) => {
            // A cancelled or superseded upload has already left the list.
            if (error instanceof FileUploadAbortedError || uploads.current.get(clientId) !== upload) {
                return;
            }
            uploads.current.delete(clientId);
            console.error('Failed to upload agent chat attachment:', error); // eslint-disable-line no-console
            patch(clientId, {status: 'failed', progress: 0});
        });
    }, [channelId, patch]);

    // Cancel whatever is still uploading when the composer goes away.
    useEffect(() => () => {
        const inFlight = Array.from(uploads.current.values());
        uploads.current.clear();
        inFlight.forEach((upload) => upload.abort());
    }, []);

    const addFiles = useCallback((files: File[]) => {
        if (!canAttach || files.length === 0) {
            return;
        }
        let room = MAX_FILES_PER_POST - list.current.filter(counts).length;
        let overflow = false;
        const added: ComposerAttachment[] = [];
        for (const file of files) {
            if (maxFileSize && file.size > maxFileSize) {
                added.push({clientId: newClientId(), file, status: 'too_large', progress: 0});
            } else if (room > 0) {
                room -= 1;
                added.push({clientId: newClientId(), file, status: 'uploading', progress: 0});
            } else {
                overflow = true;
            }
        }
        setLimitReached(overflow);
        mutate((prev) => [...prev, ...added]);
        added.filter((a) => a.status === 'uploading').forEach((a) => start(a.clientId, a.file));
    }, [canAttach, maxFileSize, mutate, start]);

    // Removing an uploading file cancels its upload.
    const remove = useCallback((clientId: string) => {
        const upload = uploads.current.get(clientId);
        uploads.current.delete(clientId);
        upload?.abort();
        setLimitReached(false);
        mutate((prev) => prev.filter((a) => a.clientId !== clientId));
    }, [mutate]);

    const retry = useCallback((clientId: string) => {
        const attachment = list.current.find((a) => a.clientId === clientId);
        if (!attachment || attachment.status !== 'failed' || !canAttach) {
            return;
        }
        patch(clientId, {status: 'uploading', progress: 0});
        start(clientId, attachment.file);
    }, [canAttach, patch, start]);

    // After a send: every remaining file went out with the post.
    const clear = useCallback(() => {
        setLimitReached(false);
        mutate(() => []);
    }, [mutate]);

    // Refuses another file once the post is full (the "+" uses this before opening the picker).
    const hasRoom = useCallback(() => {
        const room = list.current.filter(counts).length < MAX_FILES_PER_POST;
        setLimitReached(!room);
        return room;
    }, []);

    const fileIds = attachments.flatMap((a) => (a.status === 'done' && a.fileInfo ? [a.fileInfo.id] : []));

    return {
        attachments,
        canAttach,
        maxFileSize,
        limitReached,
        fileIds,
        uploading: attachments.some((a) => a.status === 'uploading'),
        hasErrors: attachments.some((a) => a.status === 'failed' || a.status === 'too_large'),
        addFiles,
        remove,
        retry,
        clear,
        hasRoom,
    };
};
