// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useRef} from 'react';
import ReactDOM from 'react-dom';
import Tippy from '@tippyjs/react';
import styled, {createGlobalStyle} from 'styled-components';

import PulsatingDot from './pulsating_dot';
import {useTourManager, useMeasurePunchouts, useShowTutorialStep} from './hooks';

const rootPortal = document.getElementById('root-portal');

const TippyStyles = createGlobalStyle`
    .tour-tip-tippy {
        .tippy-content {
            padding: 0;
        }

        .tippy-arrow {
            width: 12px;
            height: 24px;

            &::before {
                content: '';
                position: absolute;
                border-style: solid;
                border-color: transparent;
            }
        }

        &[data-placement^='left'] > .tippy-arrow {
            right: -6px;

            &::before {
                right: 0;
                border-width: 12px 0 12px 12px;
                border-left-color: #1C58D9;
                transform-origin: center left;
            }
        }

        &[data-placement^='right'] > .tippy-arrow {
            left: -6px;

            &::before {
                left: 0;
                border-width: 12px 12px 12px 0;
                border-right-color: #1C58D9;
                transform-origin: center right;
            }
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

const TourTipContent = styled.div`
    padding: 0;
`;

const TourTipHeader = styled.div`
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
    padding: 22px 24px 12px 24px;
`;

const TourTipTitle = styled.h4`
    margin: 0;
    padding-right: 24px;
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    font-weight: 600;
    line-height: 20px;
    color: white;
`;

const TourTipCloseButton = styled.button`
    position: absolute;
    top: 16px;
    right: 16px;
    background: none;
    border: none;
    cursor: pointer;
    padding: 8px;
    display: flex;
    align-items: center;
    justify-content: center;
    color: rgba(255, 255, 255, 0.56);
    border-radius: 4px;

    &:hover {
        color: white;
        background: rgba(255, 255, 255, 0.08);
    }

    i {
        font-size: 18px;
    }
`;

const TourTipBody = styled.div`
    font-family: 'Open Sans', sans-serif;
    font-size: 14px;
    font-weight: 400;
    line-height: 20px;
    color: white;
    padding: 0 24px 24px 24px;
`;

const TourTipContainer = styled.div`
    background: #1C58D9;
    border-radius: 4px;
    overflow: visible;
    position: relative;
    box-shadow: 0px 12px 32px rgba(0, 0, 0, 0.12);

    .tippy-arrow {
        color: #1C58D9;
    }
`;

type Props = {
    title: React.ReactNode;
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
    const triggerRef = useRef<HTMLDivElement>(null);
    const {show, setShow, handleOpen, handleDismiss} = useTourManager(
        tutorialCategory,
        onFinish,
    );

    const content = (
        <TourTipContainer>
            <TourTipContent>
                <TourTipHeader>
                    <TourTipTitle>{title}</TourTipTitle>
                    <TourTipCloseButton
                        data-testid='agents-tour-close'
                        onClick={handleDismiss}
                    >
                        <i className='icon icon-close'/>
                    </TourTipCloseButton>
                </TourTipHeader>
                <TourTipBody>{screen}</TourTipBody>
            </TourTipContent>
        </TourTipContainer>
    );

    return (
        <>
            <TippyStyles/>
            <DotContainer
                ref={triggerRef}
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
                    reference={triggerRef}
                    interactive={true}
                    appendTo={rootPortal}
                    onClickOutside={() => setShow(false)}
                    offset={offset}
                    placement={placement}
                    arrow={true}
                    className='tour-tip-tippy'
                />
            )}
        </>
    );
};

export default TutorialTourTip;
export {useMeasurePunchouts, useShowTutorialStep};
