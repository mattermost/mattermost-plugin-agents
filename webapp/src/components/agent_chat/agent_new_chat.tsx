// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useLayoutEffect, useRef, useState} from 'react';
import {defineMessages, useIntl, type MessageDescriptor} from 'react-intl';
import {useSelector} from 'react-redux';
import styled, {css, keyframes} from 'styled-components';

import type {GlobalState} from '@mattermost/types/store';
import {UserAvatar} from '@mattermost/compass-ui/components/user-avatar';

import {getProfilePictureUrl} from '@/client';
import type {LLMBot} from '@/bots';

import {colorSchemeFor} from '@/components/html_artifacts/artifact_broker';

import {CHAT_CONTENT_MAX_WIDTH} from './agent_chat_host_styles';
import AgentChatComposer from './agent_chat_composer';

// Greeting pairs for the new-chat state. The titles are the prototype's
// EMPTY_CHAT_TITLES; each subtitle belongs to its title, so both are picked
// together by one index.
const greetingMessages = defineMessages({
    helpTodayTitle: {defaultMessage: 'How can I help today?'},
    helpTodaySubtitle: {defaultMessage: 'Ask a question, draft something, or hand off a task.'},
    getWorkDoneTitle: {defaultMessage: 'Let\'s get some work done'},
    getWorkDoneSubtitle: {defaultMessage: 'Tell me what you\'re working on and we\'ll start there.'},
    tackleTitle: {defaultMessage: 'What should we tackle?'},
    tackleSubtitle: {defaultMessage: 'Pick a project, a problem, or just a rough idea.'},
    readyTitle: {defaultMessage: 'Ready when you are'},
    readySubtitle: {defaultMessage: 'Start with a question or describe what you need.'},
    startTitle: {defaultMessage: 'Where should we start?'},
    startSubtitle: {defaultMessage: 'Describe the goal and we\'ll work through it together.'},
    onYourMindTitle: {defaultMessage: 'Got something on your mind?'},
    onYourMindSubtitle: {defaultMessage: 'Share a thought, a question, or something you\'re stuck on.'},
});

export const NEW_CHAT_GREETINGS: ReadonlyArray<{title: MessageDescriptor; subtitle: MessageDescriptor}> = [
    {title: greetingMessages.helpTodayTitle, subtitle: greetingMessages.helpTodaySubtitle},
    {title: greetingMessages.getWorkDoneTitle, subtitle: greetingMessages.getWorkDoneSubtitle},
    {title: greetingMessages.tackleTitle, subtitle: greetingMessages.tackleSubtitle},
    {title: greetingMessages.readyTitle, subtitle: greetingMessages.readySubtitle},
    {title: greetingMessages.startTitle, subtitle: greetingMessages.startSubtitle},
    {title: greetingMessages.onYourMindTitle, subtitle: greetingMessages.onYourMindSubtitle},
];

// Secondary text emphasis, as in the prototype (0.72 opacity over the text
// color). No compass token covers text emphasis levels.
const SUBTITLE_EMPHASIS = '72%';

// 'page': the product view's centered 800px column.
// 'panel': fills a narrow container (the RHS).
export type AgentNewChatLayout = 'page' | 'panel';

interface AgentNewChatProps {
    bot: LLMBot;
    channelId: string;
    onCreated: (rootId: string) => void;
    layout: AgentNewChatLayout;

    // Extra content under the composer (e.g. the RHS pinned prompt buttons).
    children?: React.ReactNode;
}

// The new-chat state: the agent's avatar, a greeting, and the pill composer,
// centered until the first post creates the thread.
const AgentNewChat = ({bot, channelId, onCreated, layout, children}: AgentNewChatProps) => {
    const intl = useIntl();

    // One index per mount picks title and subtitle together, so they always
    // match and re-renders don't change them.
    const [greeting] = useState(() => NEW_CHAT_GREETINGS[Math.floor(Math.random() * NEW_CHAT_GREETINGS.length)]);

    return (
        <Wrapper $backdrop={layout === 'panel'}>
            <EmptyChat
                $layout={layout}
                data-testid='agent-chat-new-chat'
            >
                <EmptyIntro>
                    <UserAvatar
                        src={getProfilePictureUrl(bot.id, bot.lastIconUpdate)}
                        alt={bot.displayName}
                        name={bot.displayName}
                        size='64'
                    />
                    <Greeting>
                        <EmptyTitle $layout={layout}>{intl.formatMessage(greeting.title)}</EmptyTitle>
                        <EmptySubtitle>{intl.formatMessage(greeting.subtitle)}</EmptySubtitle>
                    </Greeting>
                </EmptyIntro>
                <AgentChatComposer
                    channelId={channelId}
                    agentName={bot.displayName}
                    bot={bot}
                    onCreated={onCreated}
                />
                {children}
            </EmptyChat>
        </Wrapper>
    );
};

