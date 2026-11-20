// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';

import {navigateTo} from './project_links';

type Props = {
    path: string;
    children: React.ReactNode;
    testId?: string;
};

// A link inside the app: a relative path goes through the host router, anything else opens normally.
const ProjectLink = ({path, children, testId}: Props) => {
    const isAppPath = path.startsWith('/');
    return (
        <Anchor
            href={path}
            data-testid={testId}
            {...(isAppPath ? {} : {target: '_blank', rel: 'noopener noreferrer'})}
            onClick={(e) => {
                if (isAppPath && !e.metaKey && !e.ctrlKey && !e.shiftKey) {
                    e.preventDefault();
                    navigateTo(path);
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
