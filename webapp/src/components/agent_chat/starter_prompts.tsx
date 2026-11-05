// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState} from 'react';
import {useIntl} from 'react-intl';
import styled from 'styled-components';

import {Button} from '@mattermost/compass-ui/components/button';
import {PopoverMenu} from '@mattermost/compass-ui/components/popover-menu';

import {DropdownMenuItem} from '@/components/dot_menu';
import Dropdown from '@/components/dropdown';
import {handleMenuNavigation} from '@/components/menu/menu_keyboard';
import {useFocusMenuOnOpen} from '@/components/menu/use_focus_menu_on_open';
import {useMenuTransition} from '@/hooks/use_menu_transition';
import type {CustomPrompt} from '@/types';

import {fitStarterLines, singleLineFit, type StarterFit, type StarterFitInput} from './starter_prompt_layout';

// The starters take at most this many lines; whatever does not fit collapses into "+N".
const MAX_STARTER_LINES = 2;

const MEASURE_ATTRIBUTE = 'data-starter-measure';

// What measure() reads from the page; everything the fit needs except the line limit.
type Metrics = Omit<StarterFitInput, 'maxLines'>;

// Plain numbers and one array of them, so a field added to Metrics is compared without editing this.
const sameMetrics = (a: Metrics | null, b: Metrics) => a !== null && JSON.stringify(a) === JSON.stringify(b);

interface StarterPromptsProps {
    prompts: CustomPrompt[];
    disabled: boolean;
    onPick: (prompt: CustomPrompt) => void;
}

// The pinned prompts as buttons on at most MAX_STARTER_LINES lines, with the ones that do not fit
// behind a "+N" chip. The browser's own wrapping can't do this (it can't swap the last button for
// a chip), so each button is measured in a hidden row and the lines are laid out here.
const StarterPrompts = ({prompts, disabled, onPick}: StarterPromptsProps) => {
    const intl = useIntl();
    const groupRef = useRef<HTMLDivElement>(null);
    const measureRef = useRef<HTMLDivElement>(null);
    const [metrics, setMetrics] = useState<Metrics | null>(null);

    const measure = useCallback(() => {
        const group = groupRef.current;
        const row = measureRef.current;
        if (!group || !row) {
            return;
        }
        const width = (selector: string) => row.querySelector<HTMLElement>(selector)?.offsetWidth ?? 0;
        const next: Metrics = {
            widths: Array.from(row.querySelectorAll<HTMLElement>(`[${MEASURE_ATTRIBUTE}='item']`), (el) => el.offsetWidth),
            chipWidth: width(`[${MEASURE_ATTRIBUTE}='chip']`),
            gap: parseFloat(getComputedStyle(row).columnGap) || 0,
            available: group.clientWidth,
        };
        setMetrics((previous) => (sameMetrics(previous, next) ? previous : next));
    }, []);

    // Measure before paint so a changed list never shows a frame in the wrong layout.
    useLayoutEffect(() => {
        measure();
    }, [measure, prompts]);

    // The width changes with the panel; the buttons change with the font loading.
    useEffect(() => {
        const observer = typeof ResizeObserver === 'function' ? new ResizeObserver(measure) : null;
        if (groupRef.current) {
            observer?.observe(groupRef.current);
        }
        if (measureRef.current) {
            observer?.observe(measureRef.current);
        }
        return () => observer?.disconnect();
    }, [measure]);

    const fit = useMemo<StarterFit | null>(() => {
        if (!metrics || metrics.widths.length !== prompts.length) {
            return null;
        }
        if (metrics.available <= 0) {
            return singleLineFit(prompts.length);
        }
        return fitStarterLines({...metrics, maxLines: MAX_STARTER_LINES});
    }, [metrics, prompts]);

    const hidden = fit ? prompts.slice(fit.visibleCount) : [];

    // The menu's open state lives here, above the lines: a resize that moves the +N chip to another
    // line remounts it, and the menu has to stay open through that.
    const [moreOpen, setMoreOpen] = useState(false);
    const nothingHidden = hidden.length === 0;
    useEffect(() => {
        if (nothingHidden) {
            setMoreOpen(false);
        }
    }, [nothingHidden]);

    return (
        <Starters
            ref={groupRef}
            role='group'
            aria-label={intl.formatMessage({defaultMessage: 'Custom prompts'})}
            data-testid='agent-chat-starter-prompts'
        >
            <MeasureRow
                ref={measureRef}
                aria-hidden={true}
            >
                {prompts.map((prompt) => (
                    <MeasureItem
                        key={prompt.id}
                        {...{[MEASURE_ATTRIBUTE]: 'item'}}
                    >
                        <StarterButton tabIndex={-1}>
                            {prompt.name}
                        </StarterButton>
                    </MeasureItem>
                ))}
                <MeasureItem {...{[MEASURE_ATTRIBUTE]: 'chip'}}>
                    <StarterButton tabIndex={-1}>
                        {`+${prompts.length}`}
                    </StarterButton>
                </MeasureItem>
            </MeasureRow>
            {fit?.lines.map((line, lineIndex) => (
                <StarterLine
                    key={lineIndex} // eslint-disable-line react/no-array-index-key -- lines are positional and carry no state
                    data-testid='agent-chat-starter-line'
                >
                    {line.map((entry) => (entry === 'more' ? (
                        <MorePrompts
                            key='more'
                            prompts={hidden}
                            disabled={disabled}
                            open={moreOpen}
                            onOpenChange={setMoreOpen}
                            onPick={onPick}
                        />
                    ) : (
                        <StarterButton
                            key={prompts[entry].id}
                            title={prompts[entry].description}
                            disabled={disabled}
                            onClick={() => onPick(prompts[entry])}
                        >
                            {prompts[entry].name}
                        </StarterButton>
                    )))}
                </StarterLine>
            ))}
        </Starters>
    );
};

