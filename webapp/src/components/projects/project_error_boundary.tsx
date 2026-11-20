// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {Component, ReactNode} from 'react';

type Props = {
    fallback: ReactNode;
    children: ReactNode;
};

type State = {
    hasError: boolean;
};

// Keeps a failing embedded view (the Boards embed comes from another plugin) from taking the
// whole sidebar down.
export default class ProjectErrorBoundary extends Component<Props, State> {
    state: State = {hasError: false};

    static getDerivedStateFromError(): State {
        return {hasError: true};
    }

    render() {
        return this.state.hasError ? this.props.fallback : this.props.children;
    }
}
