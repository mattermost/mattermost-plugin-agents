// Copyright (c) 2023-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// Mattermost HTML artifact bridge (postMessage protocol v1).
//
// Injected by the server as the first script of every HTML artifact. The
// artifact runs in an opaque-origin sandbox with no access to the Mattermost
// session; this bridge is its only channel to the host page. Every message is
// a plain object tagged with mmArtifact: 1 and carrying the viewer's bridge
// token, which the host requires on every message. Targets '*' because an
// opaque origin cannot know the parent's origin.
//
// The token must never become reachable by artifact code (which runs after
// this script and can replace any global or prototype method). So, before any
// artifact code runs, this script captures everything it needs, removes its
// own element from the DOM, and from then on only ever puts the token into
// object literals handed straight to the parent WindowProxy's postMessage
// (a cross-origin method artifact code cannot hook). Messages are never
// passed to, mutated by, or built with any overridable function.
(function () {
    'use strict';

    const TOKEN = /*MM_ARTIFACT_BRIDGE_TOKEN*/null;

    // window.parent is [Replaceable]: never read it again after this line.
    const parentWin = window.parent;
    const PromiseCtor = Promise;
    const ErrorCtor = Error;
    const setTimeoutFn = setTimeout;
    const clearTimeoutFn = clearTimeout;

    if (document.currentScript) {
        document.currentScript.remove();
    }

    var PROTOCOL = 1;
    var REQUEST_TIMEOUT_MS = 120000;

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
        window.dispatchEvent(new CustomEvent('mattermost:context', {detail: context}));
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
        if (event.source !== parentWin || !data || typeof data !== 'object' || data.mmArtifact !== PROTOCOL) {
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
                parentWin.postMessage({mmArtifact: PROTOCOL, type: 'request', id: id, method: method, token: TOKEN}, '*');
            } else {
                parentWin.postMessage({mmArtifact: PROTOCOL, type: 'request', id: id, method: method, params: params, token: TOKEN}, '*');
            }
        });
    }

    var api = {
        getCurrentUser: function () {
            return request('getCurrentUser');
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
            var margin = parseFloat(getComputedStyle(body).marginBottom) || 0;
            height = Math.max(height, body.getBoundingClientRect().bottom + margin + window.scrollY);
        }
        return Math.ceil(height);
    }

    // Report content height (debounced to one message per frame, only on change).
    function reportSize() {
        resizeScheduled = false;
        var height = contentHeight();
        if (height !== lastHeight) {
            lastHeight = height;
            parentWin.postMessage({mmArtifact: PROTOCOL, type: 'resize', height: height, token: TOKEN}, '*');
        }
    }
    function scheduleResize() {
        if (!resizeScheduled) {
            resizeScheduled = true;
            requestAnimationFrame(reportSize);
        }
    }

    // Keyboard focus inside the frame never reaches the host page, so let the
    // host close the fullscreen viewer on an Escape the artifact didn't handle.
    window.addEventListener('keydown', function (event) {
        if (event.key === 'Escape' && !event.defaultPrevented) {
            parentWin.postMessage({mmArtifact: PROTOCOL, type: 'escape', token: TOKEN}, '*');
        }
    });

    function onReady() {
        parentWin.postMessage({mmArtifact: PROTOCOL, type: 'ready', token: TOKEN}, '*');
        if (typeof ResizeObserver === 'function') {
            var observer = new ResizeObserver(scheduleResize);
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
