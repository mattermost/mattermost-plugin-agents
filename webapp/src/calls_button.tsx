// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {doSummarizeTranscription} from './client';
import {doSelectPost} from './hooks';
import {licenseAllows} from './license';

export function makeCallsPostButtonClickedHandler(dispatch: any, getState?: () => any) {
    return async (post: any) => {
        if (getState && !licenseAllows(getState(), 'meetings')) {
            return;
        }
        const result = await doSummarizeTranscription(post.id);
        doSelectPost(result.postid, result.channelid, dispatch);
    };
}