// Fine monochrome grain, tiled. Its brightness varies around mid-grey (the neutral value for the
// overlay blend), so it lightens and darkens the base. Dithers the gradient so it doesn't band.
const NOISE_IMAGE = 'url("data:image/svg+xml,%3Csvg xmlns=\'http://www.w3.org/2000/svg\' width=\'160\' height=\'160\'%3E%3Cfilter id=\'n\'%3E%3CfeTurbulence type=\'fractalNoise\' baseFrequency=\'0.8\' numOctaves=\'2\' stitchTiles=\'stitch\'/%3E%3CfeColorMatrix values=\'1.6 0 0 0 -0.3 1.6 0 0 0 -0.3 1.6 0 0 0 -0.3 0 0 0 0 1\'/%3E%3C/filter%3E%3Crect width=\'100%25\' height=\'100%25\' filter=\'url(%23n)\'/%3E%3C/svg%3E")';

const NOISE_OPACITY = 0.25;

// Soft orbs that float slowly behind the new-chat state. Each orb is two nested elements: the track
// swings it along X and the orb itself along Y (plus a scale change), each on its own period with
// ease-in-out and alternate. Two unrelated periods trace a smooth Lissajous curve that rarely repeats,
// where one animation could only go back and forth along a line. Per-orb amplitudes come in as custom
// properties; translations are a percentage of the orb's own size.
const orbSwingX = keyframes`
    from {
        transform: translate3d(calc(-1 * var(--orb-dx)), 0, 0);
    }

    to {
        transform: translate3d(var(--orb-dx), 0, 0);
    }
`;

const orbSwingY = keyframes`
    from {
        transform: translate3d(0, calc(-1 * var(--orb-dy)), 0) scale(var(--orb-scale-from));
    }

    to {
        transform: translate3d(0, var(--orb-dy), 0) scale(var(--orb-scale-to));
    }
`;

// The backdrop's base: a faint wash of the text color over the center channel background, strongest in
// the center and fading to nothing at the corners, so the orbs and the grain have a little more to
// stand out against.
const BACKGROUND_TINT = '4%';

const Orbs = styled.div<{$visible: boolean; $running: boolean}>`
    position: absolute;
    inset: 0;

    /* Orbs are sized in cqmax (the larger side of this box), so a tall, narrow panel gets bigger orbs than a width-relative size would. */
    container-type: size;

    /* The orbs swing past this box. Without a clip they would extend the parent's scrollable overflow, and
       scrolling something into view (expanding a panel in a reply) could then scroll the pane itself and push
       the header out of view. clip, unlike hidden, makes this box unscrollable. */
    overflow: clip;
    z-index: -1;
    pointer-events: none;
    background: radial-gradient(ellipse at center, color-mix(in srgb, var(--center-channel-color) ${BACKGROUND_TINT}, transparent), transparent);
    opacity: ${({$visible}) => ($visible ? 1 : 0)};
    visibility: ${({$visible}) => ($visible ? 'visible' : 'hidden')};
    transition: opacity var(--duration-moderate) var(--ease-transition), visibility 0s linear ${({$visible}) => ($visible ? '0s' : 'var(--duration-moderate)')};

    /* No point floating while the backdrop is faded out, or while the window isn't being looked at. */
    & span {
        animation-play-state: ${({$running}) => ($running ? 'running' : 'paused')};
    }

    @media (prefers-reduced-motion: reduce) {
        transition: none;

        & span {
            animation: none;
        }
    }
`;

// Share of the accent's chroma the orbs keep (0 is a pure grey, 1 the full accent).
const ORB_CHROMA = 0.5;

const OrbTrack = styled.span<{$size: string; $top: string; $left: string; $dx: number; $duration: number; $delay: number}>`
    --orb-dx: ${({$dx}) => $dx}%;

    position: absolute;
    top: ${({$top}) => $top};
    left: ${({$left}) => $left};
    width: ${({$size}) => $size};
    aspect-ratio: 1;
    animation: ${orbSwingX} ${({$duration}) => $duration}s ease-in-out ${({$delay}) => $delay}s infinite alternate;
    will-change: transform;
`;

