// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

import React, {useEffect} from 'react';
import {fireEvent, render, screen} from '@testing-library/react';
import {IntlProvider} from 'react-intl';

// In jest, babel-plugin-formatjs doesn't auto-fill the message id, so
// FormattedMessage throws without one. Substitute a literal renderer for the
// overlay label so we exercise the real component without a build-time plugin.
jest.mock('react-intl', () => {
    const actual = jest.requireActual('react-intl');
    return {
        ...actual,
        FormattedMessage: ({defaultMessage}: {defaultMessage: string}) => defaultMessage,
    };
});

// jsdom does not implement DataTransfer / DataTransferItemList; provide a
// minimal stand-in that mirrors the parts the component depends on so we can
// exercise the real DOM event flow.
class FakeFileList {
    private items: File[];
    constructor(items: File[]) {
        this.items = items;
    }
    get length() {
        return this.items.length;
    }
    item(i: number) {
        return this.items[i] ?? null;
    }
    [Symbol.iterator]() {
        return this.items[Symbol.iterator]();
    }
}

class FakeDataTransfer {
    private fileList: File[] = [];
    private typeList: string[] = [];
    private dataByType: Record<string, string> = {};
    dropEffect = 'none';
    effectAllowed = 'all';

    items = {
        add: (file: File) => {
            this.fileList.push(file);
            if (!this.typeList.includes('Files')) {
                this.typeList.push('Files');
            }
        },
    };

    get files() {
        return new FakeFileList(this.fileList);
    }

    get types() {
        return this.typeList;
    }

    setData(type: string, value: string) {
        this.dataByType[type] = value;
        if (!this.typeList.includes(type)) {
            this.typeList.push(type);
        }
    }

    getData(type: string) {
        return this.dataByType[type] ?? '';
    }
}

(globalThis as unknown as {DataTransfer: typeof FakeDataTransfer}).DataTransfer = FakeDataTransfer;

// eslint-disable-next-line import/first
import RhsFileDropZone from './rhs_file_drop_zone';

function renderZone(children: React.ReactNode = null) {
    return render(
        <IntlProvider locale='en'>
            <RhsFileDropZone>{children}</RhsFileDropZone>
        </IntlProvider>,
    );
}

function makeFileTransfer(files: File[]): DataTransfer {
    const dt = new DataTransfer();
    files.forEach((f) => dt.items.add(f));
    return dt;
}

// jsdom does not let production code assign to HTMLInputElement.files; replace
// the property with a spy-friendly accessor so we can observe what the drop
// zone forwarded to the editor's file input.
function InstrumentedFileInput({onFiles, onChange}: {
    onFiles: (files: FileList | null) => void;
    onChange?: () => void;
}) {
    const ref = React.useRef<HTMLInputElement>(null);
    useEffect(() => {
        const input = ref.current;
        if (!input) {
            return;
        }
        let storedFiles: FileList | null = null;
        Object.defineProperty(input, 'files', {
            configurable: true,
            get: () => storedFiles,
            set: (value: FileList | null) => {
                storedFiles = value;
                onFiles(value);
            },
        });
    }, [onFiles]);
    return (
        <input
            ref={ref}
            data-testid='nested-file-input'
            type='file'
            onChange={onChange}
        />
    );
}

