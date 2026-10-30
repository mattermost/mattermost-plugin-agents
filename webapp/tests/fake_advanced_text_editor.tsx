// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

export type FakeAdvancedTextEditorProps = {
    location: string;
    channelId: string;
    rootId: string;
    placeholder?: string;
    afterSubmit?: (result: {created?: {id?: string}}) => void;
    [prop: string]: unknown;
};

// The files the fake's file input received (its change handler stands in for the host's
// FileUpload.handleChange), newest last.
export const fakeAdvancedTextEditorUploads: File[][] = [];

// Every props object the fake rendered with, newest last.
export const fakeAdvancedTextEditorRenders: FakeAdvancedTextEditorProps[] = [];

export const lastAdvancedTextEditorProps = () => fakeAdvancedTextEditorRenders[fakeAdvancedTextEditorRenders.length - 1];

// Stands in for the host AdvancedTextEditor (window.Components): records its
// props and renders a marker with the placeholder and a file input, so a test
// can check where it posts, hand it files, and drive afterSubmit as the host
// does after a submit.
export const FakeAdvancedTextEditor = (props: FakeAdvancedTextEditorProps) => {
    fakeAdvancedTextEditorRenders.push(props);
    return (
        <div
            data-testid='host-advanced-text-editor'
            data-channel-id={props.channelId}
        >
            {props.placeholder}
            <input
                type='file'
                aria-label='Upload files'
                onChange={(e) => fakeAdvancedTextEditorUploads.push(Array.from(e.target.files ?? []))}
            />
        </div>
    );
};
