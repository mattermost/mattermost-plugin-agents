// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {render} from '@testing-library/react';

import ChatActionsMenuItems from './chat_actions_menu_items';

// The menu item's destructive look is compass's; what matters here is which items ask for it.
const mockItems: Array<{label: React.ReactNode; destructive?: boolean}> = [];
jest.mock('@/components/dot_menu', () => ({
    DropdownMenuItem: (props: {label: React.ReactNode; destructive?: boolean}) => {
        mockItems.push({label: props.label, destructive: props.destructive});
        return null;
    },
}));

jest.mock('react-intl', () => ({
    useIntl: () => ({formatMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage}),
}));

describe('ChatActionsMenuItems', () => {
    beforeEach(() => {
        mockItems.length = 0;
    });

    // The sidebar's chat rows and the RHS chat history share these items.
    test('Archive chat is destructive and Rename chat is not', () => {
        render(
            <ChatActionsMenuItems
                onRename={jest.fn()}
                onArchive={jest.fn()}
            />,
        );
        expect(mockItems).toEqual([
            {label: 'Rename chat', destructive: undefined}, // eslint-disable-line no-undefined
            {label: 'Archive chat', destructive: true},
        ]);
    });
});
