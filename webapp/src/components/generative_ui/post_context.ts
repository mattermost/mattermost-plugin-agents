// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createContext, useContext} from 'react';

import type {MarkdownRenderer} from '../post_text';

// Lets generated components format text through the enclosing post's markdown pipeline.
export const GenerativeUIPostContext = createContext<MarkdownRenderer | null>(null);

export const usePostMarkdownRenderer = () => useContext(GenerativeUIPostContext);
