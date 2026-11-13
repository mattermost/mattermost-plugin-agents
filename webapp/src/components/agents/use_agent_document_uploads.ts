// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {useCallback, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';

import {uploadAgentDocument} from '@/client';
import {AgentDocument, MaxAgentDocuments} from '@/types/agents';

import {documentFileError} from './agent_documents';

export type DocumentUploadItem = {
    key: string;
    name: string;
    status: 'uploading' | 'error';
    error?: string;
}

function uploadErrorMessage(e: unknown, fallback: string): string {
    const message = (e as {message?: unknown})?.message;
    return typeof message === 'string' && message.trim() ? message.trim() : fallback;
}

/**
 * Tracks in-flight document uploads for the agent editor. Lives in the editor (not the
 * Configuration tab) so uploads that finish after switching tabs still land in the draft.
 * Files upload one at a time so documents are appended in the order they were chosen.
 */
export function useAgentDocumentUploads(
    documents: AgentDocument[],
    onUploaded: (document: AgentDocument) => void,
) {
    const intl = useIntl();
    const intlRef = useRef(intl);
    intlRef.current = intl;
    const documentsRef = useRef(documents);
    documentsRef.current = documents;
    const onUploadedRef = useRef(onUploaded);
    onUploadedRef.current = onUploaded;

    const [uploads, setUploads] = useState<DocumentUploadItem[]>([]);
    const pendingRef = useRef(0);
    const nextKeyRef = useRef(0);

    const upload = useCallback((files: File[]) => {
        const currentIntl = intlRef.current;
        let freeSlots = MaxAgentDocuments - documentsRef.current.length - pendingRef.current;
        const items: DocumentUploadItem[] = [];
        const accepted: Array<{key: string; file: File}> = [];

        for (const file of files) {
            nextKeyRef.current += 1;
            const key = `upload-${nextKeyRef.current}`;
            const error = documentFileError(currentIntl, file);
            if (error) {
                items.push({key, name: file.name, status: 'error', error});
            } else if (freeSlots <= 0) {
                items.push({
                    key,
                    name: file.name,
                    status: 'error',
                    error: currentIntl.formatMessage(
                        {defaultMessage: 'An agent can have at most {max} documents.'},
                        {max: MaxAgentDocuments},
                    ),
                });
            } else {
                freeSlots--;
                items.push({key, name: file.name, status: 'uploading'});
                accepted.push({key, file});
            }
        }
        if (items.length === 0) {
            return;
        }

        pendingRef.current += accepted.length;
        setUploads((prev) => [...prev, ...items]);

        (async () => {
            for (const {key, file} of accepted) {
                try {
                    // eslint-disable-next-line no-await-in-loop
                    const doc = await uploadAgentDocument(file);

                    // The server may reuse a blob for identical bytes; an agent cannot reference one twice.
                    if (documentsRef.current.some((existing) => existing.id === doc.id)) {
                        const error = intlRef.current.formatMessage({defaultMessage: 'This file is already attached to the agent.'});
                        setUploads((prev) => prev.map((item) => (item.key === key ? {...item, status: 'error', error} : item)));
                    } else {
                        onUploadedRef.current(doc);
                        setUploads((prev) => prev.filter((item) => item.key !== key));
                    }
                } catch (e) {
                    const error = uploadErrorMessage(e, intlRef.current.formatMessage({defaultMessage: 'Failed to upload this file. Please try again.'}));
                    setUploads((prev) => prev.map((item) => (item.key === key ? {...item, status: 'error', error} : item)));
                } finally {
                    pendingRef.current -= 1;
                }
            }
        })();
    }, []);

    const dismiss = useCallback((key: string) => {
        setUploads((prev) => prev.filter((item) => item.key !== key));
    }, []);

    const uploading = useMemo(() => uploads.some((item) => item.status === 'uploading'), [uploads]);

    return {uploads, uploading, upload, dismiss};
}