interface MorePromptsProps {
    prompts: CustomPrompt[];
    disabled: boolean;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onPick: (prompt: CustomPrompt) => void;
}

// The "+N" after the starter buttons: the pinned prompts that did not fit, in a menu.
const MorePrompts = ({prompts, disabled, open, onOpenChange, onPick}: MorePromptsProps) => {
    const intl = useIntl();
    const menu = useMenuTransition(open);
    const menuId = `starter-more-menu-${useId().replace(/:/g, '')}`;
    useFocusMenuOnOpen(open, menuId);

    const label = intl.formatMessage({defaultMessage: 'More prompts'});

    return (
        <Dropdown
            isOpen={menu.rendered}
            onOpenChange={onOpenChange}
            placement='bottom-end'
            target={(
                <StarterButton
                    disabled={disabled}
                    aria-label={intl.formatMessage({defaultMessage: 'More prompts ({count})'}, {count: prompts.length})}
                    aria-haspopup='menu'
                    aria-expanded={open}
                    title={label}
                    onClick={() => onOpenChange(!open)}
                >
                    {`+${prompts.length}`}
                </StarterButton>
            )}
        >
            <PopoverMenu
                id={menuId}
                open={open}
                onExited={menu.onExited}
                role='menu'
                aria-label={label}
                data-testid='dropdownmenu'
                onKeyDown={handleMenuNavigation}
                onClick={() => onOpenChange(false)}
            >
                {prompts.map((prompt) => (
                    <DropdownMenuItem
                        key={prompt.id}
                        label={prompt.name}
                        onClick={() => onPick(prompt)}
                    />
                ))}
            </PopoverMenu>
        </Dropdown>
    );
};

// A resting tint so the text-only buttons read as buttons; hover and focus keep compass's own states.
const StarterButton = styled(Button).attrs({emphasis: 'dim', size: 'x-small', compact: true})`
    && {
        background: rgba(var(--center-channel-color-rgb), 0.04);
    }
`;

// One gap between buttons and between lines; the measuring row reads it back as pixels.
const ITEM_GAP = 'var(--spacing-xs)';

// No compass token for a content width. The lines measure against this width, so it also decides
// how many prompts fit before "+N".
const STARTERS_MAX_WIDTH = '520px';

const Starters = styled.div`
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: ${ITEM_GAP};
    width: 100%;
    max-width: ${STARTERS_MAX_WIDTH};
`;

const StarterLine = styled.div`
    display: flex;
    align-items: center;
    justify-content: center;
    gap: ${ITEM_GAP};
    max-width: 100%;
`;

// Every button on one line at its natural width, out of sight and out of the layout.
const MeasureRow = styled.div`
    position: absolute;
    top: 0;
    left: 0;
    display: flex;
    align-items: center;
    gap: ${ITEM_GAP};
    width: max-content;
    height: 0;
    overflow: hidden;
    pointer-events: none;
    visibility: hidden;
`;

const MeasureItem = styled.span`
    display: inline-flex;
`;

export default StarterPrompts;
