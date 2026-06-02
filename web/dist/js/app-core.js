/* =========================================================================
   CloudFlared UI — Core: state, helpers, API, toast
   ========================================================================= */
(() => {
    'use strict';

    const API_BASE = '/api';
    const $ = (id) => document.getElementById(id);
    const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));
    const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

    const state = {
        isRunning: false,
        config: {},
        status: 'unknown',
        currentLang: localStorage.getItem('lang') || (navigator.language?.startsWith('zh') ? 'zh' : navigator.language?.startsWith('ja') ? 'ja' : 'en'),
        currentTheme: localStorage.getItem('theme') || (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'),
        translations: {},
        /** @type {Array<{key:string,params?:object,level:string,ts:number,id:number}>} */
        logs: [],
        /** @type {Array<string>} */
        streamLines: [],
        /** @type {EventSource|null} */
        logStream: null,
        isStreamConnected: false,
        isStreamConnecting: false,
        features: { tunnel_manager: false, ddns: false, mcp: false },
        tunnelManager: { settings: {}, config: null, zones: [], zonesLoaded: false },
        mcp: { status: null, tokens: [] },
        ddns: { config: null, status: null, zones: [], zonesLoaded: false },
        pendingConfigSave: null,
        activeDialog: null,
        lastFocused: null,
        confirmResolver: null,
        nextLogId: 1,
        logFilter: localStorage.getItem('logFilter') || 'all',
        logsAtBottom: true,
        statusFailCount: 0,
        tunnelProtocol: '',
        lastError: '',
        tunnelAlertDismissed: null,
        runningSig: null,
    };

    /* ---- i18n ---- */

    function t(key, params) {
        let s = state.translations[key] || key;
        if (params) {
            for (const [k, v] of Object.entries(params)) {
                s = s.split(`{${k}}`).join(String(v ?? ''));
            }
        }
        return s;
    }

    /* ---- API ---- */

    async function apiError(res) {
        try { const d = await res.json(); return d.error || res.statusText; }
        catch { return res.statusText; }
    }

    async function apiGet(path) {
        const res = await fetch(API_BASE + path);
        if (!res.ok) throw new Error(await apiError(res));
        return res.json();
    }

    async function apiSend(path, method, body) {
        const res = await fetch(API_BASE + path, {
            method,
            headers: { 'Content-Type': 'application/json' },
            body: body == null ? undefined : JSON.stringify(body),
        });
        if (!res.ok) throw new Error(await apiError(res));
        return res.json().catch(() => ({}));
    }

    /* ---- Toast ---- */

    function showToast(message, kind = 'info', options = {}) {
        const viewport = $('toast-viewport');
        if (!viewport || !message) return;
        const node = document.createElement('div');
        node.className = 'toast';
        node.setAttribute('data-kind', kind);
        node.setAttribute('role', kind === 'error' ? 'alert' : 'status');
        const dot = document.createElement('span');
        dot.className = 'dot';
        node.appendChild(dot);
        const body = document.createElement('div');
        const title = document.createElement('div');
        title.className = 'title';
        title.textContent = options.title || t(`toast_${kind}`);
        const msg = document.createElement('div');
        msg.className = 'msg';
        msg.textContent = message;
        body.append(title, msg);
        node.appendChild(body);
        const close = document.createElement('button');
        close.type = 'button';
        close.className = 'close';
        close.setAttribute('aria-label', t('toast_close'));
        close.innerHTML = '<span aria-hidden="true">&times;</span>';
        close.addEventListener('click', () => dismiss());
        node.appendChild(close);
        viewport.appendChild(node);
        requestAnimationFrame(() => node.classList.add('show'));
        let dismissed = false;
        let timer = null;
        function dismiss() {
            if (dismissed) return;
            dismissed = true;
            if (timer) clearTimeout(timer);
            node.classList.add('removing');
            setTimeout(() => node.remove(), 200);
        }
        node.addEventListener('mouseenter', () => { if (timer) { clearTimeout(timer); timer = null; } });
        node.addEventListener('mouseleave', () => { if (!dismissed && !timer) timer = setTimeout(dismiss, options.duration ?? 4200); });
        const duration = options.duration ?? (kind === 'error' ? 6500 : 4200);
        if (duration > 0) timer = setTimeout(dismiss, duration);
        while (viewport.children.length > 5) viewport.firstElementChild?.remove();
    }

    const toast = {
        ok:   (m, o) => showToast(m, 'success', o),
        err:  (m, o) => showToast(m, 'error', o),
        info: (m, o) => showToast(m, 'info', o),
        warn: (m, o) => showToast(m, 'warning', o),
    };

    /* ---- Button busy state ---- */

    function setBusy(btn, busy, busyText) {
        if (!btn) return;
        if (busy) {
            btn.setAttribute('aria-busy', 'true');
            btn.disabled = true;
            const text = btn.querySelector('.text');
            if (text && busyText) {
                text.setAttribute('data-orig', text.textContent);
                text.textContent = busyText;
            }
        } else {
            btn.removeAttribute('aria-busy');
            btn.disabled = false;
            const text = btn.querySelector('.text');
            if (text) {
                const orig = text.getAttribute('data-orig');
                if (orig) { text.textContent = orig; text.removeAttribute('data-orig'); }
            }
        }
    }

    /* ---- Field flash ---- */

    function flashField(id) {
        const el = $(id);
        if (!el) return;
        el.classList.remove('field-saved');
        void el.offsetWidth;
        el.classList.add('field-saved');
        setTimeout(() => el.classList.remove('field-saved'), 900);
    }

    /* ---- Token visibility ---- */

    function setTokenVisible(input, btn, visible) {
        if (!input) return;
        input.type = visible ? 'text' : 'password';
        if (btn) {
            btn.setAttribute('aria-pressed', String(visible));
            btn.setAttribute('aria-label', t(visible ? 'token_hide' : 'token_show'));
        }
    }

    /* ---- Misc ---- */

    function formatTime(ts) {
        return new Date(ts).toTimeString().slice(0, 8);
    }

    /* ---- Export ---- */
    const ns = (window.cfui = window.cfui || {});
    ns.state = state;
    ns.$ = $;
    ns.$$ = $$;
    ns.sleep = sleep;
    ns.t = t;
    ns.API_BASE = API_BASE;
    ns.apiGet = apiGet;
    ns.apiSend = apiSend;
    ns.toast = toast;
    ns.showToast = showToast;
    ns.setBusy = setBusy;
    ns.flashField = flashField;
    ns.setTokenVisible = setTokenVisible;
    ns.formatTime = formatTime;
})();
