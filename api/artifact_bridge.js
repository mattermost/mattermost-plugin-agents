// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Mattermost HTML artifact bridge (postMessage protocol v1).
//
// Injected by the server as the first script of every HTML artifact. The
// artifact runs in an opaque-origin sandbox with no access to the Mattermost
// session; this bridge is its only channel to the host page. The host frames
// the artifact inside a same-origin wrapper frame, so the host page is
// window.parent.parent. Every message is a plain object tagged with
// mmArtifact: 1 and carrying the viewer's bridge token, which the host
// requires on every message. Messages target the Mattermost site origin the
// server injects ('*' only when the server has no usable site URL).
//
// The token must never become reachable by artifact code (which runs after
// this script and can replace any global or prototype method). So, before any
// artifact code runs, this script captures everything it needs, removes its
// own element from the DOM, and from then on only ever puts the token into
// object literals handed straight to the host WindowProxy's postMessage
// (a cross-origin method artifact code cannot hook). Messages are never
// passed to, mutated by, or built with any overridable function.
(function () {
    'use strict';

    const TOKEN = /*MM_ARTIFACT_BRIDGE_TOKEN*/null;
    const TARGET_ORIGIN = /*MM_ARTIFACT_BRIDGE_TARGET_ORIGIN*/null;

    // window.parent is [Replaceable]: never read it again after this line.
    const parentWin = window.parent;
    const hostWin = parentWin.parent;
    const PromiseCtor = Promise;
    const ErrorCtor = Error;
    const setTimeoutFn = setTimeout;
    const clearTimeoutFn = clearTimeout;
    const setIntervalFn = setInterval;
    const requestAnimationFrameFn = requestAnimationFrame;
    const ResizeObserverCtor = window.ResizeObserver;
    const getComputedStyleFn = getComputedStyle;
    const ceilFn = Math.ceil;
    const maxFn = Math.max;
    const parseFloatFn = parseFloat;
    const CustomEventCtor = CustomEvent;
    const dispatchEventFn = window.dispatchEvent.bind(window);
    const reflectApply = Reflect.apply;
    const DOMExceptionCtor = DOMException;

    // Brand-checking getters for download data: they throw for anything that
    // is not a real Blob, ArrayBuffer or view, whatever artifact code does to
    // prototypes, constructors or Symbol.hasInstance.
    function getterOf(proto, name) {
        return Object.getOwnPropertyDescriptor(proto, name).get;
    }
    // Transient user activation of this frame: a click or key press inside the
    // artifact, which artifact code cannot fake. Browsers without the API
    // cannot be checked.
    const userActivation = navigator.userActivation;
    const userActivationIsActive = userActivation ? getterOf(Object.getPrototypeOf(userActivation), 'isActive') : null;
    const blobSize = getterOf(Blob.prototype, 'size');
    const arrayBufferByteLength = getterOf(ArrayBuffer.prototype, 'byteLength');
    const arrayBufferSlice = ArrayBuffer.prototype.slice;
    const isArrayBufferView = ArrayBuffer.isView;
    const TypedArrayProto = Object.getPrototypeOf(Uint8Array.prototype);
    const typedArrayView = [getterOf(TypedArrayProto, 'buffer'), getterOf(TypedArrayProto, 'byteOffset'), getterOf(TypedArrayProto, 'byteLength')];
    const dataViewView = [getterOf(DataView.prototype, 'buffer'), getterOf(DataView.prototype, 'byteOffset'), getterOf(DataView.prototype, 'byteLength')];

    if (document.currentScript) {
        document.currentScript.remove();
    }

    // Opening the document (document.open(), and document.write() or
    // writeln() outside parsing, which open it implicitly) erases every
    // listener on the window and document, the bridge's included. Writes from
    // a script the parser is running keep working. Best-effort: a script that
    // artifact code inserts while the document is still loading also passes.
    (function () {
        const DocumentProto = Document.prototype;
        const readyStateGetter = Object.getOwnPropertyDescriptor(DocumentProto, 'readyState').get;
        const currentScriptGetter = Object.getOwnPropertyDescriptor(DocumentProto, 'currentScript').get;

        function lock(name, fn) {
            Object.defineProperty(DocumentProto, name, {value: fn, writable: false, enumerable: true, configurable: false});
        }
        function guardWrite(original) {
            return function () {
                if (reflectApply(readyStateGetter, this, []) !== 'loading' || reflectApply(currentScriptGetter, this, []) === null) {
                    throw new DOMExceptionCtor('Artifacts cannot write to the document after it has loaded.', 'InvalidStateError');
                }
                return reflectApply(original, this, arguments);
            };
        }

        lock('write', guardWrite(DocumentProto.write));
        lock('writeln', guardWrite(DocumentProto.writeln));
        lock('open', function () {
            throw new DOMExceptionCtor('Artifacts cannot reopen the document.', 'InvalidStateError');
        });
    }());

    // Best-effort defense in depth: CSP cannot block WebRTC, so a peer
    // connection could reach the network (e.g. STUN to reveal the viewer's
    // IP). Child frames (srcdoc, about:blank) and blob:/data: documents get
    // fresh constructors; the real block is the Connection-Allowlist response
    // header (Chromium 152+).
    ['RTCPeerConnection', 'webkitRTCPeerConnection', 'mozRTCPeerConnection', 'RTCDataChannel'].forEach(function (name) {
        try {
            Object.defineProperty(window, name, {value: undefined, writable: false, configurable: false});
        } catch (e) {
            // Already non-configurable: nothing more this script can do.
        }
    });

    // Outside the host's wrapper frame there is no host page to talk to.
    if (parentWin === window || hostWin === parentWin) {
        return;
    }

    var PROTOCOL = 1;
    var REQUEST_TIMEOUT_MS = 120000;
    var HEARTBEAT_INTERVAL_MS = 500;

    // Keep in sync with MAX_ARTIFACT_DOWNLOAD_BYTES in the webapp's
    // artifact_download.ts, which enforces the limit again on the host.
    var MAX_DOWNLOAD_BYTES = 25 * 1024 * 1024;

    // A data: URL is base64 (a third larger than its bytes) or %XX escapes
    // (three times larger); the host applies the tighter base64 limit.
    var MAX_DOWNLOAD_STRING_LENGTH = (MAX_DOWNLOAD_BYTES * 3) + 4096;
    var MAX_DOWNLOAD_NAME_LENGTH = 4096;
    var MAX_DOWNLOAD_TYPE_LENGTH = 255;

    var currentContext = null;
    var subscribers = [];
    var pending = Object.create(null);
    var nextRequestId = 1;
    var lastHeight = -1;
    var resizeScheduled = false;


    // centerChannelBg -> center-channel-bg
    function kebab(key) {
        return String(key).replace(/[A-Z]/g, function (c) {
            return '-' + c.toLowerCase();
        });
    }

    function applyContext(context) {
        var root = document.documentElement;
        var theme = (context && context.theme) || {};
        Object.keys(theme).forEach(function (key) {
            if (typeof theme[key] === 'string') {
                root.style.setProperty('--mm-' + kebab(key), theme[key]);
            }
        });
        if (context.colorScheme === 'light' || context.colorScheme === 'dark') {
            root.style.colorScheme = context.colorScheme;
        }
        if (typeof context.displayMode === 'string') {
            root.setAttribute('data-mm-display-mode', context.displayMode);
        }
    }

    function handleContext(context) {
        if (!context || typeof context !== 'object') {
            return;
        }
        currentContext = context;
        applyContext(context);
        dispatchEventFn(new CustomEventCtor('mattermost:context', {detail: context}));
        subscribers.slice().forEach(function (cb) {
            try {
                cb(context);
            } catch (e) {
                // A broken subscriber must not stop the others.
            }
        });
    }

    function handleResponse(data) {
        var entry = pending[data.id];
        if (!entry) {
            return;
        }
        delete pending[data.id];
        clearTimeoutFn(entry.timer);
        if (data.error) {
            var err = new ErrorCtor(String(data.error.message || 'Request failed'));
            err.code = String(data.error.code || 'unavailable');
            entry.reject(err);
        } else {
            entry.resolve(data.result);
        }
    }

    window.addEventListener('message', function (event) {
        var data = event.data;
        if (event.source !== hostWin || !data || typeof data !== 'object' || data.mmArtifact !== PROTOCOL) {
            return;
        }
        if (data.type === 'context') {
            handleContext(data.context);
        } else if (data.type === 'response' && typeof data.id === 'string') {
            handleResponse(data);
        }
    });

    function request(method, params) {
        return new PromiseCtor(function (resolve, reject) {
            var id = 'req-' + (nextRequestId++);
            var timer = setTimeoutFn(function () {
                delete pending[id];
                var err = new ErrorCtor('Request timed out');
                err.code = 'timeout';
                reject(err);
            }, REQUEST_TIMEOUT_MS);
            pending[id] = {resolve: resolve, reject: reject, timer: timer};
            if (params === undefined) {
                hostWin.postMessage({mmArtifact: PROTOCOL, type: 'request', id: id, method: method, token: TOKEN}, TARGET_ORIGIN);
            } else {
                hostWin.postMessage({mmArtifact: PROTOCOL, type: 'request', id: id, method: method, params: params, token: TOKEN}, TARGET_ORIGIN);
            }
        });
    }

    function bridgeError(code, message) {
        var err = new ErrorCtor(message);
        err.code = code;
        return err;
    }

    // The bytes of an ArrayBuffer view, copied so that only they are cloned
    // to the host, not the rest of a larger buffer.
    function viewBytes(view) {
        var getters = typedArrayView;
        try {
            reflectApply(getters[2], view, []);
        } catch (e) {
            getters = dataViewView;
        }
        var offset = reflectApply(getters[1], view, []);
        var length = reflectApply(getters[2], view, []);
        if (length > MAX_DOWNLOAD_BYTES) {
            throw bridgeError('too_large', 'The file is larger than the download limit');
        }
        return reflectApply(arrayBufferSlice, reflectApply(getters[0], view, []), [offset, offset + length]);
    }

    // Download data as a string, Blob or ArrayBuffer within the size limit.
    // The host validates it again.
    function downloadData(data) {
        if (typeof data === 'string') {
            if (data.length > MAX_DOWNLOAD_STRING_LENGTH) {
                throw bridgeError('too_large', 'The file is larger than the download limit');
            }
            return data;
        }
        if (data === null || (typeof data !== 'object' && typeof data !== 'function')) {
            throw bridgeError('invalid_params', 'data must be a Blob, an ArrayBuffer, a typed array, or a string');
        }
        var size = -1;
        try {
            size = reflectApply(blobSize, data, []);
        } catch (e) {
            try {
                size = reflectApply(arrayBufferByteLength, data, []);
            } catch (e2) {
                if (isArrayBufferView(data)) {
                    return viewBytes(data);
                }
                throw bridgeError('invalid_params', 'data must be a Blob, an ArrayBuffer, a typed array, or a string');
            }
        }
        if (size > MAX_DOWNLOAD_BYTES) {
            throw bridgeError('too_large', 'The file is larger than the download limit');
        }
        return data;
    }

    // Builds a fresh params object from primitives, a Blob or an
    // ArrayBuffer: none of them runs artifact code when cloned.
    function downloadParams(options) {
        if (!options || typeof options !== 'object') {
            throw bridgeError('invalid_params', 'download() takes {filename, mimeType, data}');
        }
        var filename = options.filename;
        var mimeType = options.mimeType;
        var data = options.data;
        if (filename !== undefined && (typeof filename !== 'string' || filename.length > MAX_DOWNLOAD_NAME_LENGTH)) {
            throw bridgeError('invalid_params', 'filename must be a string of at most ' + MAX_DOWNLOAD_NAME_LENGTH + ' characters');
        }
        if (mimeType !== undefined && (typeof mimeType !== 'string' || mimeType.length > MAX_DOWNLOAD_TYPE_LENGTH)) {
            throw bridgeError('invalid_params', 'mimeType must be a string of at most ' + MAX_DOWNLOAD_TYPE_LENGTH + ' characters');
        }
        return {filename: filename, mimeType: mimeType, data: downloadData(data)};
    }

    var api = {
        getCurrentUser: function () {
            return request('getCurrentUser');
        },

        // Asks the host to save a file. The viewer confirms every download.
        download: function (options) {
            var params;
            try {
                if (userActivationIsActive && !reflectApply(userActivationIsActive, userActivation, [])) {
                    throw bridgeError('permission_denied', 'download() must be called from a user action, such as a click');
                }
                params = downloadParams(options);
            } catch (err) {
                return new PromiseCtor(function (resolve, reject) {
                    reject(err);
                });
            }
            return request('download', params);
        },
        onContextChange: function (cb) {
            if (typeof cb !== 'function') {
                return function () {};
            }
            subscribers.push(cb);
            return function () {
                var i = subscribers.indexOf(cb);
                if (i !== -1) {
                    subscribers.splice(i, 1);
                }
            };
        },
    };
    Object.defineProperty(api, 'context', {
        enumerable: true,
        get: function () {
            return currentContext;
        },
    });
    Object.defineProperty(window, 'mattermost', {value: Object.freeze(api), enumerable: true});

    // Height of the document's content. documentElement.scrollHeight never
    // drops below the iframe's own height, so it cannot shrink the frame.
    function contentHeight() {
        var root = document.documentElement;
        var height = root.getBoundingClientRect().height;
        var body = document.body;
        if (body) {
            var margin = parseFloatFn(getComputedStyleFn(body).marginBottom) || 0;
            height = maxFn(height, body.getBoundingClientRect().bottom + margin + window.scrollY);
        }
        return ceilFn(height);
    }

    // Report content height (debounced to one message per frame, only on change).
    function reportSize() {
        resizeScheduled = false;
        var height = contentHeight();
        if (height !== lastHeight) {
            lastHeight = height;
            hostWin.postMessage({mmArtifact: PROTOCOL, type: 'resize', height: height, token: TOKEN}, TARGET_ORIGIN);
        }
    }
    function scheduleResize() {
        if (!resizeScheduled) {
            resizeScheduled = true;
            requestAnimationFrameFn(reportSize);
        }
    }

    // Keyboard focus inside the frame never reaches the host page, so let the
    // host close the fullscreen viewer on an Escape the artifact didn't handle.
    // Only a real key press counts, never one synthesized by artifact code.
    window.addEventListener('keydown', function (event) {
        if (!event.isTrusted) {
            return;
        }
        if (event.key === 'Escape' && !event.defaultPrevented) {
            hostWin.postMessage({mmArtifact: PROTOCOL, type: 'escape', token: TOKEN}, TARGET_ORIGIN);
        }
    });

    // Liveness signal for the host. Whatever replaces this document in the
    // frame (e.g. a navigation whose response never finishes loading, so the
    // host never sees a second load event) cannot send it, so the host hides
    // and then unloads a frame whose heartbeat stops.
    function heartbeat() {
        hostWin.postMessage({mmArtifact: PROTOCOL, type: 'heartbeat', token: TOKEN}, TARGET_ORIGIN);
    }
    heartbeat();
    setIntervalFn(heartbeat, HEARTBEAT_INTERVAL_MS);

    function onReady() {
        hostWin.postMessage({mmArtifact: PROTOCOL, type: 'ready', token: TOKEN}, TARGET_ORIGIN);
        if (typeof ResizeObserverCtor === 'function') {
            var observer = new ResizeObserverCtor(scheduleResize);
            observer.observe(document.documentElement);
            if (document.body) {
                observer.observe(document.body);
            }
        }
        scheduleResize();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', onReady, {once: true});
    } else {
        onReady();
    }
}());
