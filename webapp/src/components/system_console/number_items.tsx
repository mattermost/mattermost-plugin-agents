// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useState, useEffect, ChangeEvent} from 'react';

import {TextItem} from './item';

interface NumberItemProps {
    label: string;
    value: number | undefined;
    onChange: (value: number) => void;
    min?: number;
    max?: number;
    allowEmpty?: boolean;
    defaultValue?: number;
    placeholder?: string;
    helptext?: string;
    clampOnChange?: boolean;
    onBlur?: (e: React.FocusEvent<HTMLInputElement>) => void;
    onFocus?: (e: React.FocusEvent<HTMLInputElement>) => void;
}

export const IntItem: React.FC<NumberItemProps> = ({
    value,
    onChange,
    min,
    max,
    allowEmpty = false,
    defaultValue = 0,
    clampOnChange = true,
    onFocus,
    onBlur,
    ...restProps
}) => {
    const [textValue, setTextValue] = useState('');
    const [isFocused, setIsFocused] = useState(false);

    // Initialize and update the text value when the number value changes
    useEffect(() => {
        if (isFocused) {
            return;
        }

        // Only update if value is a number
        if (typeof value === 'number' && !isNaN(value)) {
            setTextValue(value.toString());
        } else if (typeof value !== 'number' && allowEmpty) {
            setTextValue('');
        }
    }, [value, allowEmpty, isFocused]);

    const handleChange = (e: ChangeEvent<HTMLInputElement>) => {
        const newValue = e.target.value;

        // Always update the displayed text
        setTextValue(newValue);

        // If it's empty and we allow empty, call with undefined
        if (newValue === '' && allowEmpty) {
            onChange(defaultValue);
            return;
        }

        // Parse the value
        const parsedValue = parseInt(newValue, 10);

        // Only call onChange if the value is a valid number
        if (!isNaN(parsedValue)) {
            // Apply constraints if specified
            let constrainedValue = parsedValue;
            if (clampOnChange && typeof min === 'number' && parsedValue < min) {
                constrainedValue = min;
            }
            if (clampOnChange && typeof max === 'number' && parsedValue > max) {
                constrainedValue = max;
            }
            onChange(constrainedValue);
        }
    };

    const handleFocus = (e: React.FocusEvent<HTMLInputElement>) => {
        setIsFocused(true);
        onFocus?.(e);
    };

    const handleBlur = (e: React.FocusEvent<HTMLInputElement>) => {
        setIsFocused(false);
        onBlur?.(e);
    };

    return (
        <TextItem
            type='text'
            {...restProps}
            value={textValue}
            onChange={handleChange}
            onFocus={handleFocus}
            onBlur={handleBlur}
        />
    );
};

export const FloatItem: React.FC<NumberItemProps> = ({
    value,
    onChange,
    min,
    max,
    allowEmpty = false,
    defaultValue = 0,
    ...restProps
}) => {
    const [textValue, setTextValue] = useState('');

    // Initialize and update the text value when the number value changes
    useEffect(() => {
        // Only update if value is a number
        if (typeof value === 'number' && !isNaN(value)) {
            setTextValue(value.toString());
        } else if (typeof value !== 'number' && allowEmpty) {
            setTextValue('');
        }
    }, [value, allowEmpty]);

    const handleChange = (e: ChangeEvent<HTMLInputElement>) => {
        const newValue = e.target.value;

        // Always update the displayed text
        setTextValue(newValue);

        // If it's empty and we allow empty, call with undefined
        if (newValue === '' && allowEmpty) {
            onChange(defaultValue);
            return;
        }

        // Accept values like "1." or "1.2" during typing
        if (newValue.match(/^\d*\.?\d*$/)) {
            // Only call onChange for valid floats
            // If it's a partial float like "1." we don't trigger onChange yet
            if (newValue.match(/^\d+(\.\d+)?$/)) {
                const parsedValue = parseFloat(newValue);

                // Apply constraints if specified
                let constrainedValue = parsedValue;
                if (typeof min === 'number' && parsedValue < min) {
                    constrainedValue = min;
                }
                if (typeof max === 'number' && parsedValue > max) {
                    constrainedValue = max;
                }
                onChange(constrainedValue);
            }
        }
    };

    return (
        <TextItem
            type='text'
            value={textValue}
            onChange={handleChange}
            {...restProps}
        />
    );
};