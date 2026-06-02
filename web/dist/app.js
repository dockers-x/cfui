/* =========================================================================
   CloudFlared UI - frontend
   - data-i18n attribute drives translations
   - logs store translation keys + params (no string-matching hacks)
   - reusable confirm dialog, spinner-aware buttons, busy state machine
   ========================================================================= */

(() => {
    'use strict';

    const API_BASE = '/api';

    const state = {
        isRunning: false,
        config: {},
        status: 'unknown',
        currentLang: localStorage.getItem('lang') || (navigator.language?.startsWith('zh') ? 'zh' : navigator.language?.startsWith('ja') ? 'ja' : 'en'),
        currentTheme: localStorage.getItem('theme') || (window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'),
        translations: {},
        /** @type {Array<{key:string,params?:object,level:string,ts:number,id:number}>} */
        logs: [],
        /** @type {EventSource|null} */
        logStream: null,
        isStreamConnected: false,
        isStreamConnecting: false,
        features: { tunnel_manager: false, ddns: false, mcp: false },
        tunnelManager: { settings: {}, config: null, zones: [], zonesLoaded: false },
        mcp: { status: null, tokens: [] },
        ddns: { config: null, status: null, zones: [], zonesLoaded: false },
        /** Pending save Promise (so action button can wait) */
        pendingConfigSave: null,
        /** Dialog state */
        activeDialog: null,
        lastFocused: null,
        confirmResolver: null,
        nextLogId: 1,
    };

    const $ = (id) => document.getElementById(id);
    const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

    /* =============================================================
       i18n
       ============================================================= */

    async function loadLanguage(lang) {
        try {
            const res = await fetch(`${API_BASE}/i18n/${lang}`);
            if (!res.ok) throw new Error('load failed');
            state.translations = await res.json();
        } catch (err) {
            console.error('i18n load failed', lang, err);
            if (lang !== 'en') {
                await loadLanguage('en');
                return;
            }
            state.translations = {};
        }
        state.currentLang = lang;
        localStorage.setItem('lang', lang);
        document.documentElement.lang = lang;
        applyTranslations();
        applyLogTranslations();
        // Update dynamic text (status, manager, ddns, mcp)
        document.dispatchEvent(new CustomEvent('localechange', { detail: { lang } }));
    }

    function t(key, params) {
        let s = state.translations[key] || key;
        if (params) {
            for (const [k, v] of Object.entries(params)) {
                s = s.split(`{${k}}`).join(String(v ?? ''));
            }
        }
        return s;
    }

    function applyTranslations() {
        // text content
        $$('[data-i18n]').forEach((el) => {
            const key = el.getAttribute('data-i18n');
            if (key) el.textContent = t(key);
        });
        // attributes (data-i18n-attr="attr|key")
        $$('[data-i18n-attr]').forEach((el) => {
            const spec = el.getAttribute('data-i18n-attr');
            if (!spec) return;
            spec.split(';').forEach((part) => {
                const [attr, key] = part.split('|');
                if (attr && key) el.setAttribute(attr, t(key));
            });
        });
        // title
        document.title = t('app_title');
    }

    function applyLogTranslations() {
        // rerender existing log entries from stored key+params
        const container = $('logs-container');
        if (!container) return;
        container.innerHTML = '';
        for (const entry of state.logs) {
            renderLogLine(entry);
        }
        container.scrollTop = container.scrollHeight;
    }

    /* =============================================================
       Theme
       ============================================================= */

    function initTheme() {
        document.documentElement.setAttribute('data-theme', state.currentTheme);
    }

    function toggleTheme() {
        state.currentTheme = state.currentTheme === 'light' ? 'dark' : 'light';
        document.documentElement.setAttribute('data-theme', state.currentTheme);
        localStorage.setItem('theme', state.currentTheme);
        showToast(t('theme_changed'), 'info', { duration: 1800 });
    }

    /* =============================================================
       Toast
       ============================================================= */

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
        // pause on hover
        node.addEventListener('mouseenter', () => { if (timer) { clearTimeout(timer); timer = null; } });
        node.addEventListener('mouseleave', () => { if (!dismissed && !timer) timer = setTimeout(dismiss, options.duration ?? 4200); });

        const duration = options.duration ?? (kind === 'error' ? 6500 : 4200);
        if (duration > 0) timer = setTimeout(dismiss, duration);

        // Cap at 5
        while (viewport.children.length > 5) {
            viewport.firstElementChild?.remove();
        }
    }

    const toast = {
        ok: (m, o) => showToast(m, 'success', o),
        err: (m, o) => showToast(m, 'error', o),
        info: (m, o) => showToast(m, 'info', o),
        warn: (m, o) => showToast(m, 'warning', o),
    };

    /* =============================================================
       Dialog
       ============================================================= */

    function openDialog(dialog) {
        if (!dialog) return;
        if (state.activeDialog && state.activeDialog !== dialog) {
            closeDialog(state.activeDialog);
        }
        state.lastFocused = document.activeElement;
        state.activeDialog = dialog;
        dialog.hidden = false;
        document.body.classList.add('modal-open');
        setTimeout(() => {
            const target = dialog.querySelector('input, select, textarea, button');
            target?.focus();
        }, 0);
    }

    function closeDialog(dialog) {
        if (!dialog || dialog.hidden) return;
        dialog.hidden = true;
        if (state.activeDialog === dialog) {
            state.activeDialog = null;
            if (state.confirmResolver) {
                state.confirmResolver(false);
                state.confirmResolver = null;
            }
        }
        if (!$$('.modal-backdrop').some((d) => !d.hidden)) {
            document.body.classList.remove('modal-open');
        }
        if (state.lastFocused?.focus) {
            state.lastFocused.focus();
        }
    }

    function confirm({ title, message, okText, okClass = 'btn--danger' }) {
        return new Promise((resolve) => {
            const dialog = $('confirm-dialog');
            if (!dialog) return resolve(false);
            $('confirm-title').textContent = title || t('confirm_title');
            $('confirm-message').textContent = message || '';
            const okBtn = $('confirm-ok');
            okBtn.className = `btn ${okClass}`;
            $('confirm-ok-text').textContent = okText || t('confirm');
            state.confirmResolver = resolve;
            openDialog(dialog);
        });
    }

    document.addEventListener('keydown', (e) => {
        if (!state.activeDialog) return;
        if (e.key === 'Escape') {
            e.preventDefault();
            closeDialog(state.activeDialog);
            return;
        }
        if (e.key !== 'Tab') return;
        const focusable = $$('a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
            state.activeDialog).filter((el) => el.offsetParent !== null);
        if (focusable.length === 0) return;
        const first = focusable[0];
        const last = focusable[focusable.length - 1];
        if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
        else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    });

    document.addEventListener('click', (e) => {
        const close = e.target.closest('[data-close-dialog]');
        if (close) {
            e.preventDefault();
            const dlg = close.closest('.modal-backdrop');
            if (dlg) closeDialog(dlg);
            return;
        }
        // backdrop click (only for non-transparent backdrops)
        if (e.target.classList?.contains('modal-backdrop') && e.target.id !== 'lang-menu') {
            closeDialog(e.target);
        }
    });

    $('confirm-ok')?.addEventListener('click', () => {
        const resolver = state.confirmResolver;
        state.confirmResolver = null;
        if (resolver) {
            resolver(true);
        }
        closeDialog($('confirm-dialog'));
    });

    /* =============================================================
       Tabs
       ============================================================= */

    function activateTab(name) {
        const tab = document.querySelector(`.tab[data-tab="${name}"]`);
        if (!tab || tab.hidden) return;
        $$('.tab').forEach((t) => {
            const sel = t === tab;
            t.setAttribute('aria-selected', String(sel));
            t.tabIndex = sel ? 0 : -1;
        });
        $$('.tab-panel').forEach((p) => {
            const isMine = p.id === `panel-${name}`;
            p.hidden = !isMine;
        });
        localStorage.setItem('lastTab', name);
        document.dispatchEvent(new CustomEvent('tabchange', { detail: { name } }));
    }

    document.addEventListener('click', (e) => {
        const tab = e.target.closest('.tab');
        if (tab && !tab.hidden) {
            e.preventDefault();
            activateTab(tab.dataset.tab);
        }
    });

    document.addEventListener('keydown', (e) => {
        if (!e.target.classList?.contains('tab')) return;
        if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
            e.preventDefault();
            const tabs = $$('.tab').filter((t) => !t.hidden);
            const idx = tabs.indexOf(e.target);
            const next = e.key === 'ArrowRight' ? (idx + 1) % tabs.length : (idx - 1 + tabs.length) % tabs.length;
            tabs[next].focus();
            activateTab(tabs[next].dataset.tab);
        }
    });

    /* =============================================================
       Button busy state
       ============================================================= */

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
                if (orig) {
                    text.textContent = orig;
                    text.removeAttribute('data-orig');
                }
            }
        }
    }

    /* =============================================================
       Status pill
       ============================================================= */

    function setStatusPill(state_, text) {
        const pill = $('status-pill');
        if (!pill) return;
        pill.setAttribute('data-state', state_);
        pill.querySelector('.text').textContent = text;
    }

    /* =============================================================
       API helpers
       ============================================================= */

    async function apiError(res) {
        try {
            const data = await res.json();
            return data.error || res.statusText;
        } catch {
            return res.statusText;
        }
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

    /* =============================================================
       Version
       ============================================================= */

    async function fetchVersion() {
        try {
            const data = await apiGet('/version');
            const info = $('version-info');
            if (info) {
                const m = (data.version || '').match(/^v?(\d+\.\d+\.\d+)/);
                const display = m ? 'v' + m[1] : (data.version || '');
                info.textContent = display;
                info.setAttribute('aria-label', `Version: ${data.version}\nBuild: ${data.build_time || ''}\nCommit: ${data.git_commit || ''}`);
                info.title = `${data.version} · ${data.build_time || ''}`;
            }
        } catch {
            const info = $('version-info');
            if (info) info.textContent = '';
        }
    }

    /* =============================================================
       Config
       ============================================================= */

    function readConfigFromForm() {
        return {
            token: $('token-input').value.trim(),
            custom_tag: $('custom-version-input').value.trim(),
            software_name: $('software-name-input').value.trim() || 'cfui',
            auto_start: $('autostart-toggle').checked,
            auto_restart: $('autorestart-toggle').checked,
            protocol: $('protocol-select').value,
            grace_period: $('grace-period-input').value.trim() || '30s',
            region: $('region-select').value,
            retries: parseInt($('retries-input').value, 10) || 0,
            metrics_enable: $('metrics-enable-toggle').checked,
            metrics_port: parseInt($('metrics-port-input').value, 10) || 60123,
            edge_bind_address: $('edge-bind-address-input').value.trim(),
            no_tls_verify: $('no-tls-verify-toggle').checked,
            tunnel_management: state.config.tunnel_management || {},
        };
    }

    function writeConfigToForm(cfg) {
        $('token-input').value = cfg.token || '';
        $('custom-version-input').value = cfg.custom_tag || '';
        $('software-name-input').value = cfg.software_name || 'cfui';
        $('autostart-toggle').checked = !!cfg.auto_start;
        $('autorestart-toggle').checked = cfg.auto_restart !== false;
        $('protocol-select').value = cfg.protocol || 'auto';
        $('grace-period-input').value = cfg.grace_period || '30s';
        $('region-select').value = cfg.region || '';
        $('retries-input').value = cfg.retries ?? 5;
        $('metrics-enable-toggle').checked = !!cfg.metrics_enable;
        $('metrics-port-input').value = cfg.metrics_port || 60123;
        $('edge-bind-address-input').value = cfg.edge_bind_address || '';
        $('no-tls-verify-toggle').checked = !!cfg.no_tls_verify;
        updateMetricsVisibility();
    }

    async function fetchConfig() {
        try {
            const data = await apiGet('/config');
            state.config = data;
            writeConfigToForm(data);
        } catch (err) {
            addLog({ key: 'config_load_failed', params: { err: err.message } }, 'error');
        }
    }

    let saveSeq = 0;
    function saveConfig({ showFeedback = true, source = 'auto' } = {}) {
        const seq = ++saveSeq;
        const cfg = readConfigFromForm();
        const prev = state.pendingConfigSave;
        const p = (async () => {
            // wait for prior save (queue) to keep ordering
            try { await prev; } catch { /* ignore */ }
            // Drop the request if a newer one started
            if (seq !== saveSeq) return;
            try {
                const data = await apiSend('/config', 'POST', cfg);
                state.config = data;
                if (showFeedback) {
                    const inputs = ['token-input', 'custom-version-input', 'software-name-input',
                        'autostart-toggle', 'autorestart-toggle', 'protocol-select',
                        'grace-period-input', 'region-select', 'retries-input',
                        'metrics-enable-toggle', 'metrics-port-input', 'edge-bind-address-input',
                        'no-tls-verify-toggle'];
                    inputs.forEach((id) => $(id)?.classList.remove('field-saved'));
                    if (source !== 'button' && cfg.token !== undefined) {
                        flashField('token-input');
                    }
                    if (source !== 'button') toast.ok(t('config_saved'));
                }
                addLog({ key: 'config_saved' }, 'system');
            } catch (err) {
                if (seq !== saveSeq) return;
                addLog({ key: 'config_save_failed', params: { err: err.message } }, 'error');
                toast.err(t('config_save_failed'));
            }
        })();
        state.pendingConfigSave = p;
        return p;
    }

    function flashField(id) {
        const el = $(id);
        if (!el) return;
        el.classList.remove('field-saved');
        void el.offsetWidth; // reflow
        el.classList.add('field-saved');
        setTimeout(() => el.classList.remove('field-saved'), 900);
    }

    /* =============================================================
       Status / start / stop
       ============================================================= */

    async function fetchStatus() {
        try {
            const data = await apiGet('/status');
            const prev = state.status;
            state.status = data.status;
            state.isRunning = data.running;
            updateStatusUI();
            if (prev !== state.status && prev !== 'unknown') {
                addLog({ key: 'status_changed', params: { status: data.status } }, 'system');
            }
        } catch (err) {
            // silent
        }
    }

    function updateStatusUI() {
        if (state.isRunning) {
            setStatusPill('ok', t('status_running'));
        } else if (state.status === 'error') {
            setStatusPill('error', t('status_error'));
        } else {
            setStatusPill('warn', t('status_stopped'));
        }
        const btn = $('action-btn');
        if (btn) {
            btn.setAttribute('data-action', state.isRunning ? 'stop' : 'start');
            btn.classList.toggle('btn--danger', state.isRunning);
            btn.classList.toggle('btn--primary', !state.isRunning);
            btn.querySelector('.text').textContent = t(state.isRunning ? 'stop_tunnel' : 'start_tunnel');
        }
    }

    async function onActionClick() {
        const btn = $('action-btn');
        const action = btn.getAttribute('data-action');

        if (action === 'start') {
            if (!$('token-input').value.trim()) {
                toast.err(t('error_token_required'));
                $('token-input').focus();
                return;
            }
            // Make sure pending save finishes first
            if (state.pendingConfigSave) {
                setBusy(btn, true, t('saving'));
                try { await state.pendingConfigSave; } catch { /* */ }
                setBusy(btn, false);
            }
        }

        setBusy(btn, true, t(action === 'start' ? 'starting' : 'stopping'));
        try {
            await apiSend('/control', 'POST', { action });
            toast.ok(t(action === 'start' ? 'tunnel_start_requested' : 'tunnel_stop_requested'));
            setTimeout(fetchStatus, 500);
        } catch (err) {
            // Stop endpoint may error after tunnel already shut down
            if (action === 'stop') {
                toast.ok(t('tunnel_stop_requested'));
                setTimeout(fetchStatus, 500);
            } else {
                toast.err(err.message);
            }
        } finally {
            setBusy(btn, false);
        }
    }

    /* =============================================================
       Logs
       ============================================================= */

    function addLog(entryOrKey, level = 'info') {
        let entry;
        if (typeof entryOrKey === 'string') {
            entry = { key: entryOrKey, level, ts: Date.now(), id: state.nextLogId++ };
        } else {
            entry = { ...entryOrKey, level: entryOrKey.level || level, ts: entryOrKey.ts || Date.now(), id: state.nextLogId++ };
        }
        state.logs.push(entry);
        // cap to 1000 stored
        if (state.logs.length > 1000) state.logs.shift();
        renderLogLine(entry);
    }

    function renderLogLine(entry) {
        const container = $('logs-container');
        if (!container) return;
        const row = document.createElement('div');
        row.className = `line ${entry.level}`;
        const ts = document.createElement('span');
        ts.className = 'ts';
        ts.textContent = formatTime(entry.ts);
        const msg = document.createElement('span');
        msg.className = 'msg';
        msg.textContent = t(entry.key, entry.params);
        row.append(ts, msg);
        container.appendChild(row);
        // cap rendered lines
        while (container.children.length > 500) container.firstElementChild?.remove();
        container.scrollTop = container.scrollHeight;
    }

    function formatTime(ts) {
        const d = new Date(ts);
        return d.toTimeString().slice(0, 8);
    }

    function clearLogs() {
        state.logs = [];
        const c = $('logs-container');
        if (c) c.innerHTML = '';
        toast.info(t('logs_cleared'));
    }

    function copyLogs() {
        const text = state.logs.map((e) => `[${formatTime(e.ts)}] ${t(e.key, e.params)}`).join('\n');
        navigator.clipboard?.writeText(text).then(
            () => toast.ok(t('logs_copied')),
            () => toast.err(t('copy_failed')),
        );
    }

    /* =============================================================
       Log streaming
       ============================================================= */

    function setLogConnPill(state_, text) {
        const pill = $('log-conn-state');
        if (!pill) return;
        pill.setAttribute('data-state', state_);
        pill.querySelector('.text').textContent = text;
    }

    function connectLogStream() {
        if (state.logStream || state.isStreamConnecting) return;
        state.isStreamConnecting = true;
        setLogConnPill('loading', t('log_status_connecting'));
        const es = new EventSource(API_BASE + '/logs/stream');
        state.logStream = es;
        es.onopen = () => {
            state.isStreamConnected = true;
            state.isStreamConnecting = false;
            setLogConnPill('ok', t('log_status_connected'));
            updateStreamButton();
            toast.ok(t('log_stream_connected'));
        };
        es.onmessage = (e) => {
            if (!e.data) return;
            // streamed lines aren't translatable; render raw
            const container = $('logs-container');
            const row = document.createElement('div');
            row.className = 'line info';
            const ts = document.createElement('span');
            ts.className = 'ts';
            ts.textContent = formatTime(Date.now());
            const msg = document.createElement('span');
            msg.className = 'msg';
            msg.textContent = e.data;
            row.append(ts, msg);
            container.appendChild(row);
            while (container.children.length > 500) container.firstElementChild?.remove();
            container.scrollTop = container.scrollHeight;
        };
        es.onerror = () => {
            if (state.isStreamConnected) {
                setLogConnPill('warn', t('log_status_reconnecting'));
            } else {
                setLogConnPill('error', t('log_status_failed'));
            }
            es.close();
            state.logStream = null;
            state.isStreamConnected = false;
            state.isStreamConnecting = false;
            updateStreamButton();
        };
    }

    function disconnectLogStream(silent = false) {
        if (state.logStream) {
            state.logStream.close();
            state.logStream = null;
        }
        state.isStreamConnected = false;
        state.isStreamConnecting = false;
        setLogConnPill('disabled', t('log_status_disconnected'));
        updateStreamButton();
        if (!silent) toast.info(t('log_stream_disconnected'));
    }

    function updateStreamButton() {
        const btn = $('toggle-stream');
        if (!btn) return;
        const on = state.isStreamConnected;
        btn.querySelector('.text').textContent = t(on ? 'log_stream_disable' : 'log_stream_enable');
    }

    /* =============================================================
       Token visibility
       ============================================================= */

    function setTokenVisible(input, btn, visible) {
        input.type = visible ? 'text' : 'password';
        if (btn) {
            btn.setAttribute('aria-pressed', String(visible));
            btn.setAttribute('aria-label', t(visible ? 'token_hide' : 'token_show'));
        }
    }

    let tokenHideTimer = null;
    function toggleTokenVisibility() {
        const input = $('token-input');
        const btn = $('toggle-token');
        if (!input || !btn) return;
        const visible = input.type === 'text';
        setTokenVisible(input, btn, !visible);
        if (!visible) {
            // auto-hide after 10s
            if (tokenHideTimer) clearTimeout(tokenHideTimer);
            tokenHideTimer = setTimeout(() => {
                setTokenVisible(input, btn, false);
                tokenHideTimer = null;
            }, 10000);
        } else {
            if (tokenHideTimer) { clearTimeout(tokenHideTimer); tokenHideTimer = null; }
        }
    }

    /* =============================================================
       Metrics port visibility
       ============================================================= */

    function updateMetricsVisibility() {
        const on = $('metrics-enable-toggle')?.checked;
        const field = $('metrics-port-field');
        const input = $('metrics-port-input');
        if (field) field.hidden = !on;
        if (input) input.disabled = !on;
    }

    /* =============================================================
       Toggles
       ============================================================= */

    function syncToggleRow(checkboxId) {
        const cb = $(checkboxId);
        if (!cb) return;
        cb.addEventListener('change', () => saveConfig({ source: 'toggle' }));
    }

    /* =============================================================
       Features
       ============================================================= */

    async function fetchFeatures() {
        try {
            const data = await apiGet('/features');
            state.features = data;
            applyFeatureVisibility(data);
            if ($('feature-manager-toggle')) $('feature-manager-toggle').checked = !!data.tunnel_manager;
            if ($('feature-ddns-toggle')) {
                $('feature-ddns-toggle').checked = !!data.ddns;
                $('feature-ddns-toggle').disabled = !data.tunnel_manager;
            }
            if ($('feature-mcp-toggle')) $('feature-mcp-toggle').checked = !!data.mcp;
        } catch (err) {
            console.error('features fetch failed', err);
        }
    }

    function applyFeatureVisibility(data) {
        const show = (id, on) => {
            const el = $(id);
            if (el) el.hidden = !on;
        };
        show('tab-manager', !!data.tunnel_manager);
        show('tab-ddns', !!data.ddns);
        show('tab-mcp', !!data.mcp);
        show('tab-features', true);
    }

    async function saveFeature(key, value) {
        if (key === 'ddns' && value && !state.features?.tunnel_manager) {
            $('feature-ddns-toggle').checked = false;
            toast.err(t('feature_ddns_requires_manager'));
            return;
        }
        try {
            const data = await apiSend('/features', 'POST', { [key]: value });
            state.features = data;
            applyFeatureVisibility(data);
            if ($('feature-manager-toggle')) $('feature-manager-toggle').checked = !!data.tunnel_manager;
            if ($('feature-ddns-toggle')) {
                $('feature-ddns-toggle').checked = !!data.ddns;
                $('feature-ddns-toggle').disabled = !data.tunnel_manager;
            }
            if ($('feature-mcp-toggle')) $('feature-mcp-toggle').checked = !!data.mcp;
            // Features and panel enable flags share the same backend state.
            // Re-pull panel settings so the status pill reflects the new value
            // immediately (no need to open & re-save the panel).
            if (key === 'tunnel_manager') await fetchTunnelManagerSettings();
            if (key === 'ddns') await fetchDDNSConfig();
            toast.ok(t('feature_updated'));
        } catch (err) {
            toast.err(err.message);
            await fetchFeatures();
        }
    }

    /* =============================================================
       Tunnel manager
       ============================================================= */

    async function fetchTunnelManagerSettings() {
        try {
            const data = await apiGet('/tunnel-manager/settings');
            state.tunnelManager.settings = data;
            renderTunnelManagerSettings(data);
        } catch (err) {
            setManagerStatus('error', t('error_generic', { err: err.message }));
            addLog({ key: 'tunnel_manager_settings_failed', params: { err: err.message } }, 'error');
        }
    }

    function renderTunnelManagerSettings(s) {
        $('manager-enable-toggle').checked = !!s.enabled;
        $('manager-account-id').value = s.account_id || '';
        $('manager-tunnel-id').value = s.tunnel_id || '';
        $('manager-auth-mode').value = s.auth_mode === 'key' ? 'key' : 'token';
        $('manager-api-email').value = s.api_email || '';
        $('manager-api-token').value = s.api_token || '';
        $('manager-api-key').value = s.api_key || '';
        $('manager-token-state').textContent = t(s.api_token_set ? 'api_token_configured' : 'api_token_not_saved');
        $('manager-key-state').textContent = t(s.api_key_set ? 'api_key_configured' : 'api_key_not_saved');
        updateManagerAuthMode();
        setManagerStatus(s.enabled ? 'ok' : 'disabled', t(s.enabled ? 'manager_status_ready' : 'manager_status_disabled'));
        if (s.derived_from_token) {
            $('manager-account-help').textContent = t('account_id_derived_from_token');
            $('manager-tunnel-help').textContent = t('tunnel_id_derived_from_token');
        } else if (s.derive_token_failed) {
            $('manager-account-help').textContent = t('token_identity_parse_failed');
            $('manager-tunnel-help').textContent = t('token_identity_parse_failed');
        }
        // Don't auto-collapse; keep user's choice
    }

    function updateManagerAuthMode() {
        const keyMode = $('manager-auth-mode')?.value === 'key';
        if ($('manager-token-field')) $('manager-token-field').hidden = keyMode;
        if ($('manager-key-fields')) $('manager-key-fields').hidden = !keyMode;
    }

    function setManagerStatus(state_, text) {
        const el = $('manager-status');
        if (!el) return;
        el.setAttribute('data-state', state_);
        el.querySelector('.text').textContent = text;
    }

    async function saveTunnelManagerSettings({ showFeedback = true } = {}) {
        const btn = $('manager-save-settings');
        if (showFeedback) setBusy(btn, true, t('saving'));
        const payload = {
            enabled: $('manager-enable-toggle').checked,
            account_id: $('manager-account-id').value.trim(),
            tunnel_id: $('manager-tunnel-id').value.trim(),
            api_token: $('manager-auth-mode').value === 'token' ? $('manager-api-token').value.trim() : '',
            api_email: $('manager-auth-mode').value === 'key' ? $('manager-api-email').value.trim() : '',
            api_key: $('manager-auth-mode').value === 'key' ? $('manager-api-key').value.trim() : '',
        };
        try {
            const data = await apiSend('/tunnel-manager/settings', 'POST', payload);
            state.tunnelManager.settings = data;
            state.tunnelManager.zonesLoaded = false;
            renderTunnelManagerSettings(data);
            if (showFeedback) {
                toast.ok(t('manager_settings_saved'));
                flashField('manager-save-settings');
            }
            if (canLoadTunnelManagerZones(data)) {
                await loadTunnelManagerZones(true);
            } else {
                state.tunnelManager.zones = [];
                renderTunnelManagerZones();
            }
        } catch (err) {
            setManagerStatus('error', err.message);
            if (showFeedback) toast.err(t('manager_settings_save_failed') + ': ' + err.message);
        } finally {
            if (showFeedback) setBusy(btn, false);
        }
    }

    function canLoadTunnelManagerZones(s = state.tunnelManager.settings) {
        return !!(s?.enabled && s?.account_id && (s?.api_token_set || s?.api_key_set));
    }

    async function loadTunnelManagerZones(silent = false) {
        const btn = silent ? null : $('manager-refresh-zones');
        if (btn) setBusy(btn, true);
        try {
            if (!silent) setManagerStatus('loading', t('manager_status_loading_zones'));
            const data = await apiGet('/tunnel-manager/zones');
            state.tunnelManager.zones = data.zones || [];
            state.tunnelManager.zonesLoaded = true;
            renderTunnelManagerZones();
            if (!silent) {
                setManagerStatus('ok', t('manager_status_zones_loaded'));
                toast.ok(t('manager_status_zones_loaded'));
            }
        } catch (err) {
            if (!silent) {
                setManagerStatus('error', err.message);
                toast.err(t('zone_load_failed') + ': ' + err.message);
            }
        } finally {
            if (btn) setBusy(btn, false);
        }
    }

    async function maybeLoadTunnelManagerZones(quiet = true) {
        if (!canLoadTunnelManagerZones()) {
            state.tunnelManager.zones = [];
            renderTunnelManagerZones();
            return;
        }
        if (state.tunnelManager.zonesLoaded) {
            renderTunnelManagerZones();
            return;
        }
        await loadTunnelManagerZones(quiet);
    }

    function renderTunnelManagerZones() {
        const sel = $('manager-entry-domain-select');
        if (!sel) return;
        const current = $('manager-entry-domain').value.trim() || sel.value;
        const zones = state.tunnelManager.zones || [];
        sel.innerHTML = '';
        const manual = document.createElement('option');
        manual.value = '';
        manual.textContent = t('manual_domain_option');
        sel.appendChild(manual);
        for (const z of zones) {
            const opt = document.createElement('option');
            opt.value = z.name;
            opt.textContent = z.status ? `${z.name} (${z.status})` : z.name;
            sel.appendChild(opt);
        }
        const names = new Set(zones.map((z) => z.name));
        if (current && names.has(current)) sel.value = current;
        else if (!current && zones.length) sel.value = zones[0].name;
        else sel.value = '';
        updateDomainInputMode();
    }

    function updateDomainInputMode() {
        const sel = $('manager-entry-domain-select');
        const input = $('manager-entry-domain');
        if (!sel || !input) return;
        const manual = !sel.value;
        input.hidden = !manual;
        input.disabled = !manual;
        if (!manual) input.value = sel.value;
    }

    async function loadTunnelManagerConfig(silent = false) {
        const btn = silent ? null : $('manager-load-config');
        if (btn) setBusy(btn, true);
        try {
            setManagerStatus('loading', t('manager_status_loading'));
            const data = await apiGet('/tunnel-manager/config');
            state.tunnelManager.config = data;
            renderTunnelManagerConfig(data);
            setManagerStatus('ok', t('manager_status_loaded'));
            if (!silent) toast.ok(t('manager_config_loaded'));
        } catch (err) {
            setManagerStatus('error', err.message);
            if (!silent) toast.err(t('manager_config_load_failed') + ': ' + err.message);
        } finally {
            if (btn) setBusy(btn, false);
        }
    }

    function renderTunnelManagerConfig(cfg) {
        $('manager-config-panel').hidden = false;
        const meta = $('manager-config-meta');
        meta.innerHTML = '';
        const parts = [
            t('tunnel_label') + ' ' + (cfg.tunnel_id || $('manager-tunnel-id').value),
            t('version_label') + ' ' + (cfg.version || 0),
            (cfg.entries?.length || 0) + ' ' + t('rules_label'),
        ];
        parts.forEach((p, i) => {
            if (i > 0) {
                const sep = document.createElement('span');
                sep.className = 'sep';
                meta.appendChild(sep);
            }
            const span = document.createElement('span');
            span.textContent = p;
            meta.appendChild(span);
        });

        const list = $('manager-rules-list');
        list.innerHTML = '';
        if (!cfg.entries || cfg.entries.length === 0) {
            const empty = document.createElement('div');
            empty.className = 'empty';
            empty.textContent = t('no_ingress_rules');
            list.appendChild(empty);
            return;
        }
        for (const entry of cfg.entries) {
            const row = document.createElement('div');
            row.className = 'rule';
            const body = document.createElement('div');
            body.className = 'body';
            const title = document.createElement('div');
            title.className = 'title';
            title.textContent = entry.hostname || t('catch_all_rule');
            const detail = document.createElement('div');
            detail.className = 'detail';
            const noTls = entry.no_tls_verify ? ` · ${t('no_tls_verify_detail')}` : '';
            detail.textContent = `${entry.path || '/'} → ${entry.service}${noTls}`;
            body.append(title, detail);
            const actions = document.createElement('div');
            actions.className = 'actions';
            const edit = document.createElement('button');
            edit.className = 'btn btn--sm';
            edit.type = 'button';
            edit.textContent = t('edit');
            edit.addEventListener('click', () => openTunnelEntryDialog(entry));
            const del = document.createElement('button');
            del.className = 'btn btn--sm btn--ghost';
            del.type = 'button';
            del.textContent = t('delete');
            del.addEventListener('click', () => confirmDeleteEntry(entry));
            actions.append(edit, del);
            row.append(body, actions);
            list.appendChild(row);
        }
    }

    async function confirmDeleteEntry(entry) {
        const ok = await confirm({
            title: t('delete_rule_title'),
            message: t('delete_rule_message', { hostname: entry.hostname || t('catch_all_rule'), path: entry.path || '/' }),
            okText: t('delete'),
        });
        if (!ok) return;
        await deleteTunnelManagerEntry(entry.index);
    }

    function openTunnelEntryDialog(entry = null) {
        const dialog = $('manager-entry-dialog');
        if (!dialog) return;
        if (entry) fillTunnelEntryForm(entry); else resetTunnelEntryForm();
        const editing = $('manager-entry-index').value !== '';
        $('manager-entry-dialog-title').textContent = t(editing ? 'edit_published_app_title' : 'published_app_title');
        $('manager-entry-submit').querySelector('.text').textContent = t(editing ? 'update_rule' : 'add_rule');
        openDialog(dialog);
    }

    function closeTunnelEntryDialog(reset = false) {
        if (reset) resetTunnelEntryForm();
        closeDialog($('manager-entry-dialog'));
    }

    function fillTunnelEntryForm(entry) {
        const host = splitHostname(entry.hostname || '');
        const svc = splitService(entry.service || '');
        $('manager-entry-index').value = String(entry.index);
        $('manager-entry-subdomain').value = host.subdomain;
        $('manager-entry-domain').value = host.domain;
        renderTunnelManagerZones();
        $('manager-entry-path').value = entry.path || '';
        $('manager-entry-service-type').value = svc.type;
        $('manager-entry-service').value = svc.value;
        $('manager-entry-http-host-header').value = entry.http_host_header || '';
        $('manager-entry-origin-server-name').value = entry.origin_server_name || '';
        $('manager-entry-no-tls').checked = !!entry.no_tls_verify;
        updateServicePlaceholder();
    }

    function resetTunnelEntryForm() {
        $('manager-entry-index').value = '';
        $('manager-entry-subdomain').value = '';
        $('manager-entry-domain').value = '';
        renderTunnelManagerZones();
        $('manager-entry-path').value = '';
        $('manager-entry-service-type').value = 'http';
        $('manager-entry-service').value = '';
        $('manager-entry-http-host-header').value = '';
        $('manager-entry-origin-server-name').value = '';
        $('manager-entry-no-tls').checked = false;
        updateServicePlaceholder();
    }

    function updateServicePlaceholder() {
        const sel = $('manager-entry-service-type');
        const input = $('manager-entry-service');
        if (!sel || !input) return;
        const placeholders = {
            http: 'localhost:8080',
            https: 'localhost:8443',
            ssh: 'localhost:22',
            rdp: 'localhost:3389',
            tcp: 'localhost:5432',
            unix: '/var/run/app.sock',
            http_status: '404',
            raw: 'http://localhost:8080',
        };
        input.placeholder = placeholders[sel.value] || placeholders.http;
    }

    function splitHostname(h) {
        h = (h || '').trim();
        if (!h || !h.includes('.')) return { subdomain: h, domain: '' };
        const parts = h.split('.');
        return { subdomain: parts.shift(), domain: parts.join('.') };
    }

    function splitService(s) {
        s = (s || '').trim();
        if (s.startsWith('http_status:')) return { type: 'http_status', value: s.slice('http_status:'.length) };
        const m = s.match(/^([a-z_]+):\/\/(.+)$/i);
        if (m) {
            const ok = ['http', 'https', 'ssh', 'rdp', 'tcp', 'unix'].includes(m[1]);
            return { type: ok ? m[1] : 'raw', value: m[2] };
        }
        return { type: 'raw', value: s };
    }

    function buildHostname(sub, dom) {
        sub = (sub || '').trim().replace(/^\.+|\.+$/g, '');
        dom = (dom || '').trim().replace(/^\.+|\.+$/g, '');
        if (!sub) return dom;
        if (!dom) return sub;
        return `${sub}.${dom}`;
    }

    function buildService(type, value) {
        value = (value || '').trim();
        if (type === 'raw') return value;
        if (type === 'http_status') return value.startsWith('http_status:') ? value : `http_status:${value || '404'}`;
        if (value.startsWith(`${type}:`)) return value;
        return `${type}://${value}`;
    }

    async function submitTunnelManagerEntry(e) {
        e.preventDefault();
        const index = $('manager-entry-index').value;
        const entry = {
            hostname: buildHostname($('manager-entry-subdomain').value, $('manager-entry-domain').value),
            path: $('manager-entry-path').value.trim(),
            service: buildService($('manager-entry-service-type').value, $('manager-entry-service').value),
            no_tls_verify: $('manager-entry-no-tls').checked,
            http_host_header: $('manager-entry-http-host-header').value.trim(),
            origin_server_name: $('manager-entry-origin-server-name').value.trim(),
        };
        if (!entry.service) {
            toast.err(t('service_required'));
            return;
        }
        const btn = $('manager-entry-submit');
        setBusy(btn, true);
        const url = index === '' ? '/tunnel-manager/entries' : `/tunnel-manager/entries/${index}`;
        const method = index === '' ? 'POST' : 'PUT';
        try {
            const data = await apiSend(url, method, entry);
            state.tunnelManager.config = data;
            renderTunnelManagerConfig(data);
            resetTunnelEntryForm();
            closeTunnelEntryDialog(false);
            toast.ok(t(index === '' ? 'tunnel_rule_added' : 'tunnel_rule_updated'));
        } catch (err) {
            toast.err(t('tunnel_rule_save_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    async function deleteTunnelManagerEntry(index) {
        try {
            const data = await apiSend(`/tunnel-manager/entries/${index}`, 'DELETE');
            state.tunnelManager.config = data;
            renderTunnelManagerConfig(data);
            toast.ok(t('tunnel_rule_deleted'));
        } catch (err) {
            toast.err(t('tunnel_rule_delete_failed') + ': ' + err.message);
        }
    }

    async function verifyTokenPermissions() {
        const btn = $('manager-verify-permissions');
        const result = $('manager-verify-result');
        const authMode = $('manager-auth-mode')?.value || 'token';
        const payload = {
            auth_mode: authMode,
            api_token: authMode === 'token' ? $('manager-api-token')?.value.trim() : '',
            api_email: authMode === 'key' ? $('manager-api-email')?.value.trim() : '',
            api_key: authMode === 'key' ? $('manager-api-key')?.value.trim() : '',
        };
        if (authMode === 'token' && !payload.api_token && !state.tunnelManager.settings?.api_token_set) {
            result.hidden = false;
            result.innerHTML = '';
            const span = document.createElement('span');
            span.className = 'pill';
            span.setAttribute('data-state', 'error');
            span.textContent = t('verify_enter_token');
            result.appendChild(span);
            toast.err(t('verify_enter_token'));
            return;
        }
        setBusy(btn, true, t('verify_checking'));
        result.hidden = false;
        result.innerHTML = '';
        const loading = document.createElement('span');
        loading.className = 'pill';
        loading.setAttribute('data-state', 'loading');
        loading.textContent = t('verify_checking');
        result.appendChild(loading);
        try {
            const data = await apiSend('/tunnel-manager/verify-token', 'POST', payload);
            renderVerifyResult(data);
            toast[data.valid ? 'ok' : 'err'](t(data.valid ? 'verify_permissions_passed' : 'verify_permissions_failed'));
        } catch (err) {
            result.innerHTML = '';
            const span = document.createElement('span');
            span.className = 'pill';
            span.setAttribute('data-state', 'error');
            span.textContent = err.message;
            result.appendChild(span);
            toast.err(t('verify_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    function renderVerifyResult(data) {
        const result = $('manager-verify-result');
        if (!result) return;
        result.innerHTML = '';
        if (data.token_status === 'inactive' || data.token_status === 'revoked') {
            const span = document.createElement('span');
            span.className = 'pill';
            span.setAttribute('data-state', 'error');
            span.textContent = t('verify_token_status') + ': ' + data.token_status;
            result.appendChild(span);
            return;
        }
        const perms = data.permissions || [];
        for (const p of perms) {
            const span = document.createElement('span');
            span.className = 'pill';
            const state_ = p.granted ? 'ok' : (p.required ? 'error' : 'ok');
            span.setAttribute('data-state', state_);
            const icon = document.createElement('span');
            icon.className = 'dot';
            const text = document.createElement('span');
            text.textContent = ' ' + p.description;
            span.append(icon, text);
            result.appendChild(span);
        }
    }

    /* =============================================================
       DDNS
       ============================================================= */

    async function refreshDDNS() {
        await fetchDDNSConfig();
        await fetchDDNSStatus();
    }

    async function fetchDDNSConfig() {
        try {
            const data = await apiGet('/ddns/config');
            state.ddns.config = data;
            renderDDNSConfig(data);
        } catch (err) {
            setDDNSStatus('error', err.message);
        }
    }

    function renderDDNSConfig(cfg) {
        const credsMissing = !cfg.has_credentials;
        // The "no credentials" callout is shown when credentials are missing,
        // but the status pill reflects the user's enable intent (cfg.enabled),
        // not the credentials state. The user may have just enabled DDNS in
        // Features and the pill should reflect that immediately.
        $('ddns-no-creds').hidden = !credsMissing;
        $('ddns-main').hidden = credsMissing;
        if (!credsMissing) {
            const v4 = (cfg.ip_sources || []).filter((s) => s.ip_type === 'ipv4').map((s) => s.url).join('\n');
            const v6 = (cfg.ip_sources || []).filter((s) => s.ip_type === 'ipv6').map((s) => s.url).join('\n');
            $('ddns-ipv4-textarea').value = v4;
            $('ddns-ipv6-textarea').value = v6;
            $('ddns-interval').value = String(cfg.interval_mins || 5);
            $('ddns-max-retries').value = String(cfg.max_retries || 3);
            $('ddns-only-on-change').checked = cfg.only_on_change !== false;
            renderDDNSRecords(cfg.records || []);
        }
        setDDNSStatus(cfg.enabled ? 'ok' : 'disabled', t(cfg.enabled ? 'ddns_status_running' : 'ddns_status_disabled'));
    }

    async function fetchDDNSStatus() {
        try {
            const data = await apiGet('/ddns/status');
            state.ddns.status = data;
            renderDDNSStatus(data);
        } catch { /* silent */ }
    }

    function renderDDNSStatus(data) {
        if ($('ddns-ipv4-value')) $('ddns-ipv4-value').textContent = data.current_v4 || t('ddns_unknown');
        if ($('ddns-ipv6-value')) $('ddns-ipv6-value').textContent = data.current_v6 || t('ddns_unknown');
        if ($('ddns-last-check')) {
            $('ddns-last-check').textContent = data.last_check
                ? `${t('ddns_last_check')}: ${new Date(data.last_check).toLocaleString()}`
                : '—';
        }
        if ($('ddns-sync-log-list') && data.results) {
            const list = $('ddns-sync-log-list');
            list.innerHTML = '';
            const results = data.results.slice().reverse();
            if (results.length === 0) {
                const empty = document.createElement('div');
                empty.className = 'empty';
                empty.textContent = t('ddns_no_sync_history');
                list.appendChild(empty);
            } else {
                for (const r of results) {
                    const row = document.createElement('div');
                    row.className = 'item';
                    const ts = document.createElement('span');
                    ts.className = 'ts';
                    ts.textContent = new Date(r.time).toLocaleTimeString();
                    const ind = document.createElement('span');
                    ind.className = `indicator ${r.success ? 'ok' : 'err'}`;
                    ind.textContent = r.success ? '✓' : '✗';
                    const host = document.createElement('span');
                    host.className = 'host';
                    host.textContent = r.hostname || '';
                    const ip = document.createElement('span');
                    ip.className = 'ip';
                    ip.textContent = r.ip || '';
                    const msg = document.createElement('span');
                    msg.className = 'msg';
                    msg.textContent = r.message || '';
                    row.append(ts, ind, host, ip, msg);
                    list.appendChild(row);
                }
            }
        }
    }

    function setDDNSStatus(state_, text) {
        const el = $('ddns-status');
        if (!el) return;
        el.setAttribute('data-state', state_);
        el.querySelector('.text').textContent = text;
    }

    async function ddnsSaveSettings() {
        const btn = $('ddns-save-settings');
        setBusy(btn, true, t('saving'));
        const v4Lines = $('ddns-ipv4-textarea').value.split('\n').map((l) => l.trim()).filter(Boolean);
        const v6Lines = $('ddns-ipv6-textarea').value.split('\n').map((l) => l.trim()).filter(Boolean);
        const sources = [
            ...v4Lines.map((url) => ({ url, ip_type: 'ipv4' })),
            ...v6Lines.map((url) => ({ url, ip_type: 'ipv6' })),
        ];
        const payload = {
            enabled: state.ddns.config?.enabled ?? false,
            ip_sources: sources,
            interval_mins: parseInt($('ddns-interval').value, 10) || 5,
            max_retries: parseInt($('ddns-max-retries').value, 10) || 3,
            only_on_change: $('ddns-only-on-change').checked,
        };
        try {
            const data = await apiSend('/ddns/config', 'POST', payload);
            state.ddns.config = data;
            renderDDNSConfig(data);
            toast.ok(t('ddns_settings_saved'));
        } catch (err) {
            toast.err(t('ddns_save_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    async function ddnsSyncNow() {
        const btn = $('ddns-sync-now');
        setBusy(btn, true, t('ddns_status_syncing'));
        try {
            const data = await apiSend('/ddns/sync-now', 'POST');
            state.ddns.status = data;
            renderDDNSStatus(data);
            setDDNSStatus('ok', t('ddns_status_running'));
            toast.ok(t('ddns_sync_triggered'));
        } catch (err) {
            setDDNSStatus('error', err.message);
            toast.err(t('ddns_sync_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    function defaultDDNSRecordValue(type) {
        return type === 'AAAA' ? '{IPV6}' : '{IPV4}';
    }

    function normalizeDDNSRecordValue(type, value) {
        const trimmed = (value || '').trim();
        return trimmed || defaultDDNSRecordValue(type);
    }

    function formatDDNSRecordValue(rec) {
        const n = normalizeDDNSRecordValue(rec.type, rec.value);
        if (n === '{IPV4}') return `{IPV4} · ${t('ddns_record_value_auto_ipv4')}`;
        if (n === '{IPV6}') return `{IPV6} · ${t('ddns_record_value_auto_ipv6')}`;
        return n;
    }

    function syncDDNSRecordValueFields() {
        const g4 = $('ddns-record-ipv4-value-group');
        const g6 = $('ddns-record-ipv6-value-group');
        if (!g4 || !g6) return;
        const show4 = $('ddns-record-ipv4')?.checked;
        const show6 = $('ddns-record-ipv6')?.checked;
        g4.hidden = !show4;
        g6.hidden = !show6;
        if ($('ddns-record-ipv4-value')) {
            if (!$('ddns-record-ipv4-value').value.trim()) $('ddns-record-ipv4-value').value = defaultDDNSRecordValue('A');
            $('ddns-record-ipv4-value').disabled = !show4;
        }
        if ($('ddns-record-ipv6-value')) {
            if (!$('ddns-record-ipv6-value').value.trim()) $('ddns-record-ipv6-value').value = defaultDDNSRecordValue('AAAA');
            $('ddns-record-ipv6-value').disabled = !show6;
        }
    }

    function renderDDNSRecords(records) {
        const list = $('ddns-records-list');
        if (!list) return;
        list.innerHTML = '';
        if (!records || records.length === 0) {
            const empty = document.createElement('div');
            empty.className = 'empty';
            empty.textContent = t('ddns_no_records');
            list.appendChild(empty);
            return;
        }
        records.forEach((rec, i) => {
            const row = document.createElement('div');
            row.className = 'rule';
            const body = document.createElement('div');
            body.className = 'body';
            const title = document.createElement('div');
            title.className = 'title';
            title.textContent = rec.name || '—';
            const detail = document.createElement('div');
            detail.className = 'detail';
            const ttlText = rec.ttl === 1 ? t('ddns_ttl_auto') : rec.ttl + 's';
            const proxied = rec.proxied ? ` · ${t('ddns_record_proxied')}` : '';
            detail.textContent = `${rec.type} · ${t('ddns_record_value')}: ${formatDDNSRecordValue(rec)} · ${t('ddns_record_ttl')}: ${ttlText}${proxied}`;
            body.append(title, detail);
            const actions = document.createElement('div');
            actions.className = 'actions';
            const editBtn = document.createElement('button');
            editBtn.type = 'button';
            editBtn.className = 'btn btn--sm';
            editBtn.textContent = t('edit');
            editBtn.addEventListener('click', () => editDDNSRecord(i, rec));
            const delBtn = document.createElement('button');
            delBtn.type = 'button';
            delBtn.className = 'btn btn--sm btn--ghost';
            delBtn.textContent = t('delete');
            delBtn.addEventListener('click', () => confirmDeleteDDNSRecord(i, rec));
            actions.append(editBtn, delBtn);
            row.append(body, actions);
            list.appendChild(row);
        });
    }

    async function confirmDeleteDDNSRecord(index, rec) {
        const ok = await confirm({
            title: t('delete_ddns_record_title'),
            message: t('delete_ddns_record_message', { name: rec.name || '' }),
            okText: t('delete'),
        });
        if (!ok) return;
        await deleteDDNSRecord(index);
    }

    function openDDNSRecordDialog(index = null, rec = null) {
        const dialog = $('ddns-record-dialog');
        if (!dialog) return;
        if (rec) fillDDNSRecordForm(index, rec); else resetDDNSRecordForm();
        const editing = rec != null;
        $('ddns-record-dialog-title').textContent = t(editing ? 'ddns_edit_record' : 'ddns_add_record');
        $('ddns-record-submit').querySelector('.text').textContent = t(editing ? 'update_rule' : 'ddns_add_record');
        openDialog(dialog);
        loadDDNSZones().then(() => {
            if (rec?.zone_id && $('ddns-record-zone-select')) {
                $('ddns-record-zone-select').value = rec.zone_id;
            }
        });
    }

    function closeDDNSRecordDialog(reset = false) {
        if (reset) resetDDNSRecordForm();
        closeDialog($('ddns-record-dialog'));
    }

    function fillDDNSRecordForm(index, rec) {
        $('ddns-record-subdomain').value = rec.subdomain || '';
        $('ddns-record-ipv4').checked = rec.type === 'A';
        $('ddns-record-ipv6').checked = rec.type === 'AAAA';
        $('ddns-record-ipv4-value').value = normalizeDDNSRecordValue('A', rec.type === 'A' ? rec.value : '');
        $('ddns-record-ipv6-value').value = normalizeDDNSRecordValue('AAAA', rec.type === 'AAAA' ? rec.value : '');
        $('ddns-record-ttl-select').value = String(rec.ttl || 1);
        $('ddns-record-proxied').checked = rec.proxied !== false;
        $('ddns-record-form').dataset.editIndex = String(index);
        $('ddns-record-ipv4').disabled = true;
        $('ddns-record-ipv6').disabled = true;
        syncDDNSRecordValueFields();
    }

    function resetDDNSRecordForm() {
        $('ddns-record-subdomain').value = '';
        $('ddns-record-ipv4').checked = true;
        $('ddns-record-ipv6').checked = true;
        $('ddns-record-ipv4-value').value = defaultDDNSRecordValue('A');
        $('ddns-record-ipv6-value').value = defaultDDNSRecordValue('AAAA');
        $('ddns-record-ttl-select').value = '1';
        $('ddns-record-proxied').checked = true;
        delete $('ddns-record-form').dataset.editIndex;
        $('ddns-record-ipv4').disabled = false;
        $('ddns-record-ipv6').disabled = false;
        syncDDNSRecordValueFields();
    }

    async function ddnsSubmitRecord(e) {
        e.preventDefault();
        const editing = $('ddns-record-form').dataset.editIndex;
        const sel = $('ddns-record-zone-select');
        const zoneName = sel?.selectedOptions[0]?.textContent?.replace(/ \(.*\)/, '') || '';
        const entry = {
            subdomain: $('ddns-record-subdomain').value.trim(),
            zone_id: sel?.value,
            zone_name: zoneName,
            ipv4: $('ddns-record-ipv4').checked,
            ipv6: $('ddns-record-ipv6').checked,
            ipv4_value: normalizeDDNSRecordValue('A', $('ddns-record-ipv4-value').value),
            ipv6_value: normalizeDDNSRecordValue('AAAA', $('ddns-record-ipv6-value').value),
            proxied: $('ddns-record-proxied').checked,
            ttl: parseInt($('ddns-record-ttl-select').value, 10) || 1,
        };
        if (editing) {
            entry.value = entry.ipv4
                ? normalizeDDNSRecordValue('A', $('ddns-record-ipv4-value').value)
                : normalizeDDNSRecordValue('AAAA', $('ddns-record-ipv6-value').value);
        }
        if (!entry.ipv4 && !entry.ipv6) {
            toast.err(t('ddns_record_ip_required'));
            return;
        }
        const btn = $('ddns-record-submit');
        setBusy(btn, true);
        try {
            const url = editing ? `/ddns/records/${editing}` : '/ddns/records';
            const method = editing ? 'PUT' : 'POST';
            const data = await apiSend(url, method, entry);
            state.ddns.config = data;
            renderDDNSConfig(data);
            resetDDNSRecordForm();
            closeDDNSRecordDialog(false);
            toast.ok(t(editing ? 'ddns_record_updated' : 'ddns_record_added'));
        } catch (err) {
            toast.err(t('ddns_record_save_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    async function deleteDDNSRecord(index) {
        try {
            const data = await apiSend(`/ddns/records/${index}`, 'DELETE');
            state.ddns.config = data;
            renderDDNSConfig(data);
            toast.ok(t('ddns_record_deleted'));
        } catch (err) {
            toast.err(t('ddns_record_delete_failed') + ': ' + err.message);
        }
    }

    async function loadDDNSZones() {
        if (state.ddns.zonesLoaded) {
            renderDDNSZones();
            return;
        }
        try {
            const data = await apiGet('/ddns/zones');
            state.ddns.zones = data.zones || [];
            state.ddns.zonesLoaded = true;
            renderDDNSZones();
        } catch (err) {
            toast.err(t('zone_load_failed') + ': ' + err.message);
        }
    }

    function renderDDNSZones() {
        const sel = $('ddns-record-zone-select');
        if (!sel) return;
        sel.innerHTML = '';
        for (const z of state.ddns.zones) {
            const opt = document.createElement('option');
            opt.value = z.id;
            opt.textContent = z.name + (z.status ? ` (${z.status})` : '');
            sel.appendChild(opt);
        }
        if (!sel.value && state.ddns.zones.length) sel.value = state.ddns.zones[0].id;
    }

    function editDDNSRecord(index, rec) {
        openDDNSRecordDialog(index, rec);
    }

    function switchDDNSSubTab(name) {
        const sources = $('ddns-subtab-sources');
        const auto = $('ddns-subtab-auto');
        const isSrc = name === 'sources';
        if (sources) {
            sources.setAttribute('aria-selected', String(isSrc));
            sources.tabIndex = isSrc ? 0 : -1;
        }
        if (auto) {
            auto.setAttribute('aria-selected', String(!isSrc));
            auto.tabIndex = isSrc ? -1 : 0;
        }
        $('ddns-panel-sources').hidden = !isSrc;
        $('ddns-panel-auto').hidden = isSrc;
    }

    /* =============================================================
       MCP
       ============================================================= */

    async function fetchMCPStatus() {
        try {
            const data = await apiGet('/mcp/status');
            state.mcp.status = data;
            state.mcp.tokens = data.tokens || [];
            renderMCPStatus(data);
            renderMCPTokens();
        } catch (err) {
            setMCPStatus('error', err.message);
        }
    }

    function setMCPStatus(state_, text) {
        const el = $('mcp-status');
        if (!el) return;
        el.setAttribute('data-state', state_);
        el.querySelector('.text').textContent = text;
    }

    function renderMCPStatus(status) {
        const endpoint = status.endpoint || '/mcp';
        const absolute = `${window.location.origin}${endpoint}`;
        $('mcp-endpoint').value = absolute;
        updateMCPConfigExample(absolute);
        setMCPStatus(status.enabled ? 'ok' : 'disabled', t(status.enabled ? 'mcp_status_enabled' : 'mcp_status_disabled'));
    }

    function updateMCPConfigExample(endpoint) {
        const example = $('mcp-config-example');
        if (!example) return;
        example.textContent = `{
  "mcpServers": {
    "cfui": {
      "url": "${endpoint}",
      "headers": {
        "Authorization": "Bearer YOUR_TOKEN"
      }
    }
  }
}`;
    }

    function renderMCPTokens() {
        const list = $('mcp-token-list');
        if (!list) return;
        list.innerHTML = '';
        const tokens = state.mcp.tokens || [];
        if (tokens.length === 0) {
            const empty = document.createElement('div');
            empty.className = 'empty';
            empty.textContent = t('mcp_no_tokens');
            list.appendChild(empty);
            return;
        }
        for (const token of tokens) {
            const row = document.createElement('div');
            row.className = 'rule';
            const body = document.createElement('div');
            body.className = 'body';
            const title = document.createElement('div');
            title.className = 'title';
            title.textContent = token.name || t('mcp_token');
            const detail = document.createElement('div');
            detail.className = 'detail';
            const createdAt = token.created_at ? new Date(token.created_at).toLocaleString() : '';
            detail.textContent = createdAt ? `${token.masked} · ${createdAt}` : token.masked;
            body.append(title, detail);
            const actions = document.createElement('div');
            actions.className = 'actions';
            const del = document.createElement('button');
            del.className = 'btn btn--sm btn--ghost';
            del.type = 'button';
            del.textContent = t('delete');
            del.addEventListener('click', () => confirmDeleteMCPToken(token));
            actions.append(del);
            row.append(body, actions);
            list.appendChild(row);
        }
    }

    async function confirmDeleteMCPToken(token) {
        const ok = await confirm({
            title: t('delete_mcp_token_title'),
            message: t('delete_mcp_token_message', { name: token.name || t('mcp_token') }),
            okText: t('delete'),
        });
        if (!ok) return;
        await deleteMCPToken(token.id);
    }

    async function deleteMCPToken(id) {
        try {
            await apiSend(`/mcp/tokens/${encodeURIComponent(id)}`, 'DELETE');
            await fetchMCPStatus();
            toast.ok(t('mcp_token_deleted'));
        } catch (err) {
            toast.err(t('mcp_token_delete_failed') + ': ' + err.message);
        }
    }

    function showCreatedMCPToken(token) {
        const box = $('mcp-created-token');
        const code = $('mcp-created-value');
        if (!box || !code) return;
        code.textContent = token || '';
        box.hidden = !token;
        if (token) {
            navigator.clipboard?.writeText(token).then(
                () => toast.ok(t('copied_to_clipboard')),
                () => { /* ignore */ },
            );
        }
    }

    async function createMCPToken(e) {
        e.preventDefault();
        const name = $('mcp-token-name').value.trim();
        const token = $('mcp-token-input').value.trim();
        if (token && token.length < 16) {
            const ok = await confirm({
                title: t('weak_token_title'),
                message: t('weak_token_message'),
                okText: t('continue'),
                okClass: 'btn--primary',
            });
            if (!ok) return;
        }
        const btn = $('mcp-token-create');
        setBusy(btn, true, t('creating'));
        try {
            const data = await apiSend('/mcp/tokens', 'POST', { name, token });
            $('mcp-token-name').value = '';
            $('mcp-token-input').value = '';
            showCreatedMCPToken(data.token);
            await fetchMCPStatus();
            toast.ok(t('mcp_token_created'));
        } catch (err) {
            toast.err(t('mcp_token_create_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    /* =============================================================
       TLS confirm (production-affecting toggle)
       ============================================================= */

    async function maybeConfirmTLS() {
        const cb = $('no-tls-verify-toggle');
        if (!cb || !cb.checked) return;
        const ok = await confirm({
            title: t('tls_disable_title'),
            message: t('tls_disable_message'),
            okText: t('tls_i_understand'),
            okClass: 'btn--danger',
        });
        if (!ok) {
            cb.checked = false;
        }
    }

    /* =============================================================
       Language menu
       ============================================================= */

    function openLangMenu() {
        const menu = $('lang-menu');
        if (!menu) return;
        $$('[data-lang]', menu).forEach((b) => {
            b.classList.toggle('btn--primary', b.dataset.lang === state.currentLang);
        });
        openDialog(menu);
    }

    /* =============================================================
       Event wiring
       ============================================================= */

    function wireEvents() {
        $('theme-btn')?.addEventListener('click', toggleTheme);
        $('lang-btn')?.addEventListener('click', openLangMenu);
        $$('[data-lang]').forEach((b) => {
            b.addEventListener('click', () => {
                loadLanguage(b.dataset.lang);
                closeDialog($('lang-menu'));
            });
        });

        // Token
        $('toggle-token')?.addEventListener('click', toggleTokenVisibility);
        $('token-input')?.addEventListener('blur', () => saveConfig({ source: 'input' }));
        $('custom-version-input')?.addEventListener('change', () => saveConfig({ source: 'input' }));
        $('software-name-input')?.addEventListener('change', () => saveConfig({ source: 'input' }));
        $('protocol-select')?.addEventListener('change', () => saveConfig({ source: 'input' }));
        $('grace-period-input')?.addEventListener('change', () => saveConfig({ source: 'input' }));
        $('region-select')?.addEventListener('change', () => saveConfig({ source: 'input' }));
        $('retries-input')?.addEventListener('change', () => saveConfig({ source: 'input' }));
        $('edge-bind-address-input')?.addEventListener('change', () => saveConfig({ source: 'input' }));
        $('metrics-port-input')?.addEventListener('change', () => saveConfig({ source: 'input' }));
        $('metrics-enable-toggle')?.addEventListener('change', () => { updateMetricsVisibility(); saveConfig({ source: 'toggle' }); });
        $('autostart-toggle')?.addEventListener('change', () => saveConfig({ source: 'toggle' }));
        $('autorestart-toggle')?.addEventListener('change', () => saveConfig({ source: 'toggle' }));
        $('no-tls-verify-toggle')?.addEventListener('change', async () => { await maybeConfirmTLS(); saveConfig({ source: 'toggle' }); });

        $('action-btn')?.addEventListener('click', onActionClick);

        // Logs
        $('toggle-stream')?.addEventListener('click', () => {
            if (state.isStreamConnected || state.isStreamConnecting) disconnectLogStream(); else connectLogStream();
        });
        $('clear-logs')?.addEventListener('click', clearLogs);
        $('copy-logs')?.addEventListener('click', copyLogs);

        // Features
        $('feature-manager-toggle')?.addEventListener('change', (e) => saveFeature('tunnel_manager', e.target.checked));
        $('feature-ddns-toggle')?.addEventListener('change', (e) => saveFeature('ddns', e.target.checked));
        $('feature-mcp-toggle')?.addEventListener('change', (e) => saveFeature('mcp', e.target.checked));

        // Manager
        $('manager-auth-mode')?.addEventListener('change', updateManagerAuthMode);
        $('manager-save-settings')?.addEventListener('click', () => saveTunnelManagerSettings({ showFeedback: true }));
        $('manager-load-config')?.addEventListener('click', () => loadTunnelManagerConfig(false));
        $('manager-refresh-zones')?.addEventListener('click', () => loadTunnelManagerZones(false));
        $('manager-add-entry-btn')?.addEventListener('click', () => openTunnelEntryDialog());
        $('manager-entry-form')?.addEventListener('submit', submitTunnelManagerEntry);
        $('manager-entry-domain-select')?.addEventListener('change', updateDomainInputMode);
        $('manager-entry-service-type')?.addEventListener('change', updateServicePlaceholder);
        $('manager-verify-permissions')?.addEventListener('click', verifyTokenPermissions);
        $('manager-api-token-toggle')?.addEventListener('click', () => {
            const input = $('manager-api-token');
            const btn = $('manager-api-token-toggle');
            setTokenVisible(input, btn, input.type === 'password');
        });
        $('manager-api-key-toggle')?.addEventListener('click', () => {
            const input = $('manager-api-key');
            const btn = $('manager-api-key-toggle');
            setTokenVisible(input, btn, input.type === 'password');
        });

        // MCP
        $('mcp-help-toggle')?.addEventListener('click', () => {
            const panel = $('mcp-help-panel');
            const hidden = panel.hidden;
            panel.hidden = !hidden;
            $('mcp-help-toggle').setAttribute('aria-expanded', String(hidden));
        });
        $('mcp-token-form')?.addEventListener('submit', createMCPToken);
        $('mcp-copy-created')?.addEventListener('click', () => {
            const v = $('mcp-created-value')?.textContent || '';
            navigator.clipboard?.writeText(v).then(
                () => toast.ok(t('copied_to_clipboard')),
                () => toast.err(t('copy_failed')),
            );
        });

        // DDNS
        $('ddns-sync-now')?.addEventListener('click', ddnsSyncNow);
        $('ddns-save-settings')?.addEventListener('click', ddnsSaveSettings);
        $('ddns-add-record-btn')?.addEventListener('click', () => openDDNSRecordDialog());
        $('ddns-record-form')?.addEventListener('submit', ddnsSubmitRecord);
        $('ddns-record-ipv4')?.addEventListener('change', syncDDNSRecordValueFields);
        $('ddns-record-ipv6')?.addEventListener('change', syncDDNSRecordValueFields);
        $('ddns-subtab-sources')?.addEventListener('click', () => switchDDNSSubTab('sources'));
        $('ddns-subtab-auto')?.addEventListener('click', () => switchDDNSSubTab('auto'));

        // Click outside any open help-popover to close
        document.addEventListener('click', (e) => {
            $$('details.help-popover[open]').forEach((p) => {
                if (!p.contains(e.target)) p.removeAttribute('open');
            });
        });

        // Keyboard: Ctrl+Enter to start/stop tunnel
        document.addEventListener('keydown', (e) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
                e.preventDefault();
                onActionClick();
            }
        });
    }

    /* =============================================================
       Init
       ============================================================= */

    function restoreLastTab() {
        const last = localStorage.getItem('lastTab');
        if (last) {
            const tab = document.querySelector(`.tab[data-tab="${last}"]`);
            if (tab && !tab.hidden) activateTab(last);
        }
    }

    async function init() {
        initTheme();
        wireEvents();
        await loadLanguage(state.currentLang);
        updateMetricsVisibility();
        addLog({ key: 'system_ready' }, 'system');
        await fetchVersion();
        await fetchConfig();
        await fetchFeatures();
        restoreLastTab();
        if (state.features.tunnel_manager) {
            await fetchTunnelManagerSettings();
            await maybeLoadTunnelManagerZones(true);
            if (canLoadTunnelManagerZones()) await loadTunnelManagerConfig(true);
        }
        if (state.features.mcp) await fetchMCPStatus();
        if (state.features.ddns) await refreshDDNS();
        await fetchStatus();
        setInterval(fetchStatus, 2000);
        setInterval(() => {
            if (!$('panel-ddns').hidden) fetchDDNSStatus();
        }, 10000);
    }

    // Refresh data when panels become visible (e.g. after toggling a feature
    // in the Features tab and then opening the now-visible panel). Without
    // this the status pills would show stale state from the last fetch.
    document.addEventListener('tabchange', (e) => {
        const name = e.detail?.name;
        if (name === 'manager' && state.features?.tunnel_manager) {
            fetchTunnelManagerSettings();
        } else if (name === 'ddns' && state.features?.ddns) {
            refreshDDNS();
        }
    });

    window.addEventListener('beforeunload', () => disconnectLogStream(true));

    init().catch((err) => {
        console.error('init failed', err);
        toast.err('Initialization failed: ' + err.message);
    });
})();
