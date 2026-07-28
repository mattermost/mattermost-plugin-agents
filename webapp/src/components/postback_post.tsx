// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {useSelector} from 'react-redux';
import {FormattedMessage} from 'react-intl';

import {GlobalState} from '@mattermost/types/store';

import PostText from './post_text';

interface Props {
    post: any;
}

export const PostbackPost = (props: Props) => {
    const editorUsername = useSelector<GlobalState, string>((state) => state.entities.users.profiles[props.post.props.userid]?.username);
    const botUsername = useSelector<GlobalState, string>((state) => state.entities.users.profiles[props.post.user_id]?.username);
    return (
        <>
            <PostText
                message={props.post.message}
                channelID={props.post.channel_id}
                postID={props.post.id}
            />
            <br/>
            <i>
                <FormattedMessage
                    defaultMessage='This summary was created by {botUsername} then edited and posted by @{editorUsername}'
                    values={{botUsername, editorUsername}}
                />
            </i>
        </>
    );
};
