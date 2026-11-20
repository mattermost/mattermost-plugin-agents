// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useLayoutEffect, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import ReactDOM from 'react-dom';
import styled, {keyframes} from 'styled-components';

import {TourPoint, TourPointPointerPosition} from '@mattermost/compass-ui/components/tour-point';

import PulsatingDot from './pulsating_dot';
import {useTourManager, useMeasurePunchouts, useShowTutorialStep} from './hooks';

const VIEWPORT_PADDING = 8;

const scaleIn = keyframes`
    from {
        opacity: 0;
        transform: scale(0.96);
    }

    to {
        opacity: 1;
        transform: scale(1);
    }
`;

export type Placement =
    | 'top' | 'bottom' | 'left' | 'right'
    | 'top-start' | 'top-end' | 'bottom-start' | 'bottom-end'
    | 'left-start' | 'left-end' | 'right-start' | 'right-end';

type RectLike = {
    top: number;
    left: number;
    right: number;
    bottom: number;
    width: number;
    height: number;
};

type SizeLike = {
    width: number;
    height: number;
};

type Side = 'top' | 'bottom' | 'left' | 'right';

const OPPOSITE_SIDE: Record<Side, Side> = {
    left: 'right',
    right: 'left',
    top: 'bottom',
    bottom: 'top',
};

function fitsOnSide(
    side: Side,
    triggerRect: RectLike,
    tipSize: SizeLike,
    distance: number,
    viewport: SizeLike,
): boolean {
    switch (side) {
    case 'left':
        return triggerRect.left - tipSize.width - distance >= VIEWPORT_PADDING;
    case 'right':
        return triggerRect.right + distance + tipSize.width <= viewport.width - VIEWPORT_PADDING;
    case 'top':
        return triggerRect.top - tipSize.height - distance >= VIEWPORT_PADDING;
    default:
        return triggerRect.bottom + distance + tipSize.height <= viewport.height - VIEWPORT_PADDING;
    }
}

function resolvePlacement(
    triggerRect: RectLike,
    tipSize: SizeLike,
    placement: Placement,
    distance: number,
    viewport: SizeLike,
): Placement {
    const [side, alignment] = placement.split('-') as [Side, string | undefined];
    const withAlignment = (nextSide: Side): Placement => (alignment ? `${nextSide}-${alignment}` as Placement : nextSide);

    if (fitsOnSide(side, triggerRect, tipSize, distance, viewport)) {
        return placement;
    }

    const opposite = OPPOSITE_SIDE[side];
    if (fitsOnSide(opposite, triggerRect, tipSize, distance, viewport)) {
        return withAlignment(opposite);
    }

    const fallbacks: Side[] = side === 'left' || side === 'right' ? ['bottom', 'top'] : ['right', 'left'];
    for (const candidate of fallbacks) {
        if (fitsOnSide(candidate, triggerRect, tipSize, distance, viewport)) {
            return candidate;
        }
    }

    return placement;
}

// Estimated card height used only for flip/fit when the TourPoint tip-origin
// host reports a 0×0 bounding box (compass-ui ≥ alpha.34).
const TIP_SIZE_FALLBACK_HEIGHT = 240;

export function computeAnchoredPosition(
    triggerRect: RectLike,
    tipSize: SizeLike,
    placement: Placement,
    offset: [number, number],
    viewport: SizeLike,
): {top: number; left: number; placement: Placement} {
    const [skidding, distance] = offset;
    const resolvedPlacement = resolvePlacement(triggerRect, tipSize, placement, distance, viewport);
    const [side, alignment] = resolvedPlacement.split('-');

    // TourPoint's root is a 0×0 tip origin — place that origin on the trigger
    // edge facing the tip. The card/pointer lay out around (0,0).
    let top = 0;
    let left = 0;

    if (side === 'left' || side === 'right') {
        left = side === 'left' ? triggerRect.left - distance : triggerRect.right + distance;
        if (alignment === 'start') {
            top = triggerRect.top + skidding;
        } else if (alignment === 'end') {
            top = triggerRect.bottom + skidding;
        } else {
            top = triggerRect.top + (triggerRect.height / 2) + skidding;
        }
    } else {
        top = side === 'top' ? triggerRect.top - distance : triggerRect.bottom + distance;
        if (alignment === 'start') {
            left = triggerRect.left + skidding;
        } else if (alignment === 'end') {
            left = triggerRect.right + skidding;
        } else {
            left = triggerRect.left + (triggerRect.width / 2) + skidding;
        }
    }

    return {
        top: Math.min(Math.max(VIEWPORT_PADDING, top), viewport.height - VIEWPORT_PADDING),
        left: Math.min(Math.max(VIEWPORT_PADDING, left), viewport.width - VIEWPORT_PADDING),
        placement: resolvedPlacement,
    };
}

const TourOverlay = styled.div`
    position: fixed;
    top: 0;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 9998;
    background: rgba(0, 0, 0, 0.5);
`;

const DotContainer = styled.button<{$placement: Placement; $translateX: number; $translateY: number}>`
    position: absolute;
    z-index: 9999;
    padding: 0;
    border: none;
    background: none;
    cursor: pointer;
    transform: translate(${(props) => props.$translateX}px, ${(props) => props.$translateY}px);
`;