// Theme-aware: the orbs are the host theme's button color, muted.
const Orb = styled.span<{$strength: number; $surface: boolean; $dy: number; $scaleFrom: number; $scaleTo: number; $duration: number; $delay: number}>`
    --orb-dy: ${({$dy}) => $dy}%;
    --orb-scale-from: ${({$scaleFrom}) => $scaleFrom};
    --orb-scale-to: ${({$scaleTo}) => $scaleTo};

    /* Only one of the two colors is ever set. A nested @supports rule is emitted after the plain
       declarations, so setting both would let the accent win over a surface orb whatever the order. */
    ${({$surface, $strength}) => ($surface ? css`

        /* A surface orb: neutral-0, still transparent, with a firmer edge. Drawn above the accent orbs,
           it adds some mid-ground definition. */
        --orb-color: color-mix(in srgb, var(--color-neutral-0, var(--neutral-0)) ${$strength}%, transparent);
        --orb-solid: 65%;
        --orb-half: 88%;
    ` : css`
        --orb-color: color-mix(in srgb, var(--button-bg) ${$strength}%, transparent);

        /* The accent with most of its chroma taken out: a hint of the theme's hue, so a neutral theme
           (Quartz) gets grey orbs where the saturated accent clashed with its greys. */
        @supports (color: oklch(from red l c h)) {
            --orb-color: oklch(from var(--button-bg) l calc(c * ${ORB_CHROMA}) h / ${$strength}%);
        }
    `)}

    display: block;
    width: 100%;
    height: 100%;
    border-radius: 50%;

    /* The soft edge is in the gradient, not a blur filter: a filter on large moving layers is re-run by the GPU every frame. */
    background: radial-gradient(
        closest-side,
        var(--orb-color) 0%,
        var(--orb-color) var(--orb-solid, 55%),
        color-mix(in srgb, var(--orb-color) 50%, transparent) var(--orb-half, 80%),
        transparent 100%
    );
    animation: ${orbSwingY} ${({$duration}) => $duration}s ease-in-out ${({$delay}) => $delay}s infinite alternate;
`;

// The grain sits above the orbs in overlay blend and dithers them, so they don't band.
const Grain = styled.div<{$visible: boolean}>`
    position: absolute;
    inset: 0;
    z-index: -1;
    pointer-events: none;
    background-image: ${NOISE_IMAGE};
    background-size: 160px 160px;
    mix-blend-mode: overlay;
    opacity: ${({$visible}) => ($visible ? NOISE_OPACITY : 0)};
    visibility: ${({$visible}) => ($visible ? 'visible' : 'hidden')};
    transition: opacity var(--duration-moderate) var(--ease-transition), visibility 0s linear ${({$visible}) => ($visible ? '0s' : 'var(--duration-moderate)')};

    @media (prefers-reduced-motion: reduce) {
        transition: none;
    }
`;

// dx/dy: swing in percent of the orb's size. xDuration/yDuration: one-way seconds per axis.
const ORBS: ReadonlyArray<{surface?: boolean; size: string; top: string; left: string; strength: number; lightStrength?: number; dx: number; dy: number; scaleFrom: number; scaleTo: number; xDuration: number; yDuration: number}> = [
    {size: '40cqmax', top: '-10%', left: '-5%', strength: 9, dx: 40, dy: 30, scaleFrom: 0.95, scaleTo: 1.2, xDuration: 7, yDuration: 10.5},
    {size: '36cqmax', top: '5%', left: '60%', strength: 8.5, dx: 35, dy: 40, scaleFrom: 1.2, scaleTo: 0.9, xDuration: 9.5, yDuration: 6.5},
    {size: '42cqmax', top: '55%', left: '25%', strength: 5, dx: 45, dy: 25, scaleFrom: 0.9, scaleTo: 1.15, xDuration: 8.5, yDuration: 11},
    {size: '34cqmax', top: '70%', left: '70%', strength: 7, dx: 30, dy: 40, scaleFrom: 1.15, scaleTo: 0.9, xDuration: 7, yDuration: 9},
    {size: '38cqmax', top: '30%', left: '35%', strength: 5, dx: 40, dy: 35, scaleFrom: 0.85, scaleTo: 1.2, xDuration: 10.5, yDuration: 7.5},

    // Surface orbs, after the accent ones so they paint over them. Strength is the opacity of neutral-0 on a dark theme, lightStrength on a light one (near-white
    // orbs are what shows against a light surface).
    {surface: true, size: '24cqmax', top: '20%', left: '10%', strength: 3, lightStrength: 95, dx: 35, dy: 30, scaleFrom: 0.9, scaleTo: 1.1, xDuration: 9, yDuration: 12},
    {surface: true, size: '20cqmax', top: '60%', left: '55%', strength: 3, lightStrength: 85, dx: 40, dy: 35, scaleFrom: 1.1, scaleTo: 0.9, xDuration: 11, yDuration: 8},
    {surface: true, size: '26cqmax', top: '10%', left: '75%', strength: 3, lightStrength: 90, dx: 30, dy: 40, scaleFrom: 0.95, scaleTo: 1.15, xDuration: 8, yDuration: 10.5},
];

