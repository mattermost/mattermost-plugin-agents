// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Validation for window.mattermost.download(). Everything in the request comes
// from the artifact and is untrusted: the host rebuilds the file with an
// allowlisted type and a sanitized name before the viewer is asked to save it.

// Keep in sync with MAX_DOWNLOAD_BYTES in api/artifact_bridge.js, which checks
// the size before the data is cloned to the host.
export const MAX_ARTIFACT_DOWNLOAD_BYTES = 25 * 1024 * 1024;

// A data: URL carries base64 (a third larger than its bytes) or %XX escapes
// (three times larger). Longer strings are rejected before they are decoded or
// encoded.
export const MAX_ARTIFACT_DOWNLOAD_BASE64_LENGTH = (Math.ceil(MAX_ARTIFACT_DOWNLOAD_BYTES / 3) * 4) + 4096;
export const MAX_ARTIFACT_DOWNLOAD_STRING_LENGTH = (MAX_ARTIFACT_DOWNLOAD_BYTES * 3) + 4096;

const MAX_FILENAME_INPUT_LENGTH = 4096;
const MAX_MIME_TYPE_LENGTH = 255;

export const MAX_DOWNLOAD_FILENAME_LENGTH = 120;
export const DEFAULT_DOWNLOAD_NAME = 'download';

// The only types an artifact can save, with their extensions: the first one is
// added to a name that has none of them. Nothing a browser or OS would render
// or run as active content (HTML, SVG, XML, scripts, executables).
export const DOWNLOAD_MIME_TYPES: ReadonlyMap<string, readonly string[]> = new Map([
    ['image/png', ['png']],
    ['image/jpeg', ['jpg', 'jpeg']],
    ['image/gif', ['gif']],
    ['image/webp', ['webp']],
    ['text/plain', ['txt']],
    ['text/csv', ['csv']],
    ['text/markdown', ['md', 'markdown']],
    ['application/json', ['json']],
    ['application/pdf', ['pdf']],
]);

const MIME_TYPE_ALIASES: ReadonlyMap<string, string> = new Map([
    ['image/jpg', 'image/jpeg'],
]);

export type ArtifactDownloadErrorCode = 'invalid_params' | 'unsupported_type' | 'too_large';

export interface ArtifactDownload {
    blob: Blob;
    filename: string;
    mimeType: string;
}

export type ArtifactDownloadResult =
    {ok: true; download: ArtifactDownload} |
    {ok: false; code: ArtifactDownloadErrorCode; message: string};

// Returns the allowlisted type for raw (parameters such as charset dropped),
// or null.
export function normalizeDownloadMimeType(raw: string): string | null {
    if (raw.length > MAX_MIME_TYPE_LENGTH) {
        return null;
    }
    const base = raw.split(';')[0].trim().toLowerCase();
    const mimeType = MIME_TYPE_ALIASES.get(base) ?? base;
    return DOWNLOAD_MIME_TYPES.has(mimeType) ? mimeType : null;
}

