// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import HTMLArtifactViewer from './html_viewer';
import MarkdownArtifactViewer from './markdown_viewer';
import {registerArtifactViewer} from './registry';

registerArtifactViewer('html', HTMLArtifactViewer);
registerArtifactViewer('markdown', MarkdownArtifactViewer);
