// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import {FormattedMessage} from 'react-intl';
import styled from 'styled-components';

const Fallback = styled.div`
    padding: 16px;
    color: var(--error-text);
`;

const ErrorDetail = styled.pre`
    margin-top: 8px;
    white-space: pre-wrap;
    word-break: break-word;
    font-size: 12px;
`;

type Props = {
    children: React.ReactNode
}

type State = {
    error: Error | null
}

// Catches RHS render exceptions so Mattermost's plugin error boundary does not
// swallow the real message. Fallback keeps data-testid="mattermost-ai-rhs" and
// exposes the exception at data-testid="mattermost-ai-rhs-error".
export default class RHSErrorBoundary extends React.Component<Props, State> {
    state: State = {error: null};

    static getDerivedStateFromError(error: Error): State {
        return {error};
    }

    render() {
        if (this.state.error) {
            return (
                <Fallback data-testid='mattermost-ai-rhs-error'>
                    <FormattedMessage defaultMessage='An error occurred in the Agents panel.'/>
                    <ErrorDetail>{this.state.error.message}</ErrorDetail>
                </Fallback>
            );
        }
        return this.props.children;
    }
}
