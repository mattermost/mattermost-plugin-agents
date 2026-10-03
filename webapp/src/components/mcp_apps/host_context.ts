// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

function rootElement(doc: Document): Element {
    return doc.getElementById('root') ?? doc.body;
}

function parseColorComponents(value: string): {r: number; g: number; b: number} | null {
    const trimmed = value.trim();
    if (!trimmed) {
        return null;
    }

    const hexMatch = trimmed.match(/^#([0-9a-f]{3}|[0-9a-f]{6})$/i);
    if (hexMatch) {
        const hex = hexMatch[1];
        if (hex.length === 3) {
            return {
                r: parseInt(hex[0] + hex[0], 16),
                g: parseInt(hex[1] + hex[1], 16),
                b: parseInt(hex[2] + hex[2], 16),
            };
        }
        return {
            r: parseInt(hex.slice(0, 2), 16),
            g: parseInt(hex.slice(2, 4), 16),
            b: parseInt(hex.slice(4, 6), 16),
        };
    }

    const rgbMatch = trimmed.match(/^rgba?\(\s*(\d+)\s*,\s*(\d+)\s*,\s*(\d+)/i);
    if (rgbMatch) {
        return {
            r: Number(rgbMatch[1]),
            g: Number(rgbMatch[2]),
            b: Number(rgbMatch[3]),
        };
    }

    return null;
}

export function resolveAppTheme(doc: Document = document): 'light' | 'dark' {
    const style = doc.defaultView?.getComputedStyle(rootElement(doc));
    if (!style) {
        return 'light';
    }
    const bg = style.getPropertyValue('--center-channel-bg');
    const components = parseColorComponents(bg);
    if (!components) {
        return 'light';
    }
    const luminance = (0.2126 * components.r) + (0.7152 * components.g) + (0.0722 * components.b);
    return luminance < 128 ? 'dark' : 'light';
}

export function buildHostStyleVariables(doc: Document = document): Record<string, string> {
    const style = doc.defaultView?.getComputedStyle(rootElement(doc));
    if (!style) {
        return {};
    }

    const variables: Record<string, string> = {};
    const centerBg = style.getPropertyValue('--center-channel-bg').trim();
    const centerColor = style.getPropertyValue('--center-channel-color').trim();
    const centerColorRGB = style.getPropertyValue('--center-channel-color-rgb').trim();
    const buttonBg = style.getPropertyValue('--button-bg').trim();
    const buttonColor = style.getPropertyValue('--button-color').trim();
    const fontFamily = style.fontFamily?.trim();

    if (centerBg) {
        variables['--color-background-primary'] = centerBg;
    }
    if (centerColorRGB) {
        variables['--color-background-secondary'] = `rgba(${centerColorRGB}, 0.04)`;
        variables['--color-text-secondary'] = `rgba(${centerColorRGB}, 0.72)`;
        variables['--color-border-primary'] = `rgba(${centerColorRGB}, 0.16)`;
    }
    if (centerColor) {
        variables['--color-text-primary'] = centerColor;
    }
    if (buttonBg) {
        variables['--color-background-info'] = buttonBg;
    }
    if (buttonColor) {
        variables['--color-text-inverse'] = buttonColor;
    }
    if (fontFamily) {
        variables['--font-sans'] = fontFamily;
    }

    return variables;
}