// Returns a plain file name ending in an extension of mimeType, which must be
// allowlisted. Path separators, characters Windows rejects, control and format
// characters (bidi overrides, zero-width) are removed or replaced, and the
// name is capped at MAX_DOWNLOAD_FILENAME_LENGTH characters.
export function sanitizeDownloadFilename(raw: unknown, mimeType: string): string {
    const extensions = DOWNLOAD_MIME_TYPES.get(mimeType);
    if (!extensions) {
        throw new Error(`unsupported download type: ${mimeType}`);
    }
    const name = (typeof raw === 'string' ? raw.slice(0, MAX_FILENAME_INPUT_LENGTH) : '').
        replace(/[\p{Cc}\p{Cf}]/gu, '').
        replace(/[/\\<>:"|?*]/g, '_').
        replace(/\s+/g, ' ').
        replace(/^[\s.]+|[\s.]+$/g, '');

    let stem = name;
    let extension = extensions[0];
    const dot = name.lastIndexOf('.');
    if (dot > 0 && extensions.includes(name.slice(dot + 1).toLowerCase())) {
        stem = name.slice(0, dot);
        extension = name.slice(dot + 1).toLowerCase();
    }

    // Code points, so a surrogate pair is never cut in half.
    stem = Array.from(stem).slice(0, MAX_DOWNLOAD_FILENAME_LENGTH - extension.length - 1).join('').replace(/[\s.]+$/, '');
    return `${stem || DEFAULT_DOWNLOAD_NAME}.${extension}`;
}

// A data: URL with a well-formed header. Other strings, even ones starting
// with "data:", are file content.
const DATA_URL_RE = /^data:([a-z0-9!#$&^_.+-]+\/[a-z0-9!#$&^_.+-]+)?((?:;[a-z0-9!#$&^_.+-]+=[^;,]*)*)(;base64)?,/i;

const isHexDigit = (c: number) => (c >= 48 && c <= 57) || (c >= 65 && c <= 70) || (c >= 97 && c <= 102);

// Bytes of a data: URL body that is not base64: %XX escapes, and every other
// character as UTF-8. Returns null for a lone surrogate, and stops one byte
// past the size cap.
function percentDecode(text: string): Uint8Array<ArrayBuffer> | null {
    const out = new Uint8Array(Math.min(text.length * 3, MAX_ARTIFACT_DOWNLOAD_BYTES + 1));
    let n = 0;
    const put = (...bytes: number[]) => {
        for (const b of bytes) {
            if (n === out.length) {
                return false;
            }
            out[n++] = b;
        }
        return true;
    };
    for (let i = 0; i < text.length; i++) {
        const c = text.charCodeAt(i);
        let ok: boolean;
        if (c === 0x25 && isHexDigit(text.charCodeAt(i + 1)) && isHexDigit(text.charCodeAt(i + 2))) {
            ok = put(parseInt(text.slice(i + 1, i + 3), 16));
            i += 2;
        } else if (c < 0x80) {
            ok = put(c);
        } else if (c < 0x800) {
            ok = put(0xc0 | (c >> 6), 0x80 | (c & 0x3f));
        } else if (c >= 0xd800 && c <= 0xdbff && (text.charCodeAt(i + 1) & 0xfc00) === 0xdc00) {
            const cp = 0x10000 + ((c - 0xd800) << 10) + (text.charCodeAt(i + 1) - 0xdc00);
            ok = put(0xf0 | (cp >> 18), 0x80 | ((cp >> 12) & 0x3f), 0x80 | ((cp >> 6) & 0x3f), 0x80 | (cp & 0x3f));
            i++;
        } else if (c >= 0xd800 && c <= 0xdfff) {
            return null;
        } else {
            ok = put(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 0x3f), 0x80 | (c & 0x3f));
        }
        if (!ok) {
            break;
        }
    }
    return out.subarray(0, n);
}

function base64Decode(text: string): Uint8Array<ArrayBuffer> | null {
    let binary: string;
    try {
        binary = atob(text);
    } catch {
        return null;
    }
    const out = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) {
        out[i] = binary.charCodeAt(i);
    }
    return out;
}

type DownloadFailure = Extract<ArtifactDownloadResult, {ok: false}>;
type DecodedData = {part: BlobPart; size: number; declaredType: string} | DownloadFailure;

function fail(code: ArtifactDownloadErrorCode, message: string): DownloadFailure {
    return {ok: false, code, message};
}

const tooLarge = () => fail('too_large', `The file is larger than the ${MAX_ARTIFACT_DOWNLOAD_BYTES / (1024 * 1024)} MB download limit`);

function decodeData(data: unknown): DecodedData {
    if (typeof data === 'string') {
        if (data.length > MAX_ARTIFACT_DOWNLOAD_STRING_LENGTH) {
            return tooLarge();
        }
        const match = DATA_URL_RE.exec(data);
        if (!match) {
            // Every UTF-16 code unit is at least one byte of UTF-8.
            if (data.length > MAX_ARTIFACT_DOWNLOAD_BYTES) {
                return tooLarge();
            }
            const text = new Blob([data]);
            return {part: text, size: text.size, declaredType: ''};
        }
        const body = data.slice(match[0].length);
        if (match[3] && body.length > MAX_ARTIFACT_DOWNLOAD_BASE64_LENGTH) {
            return tooLarge();
        }
        const bytes = match[3] ? base64Decode(body) : percentDecode(body);
        if (!bytes) {
            return fail('invalid_params', 'data is not a valid data: URL');
        }
        return {part: bytes, size: bytes.byteLength, declaredType: match[1] ?? 'text/plain'};
    }
    if (data instanceof Blob) {
        return {part: data, size: data.size, declaredType: data.type};
    }
    if (data instanceof ArrayBuffer) {
        return {part: data, size: data.byteLength, declaredType: ''};
    }
    if (ArrayBuffer.isView(data)) {
        return {part: new Uint8Array(data.buffer, data.byteOffset, data.byteLength).slice(), size: data.byteLength, declaredType: ''};
    }
    return fail('invalid_params', 'data must be a Blob, an ArrayBuffer, a typed array, or a string');
}

// prepareArtifactDownload validates the params of a download request and
// rebuilds the file with the allowlisted type. The type is params.mimeType,
// or else the type of a Blob or data: URL passed as data.
export function prepareArtifactDownload(params: Record<string, unknown> | undefined): ArtifactDownloadResult {
    if (!params) {
        return fail('invalid_params', 'download() takes {filename, mimeType, data}');
    }
    const {filename, mimeType: requestedType, data} = params;
    if (typeof filename !== 'undefined' && typeof filename !== 'string') {
        return fail('invalid_params', 'filename must be a string');
    }
    if (typeof filename === 'string' && filename.length > MAX_FILENAME_INPUT_LENGTH) {
        return fail('invalid_params', 'filename is too long');
    }
    if (typeof requestedType !== 'undefined' && typeof requestedType !== 'string') {
        return fail('invalid_params', 'mimeType must be a string');
    }
    if (typeof requestedType === 'string' && requestedType.length > MAX_MIME_TYPE_LENGTH) {
        return fail('invalid_params', 'mimeType is too long');
    }

    const decoded = decodeData(data);
    if ('ok' in decoded) {
        return decoded;
    }
    if (decoded.size > MAX_ARTIFACT_DOWNLOAD_BYTES) {
        return tooLarge();
    }

    const declared = requestedType || decoded.declaredType;
    if (!declared) {
        return fail('invalid_params', 'mimeType is required');
    }
    const mimeType = normalizeDownloadMimeType(declared);
    if (!mimeType) {
        return fail('unsupported_type', `Files of type ${declared.slice(0, 64)} cannot be downloaded. Allowed types: ${Array.from(DOWNLOAD_MIME_TYPES.keys()).join(', ')}`);
    }

    return {
        ok: true,
        download: {
            blob: new Blob([decoded.part], {type: mimeType}),
            filename: sanitizeDownloadFilename(filename, mimeType),
            mimeType,
        },
    };
}
