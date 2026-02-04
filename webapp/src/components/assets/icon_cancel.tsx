// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

import Svg from '../svg';

type Props = {
    size?: number;
};

const IconCancel = ({size = 20}: Props) => (
    <Svg
        width={size}
        height={size}
        viewBox='0 0 10 10'
        fill='none'
        xmlns='http://www.w3.org/2000/svg'
    >
        <path
            d='M9.2048 1.6546L5.8496 5.0098L9.2048 8.365L8.3552 9.2146L5 5.8594L1.6448 9.2146L0.7952 8.365L4.1504 5.0098L0.7952 1.6546L1.6448 0.805005L5 4.1602L8.3552 0.805005L9.2048 1.6546Z'
            fill='currentColor'
        />
    </Svg>
);

export default IconCancel;
