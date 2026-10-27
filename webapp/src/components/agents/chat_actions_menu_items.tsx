// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useIntl} from 'react-intl';
import ArchiveOutlineIcon from '@mattermost/compass-icons/components/archive-outline';
import PencilOutlineIcon from '@mattermost/compass-icons/components/pencil-outline';

import {DropdownMenuItem} from '@/components/dot_menu';

interface ChatActionsMenuItemsProps {
    onRename: () => void;
    onArchive: () => void;
}

// The actions of a chat in a list: the Agents sidebar's chat rows and the RHS chat history open
// the same menu.
const ChatActionsMenuItems = ({onRename, onArchive}: ChatActionsMenuItemsProps) => {
    const intl = useIntl();
    return (
        <>
            <DropdownMenuItem
                label={intl.formatMessage({defaultMessage: 'Rename chat'})}
                icon={<PencilOutlineIcon/>}
                onClick={onRename}
            />
            <DropdownMenuItem
                label={intl.formatMessage({defaultMessage: 'Archive chat'})}
                icon={<ArchiveOutlineIcon/>}
                onClick={onArchive}
            />
        </>
    );
};

export default ChatActionsMenuItems;