const TipContainer = styled.div.attrs<{$top: number; $left: number; $maxWidth: number}>((props) => ({
    style: {
        top: `${props.$top}px`,
        left: `${props.$left}px`,
        maxWidth: `${props.$maxWidth}px`,
    },
}))<{$top: number; $left: number; $maxWidth: number}>`
    position: fixed;
    z-index: 9999;
`;

const TipContent = styled.div`
    animation: ${scaleIn} 250ms ease-out;
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
    const [triggerElement, setTriggerElement] = useState<HTMLButtonElement | null>(null);
    const tipRef = useRef<HTMLDivElement | null>(null);
    const overlayRef = useRef<HTMLDivElement | null>(null);
    const [position, setPosition] = useState<{top: number; left: number; placement: Placement}>({
        top: 0,
        left: 0,
        placement,
    });
    const intl = useIntl();
    const {show, handleOpen, handleDismiss} = useTourManager(
        tutorialCategory,
        onFinish,
    );

    const offsetSkidding = offset[0];
    const offsetDistance = offset[1];

    const updatePosition = useCallback(() => {
        if (!triggerElement || !tipRef.current) {
            return;
        }
        const triggerRect = triggerElement.getBoundingClientRect();
        const tipRect = tipRef.current.getBoundingClientRect();
        // Tip-origin hosts report 0×0; keep a card-sized estimate for side flips.
        const next = computeAnchoredPosition(
            triggerRect,
            {
                width: tipRect.width || width,
                height: tipRect.height || TIP_SIZE_FALLBACK_HEIGHT,
            },
            placement,
            [offsetSkidding, offsetDistance],
            {width: window.innerWidth, height: window.innerHeight},
        );
        setPosition((prev) => (
            prev.top === next.top && prev.left === next.left && prev.placement === next.placement ? prev : next
        ));
    }, [triggerElement, placement, offsetSkidding, offsetDistance, width]);

    // Re-measure after rerenders so parent-driven trigger moves (e.g. useMeasurePunchouts) update the portal.
    useLayoutEffect(() => {
        if (show) {
            updatePosition();
        }
    });

    useLayoutEffect(() => {
        if (!show) {
            return () => { /* empty */ };
        }
        window.addEventListener('resize', updatePosition);
        window.addEventListener('scroll', updatePosition, true);

        let observer: ResizeObserver | undefined;
        if (typeof ResizeObserver !== 'undefined') {
            observer = new ResizeObserver(updatePosition);
            if (tipRef.current) {
                observer.observe(tipRef.current);
            }
            if (triggerElement) {
                observer.observe(triggerElement);
            }
        }

        return () => {
            window.removeEventListener('resize', updatePosition);
            window.removeEventListener('scroll', updatePosition, true);
            observer?.disconnect();
        };
    }, [show, triggerElement, updatePosition]);

    useEffect(() => {
        if (!show) {
            return () => { /* empty */ };
        }

        const handleEscape = (event: KeyboardEvent) => {
            if (event.key !== 'Escape') {
                return;
            }

            event.preventDefault();
            event.stopPropagation();
            handleDismiss();
        };

        const handleOutsideClick = (event: MouseEvent) => {
            const target = event.target as Node | null;
            if (!target) {
                return;
            }
            if (tipRef.current?.contains(target) || triggerElement?.contains(target) || overlayRef.current?.contains(target)) {
                return;
            }
            handleDismiss();
        };

        document.addEventListener('keydown', handleEscape, true);
        document.addEventListener('click', handleOutsideClick);

        return () => {
            document.removeEventListener('keydown', handleEscape, true);
            document.removeEventListener('click', handleOutsideClick);
        };
    }, [show, handleDismiss, triggerElement]);

    const rootPortal = document.getElementById('root-portal');

    return (
        <>
            <DotContainer
                ref={setTriggerElement}
                type='button'
                aria-label={intl.formatMessage({defaultMessage: 'Show tip'})}
                data-testid='agents-tour-dot'
                onClick={handleOpen}
                $placement={pulsatingDotPlacement}
                $translateX={pulsatingDotTranslate.x}
                $translateY={pulsatingDotTranslate.y}
            >
                <PulsatingDot/>
            </DotContainer>

            {show && rootPortal && ReactDOM.createPortal(
                // eslint-disable-next-line agents/click-events-have-key-events, agents/no-static-element-interactions -- backdrop; Escape and the tip's Close button dismiss the tour
                <TourOverlay
                    ref={overlayRef}
                    data-testid='agents-tour-overlay'
                    onClick={(event) => {
                        event.stopPropagation();
                        handleDismiss();
                    }}
                />,
                rootPortal,
            )}

            {show && rootPortal && ReactDOM.createPortal(
                <TipContainer
                    ref={tipRef}
                    className='tour-tip-tippy'
                    $top={position.top}
                    $left={position.left}
                    $maxWidth={width}
                >
                    <TipContent>
                        <TourPoint
                            title={title}
                            pointerPosition={pointerPositionFor(position.placement)}
                            showPulsingDot={false}
                            onClose={handleDismiss}
                            closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
                            closeButtonProps={{'data-testid': 'agents-tour-close'}}
                        >
                            {screen}
                        </TourPoint>
                    </TipContent>
                </TipContainer>,
                rootPortal,
            )}
        </>
    );
};

export default TutorialTourTip;
export {useMeasurePunchouts, useShowTutorialStep};
