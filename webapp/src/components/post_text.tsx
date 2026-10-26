// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback, useMemo} from 'react';
import {useSelector} from 'react-redux';
import styled, {keyframes, css} from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Channel} from '@mattermost/types/channels';
import {Team} from '@mattermost/types/teams';

import {getGenerativeUIEnabled, getLinkPolicy} from '@/selectors';
import {LinkRendering, linkRendering} from '@/utils/link_allowlist';

import {insertAnnotationMarkers, replaceCitationMarkers} from './citations/citation_processor';
import {Annotation} from './citations/types';
import GenerativeUIBlock from './generative_ui/generative_ui_block';
import {GenerativeUIPostContext} from './generative_ui/post_context';
import {splitGenerativeUI} from './generative_ui/split_message';
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

    // Render fenced OpenUI Lang blocks as components; only agent replies opt in.
    allowGenerativeUI?: boolean;
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

type PostRenderContext = {
    team: Team;
    siteURL?: string;
    links: LinkRendering;
};

const usePostRenderContext = (channelID: string): PostRenderContext => {
    const channel = useSelector<GlobalState, Channel>((state) => state.entities.channels.channels[channelID]);
    const team = useSelector<GlobalState, Team>((state) => state.entities.teams.teams[channel?.team_id]);
    const siteURL = useSelector<GlobalState, string | undefined>((state) => state.entities.general.config.SiteURL);
    const linkPolicy = useSelector(getLinkPolicy);
    const links = useMemo(() => linkRendering(linkPolicy, siteURL || window.location.origin), [linkPolicy, siteURL]);
    return {team, siteURL, links};
};

export type MarkdownRenderer = (message: string) => React.ReactNode;

/**
 * Renders markdown through the host post pipeline and the link policy, so
 * agent text (including text inside generative UI) gets the same mentions,
 * link safety, and formatting as any other post.
 */
const usePostMarkdown = (postID: string, {team, siteURL, links}: PostRenderContext): MarkdownRenderer => {
    return useCallback((message: string) => {
        // @ts-ignore
        const {formatText, messageHtmlToComponent} = window.PostUtils;

        return messageHtmlToComponent(
            links.restrictLinks(formatText(message, buildMarkdownOptions(team, links, siteURL))),
            buildHtmlOptions(postID),
        );
    }, [team, siteURL, links, postID]);
};

const PostText = (props: Props) => {
    const renderContext = usePostRenderContext(props.channelID);
    const {team, siteURL, links} = renderContext;
    const renderMarkdown = usePostMarkdown(props.postID, renderContext);
    const generativeUIEnabled = useSelector(getGenerativeUIEnabled) && Boolean(props.allowGenerativeUI);

    // A citation whose URL the policy rejects keeps its marker but loses the
    // URL, so the chip renders without a destination to open.
    const annotations = useMemo(
        () => props.annotations?.map((annotation) => (
            annotation.url && !links.isCitationUrlAllowed(annotation.url) ? {...annotation, url: undefined} : annotation // eslint-disable-line no-undefined
        )),
        [props.annotations, links],
    );
    const hasAnnotations = Boolean(annotations && annotations.length > 0);

    // Process message with annotations if they exist
    let processedMessage = props.message;
    if (annotations && hasAnnotations) {
        processedMessage = insertAnnotationMarkers(props.message, annotations);
    }

    const renderText = (message: string) => {
        const text = renderMarkdown(message);
        if (!text) {
            return null;
        }

        // Post-process the rendered JSX to replace citation markers with React components
        return annotations && hasAnnotations ? replaceCitationMarkers(text, annotations) : text;
    };

    // Generative UI blocks take precedence over the streaming reveal, which
    // would otherwise show the raw OpenUI Lang fence while it streams.
    const segments = generativeUIEnabled ? splitGenerativeUI(processedMessage) : [];
    if (segments.some((segment) => segment.type === 'ui')) {
        const lastIndex = segments.length - 1;
        return (
            <GenerativeUIPostContext.Provider value={renderMarkdown}>
                {segments.map((segment, index) => {
                    const key = `${segment.type}-${index}`;
                    if (segment.type === 'ui') {
                        return (
                            <GenerativeUIBlock
                                key={key}
                                source={segment.source}
                                isStreaming={Boolean(props.showCursor) && !segment.closed}
                            />
                        );
                    }

                    return (
                        // eslint-disable-next-line agents/click-events-have-key-events, agents/no-static-element-interactions -- delegates clicks from the links inside, which stay keyboard-operable
                        <TextContainer
                            key={key}
                            data-testid='posttext'
                            $showCursor={props.showCursor && index === lastIndex}
                            onClick={handleInternalLinkClick}
                        >
                            {renderText(segment.text)}
                        </TextContainer>
                    );
                })}
            </GenerativeUIPostContext.Provider>
        );
    }

    // Citation markers index into the whole message, so annotated text keeps
    // the single-pass render.
    if (props.reveal && !hasAnnotations) {
        return (
            <RevealText
                message={props.message}
                postID={props.postID}
                showCursor={props.showCursor}
                team={team}
                siteURL={siteURL}
                links={links}
            />
        );
    }

    const processedText = renderText(processedMessage);
    if (!processedText) {
        return <TextContainer $showCursor={props.showCursor}>{<p/>}</TextContainer>;
    }

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
