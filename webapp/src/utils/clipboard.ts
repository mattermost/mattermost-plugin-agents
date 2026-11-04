// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Set on the DOM node (not JSX) so the temporary fallback textarea never shifts layout.
const OFFSCREEN_TEXTAREA_STYLE: Partial<CSSStyleDeclaration> = {
    position: 'fixed',
    top: '0',
    left: '0',
    width: '1px',
    height: '1px',
    opacity: '0',
    pointerEvents: 'none',
};

// Copies text, falling back to a hidden textarea where the Clipboard API is missing or denied
// (insecure context, permissions). Resolves whether it worked.
export async function copyToClipboard(text: string): Promise<boolean> {
    try {
        if (navigator.clipboard?.writeText) {
            await navigator.clipboard.writeText(text);
            return true;
        }
    } catch {
        // Fall through to the textarea fallback (permission denied, insecure context).
    }
    return copyWithTextarea(text);
}

function copyWithTextarea(text: string): boolean {
    const previouslyFocused = document.activeElement as HTMLElement | null;
    const selection = document.getSelection();
    const savedRanges = Array.from({length: selection?.rangeCount ?? 0}, (_, i) => selection!.getRangeAt(i));

    const textarea = document.createElement('textarea');
    textarea.value = text;
    textarea.setAttribute('readonly', '');
    textarea.setAttribute('aria-hidden', 'true');
    textarea.setAttribute('tabindex', '-1');
    textarea.setAttribute('data-copy-fallback', 'true');
    Object.assign(textarea.style, OFFSCREEN_TEXTAREA_STYLE);
    document.body.appendChild(textarea);

    let ok = false;
    try {
        textarea.select();
        ok = typeof document.execCommand === 'function' && document.execCommand('copy');
    } catch {
        ok = false;
    }

    document.body.removeChild(textarea);
    if (selection) {
        selection.removeAllRanges();
        savedRanges.forEach((range) => selection.addRange(range));
    }
    previouslyFocused?.focus?.();
    return ok;
}
