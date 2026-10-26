// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';

import {ArrowLeftIcon} from '@mattermost/compass-icons/components';

import {Icon} from '@mattermost/compass-ui/components/icon';
import {IconButton} from '@mattermost/compass-ui/components/icon-button';

import CustomPromptsContent from './custom_prompts_content';

// The delete confirmation must sit above the host product header; matches the
// delete-agent dialog used elsewhere in the Agents product view.
const CONFIRM_Z_INDEX = 1100;

/**
 * Custom Prompts as a product page (the `prompts` view of the Agents product).
 * Same content and behavior as the modal, under page chrome. Fills the product
 * content outlet: the outlet is a flex column with min-height: 0, so this page
 * takes the remaining height and the list/form scroll inside it.
 */
const CustomPromptsPage = () => {
    const intl = useIntl();

    return (
        <CustomPromptsContent
            active={true}
            confirmZIndex={CONFIRM_Z_INDEX}
        >
            {({title, showBack, onBack, body}) => (
                <PageContainer>
                    <PageHeader>
                        {showBack && (
                            <BackButton
                                icon={<Icon glyph={<ArrowLeftIcon/>}/>}
                                size='medium'
                                onClick={onBack}
                                aria-label={intl.formatMessage({defaultMessage: 'Back to prompts'})}
                            />
                        )}
                        <PageTitle>{title}</PageTitle>
                    </PageHeader>
                    <PageBody>{body}</PageBody>
                </PageContainer>
            )}
        </CustomPromptsContent>
    );
};

const PageContainer = styled.div`
    display: flex;
    flex: 1 1 0;
    flex-direction: column;
    min-height: 0;
    width: 100%;
`;

const PageHeader = styled.div`
    display: flex;
    flex: 0 0 auto;
    align-items: center;
    gap: var(--spacing-xs);
    min-width: 0;
    padding: var(--spacing-xxxl) var(--spacing-xxxl) var(--spacing-l);
`;

const PageTitle = styled.h1`
    margin: 0;
    overflow: hidden;
    font-family: var(--font-family-heading);
    font-size: var(--font-size-500);
    font-weight: var(--font-weight-semibold);
    line-height: var(--line-height-500);
    color: var(--center-channel-color);
    text-overflow: ellipsis;
    white-space: nowrap;
`;

const BackButton = styled(IconButton)`
    flex-shrink: 0;
    margin-left: calc(-1 * var(--spacing-xs));
`;

// Definite-height flex parent for the content's height: 100% sheet.
const PageBody = styled.div`
    display: flex;
    flex: 1 1 0;
    flex-direction: column;
    min-height: 0;
`;

export default CustomPromptsPage;
