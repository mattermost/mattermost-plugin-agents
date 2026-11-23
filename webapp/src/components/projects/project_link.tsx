// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';

import {navigateTo, toAppPath} from './project_links';

type Props = {
    path: string;
    children: React.ReactNode;
    testId?: string;
};

// A link inside the app goes through the host router, a link to this server included; anything
// else opens in a new tab.
const ProjectLink = ({path: link, children, testId}: Props) => {
    const path = toAppPath(link);
    const isAppPath = path.startsWith('/') && !path.startsWith('//');
    return (
        <Anchor
            href={path}
            data-testid={testId}
            {...(isAppPath ? {} : {target: '_blank', rel: 'noopener noreferrer'})}
            onClick={(e) => {
                if (isAppPath && !e.metaKey && !e.ctrlKey && !e.shiftKey && navigateTo(path)) {
                    e.preventDefault();
                }
            }}
        >
            {children}
        </Anchor>
    );
};

export default ProjectLink;

const Anchor = styled.a`
    font-weight: 600;
`;
