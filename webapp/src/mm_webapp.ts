// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

export const AdvancedTextEditor = (window as any).Components.AdvancedTextEditor;

// Compatibility with pre v10 create post export
export const CreatePost = (window as any).Components.CreatePost;

export function isRHSCompatable(): boolean {
    return AdvancedTextEditor || CreatePost;
}

export const PostMessagePreview = (window as any).Components.PostMessagePreview;

export const Timestamp = (window as any).Components.Timestamp;

export const ThreadViewer = (window as any).Components.ThreadViewer;

export const DatePicker = (window as any).Components?.DatePicker;

export const useWebSocketClient = (window as any).ProductApi.useWebSocketClient;
