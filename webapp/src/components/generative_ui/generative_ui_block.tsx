// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState} from 'react';
import styled from 'styled-components';
import {FormattedMessage} from 'react-intl';
import {Renderer} from '@openuidev/react-lang';

import {Button} from '@mattermost/compass-ui/components/button';

import {mattermostLibrary} from './library';

interface Props {
    source: string;
    isStreaming: boolean;
}

/**
 * Renders one OpenUI Lang program with the Mattermost component library. Only
 * library components can render; anything the parser cannot map is dropped.
 */
const GenerativeUIBlock = ({source, isStreaming}: Props) => {
    const [showSource, setShowSource] = useState(false);

    return (
        <Block data-testid='generative-ui-block'>
            <RenderBoundary source={source}>
                <Renderer
                    response={source}
                    library={mattermostLibrary}
                    isStreaming={isStreaming}
                />
            </RenderBoundary>
            {!isStreaming && (
                <SourceToggle>
                    <Button
                        emphasis='link'
                        size='x-small'
                        onClick={() => setShowSource((prev) => !prev)}
                        aria-expanded={showSource}
                    >
                        {showSource ? (
                            <FormattedMessage
                                id='ai.generative_ui.hide_source'
                                defaultMessage='Hide source'
                            />
                        ) : (
                            <FormattedMessage
                                id='ai.generative_ui.view_source'
                                defaultMessage='View source'
                            />
                        )}
                    </Button>
                </SourceToggle>
            )}
            {showSource && <Source>{source}</Source>}
        </Block>
    );
};

interface BoundaryProps {
    source: string;
    children: React.ReactNode;
}

// A component that throws on malformed props falls back to the raw program
// instead of taking down the whole post.
class RenderBoundary extends React.Component<BoundaryProps, {failed: boolean}> {
    state = {failed: false};

    static getDerivedStateFromError() {
        return {failed: true};
    }

    componentDidUpdate(prevProps: BoundaryProps) {
        if (this.state.failed && prevProps.source !== this.props.source) {
            this.setState({failed: false});
        }
    }

    render() {
        if (this.state.failed) {
            return <Source>{this.props.source}</Source>;
        }
        return this.props.children;
    }
}

const Block = styled.div`
    display: flex;
    flex-direction: column;
    gap: var(--spacing-xxxs);
    margin: var(--spacing-xs) 0;
    max-width: 720px;
`;

const SourceToggle = styled.div`
    display: flex;
`;

const Source = styled.pre`
    margin: 0;
    padding: var(--spacing-xs) var(--spacing-m);
    border-radius: var(--radius-s);
    background: rgba(var(--center-channel-color-rgb), 0.04);
    font-size: 12px;
    line-height: 16px;
    white-space: pre-wrap;
    overflow-wrap: anywhere;
`;

export default GenerativeUIBlock;
