// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useLayoutEffect, useMemo, useRef, useState} from 'react';
import {useSelector} from 'react-redux';
import styled, {keyframes, css} from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Channel} from '@mattermost/types/channels';
import {Team} from '@mattermost/types/teams';

import manifest from '@/manifest';

import {insertAnnotationMarkers, replaceCitationMarkers} from './citations/citation_processor';
import {Annotation} from './citations/types';
import {noMotionWhenReduced} from './llmbot_post/motion';
import {StreamingMarkdown, fadeOnAnimation} from './llmbot_post/streaming_markdown';
import type {FreshRange} from './llmbot_post/use_smooth_text';

export type ChannelNamesMap = {
    [name: string]: {
        display_name: string;
        team_name?: string;
    } | Channel;
};

/** Streaming reveal: the message renders block by block, fresh text fading in. */
export interface TextReveal {
    fresh: readonly FreshRange[];
    onFreshEnd: (id: number) => void;
}

interface Props {
    message: string;
    channelID: string;
    postID: string;
    showCursor?: boolean;
    annotations?: Annotation[];
    reveal?: TextReveal;
}

const blinkKeyframes = keyframes`
	0% { opacity: 0.48; }
	20% { opacity: 0.48; }
	100% { opacity: 0.12; }
`;

const TextContainer = styled.div<{$showCursor?: boolean}>`
	${(props) => props.$showCursor && css`
		>ul:last-child>li:last-child>span:not(:has(li))::after,
		>ol:last-child>li:last-child>span:not(:has(li))::after,
		>ul:last-child>li:last-child>span>ul>li:last-child>span:not(:has(li))::after,
		>ol:last-child>li:last-child>span>ul>li:last-child>span:not(:has(li))::after,
		>ul:last-child>li:last-child>span>ol>li:last-child>span:not(:has(li))::after,
		>ol:last-child>li:last-child>span>ol>li:last-child>span:not(:has(li))::after,
		>h1:last-child::after,
		>h2:last-child::after,
		>h3:last-child::after,
		>h4:last-child::after,
		>h5:last-child::after,
		>h6:last-child::after,
		>blockquote:last-child>p::after,
		>p:last-child::after,
		>p:empty::after {
			content: '';
			width: 7px;
			height: 16px;
			background: rgba(var(--center-channel-color-rgb), 0.48);
			display: inline-block;
			margin-left: 3px;

			animation: ${blinkKeyframes} 500ms ease-in-out infinite;
		}
	`}
`;

// Core only routes data-link anchors in-app for posts it renders itself, so
// without this handler internal links in agent posts reload the whole page.
const handleInternalLinkClick = (e: React.MouseEvent<HTMLDivElement>) => {
    if (e.defaultPrevented || e.button !== 0 || e.altKey || e.ctrlKey || e.metaKey || e.shiftKey) {
        return;
    }

    const path = (e.target as Element).closest('a[data-link]')?.getAttribute('data-link');
    const history = window.WebappUtils?.browserHistory;
    if (!path || !history) {
        return;
    }

    e.preventDefault();
    history.push(path);
};

// Blocks appended after mount fade in; those present at mount do not.
const RevealTextContainer = styled(TextContainer)<{$enterFrom: number | null}>`
	${(props) => props.$enterFrom !== null && css`
		> :nth-child(n + ${props.$enterFrom}) {
			animation: ${fadeOnAnimation} var(--duration-moderate) var(--ease-entrance) both;
			${noMotionWhenReduced}
		}
	`}
`;

interface RevealTextProps {
    message: string;
    postID: string;
    showCursor?: boolean;
    reveal: TextReveal;
    team: Team;
    siteURL?: string;
    allowUnsafeLinks: boolean;
}

