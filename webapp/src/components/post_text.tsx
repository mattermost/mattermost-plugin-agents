// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useSelector} from 'react-redux';
import styled, {keyframes, css} from 'styled-components';

import {GlobalState} from '@mattermost/types/store';
import {Channel} from '@mattermost/types/channels';
import {Team} from '@mattermost/types/teams';

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
}

const blinkKeyframes = keyframes`
	0% { opacity: 0.48; }
	20% { opacity: 0.48; }
	100% { opacity: 0.12; }
`;

const TextContainer = styled.div<{showCursor?: boolean}>`
	${(props) => props.showCursor && css`
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

const PostText = (props: Props) => {
    const channel = useSelector<GlobalState, Channel>((state) => state.entities.channels.channels[props.channelID]);
    const team = useSelector<GlobalState, Team>((state) => state.entities.teams.teams[channel?.team_id]);
    const siteURL = useSelector<GlobalState, string | undefined>((state) => state.entities.general.config.SiteURL);

    // @ts-ignore
    const {formatText, messageHtmlToComponent} = window.PostUtils;

    const markdownOptions = {
        singleline: false,
        mentionHighlight: true,
        atMentions: true,
        team,
        unsafeLinks: true,
        minimumHashtagLength: 1000000000,
        siteURL,
    };

    const messageHtmlToComponentOptions = {
        hasPluginTooltips: true,
        latex: false,
        inlinelatex: false,
        postId: props.postID,
    };

    const text = messageHtmlToComponent(
        formatText(props.message, markdownOptions),
        messageHtmlToComponentOptions,
    );

    if (!text) {
        return <TextContainer showCursor={props.showCursor}>{<p/>}</TextContainer>;
    }

    return (
        <TextContainer
            data-testid='posttext'
            showCursor={props.showCursor}
        >
            {text}
        </TextContainer>
    );
};

export default PostText;
