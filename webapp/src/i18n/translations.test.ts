// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import {isPluralElement, isSelectElement, isTagElement, parse, MessageFormatElement} from '@formatjs/icu-messageformat-parser';
import {createIntl} from 'react-intl';

import en from './en.json';

/**
 * These guard the translation files themselves rather than any component. The
 * plugin registers them with the host webapp, and a registered message wins
 * over the `defaultMessage` compiled into the bundle, so en.json — not the
 * source string next to the component — is what readers actually see. Every
 * other test in this repo renders the default, which means a broken message
 * here would ship while the whole suite stayed green.
 */
const messages: Record<string, string> = en;

/** Silences react-intl's console reporting; these tests assert on the output. */
function noop() {
    // Intentionally empty.
}

function pluralElements(elements: MessageFormatElement[]): MessageFormatElement[] {
    const found: MessageFormatElement[] = [];
    for (const element of elements) {
        if (isPluralElement(element)) {
            found.push(element);
        }
        if (isPluralElement(element) || isSelectElement(element)) {
            for (const option of Object.values(element.options)) {
                found.push(...pluralElements(option.value));
            }
        }
        if (isTagElement(element)) {
            found.push(...pluralElements(element.children));
        }
    }
    return found;
}

describe('en.json', () => {
    const entries = Object.entries(messages);

    test('is not empty', () => {
        expect(entries.length).toBeGreaterThan(0);
    });

    test('parses as ICU throughout', () => {
        const unparseable = entries.
            filter(([, message]) => {
                try {
                    parse(message);
                    return false;
                } catch {
                    return true;
                }
            }).
            map(([id]) => id);

        expect(unparseable).toEqual([]);
    });

    // English distinguishes one from other. ICU has no error for a missing
    // category — it silently falls back to `other`, so a message that lost its
    // `one` arm renders "1 tools" with no warning anywhere.
    test('every cardinal plural declares a "one" category', () => {
        const missing = entries.
            filter(([, message]) => {
                return pluralElements(parse(message)).
                    some((element) => isPluralElement(element) &&
                        element.pluralType === 'cardinal' &&
                        !('one' in element.options));
            }).
            map(([id]) => id);

        expect(missing).toEqual([]);
    });

    // The same guarantee stated as output rather than structure: formatting the
    // shipped message for a count of one must not produce "1 things". A noun
    // that is singular but ends in s would trip this; none exist today, and the
    // failure names the message, so that is cheap to handle if one appears.
    test('formats a count of one without a plural noun', () => {
        const intl = createIntl({locale: 'en', messages, onError: noop});
        const plurals = entries.filter(([, message]) => message.includes('plural'));
        const misread = plurals.
            map(([id]) => [id, intl.formatMessage({id}, {count: 1})]).
            filter(([, formatted]) => (/\b1 \w+s\b/).test(formatted));

        expect(plurals.length).toBeGreaterThan(0);
        expect(misread).toEqual([]);
    });
});