const RevealText = (props: RevealTextProps) => {
    // @ts-ignore
    const {formatText, messageHtmlToComponent} = window.PostUtils;
    const containerRef = useRef<HTMLDivElement>(null);
    const [enterFrom, setEnterFrom] = useState<number | null>(null);

    useLayoutEffect(() => {
        setEnterFrom((containerRef.current?.childElementCount ?? 0) + 1);
    }, []);

    const markdownOptions = useMemo(
        () => buildMarkdownOptions(props.team, props.allowUnsafeLinks, props.siteURL),
        [props.team, props.allowUnsafeLinks, props.siteURL],
    );
    const htmlOptions = useMemo(() => buildHtmlOptions(props.postID), [props.postID]);

    return (
        // eslint-disable-next-line agents/click-events-have-key-events, agents/no-static-element-interactions -- delegates clicks from the links inside, which stay keyboard-operable
        <RevealTextContainer
            ref={containerRef}
            data-testid='posttext'
            $showCursor={props.showCursor}
            $enterFrom={enterFrom}
            onClick={handleInternalLinkClick}
        >
            <StreamingMarkdown
                message={props.message}
                fresh={props.reveal.fresh}
                onFreshEnd={props.reveal.onFreshEnd}
                formatText={formatText}
                messageHtmlToComponent={messageHtmlToComponent}
                markdownOptions={markdownOptions}
                htmlOptions={htmlOptions}
            />
        </RevealTextContainer>
    );
};

function buildMarkdownOptions(team: Team, allowUnsafeLinks: boolean, siteURL: string | undefined) {
    return {
        singleline: false,
        mentionHighlight: true,
        atMentions: true,
        team,
        unsafeLinks: !allowUnsafeLinks,
        minimumHashtagLength: 1000000000,
        siteURL,
    };
}

function buildHtmlOptions(postID: string) {
    return {
        hasPluginTooltips: true,
        latex: false,
        inlinelatex: false,
        postId: postID,
    };
}

const PostText = (props: Props) => {
    const channel = useSelector<GlobalState, Channel>((state) => state.entities.channels.channels[props.channelID]);
    const team = useSelector<GlobalState, Team>((state) => state.entities.teams.teams[channel?.team_id]);
    const siteURL = useSelector<GlobalState, string | undefined>((state) => state.entities.general.config.SiteURL);
    const allowUnsafeLinks = useSelector<GlobalState, boolean>((state: any) => state['plugins-' + manifest.id]?.allowUnsafeLinks ?? false);

    // Citation markers index into the whole message, so annotated text keeps
    // the single-pass render.
    if (props.reveal && !(props.annotations && props.annotations.length > 0)) {
        return (
            <RevealText
                message={props.message}
                postID={props.postID}
                showCursor={props.showCursor}
                reveal={props.reveal}
                team={team}
                siteURL={siteURL}
                allowUnsafeLinks={allowUnsafeLinks}
            />
        );
    }

    // @ts-ignore
    const {formatText, messageHtmlToComponent} = window.PostUtils;

    const markdownOptions = buildMarkdownOptions(team, allowUnsafeLinks, siteURL);
    const messageHtmlToComponentOptions = buildHtmlOptions(props.postID);

    // Process message with annotations if they exist
    let processedMessage = props.message;
    if (props.annotations && props.annotations.length > 0) {
        processedMessage = insertAnnotationMarkers(props.message, props.annotations);
    }

    const text = messageHtmlToComponent(
        formatText(processedMessage, markdownOptions),
        messageHtmlToComponentOptions,
    );

    if (!text) {
        return <TextContainer $showCursor={props.showCursor}>{<p/>}</TextContainer>;
    }

    // Post-process the rendered JSX to replace citation markers with React components
    const processedText = props.annotations && props.annotations.length > 0 ? replaceCitationMarkers(text, props.annotations) : text;

    return (
        // eslint-disable-next-line agents/click-events-have-key-events, agents/no-static-element-interactions -- delegates clicks from the links inside, which stay keyboard-operable
        <TextContainer
            data-testid='posttext'
            $showCursor={props.showCursor}
            onClick={handleInternalLinkClick}
        >
            {processedText}
        </TextContainer>
    );
};

export default PostText;
