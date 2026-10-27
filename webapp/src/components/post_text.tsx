// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useMemo} from 'react';
import {useSelector} from 'react-redux';
import styled, {keyframes, css} from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Channel} from '@mattermost/types/channels';
import {Team} from '@mattermost/types/teams';

import {getLinkPolicy} from '@/selectors';
import {linkRendering} from '@/utils/link_allowlist';

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

const PostText = (props: Props) => {
    const channel = useSelector<GlobalState, Channel>((state) => state.entities.channels.channels[props.channelID]);
    const team = useSelector<GlobalState, Team>((state) => state.entities.teams.teams[channel?.team_id]);
    const siteURL = useSelector<GlobalState, string | undefined>((state) => state.entities.general.config.SiteURL);
    const linkPolicy = useSelector(getLinkPolicy);
    const links = useMemo(() => linkRendering(linkPolicy, siteURL || window.location.origin), [linkPolicy, siteURL]);

    // @ts-ignore
    const {formatText, messageHtmlToComponent} = window.PostUtils;

    const markdownOptions = {
        singleline: false,
        mentionHighlight: true,
        atMentions: true,
        team,
        unsafeLinks: links.unsafeLinks,
        minimumHashtagLength: 1000000000,
        siteURL,
    };

    const messageHtmlToComponentOptions = {
        hasPluginTooltips: true,
        latex: false,
        inlinelatex: false,
        postId: props.postID,
    };

    // A citation whose URL the policy rejects keeps its marker but loses the
    // URL, so the chip renders without a destination to open.
    const annotations = useMemo(
        () => props.annotations?.map((annotation) => (
            annotation.url && !links.isCitationUrlAllowed(annotation.url) ? {...annotation, url: undefined} : annotation // eslint-disable-line no-undefined
        )),
        [props.annotations, links],
    );

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
    const processedText = annotations && annotations.length > 0 ?
        replaceCitationMarkers(text, annotations) :
        text;

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
