// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';

export const PanelContainer = styled.div`
	display: flex;
	flex-direction: column;
	padding: 32px;
	border: 1px solid #ccc;
	background: white;
	border-radius: 4px;
	box-shadow: 0px 2px 3px 0px rgba(0, 0, 0, 0.08);
`;

const PanelHeader = styled.div`
	display: flex;
	flex-direction: column;
	gap: 4px;
	padding-bottom: 24px;
`;

const PanelTitle = styled.div`
	font-size: 16px;
	font-weight: 600;
`;

const PanelSubtitle = styled.div`
	color: rgba(63, 67, 80, 0.72);
	font-size: 14px;
	font-weight: 400;
`;

export const PanelFooterText = styled(PanelSubtitle)`
	margin-top: 20px;
`;

type PanelProps = {
    title: React.ReactNode
    subtitle: string
    children: React.ReactNode
    testId?: string
}

const Panel = (props: PanelProps) => {
    return (
        <PanelContainer data-testid={props.testId}>
            <PanelHeader>
                <PanelTitle>{props.title}</PanelTitle>
                <PanelSubtitle>{props.subtitle}</PanelSubtitle>
            </PanelHeader>
            <div>
                {props.children}
            </div>
        </PanelContainer>
    );
};

export default Panel;
