// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

// The chat bubble outline used for chats in the Agents sidebar. It is not in the installed
// compass-icons yet, so the glyph is kept here (24px grid, like the compass icons).
const MessageOutlineIcon = () => (
    <svg
        xmlns='http://www.w3.org/2000/svg'
        viewBox='0 0 24 24'
        fill='currentColor'
    >
        <path d='M20 2H4C2.9 2 2 2.9 2 4V22L6 18H20C21.1 18 22 17.1 22 16V4C22 2.9 21.1 2 20 2M20 16H5.2L4 17.2V4H20V16Z'/>
    </svg>
);

export default MessageOutlineIcon;
