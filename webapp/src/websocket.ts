// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {PostUpdateWebsocketMessage} from './components/llmbot_post/llmbot_post';
import {PluginWebSocketMessage} from './types';

type WebsocketListener = (msg: PluginWebSocketMessage<PostUpdateWebsocketMessage>) => void
type WebsocketListenerObject = {
    postID: string;
    listenerID: string;
    listener: WebsocketListener;
}
type WebsocketListeners = WebsocketListenerObject[]
type ProgressUpdate = PluginWebSocketMessage<PostUpdateWebsocketMessage> | null;

export default class PostEventListener {
    postUpdateWebsocketListeners: WebsocketListeners = [];
    progressUpdates = new Map<string, ProgressUpdate>();

    private setProgressUpdate = (postID: string, update: ProgressUpdate) => {
        if (!this.progressUpdates.has(postID) && this.progressUpdates.size >= 100) {
            const oldestPostID = this.progressUpdates.keys().next().value;
            if (oldestPostID) {
                this.progressUpdates.delete(oldestPostID);
            }
        }
        this.progressUpdates.set(postID, update);
    };

    public registerPostUpdateListener = (postID: string, listenerID: string, listener: WebsocketListener) => {
        this.postUpdateWebsocketListeners.push({postID, listenerID, listener});
        const pending = this.progressUpdates.get(postID);
        if (pending) {
            listener(pending);
        }
    };

    public unregisterPostUpdateListener = (postID: string, listenerID: string) => {
        this.postUpdateWebsocketListeners = this.postUpdateWebsocketListeners.filter((listenerObject) => {
            const isSamePostID = listenerObject.postID === postID;
            const isSameListenerID = listenerObject.listenerID === listenerID;
            return !(isSamePostID && isSameListenerID);
        });
    };

    public handlePostUpdateWebsockets = (msg: PluginWebSocketMessage<PostUpdateWebsocketMessage>) => {
        const postID = msg.data.post_id;
        if (msg.data.control === 'progress') {
            const pending = this.progressUpdates.get(postID);
            if (pending === null) {
                return;
            }
            const pendingSequence = pending?.data.progress_seq ?? 0;
            const incomingSequence = msg.data.progress_seq ?? 0;
            if (incomingSequence <= pendingSequence) {
                return;
            }
            this.setProgressUpdate(postID, msg);
        } else if (
            typeof msg.data.next === 'string' ||
            (typeof msg.data.control === 'string' && msg.data.control !== 'start' && msg.data.control !== 'continue')
        ) {
            this.setProgressUpdate(postID, null);
        }

        this.postUpdateWebsocketListeners.forEach((listenerObject) => {
            if (listenerObject.postID === postID) {
                listenerObject.listener(msg);
            }
        });
    };
}