// The accent orbs are the theme's button color, which CSS can't weight by theme lightness, so the
// light-theme boost is applied in JS from the detected surface (see NewChatBackdrop). A very slight
// touch: they read weaker against a light surface.
const LIGHT_THEME_ORB_BOOST = 1.2;

// Surface orbs have their own light-theme opacity. Accent orbs just get the slight boost.
export const orbStrength = (orb: (typeof ORBS)[number], light: boolean) => {
    if (!light) {
        return orb.strength;
    }
    return orb.surface ? (orb.lightStrength ?? orb.strength) : orb.strength * LIGHT_THEME_ORB_BOOST;
};

// Whether the surface behind the backdrop is light. It reads the pane's resolved background color (the
// browser always gives it as rgb()), not a theme variable from the root, which may be unset or in a
// format the helper can't parse. colorSchemeFor answers "light" for anything unparsable, so an unreadable
// color is treated as dark here: the dim setting is the safe failure.
export const isLightSurface = (el: Element | null) => {
    const bg = el ? getComputedStyle(el).backgroundColor : '';
    return (/^rgba?\(/i).test(bg) && colorSchemeFor(bg) === 'light';
};

// Whether the window is visible and focused. An unfocused window keeps animating at full frame rate
// for nobody, so the orbs pause then.
const useWindowActive = () => {
    const [active, setActive] = useState(() => !document.hidden && document.hasFocus());
    useEffect(() => {
        const update = () => setActive(!document.hidden && document.hasFocus());
        window.addEventListener('focus', update);
        window.addEventListener('blur', update);
        document.addEventListener('visibilitychange', update);
        return () => {
            window.removeEventListener('focus', update);
            window.removeEventListener('blur', update);
            document.removeEventListener('visibilitychange', update);
        };
    }, []);
    return active;
};

// The user's theme preferences ('theme--' and one 'theme--<teamId>' per team that overrides it) as one
// string, so the backdrop re-reads the surface only when a theme changes, not on every preference save.
const selectThemePreferences = (state: GlobalState) => {
    const preferences = state.entities?.preferences?.myPreferences ?? {};
    return Object.keys(preferences).
        filter((key) => key.startsWith('theme--')).
        map((key) => `${key}=${preferences[key]?.value}`).
        join('|');
};

// The orbs and grain paint behind the parent's content, so the parent must be a stacking context
// (position: relative; isolation: isolate) with overflow hidden, and an opaque background (the grain's
// overlay blend needs one, and the theme is read from it). The chat pane keeps this mounted and fades
// it out when a thread opens.
export const NewChatBackdrop = ({visible}: {visible: boolean}) => {
    // Read from the parent's background once mounted (before first paint), and again when the theme
    // changes: a theme switch lives in the user's theme preferences (the user's own and any per-team ones). The host may
    // apply the new colors a frame after the store update, so a second read follows on the next frame.
    const orbsRef = useRef<HTMLDivElement>(null);
    const [light, setLight] = useState(false);
    const theme = useSelector<GlobalState, string>(selectThemePreferences);
    useLayoutEffect(() => {
        const read = () => setLight(isLightSurface(orbsRef.current?.parentElement ?? null));
        read();
        const frame = requestAnimationFrame(read);
        return () => cancelAnimationFrame(frame);
    }, [theme]);
    const windowActive = useWindowActive();

    // Each mount starts every orb at a random point of both of its cycles (negative delays), so no two
    // visits open on the same arrangement. A full cycle is two durations, as the animations alternate.
    const [delays] = useState(() => ORBS.map((orb) => ({
        x: -Math.random() * orb.xDuration * 2,
        y: -Math.random() * orb.yDuration * 2,
    })));

    return (
        <>
            <Orbs
                ref={orbsRef}
                $visible={visible}
                $running={visible && windowActive}
                aria-hidden='true'
            >
                {ORBS.map((orb, i) => (
                    <OrbTrack
                        key={i}
                        $size={orb.size}
                        $top={orb.top}
                        $left={orb.left}
                        $dx={orb.dx}
                        $duration={orb.xDuration}
                        $delay={delays[i].x}
                    >
                        <Orb
                            $strength={orbStrength(orb, light)}
                            $surface={Boolean(orb.surface)}
                            $dy={orb.dy}
                            $scaleFrom={orb.scaleFrom}
                            $scaleTo={orb.scaleTo}
                            $duration={orb.yDuration}
                            $delay={delays[i].y}
                        />
                    </OrbTrack>
                ))}
            </Orbs>
            <Grain
                $visible={visible}
                aria-hidden='true'
            />
        </>
    );
};

// Only the RHS panel paints its own backdrop; the product page paints it on the whole chat pane.
const Backdrop = styled.div`
    position: relative;
    isolation: isolate;
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    width: 100%;
    min-height: 0;
    overflow: hidden;

    /* An opaque base inside the isolated group, so the grain's overlay blend has something to blend with,
       and the backdrop can read the surface's lightness from it. This assumes the host RHS panel is the
       center channel color too (it is in the default themes): a host panel of another color would show
       this rectangle as a different shade. */
    background: var(--center-channel-bg);
`;

const Wrapper = ({$backdrop, children}: {$backdrop: boolean; children: React.ReactNode}) => (
    $backdrop ? (
        <Backdrop>
            <NewChatBackdrop visible={true}/>
            {children}
        </Backdrop>
    ) : <>{children}</>
);

const pageLayout = css`
    gap: var(--spacing-xxl);
    max-width: ${CHAT_CONTENT_MAX_WIDTH};
    margin-inline: auto;
    padding: var(--spacing-xxxxl);

    /* Sit slightly above true center, as in the prototype. */
    --empty-chat-lift: calc(var(--spacing-xxxxxl) + var(--spacing-l) + var(--spacing-xxl));
`;

// The RHS is short as well as narrow: a smaller lift, and 24px padding on all
// sides (the RHS thread composer uses the same side padding, so they line up).
const panelLayout = css`
    gap: var(--spacing-xl);
    padding: var(--spacing-xxl);
    --empty-chat-lift: calc(2 * var(--spacing-xxl));
`;

// The content fades in and rises 24px into its lifted position. The element's own transform holds
// the end state, so the animation only fills backwards.
const emptyChatEnter = keyframes`
    from {
        opacity: 0;
        transform: translateY(calc(var(--spacing-xxl) - var(--empty-chat-lift)));
    }

    to {
        opacity: 1;
        transform: translateY(calc(-1 * var(--empty-chat-lift)));
    }
`;

const EmptyChat = styled.div<{$layout: AgentNewChatLayout}>`
    display: flex;
    flex: 1 1 auto;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    width: 100%;
    min-height: 0;

    ${({$layout}) => ($layout === 'page' ? pageLayout : panelLayout)}
    transform: translateY(calc(-1 * var(--empty-chat-lift)));
    animation: ${emptyChatEnter} var(--duration-moderate) var(--ease-entrance) backwards;

    @media (prefers-reduced-motion: reduce) {
        animation: none;
    }
`;

const EmptyIntro = styled.div`
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: var(--spacing-m);
    text-align: center;
`;

const Greeting = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xs);
`;

// The product view's title is larger (36px); the narrow RHS keeps the smaller one.
const EmptyTitle = styled.h2<{$layout: AgentNewChatLayout}>`
    margin: 0;
    font-family: var(--font-family-heading);
    font-size: ${({$layout}) => ($layout === 'page' ? 'var(--font-size-900)' : 'var(--font-size-600)')};
    font-weight: var(--font-weight-semibold);
    line-height: ${({$layout}) => ($layout === 'page' ? 'var(--line-height-900)' : 'var(--line-height-600)')};
    letter-spacing: ${({$layout}) => ($layout === 'page' ? '-0.015em' : 'normal')};
    color: var(--center-channel-color);
`;

const EmptySubtitle = styled.p`
    margin: 0;
    font-family: var(--font-family-body);
    font-size: var(--font-size-100);
    font-weight: var(--font-weight-regular);
    line-height: var(--line-height-200);
    color: color-mix(in srgb, var(--center-channel-color) ${SUBTITLE_EMPHASIS}, transparent);
    text-wrap: balance;
`;

export default AgentNewChat;
