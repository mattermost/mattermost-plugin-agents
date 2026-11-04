// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {shallowEqual, useSelector} from 'react-redux';

import {GlobalState} from '@mattermost/types/store';

import {getHTMLArtifactsEnabled} from '@/selectors';
import {isValidId} from '@/utils/ids';

import {isHTMLArtifactFile} from './download';
import HTMLArtifactCard from './html_artifact_card';

interface FileInfoLike {
    id?: string;
    name?: string;
    extension?: string;
}

interface Props {
    postId: string;

    // post.metadata.files; files attach to the post when generation finishes.
    files: FileInfoLike[] | undefined | null;
}

// Renders an inline artifact card for each HTML attachment of an agent post.
const HTMLArtifacts = ({postId, files: metadataFiles}: Props) => {
    const enabled = useSelector(getHTMLArtifactsEnabled);

    // Prefer post.file_ids (authoritative after edits). metadata.files can lag
    // when a streamed UpdatePost swaps attachments; fileIdsByPostId only updates
    // when metadata.files is present on the received post.
    const channelId = useSelector<GlobalState, string>((state) => {
        const post = state.entities.posts.posts[postId];
        return post?.channel_id || '';
    });
    const files = useSelector<GlobalState, FileInfoLike[]>((state) => {
        const postIds = state.entities.posts?.posts?.[postId]?.file_ids ?? [];
        const byId = state.entities.files?.files ?? {};
        const fromPost = postIds.map((id: string) => byId[id]).filter(Boolean);
        if (fromPost.length > 0) {
            return fromPost;
        }
        if (Array.isArray(metadataFiles) && metadataFiles.length > 0) {
            return metadataFiles;
        }
        const ids = state.entities.files?.fileIdsByPostId?.[postId] ?? [];
        return ids.map((id: string) => byId[id]).filter(Boolean);
    }, shallowEqual);
    if (!enabled || files.length === 0) {
        return null;
    }
    const artifacts = files.filter((f) => isValidId(f?.id) && isHTMLArtifactFile(f));
    if (artifacts.length === 0) {
        return null;
    }
    return (
        <>
            {artifacts.map((file) => (
                <HTMLArtifactCard
                    key={file.id}
                    fileId={file.id as string}
                    fileName={file.name || `${file.id}.html`}
                    postId={postId}
                    channelId={channelId}
                />
            ))}
        </>
    );
};

export default HTMLArtifacts;
