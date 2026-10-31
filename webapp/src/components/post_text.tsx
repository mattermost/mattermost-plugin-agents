// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useMemo} from 'react';
import {useSelector} from 'react-redux';
import styled, {keyframes, css} from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Channel} from '@mattermost/types/channels';
import {Team} from '@mattermost/types/teams';

import {getLinkPolicy} from '@/selectors';
import {LinkRendering, linkRendering} from '@/utils/link_allowlist';

import {insertAnnotationMarkers, replaceCitationMarkers} from './citations/citation_processor';
import {Annotation} from './citations/types';
import {StreamingMarkdown} from './llmbot_post/streaming_markdown';

export type ChannelNamesMap = {
    [name: string]: {
        display_name: string;
        team_name?: string;
    } | Channel;
};

interface Props {
    message: string;
    channelID: string;
    postID: string;
    showCursor?: boolean;
    annotations?: Annotation[];

    /** Streaming reveal: the message renders block by block as text is revealed. */
    reveal?: boolean;
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

interface RevealTextProps {
    message: string;
    postID: string;
    showCursor?: boolean;
    reveal: boolean;
    team: Team;
    siteURL?: string;
    links: LinkRendering;
}

const RevealText = (props: RevealTextProps) => {
    // @ts-ignore
    const {formatText: hostFormatText, messageHtmlToComponent} = window.PostUtils;

    // Each block goes through the same link policy as the single-pass render.
    const {links} = props;
    const formatText = useCallback(
        (source: string, options: unknown) => links.restrictLinks(hostFormatText(source, options)),
        [hostFormatText, links],
    );
    const markdownOptions = useMemo(
        () => buildMarkdownOptions(props.team, links, props.siteURL),
        [props.team, links, props.siteURL],
    );
    const htmlOptions = useMemo(() => buildHtmlOptions(props.postID), [props.postID]);

    return (
        // eslint-disable-next-line agents/click-events-have-key-events, agents/no-static-element-interactions -- delegates clicks from the links inside, which stay keyboard-operable
        <TextContainer
            data-testid='posttext'
            $showCursor={props.showCursor}
            onClick={handleInternalLinkClick}
        >
            <StreamingMarkdown
                message={props.message}
                formatText={formatText}
                messageHtmlToComponent={messageHtmlToComponent}
                markdownOptions={markdownOptions}
                htmlOptions={htmlOptions}
            />
        </TextContainer>
    );
};

function buildMarkdownOptions(team: Team, links: LinkRendering, siteURL: string | undefined) {
    return {
        singleline: false,
        mentionHighlight: true,
        atMentions: true,
        team,
        unsafeLinks: links.unsafeLinks,
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
    const linkPolicy = useSelector(getLinkPolicy);
    const links = useMemo(() => linkRendering(linkPolicy, siteURL || window.location.origin), [linkPolicy, siteURL]);

    // A citation whose URL the policy rejects keeps its marker but loses the
    // URL, so the chip renders without a destination to open.
    const annotations = useMemo(
        () => props.annotations?.map((annotation) => (
            annotation.url && !links.isCitationUrlAllowed(annotation.url) ? {...annotation, url: undefined} : annotation // eslint-disable-line no-undefined
        )),
        [props.annotations, links],
    );

    // Citation markers index into the whole message, so annotated text keeps
    // the single-pass render.
    if (props.reveal && !(annotations && annotations.length > 0)) {
        return (
            <RevealText
                message={props.message}
                postID={props.postID}
                showCursor={props.showCursor}
                reveal={props.reveal}
                team={team}
                siteURL={siteURL}
                links={links}
            />
        );
    }

    // @ts-ignore
    const {formatText, messageHtmlToComponent} = window.PostUtils;

    const markdownOptions = buildMarkdownOptions(team, links, siteURL);
    const messageHtmlToComponentOptions = buildHtmlOptions(props.postID);

    // Process message with annotations if they exist
    let processedMessage = props.message;
    if (annotations && annotations.length > 0) {
        processedMessage = insertAnnotationMarkers(props.message, annotations);
    }

    const text = messageHtmlToComponent(
        links.restrictLinks(formatText(processedMessage, markdownOptions)),
        messageHtmlToComponentOptions,
    );

    if (!text) {
        return <TextContainer $showCursor={props.showCursor}>{<p/>}</TextContainer>;
    }

    // Post-process the rendered JSX to replace citation markers with React components
    const processedText = annotations && annotations.length > 0 ? replaceCitationMarkers(text, annotations) : text;

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
