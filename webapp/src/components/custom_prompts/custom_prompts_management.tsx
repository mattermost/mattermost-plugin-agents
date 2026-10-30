// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useCallback} from 'react';
import styled from 'styled-components';
import {useIntl} from 'react-intl';
import {useSelector, useDispatch} from 'react-redux';

import {Modal} from '@mattermost/compass-ui/components/modal';

import {getShowCustomPromptsModal} from '@/selectors';
import {ShowCustomPromptsModalHandler} from '@/redux';

import {AnimatedModalShell, MODAL_SHEET_CLASS} from '@/components/animated_modal_shell';

import CustomPromptsContent from './custom_prompts_content';

const Sheet = styled.div`
    display: flex;
    max-width: calc(100vw - 32px);
`;

const PromptsModal = styled(Modal)`
    && {
        height: 80vh;
        max-width: 100%;
    }
`;

const CustomPromptsManagement = () => {
    const intl = useIntl();
    const dispatch = useDispatch();
    const show = useSelector(getShowCustomPromptsModal);

    const handleClose = useCallback(() => {
        dispatch({type: ShowCustomPromptsModalHandler, show: false});
    }, [dispatch]);

    const handleModalClick = (e: React.MouseEvent) => {
        e.stopPropagation();
    };

    return (
        <CustomPromptsContent active={show}>
            {({title, showBack, onBack, body}) => (
                <AnimatedModalShell
                    show={show}
                    onBackdropClick={handleClose}
                    zIndex={2000}
                >
                    {/* eslint-disable-next-line agents/click-events-have-key-events, agents/no-static-element-interactions -- only stops clicks inside the sheet from reaching the backdrop */}
                    <Sheet
                        className={MODAL_SHEET_CLASS}
                        onClick={handleModalClick}
                    >
                        <PromptsModal
                            open={show}
                            closeOnEscape={false}
                            size='medium'
                            title={title}
                            onClose={handleClose}
                            closeLabel={intl.formatMessage({defaultMessage: 'Close'})}
                            showBackButton={showBack}
                            onBack={onBack}
                            backLabel={intl.formatMessage({defaultMessage: 'Back to prompts'})}
                            headerDivider={false}
                            bodyPadding='none'
                            scrollable={false}
                        >
                            {body}
                        </PromptsModal>
                    </Sheet>
                </AnimatedModalShell>
            )}
        </CustomPromptsContent>
    );
};

export default CustomPromptsManagement;
