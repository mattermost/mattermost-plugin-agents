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

// Subscribers to every post update, regardless of post ID. index.tsx owns its own
// PostEventListener instance and the host allows one handler per websocket event,
// so this registry is module-level: any instance's handler notifies it, and
// consumers outside the post tree (e.g. the Agents sidebar) need no shared instance.
const allPostUpdateSubscribers = new Set<WebsocketListener>();

export const subscribeToAllPostUpdates = (callback: WebsocketListener): (() => void) => {
    allPostUpdateSubscribers.add(callback);
    return () => {
        allPostUpdateSubscribers.delete(callback);
    };
};

export default class PostEventListener {
    postUpdateWebsocketListeners: WebsocketListeners = [];
    progressUpdates = new Map<string, ProgressUpdate>();
    lifecycleUpdates = new Map<string, PluginWebSocketMessage<PostUpdateWebsocketMessage>>();

    private setProgressUpdate = (postID: string, update: ProgressUpdate) => {
        if (!this.progressUpdates.has(postID) && this.progressUpdates.size >= 100) {
            const oldestPostID = this.progressUpdates.keys().next().value;
            if (oldestPostID) {
                this.progressUpdates.delete(oldestPostID);
            }
        }
        this.progressUpdates.set(postID, update);
    };

    private setLifecycleUpdate = (postID: string, update: PluginWebSocketMessage<PostUpdateWebsocketMessage>) => {
        if (!this.lifecycleUpdates.has(postID) && this.lifecycleUpdates.size >= 100) {
            const oldestPostID = this.lifecycleUpdates.keys().next().value;
            if (oldestPostID) {
                this.lifecycleUpdates.delete(oldestPostID);
            }
        }
        this.lifecycleUpdates.set(postID, update);
    };

    public registerPostUpdateListener = (postID: string, listenerID: string, listener: WebsocketListener) => {
        this.postUpdateWebsocketListeners.push({postID, listenerID, listener});
        const lifecycle = this.lifecycleUpdates.get(postID);
        if (lifecycle) {
            listener(lifecycle);
        }
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
        } else if (msg.data.control === 'start' || msg.data.control === 'continue') {
            if (this.progressUpdates.get(postID) !== null) {
                this.setLifecycleUpdate(postID, msg);
            }
        } else if (
            typeof msg.data.next === 'string' ||
            typeof msg.data.control === 'string'
        ) {
            this.setProgressUpdate(postID, null);
            this.lifecycleUpdates.delete(postID);
        }

        this.postUpdateWebsocketListeners.forEach((listenerObject) => {
            if (listenerObject.postID === postID) {
                listenerObject.listener(msg);
            }
        });

        Array.from(allPostUpdateSubscribers).forEach((callback) => callback(msg));
    };
}
