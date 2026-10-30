// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React from 'react';

// Pretext (end of the text) -> completion the fake suggestion list offers.
export const FAKE_SUGGESTIONS: Record<string, string> = {
    '@ma': '@matty ',
    ':smi': ':smile: ',
    '~to': '~town-square ',
};

// Typed faster than the list loaded: the host stops the Enter, passes it on
// anyway, then cancels it (the pretext-mismatch branch of handleKeyDown).
export const FAKE_STALE_PRETEXT = '@lag';

type Props = {
    id: string;
    channelId: string;
    rootId?: string;
    value: string;
    createMessage: string;
    onChange: (e: {target: {value: string}}) => void;
    onKeyDown?: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
    onKeyPress: (e: React.KeyboardEvent<HTMLTextAreaElement>) => void;
    onPaste?: (e: React.ClipboardEvent<HTMLTextAreaElement>) => void;
    [prop: string]: unknown;
};

// Every props object the fake rendered with, newest last.
export const fakeHostTextboxRenders: Props[] = [];

// Stands in for the host Textbox (webapp/channels/src/components/textbox):
// the same instance methods (focus, getInputBox), a textarea with the host's
// role, and a suggestion list that opens on a FAKE_SUGGESTIONS pretext. While
// it is open, Enter or Tab picks the suggestion and is stopped before it
// reaches onKeyDown, as SuggestionBox does (suggestion_box.tsx handleKeyDown).
export class FakeHostTextbox extends React.Component<Props> {
    input = React.createRef<HTMLTextAreaElement>();

    focus() {
        this.input.current?.focus();
    }

    getInputBox() {
        return this.input.current;
    }

    pretext() {
        return Object.keys(FAKE_SUGGESTIONS).find((p) => this.props.value.endsWith(p));
    }

    handleKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
        if (this.props.value.endsWith(FAKE_STALE_PRETEXT) && e.key === 'Enter') {
            e.stopPropagation();
            this.props.onKeyDown?.(e);
            e.preventDefault();
            return;
        }
        const pretext = this.pretext();
        if (pretext && (e.key === 'Enter' || e.key === 'Tab') && !e.ctrlKey && !e.metaKey) {
            e.stopPropagation();
            this.props.onChange({target: {value: this.props.value.slice(0, -pretext.length) + FAKE_SUGGESTIONS[pretext]}});
            e.preventDefault();
            return;
        }
        this.props.onKeyDown?.(e);
    };

    render() {
        fakeHostTextboxRenders.push(this.props);
        const pretext = this.pretext();
        return (
            <div
                className='textarea-wrapper'
                data-testid='fake-host-textbox'
            >
                {pretext && (
                    <ul
                        role='listbox'
                        aria-label='Suggestions'
                    >
                        <li
                            role='option'
                            aria-selected={true}
                        >
                            {FAKE_SUGGESTIONS[pretext].trim()}
                        </li>
                    </ul>
                )}
                <textarea
                    ref={this.input}
                    id={this.props.id}
                    role='textbox'
                    placeholder={this.props.createMessage}
                    value={this.props.value}
                    onChange={(e) => this.props.onChange(e)}
                    onKeyDown={this.handleKeyDown}
                    onKeyPress={this.props.onKeyPress}
                    onPaste={this.props.onPaste}
                />
            </div>
        );
    }
}
