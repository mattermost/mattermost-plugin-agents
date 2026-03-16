// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect, useMemo, useState} from 'react';
import {createPortal} from 'react-dom';
import styled from 'styled-components';

import AskChannelButton from './ask_channel_button';

const PortalSlot = styled.div`
    display: flex;
    align-items: center;
    margin-right: 4px;
`;

const HEADER_REGION_SELECTORS = [
    '#channelHeaderInfo',
    '[aria-label="channel header region"]',
];

const VIEW_INFO_BUTTON_SELECTORS = [
    'button[aria-label="View Info"]',
    'button[title="View Info"]',
];

const getHeaderRegion = (): HTMLElement | null => {
    for (const selector of HEADER_REGION_SELECTORS) {
        const element = document.querySelector(selector);
        if (element instanceof HTMLElement) {
            return element;
        }
    }

    return null;
};

const getViewInfoButton = (headerRegion: HTMLElement): HTMLButtonElement | null => {
    for (const selector of VIEW_INFO_BUTTON_SELECTORS) {
        const element = headerRegion.querySelector(selector);
        if (element instanceof HTMLButtonElement) {
            return element;
        }
    }

    return null;
};

const createHostElement = (): HTMLDivElement => {
    const host = document.createElement('div');
    host.setAttribute('data-testid', 'ask-channel-button-fallback-host');

    return host;
};

const mountHostElement = (host: HTMLDivElement): boolean => {
    const headerRegion = getHeaderRegion();
    if (!headerRegion) {
        return false;
    }

    const viewInfoButton = getViewInfoButton(headerRegion);
    const targetParent = viewInfoButton?.parentElement ?? headerRegion;
    if (!targetParent) {
        return false;
    }

    if (host.parentElement !== targetParent) {
        host.remove();

        if (viewInfoButton) {
            targetParent.insertBefore(host, viewInfoButton);
        } else {
            targetParent.appendChild(host);
        }
    }

    return host.isConnected;
};

const ChannelHeaderAskChannelPortal = () => {
    const host = useMemo(() => createHostElement(), []);
    const [isMounted, setIsMounted] = useState(false);

    useEffect(() => {
        const syncHost = () => {
            setIsMounted(mountHostElement(host));
        };

        syncHost();

        const observer = new MutationObserver(() => {
            syncHost();
        });

        observer.observe(document.body, {
            childList: true,
            subtree: true,
        });

        window.addEventListener('resize', syncHost);

        return () => {
            observer.disconnect();
            window.removeEventListener('resize', syncHost);
            host.remove();
        };
    }, [host]);

    if (!isMounted) {
        return null;
    }

    return createPortal(
        <PortalSlot>
            <AskChannelButton/>
        </PortalSlot>,
        host,
    );
};

export default ChannelHeaderAskChannelPortal;
