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

    // A streamed post gains file_ids through a websocket edit that carries no
    // metadata.files; core then loads the infos into entities.files.
    const storeFiles = useSelector<GlobalState, FileInfoLike[]>((state) => {
        const ids = state.entities.files?.fileIdsByPostId?.[postId] ?? [];
        return ids.map((id) => state.entities.files.files[id]).filter(Boolean);
    }, shallowEqual);
    const files = Array.isArray(metadataFiles) && metadataFiles.length > 0 ? metadataFiles : storeFiles;
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
                />
            ))}
        </>
    );
};

export default HTMLArtifacts;
