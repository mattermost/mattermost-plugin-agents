// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import type {RHSTab} from './redux';

export const selectPost = (postid: string) => {
    return {
        type: 'SELECT_AI_POST',
        postId: postid,
    };
};

// Shows one of the RHS's list views (a new chat or the chat history), closing the open thread.
export const showRHSList = (tab: RHSTab) => ({type: 'AI_RHS_SET_TAB', tab});

let openRHSAction: any = null;

export const openRHS = () => {
    if (openRHSAction) {
        return openRHSAction;
    }
    return {
        type: 'NONE',
    };
};

export const setOpenRHSAction = (action: any) => {
    openRHSAction = action;
};

export const selectRegularPost = (postid: string, channelid: string) => {
    return {
        type: 'SELECT_POST',
        postId: postid,
        channelId: channelid,
    };
};
