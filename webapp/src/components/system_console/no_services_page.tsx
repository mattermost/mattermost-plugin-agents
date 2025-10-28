// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {PlusIcon} from '@mattermost/compass-icons/components';
import React from 'react';
import styled from 'styled-components';

import {PrimaryButton} from 'src/components/assets/buttons';
import SparklesGraphic from 'src/components/assets/sparkles_graphic';

import {PanelContainer} from './panel';

type Props = {
    onAddServicePressed: () => void;
};

const NoServicesPage = (props: Props) => {
    return (
        <StyledPanelContainer>
            <SparklesGraphic/>
            <Title>{'No AI services added yet'}</Title>
            <Subtitle>{'To get started with Agents, add an AI service'}</Subtitle>
            <PrimaryButton onClick={props.onAddServicePressed}>
                <StyledPlusIcon/>
                {'Add an AI Service'}
            </PrimaryButton>
        </StyledPanelContainer>
    );
};

const StyledPlusIcon = styled(PlusIcon)`
	margin-right: 8px;
	width: 18px;
	height: 18px;
`;

const StyledPanelContainer = styled(PanelContainer)`
	display: flex;
	flex-direction: column;
	align-items: center;
	gap: 16px;
	padding-bottom: 56px;
`;

const Title = styled.div`
	font-size: 20px;
	font-weight: 600;
	font-family: Metropolis;
	line-height: 28px;
`;

const Subtitle = styled.div`
	font-size: 14px;
	font-weight: 400;
	line-height: 20px;
`;

export default NoServicesPage;
