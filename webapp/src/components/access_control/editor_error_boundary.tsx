// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {Component, ReactNode} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';

type Props = {
    children: ReactNode;
};

type State = {
    hasError: boolean;
};

// Isolates host-editor / Monaco chunk failures so they don't bubble into the
// product-level PluggableErrorBoundary and take down the whole Agents UI.
export default class EditorErrorBoundary extends Component<Props, State> {
    state: State = {hasError: false};

    static getDerivedStateFromError(): State {
        return {hasError: true};
    }

    render() {
        if (this.state.hasError) {
            return (
                <ErrorText>
                    <FormattedMessage defaultMessage='The access policy editor failed to load. Refresh the page and try again.'/>
                </ErrorText>
            );
        }
        return this.props.children;
    }
}

const ErrorText = styled.div`
    color: var(--dnd-indicator, #D24B4E);
    font-size: 13px;
    padding: 12px 0;
`;
