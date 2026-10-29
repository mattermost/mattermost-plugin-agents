// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import {useSelector} from 'react-redux';
import styled, {keyframes, css} from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Channel} from '@mattermost/types/channels';
import {Team} from '@mattermost/types/teams';

import manifest from '@/manifest';
import {getGenerativeUIEnabled} from '@/selectors';

import GenerativeUIBlock from './generative_ui/generative_ui_block';
import {GenerativeUIPostContext} from './generative_ui/post_context';
import {splitGenerativeUI} from './generative_ui/split_message';

import {insertAnnotationMarkers, replaceCitationMarkers} from './citations/citation_processor';
import {Annotation} from './citations/types';

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

export type MarkdownRenderer = (message: string) => React.ReactNode;

/**
 * Renders markdown through the host post pipeline, so agent text gets the same
 * mentions, link safety, and formatting as any other post.
 */
export const usePostMarkdown = (channelID: string, postID: string): MarkdownRenderer => {
    const channel = useSelector<GlobalState, Channel>((state) => state.entities.channels.channels[channelID]);
    const team = useSelector<GlobalState, Team>((state) => state.entities.teams.teams[channel?.team_id]);
    const siteURL = useSelector<GlobalState, string | undefined>((state) => state.entities.general.config.SiteURL);
    const allowUnsafeLinks = useSelector<GlobalState, boolean>((state: any) => state['plugins-' + manifest.id]?.allowUnsafeLinks ?? false);

    return useCallback((message: string) => {
        // @ts-ignore
        const {formatText, messageHtmlToComponent} = window.PostUtils;

        const markdownOptions = {
            singleline: false,
            mentionHighlight: true,
            atMentions: true,
            team,
            unsafeLinks: !allowUnsafeLinks,
            minimumHashtagLength: 1000000000,
            siteURL,
        };

        const messageHtmlToComponentOptions = {
            hasPluginTooltips: true,
            latex: false,
            inlinelatex: false,
            postId: postID,
        };

        return messageHtmlToComponent(
            formatText(message, markdownOptions),
            messageHtmlToComponentOptions,
        );
    }, [team, allowUnsafeLinks, siteURL, postID]);
};

const PostText = (props: Props) => {
    const renderMarkdown = usePostMarkdown(props.channelID, props.postID);
    const generativeUIEnabled = useSelector(getGenerativeUIEnabled) && Boolean(props.allowGenerativeUI);

    // Process message with annotations if they exist
    let processedMessage = props.message;
    if (props.annotations && props.annotations.length > 0) {
        processedMessage = insertAnnotationMarkers(props.message, props.annotations);
    }

    const renderText = (message: string) => {
        const text = renderMarkdown(message);
        if (!text) {
            return null;
        }

        // Post-process the rendered JSX to replace citation markers with React components
        return props.annotations && props.annotations.length > 0 ?
            replaceCitationMarkers(text, props.annotations) :
            text;
    };

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
