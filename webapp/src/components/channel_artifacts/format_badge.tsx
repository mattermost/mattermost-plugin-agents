// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';

import {Tag} from '@mattermost/compass-ui/components/tag';

import type {ChannelArtifactEntry} from '@/client';

import {entryFormat} from './library_client';

// A label for an artifact's format, so a list tells HTML apps from documents.
const FormatBadge = ({entry}: {entry: Pick<ChannelArtifactEntry, 'id' | 'format'>}) => {
    const format = entryFormat(entry);
    return (
        <Tag
            size='x-small'
            type='default'
            data-testid={`channel-artifact-format-${entry.id}`}
            data-format={format}
            label={format === 'markdown' ? <FormattedMessage defaultMessage='Markdown'/> : <FormattedMessage defaultMessage='HTML'/>}
        />
    );
};

export default FormatBadge;
