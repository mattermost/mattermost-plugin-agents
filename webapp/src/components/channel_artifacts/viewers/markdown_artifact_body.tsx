// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useState} from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

import {Button} from '@mattermost/compass-ui/components/button';

import type {ChannelArtifactEntry} from '@/client';

import PostText from '../../post_text';
import {getChannelLibraryClient} from '../library_client';

type Props = {
    entry: Pick<ChannelArtifactEntry, 'id' | 'channel_id' | 'source_post_id'>;

    // The version to show; 0 is the current one.
    version: number;

    // Changes whenever the content of the shown version may have changed;
    // the text is fetched again.
    revision?: string;
};

type State = {status: 'loading'} | {status: 'error'} | {status: 'ready'; text: string};

// The source of a Markdown artifact, rendered by the host's Markdown pipeline
// under the same link policy as agent posts. It runs no script, unlike the
// sandboxed frame HTML artifacts use.
const MarkdownArtifactBody = ({entry, version, revision = ''}: Props) => {
    const [state, setState] = useState<State>({status: 'loading'});
    const [attempt, setAttempt] = useState(0);
    const retry = useCallback(() => setAttempt((n) => n + 1), []);

    useEffect(() => {
        let current = true;
        setState({status: 'loading'});
        getChannelLibraryClient().readText(entry.id, version).then(
            (text) => {
                if (current) {
                    setState({status: 'ready', text});
                }
            },
            () => {
                if (current) {
                    setState({status: 'error'});
                }
            },
        );
        return () => {
            current = false;
        };
    }, [entry.id, version, revision, attempt]);

    if (state.status === 'loading') {
        return (
            <Notice
                role='status'
                data-testid='artifact-markdown-loading'
            >
                <FormattedMessage defaultMessage='Loading…'/>
            </Notice>
        );
    }
    if (state.status === 'error') {
        return (
            <Notice
                role='alert'
                data-testid='artifact-markdown-error'
            >
                <FormattedMessage defaultMessage='This document could not be loaded. The message or file it came from may have been deleted.'/>
                <Button
                    emphasis='tertiary'
                    size='x-small'
                    onClick={retry}
                    data-testid='artifact-markdown-retry'
                >
                    <FormattedMessage defaultMessage='Retry'/>
                </Button>
            </Notice>
        );
    }
    return (
        <Document data-testid='artifact-markdown-body'>
            <PostText
                message={state.text}
                channelID={entry.channel_id}
                postID={entry.source_post_id}
            />
        </Document>
    );
};

export default MarkdownArtifactBody;

const Document = styled.div`
    min-width: 0;
    padding: var(--spacing-xs, 8px) var(--spacing-xxs, 4px);
    overflow-wrap: anywhere;
`;

const Notice = styled.div`
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--spacing-xs, 8px);
    padding: var(--spacing-xl, 24px) var(--spacing-xs, 8px);
    color: rgba(var(--center-channel-color-rgb), 0.72);
    font-size: 14px;
    line-height: 20px;
    text-align: center;
`;
