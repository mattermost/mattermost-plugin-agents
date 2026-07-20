// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';
import styled from 'styled-components';

import {getProfilePictureUrl} from '@/client';

type AvatarSize = 'small' | 'default';

const SIZES: Record<AvatarSize, number> = {
    small: 16,
    default: 24,
};

type Props = {
    userId: string;
    size?: AvatarSize;
};

const Avatar = ({userId, size = 'default'}: Props) => (
    <Image
        $size={size}
        src={getProfilePictureUrl(userId, 0)}
        alt=''
        aria-hidden={true}
    />
);

const Image = styled.img<{$size: AvatarSize}>`
    width: ${(p) => SIZES[p.$size]}px;
    height: ${(p) => SIZES[p.$size]}px;
    border-radius: 50%;
    flex-shrink: 0;
`;

export default Avatar;