describe('RhsFileDropZone', () => {
    test('hides overlay by default', () => {
        renderZone();
        const overlay = screen.getByTestId('rhs-file-drop-overlay');
        expect(overlay.getAttribute('aria-hidden')).toBe('true');
    });

    test('shows overlay while a file is dragged over', () => {
        renderZone();
        const zone = screen.getByTestId('rhs-file-drop-zone');

        fireEvent.dragEnter(zone, {dataTransfer: makeFileTransfer([new File(['x'], 'a.txt')])});

        expect(screen.getByTestId('rhs-file-drop-overlay').getAttribute('aria-hidden')).toBe('false');
    });

    test('ignores drags that do not carry files (e.g. text selection)', () => {
        renderZone();
        const zone = screen.getByTestId('rhs-file-drop-zone');

        const textDrag = new DataTransfer();
        textDrag.setData('text/plain', 'hello');
        fireEvent.dragEnter(zone, {dataTransfer: textDrag});

        expect(screen.getByTestId('rhs-file-drop-overlay').getAttribute('aria-hidden')).toBe('true');
    });

    test('hides overlay on dragleave once the counter unwinds', () => {
        renderZone();
        const zone = screen.getByTestId('rhs-file-drop-zone');
        const dt = makeFileTransfer([new File(['x'], 'a.txt')]);

        fireEvent.dragEnter(zone, {dataTransfer: dt});
        fireEvent.dragLeave(zone, {dataTransfer: dt});

        expect(screen.getByTestId('rhs-file-drop-overlay').getAttribute('aria-hidden')).toBe('true');
    });

    test('forwards dropped files to the nested file input and hides the overlay', () => {
        const onFiles = jest.fn();
        const onChange = jest.fn();
        renderZone(
            <InstrumentedFileInput
                onFiles={onFiles}
                onChange={onChange}
            />,
        );
        const zone = screen.getByTestId('rhs-file-drop-zone');
        const file = new File(['hello'], 'hello.txt', {type: 'text/plain'});

        fireEvent.dragEnter(zone, {dataTransfer: makeFileTransfer([file])});
        fireEvent.drop(zone, {dataTransfer: makeFileTransfer([file])});

        expect(onFiles).toHaveBeenCalledTimes(1);
        const forwarded = onFiles.mock.calls[0][0] as FileList;
        expect(forwarded).not.toBeNull();
        expect(forwarded.length).toBe(1);
        expect(forwarded.item(0)?.name).toBe('hello.txt');
        expect(onChange).toHaveBeenCalledTimes(1);
        expect(screen.getByTestId('rhs-file-drop-overlay').getAttribute('aria-hidden')).toBe('true');
    });

    test('does nothing on drops without files', () => {
        const onFiles = jest.fn();
        const onChange = jest.fn();
        renderZone(
            <InstrumentedFileInput
                onFiles={onFiles}
                onChange={onChange}
            />,
        );
        const zone = screen.getByTestId('rhs-file-drop-zone');

        const textDrop = new DataTransfer();
        textDrop.setData('text/plain', 'just text');
        fireEvent.drop(zone, {dataTransfer: textDrop});

        expect(onFiles).not.toHaveBeenCalled();
        expect(onChange).not.toHaveBeenCalled();
    });

    test('silently no-ops when no file input is rendered yet', () => {
        renderZone();
        const zone = screen.getByTestId('rhs-file-drop-zone');
        const file = new File(['hello'], 'hello.txt');

        expect(() => {
            fireEvent.drop(zone, {dataTransfer: makeFileTransfer([file])});
        }).not.toThrow();
        expect(screen.getByTestId('rhs-file-drop-overlay').getAttribute('aria-hidden')).toBe('true');
    });

    test('forwards files when the editor file input is nested several levels deep', () => {
        // AdvancedTextEditor wraps its <input type="file"> several layers deep;
        // a shallow selector would silently regress and break the bug fix.
        const onFiles = jest.fn();
        renderZone(
            <div>
                <section>
                    <div className='inner-wrapper'>
                        <InstrumentedFileInput onFiles={onFiles}/>
                    </div>
                </section>
            </div>,
        );
        const zone = screen.getByTestId('rhs-file-drop-zone');
        const file = new File(['a'], 'deeply-nested.txt');

        fireEvent.drop(zone, {dataTransfer: makeFileTransfer([file])});

        expect(onFiles).toHaveBeenCalledTimes(1);
        const forwarded = onFiles.mock.calls[0][0] as FileList;
        expect(forwarded.item(0)?.name).toBe('deeply-nested.txt');
    });

    test('preserves order when multiple files are dropped at once', () => {
        const onFiles = jest.fn();
        renderZone(<InstrumentedFileInput onFiles={onFiles}/>);
        const zone = screen.getByTestId('rhs-file-drop-zone');
        const files = [
            new File(['a'], 'a.txt'),
            new File(['b'], 'b.png'),
            new File(['c'], 'c.pdf'),
        ];

        fireEvent.drop(zone, {dataTransfer: makeFileTransfer(files)});

        const forwarded = onFiles.mock.calls[0][0] as FileList;
        expect(forwarded.length).toBe(3);
        expect(forwarded.item(0)?.name).toBe('a.txt');
        expect(forwarded.item(1)?.name).toBe('b.png');
        expect(forwarded.item(2)?.name).toBe('c.pdf');
    });

    test('forwards to the first file input when multiple are present', () => {
        const onFirst = jest.fn();
        const onSecond = jest.fn();
        renderZone(
            <>
                <InstrumentedFileInput onFiles={onFirst}/>
                <InstrumentedFileInput onFiles={onSecond}/>
            </>,
        );
        const zone = screen.getByTestId('rhs-file-drop-zone');
        const file = new File(['a'], 'a.txt');

        fireEvent.drop(zone, {dataTransfer: makeFileTransfer([file])});

        expect(onFirst).toHaveBeenCalledTimes(1);
        expect(onSecond).not.toHaveBeenCalled();
    });

    test('calls preventDefault on dragover so the browser does not navigate to the file', () => {
        // Without preventDefault on dragover/drop, the browser opens dropped
        // files in the tab — the user-visible failure mode is far worse than
        // "drop is ignored".
        renderZone();
        const zone = screen.getByTestId('rhs-file-drop-zone');

        const dt = makeFileTransfer([new File(['x'], 'a.txt')]);
        const overReturn = fireEvent.dragOver(zone, {dataTransfer: dt});
        const dropReturn = fireEvent.drop(zone, {dataTransfer: dt});

        // fireEvent returns false when any handler called preventDefault.
        expect(overReturn).toBe(false);
        expect(dropReturn).toBe(false);
    });

    test('keeps the overlay visible until every nested dragleave unwinds', () => {
        // The dragCounter exists for this case: browsers fire dragenter/leave
        // per element entered, so a naive boolean toggle flickers the overlay
        // when the cursor moves across children.
        renderZone(<div data-testid='inner'>{'inner'}</div>);
        const zone = screen.getByTestId('rhs-file-drop-zone');
        const dt = makeFileTransfer([new File(['x'], 'a.txt')]);

        fireEvent.dragEnter(zone, {dataTransfer: dt});
        fireEvent.dragEnter(zone, {dataTransfer: dt}); // crossed into nested child
        expect(screen.getByTestId('rhs-file-drop-overlay').getAttribute('aria-hidden')).toBe('false');

        fireEvent.dragLeave(zone, {dataTransfer: dt}); // left nested child only
        expect(screen.getByTestId('rhs-file-drop-overlay').getAttribute('aria-hidden')).toBe('false');

        fireEvent.dragLeave(zone, {dataTransfer: dt}); // left outer zone
        expect(screen.getByTestId('rhs-file-drop-overlay').getAttribute('aria-hidden')).toBe('true');
    });
});
