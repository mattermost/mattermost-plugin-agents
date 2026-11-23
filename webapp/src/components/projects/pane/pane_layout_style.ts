// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {createGlobalStyle} from 'styled-components';

// The project pane's only touchpoint with the host's layout. Mattermost renders root components
// inside #channel_view .channel-view-inner, beside the channel's .inner-wrap, and the regular
// right-hand sidebar sits in its own grid cell outside it. Every rule here applies only while the
// pane's element is in that container, so unmounting the pane restores the stock layout.
//
// With the messages hidden, the channel column keeps the width it had beside the pane, so its
// virtualized post list keeps its layout and scroll position.
export const PaneLayoutStyle = createGlobalStyle<{$postColumnWidth: number}>`
    #channel_view .channel-view-inner:has(> [data-mm-ai-project-pane]) {
        display: flex;
        flex-direction: row;
    }

    #channel_view .channel-view-inner:has(> [data-mm-ai-project-pane])::after {
        display: none;
    }

    #channel_view .channel-view-inner:has(> [data-mm-ai-project-pane]) > .inner-wrap {
        flex: 1 1 0;
        min-width: 0;
    }

    #channel_view .channel-view-inner:has(> [data-mm-ai-project-pane='pane']) > .inner-wrap,
    #channel_view .channel-view-inner:has(> [data-mm-ai-project-pane='fullscreen']) > .inner-wrap {
        position: absolute;
        top: 0;
        bottom: 0;
        left: 0;
        width: ${({$postColumnWidth}) => ($postColumnWidth > 0 ? `${$postColumnWidth}px` : '100%')};
        pointer-events: none;
        visibility: hidden;
    }

    body:has([data-mm-ai-project-pane='fullscreen']) #SidebarContainer,
    body:has([data-mm-ai-project-pane='fullscreen']) .team-sidebar {
        display: none;
    }
`;
