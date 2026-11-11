// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import fs from 'fs';
import path from 'path';

import {generativeUIPrompt} from './library';

const TEMPLATE_PATH = path.resolve(__dirname, '../../../../prompts/generative_ui_library.tmpl');

const HEADER = '{{/* Generated from webapp/src/components/generative_ui/library.tsx by `npm run generate-genui-prompt`. DO NOT EDIT. */}}\n';

// The prompt is Go template source, so literal braces must not start actions.
const toGoTemplate = (prompt: string) => HEADER + prompt.replaceAll('{{', '{{"{{"}}') + '\n';

describe('generative UI prompt', () => {
    it('matches the server prompt template generated from the component library', () => {
        const expected = toGoTemplate(generativeUIPrompt());
        // eslint-disable-next-line no-process-env -- opt-in regeneration switch set by `npm run generate-genui-prompt`
        if (process.env.UPDATE_GENERATIVE_UI_PROMPT === '1') {
            fs.writeFileSync(TEMPLATE_PATH, expected);
        }

        const actual = fs.readFileSync(TEMPLATE_PATH, 'utf8');
        expect(actual).toBe(expected);
    });
});
