// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import styled from 'styled-components';

import {useBotlist} from '@/bots';

import TutorialTourTip, {useMeasurePunchouts, useShowTutorialStep} from './tutorial_tour_tip';
import {AgentsTutorialSteps, TutorialTourCategories} from './tours';

const AGENTS_ICON_ID = 'app-bar-icon-mattermost-ai';

const TourContainer = styled.div`
    position: fixed;
    z-index: 9999;
    pointer-events: auto;
`;

const AgentsTour: React.FC = () => {
    const {bots} = useBotlist();
    const [mounted, setMounted] = useState(false);
    const [dismissed, setDismissed] = useState(false);

    const showStep = useShowTutorialStep(
        AgentsTutorialSteps.AgentsIcon,
        TutorialTourCategories.AGENTS_TOUR,
        true,
    );

    const targetPunchout = useMeasurePunchouts(
        [AGENTS_ICON_ID],
        [mounted],
        {y: 0, height: 0, x: 0, width: 0},
    );

    useEffect(() => {
        const timer = setTimeout(() => setMounted(true), 500);
        return () => clearTimeout(timer);
    }, []);

    if (!bots || bots.length === 0) {
        return null;
    }

    if (!showStep || dismissed) {
        return null;
    }

    if (!targetPunchout) {
        return null;
    }

    const iconX = parseFloat(targetPunchout.x);
    const iconY = parseFloat(targetPunchout.y);
    const iconHeight = parseFloat(targetPunchout.height);

    if (!iconX || !iconY) {
        return null;
    }

    const dotLeft = iconX;
    const dotTop = (iconY + (iconHeight / 2)) - 6;

    return (
        <TourContainer
            style={{
                top: `${dotTop}px`,
                left: `${dotLeft}px`,
            }}
        >
            <TutorialTourTip
                title='Agents are ready to help'
                screen='AI agents now live here. Chat one-on-one to ask questions, explore ideas, draft messages and more with AI models approved by your organization.'
                tutorialCategory={TutorialTourCategories.AGENTS_TOUR}
                step={AgentsTutorialSteps.AgentsIcon}
                placement='left-start'
                pulsatingDotPlacement='left'
                width={352}
                offset={[-5, 12]}
                onFinish={() => setDismissed(true)}
            />
        </TourContainer>
    );
};

export default AgentsTour;
