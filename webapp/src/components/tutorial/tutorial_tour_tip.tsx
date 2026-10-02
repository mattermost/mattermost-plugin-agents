// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useState} from 'react';
import ReactDOM from 'react-dom';
import Tippy from '@tippyjs/react';
import styled, {createGlobalStyle} from 'styled-components';

import {TourPoint, TourPointPointerPosition} from '@mattermost/compass-ui/components/tour-point';

import PulsatingDot from './pulsating_dot';
import {useTourManager, useMeasurePunchouts, useShowTutorialStep} from './hooks';

const rootPortal = document.getElementById('root-portal');

const TippyStyles = createGlobalStyle`
    .tour-tip-tippy {
        background: transparent;
        box-shadow: none;

        .tippy-content {
            padding: 0;
        }
    }
`;

type Placement =
    | 'top' | 'bottom' | 'left' | 'right'
    | 'top-start' | 'top-end' | 'bottom-start' | 'bottom-end'
    | 'left-start' | 'left-end' | 'right-start' | 'right-end';

const TourOverlay = styled.div`
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 9998;
    background: rgba(0, 0, 0, 0.5);
`;

const DotContainer = styled.div<{$placement: Placement; $translateX: number; $translateY: number}>`
    position: absolute;
    z-index: 9999;
    transform: translate(${(props) => props.$translateX}px, ${(props) => props.$translateY}px);
`;

// TourPoint draws its pointer at a fixed spot on the card edge, so it only
// lines up with the dot when the placement is centered on that axis.
const pointerPositionFor = (placement: Placement): TourPointPointerPosition => {
    const [side, alignment] = placement.split('-');
    switch (side) {
    case 'left':
        return 'right-center';
    case 'right':
        return 'left-center';
    case 'top':
        if (alignment === 'start') {
            return 'bottom-left';
        }
        return alignment === 'end' ? 'bottom-right' : 'bottom-center';
    default:
        if (alignment === 'start') {
            return 'top-left';
        }
        return alignment === 'end' ? 'top-right' : 'top-center';
    }
};

type Props = {
    title: string;
    screen: React.ReactNode;
    step: number;
    tutorialCategory: string;
    placement?: Placement;
    pulsatingDotPlacement?: Placement;
    pulsatingDotTranslate?: {x: number; y: number};
    width?: number;
    offset?: [number, number];
    onFinish?: () => void;
};

const TutorialTourTip: React.FC<Props> = ({
    title,
    screen,
    tutorialCategory,
    placement = 'left',
    pulsatingDotPlacement = 'left',
    pulsatingDotTranslate = {x: 0, y: 0},
    width = 352,
    offset = [0, 12],
    onFinish,
}) => {
    // Held in state rather than a ref so Tippy re-renders once the dot mounts.
    const [triggerElement, setTriggerElement] = useState<HTMLDivElement | null>(null);
    const {show, handleOpen, handleDismiss} = useTourManager(
        tutorialCategory,
        onFinish,
    );

    const content = (
        <TourPoint
            title={title}
            pointerPosition={pointerPositionFor(placement)}
            showPulsingDot={false}
            onClose={handleDismiss}
        >
            {screen}
        </TourPoint>
    );

    useEffect(() => {
        const handleEscape = (event: KeyboardEvent) => {
            if (event.key !== 'Escape') {
                return;
            }

            event.preventDefault();
            event.stopPropagation();
            handleDismiss();
        };

        if (show) {
            document.addEventListener('keydown', handleEscape, true);
        }

        return () => {
            document.removeEventListener('keydown', handleEscape, true);
        };
    }, [show, handleDismiss]);

    return (
        <>
            <TippyStyles/>
            <DotContainer
                ref={setTriggerElement}
                data-testid='agents-tour-dot'
                onClick={handleOpen}
                $placement={pulsatingDotPlacement}
                $translateX={pulsatingDotTranslate.x}
                $translateY={pulsatingDotTranslate.y}
            >
                <PulsatingDot onClick={handleOpen}/>
            </DotContainer>

            {show && rootPortal && ReactDOM.createPortal(
                <TourOverlay
                    data-testid='agents-tour-overlay'
                    onClick={handleDismiss}
                />,
                rootPortal,
            )}

            {show && rootPortal && (
                <Tippy
                    visible={true}
                    content={content}
                    animation='scale-subtle'
                    duration={[250, 150]}
                    maxWidth={width}
                    zIndex={9999}
                    reference={triggerElement}
                    interactive={true}
                    appendTo={rootPortal}
                    onClickOutside={() => {
                        handleDismiss();
                    }}
                    offset={offset}
                    placement={placement}
                    arrow={false}
                    className='tour-tip-tippy'
                />
            )}
        </>
    );
};

export default TutorialTourTip;
export {useMeasurePunchouts, useShowTutorialStep};
