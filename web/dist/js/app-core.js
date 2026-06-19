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
        workspace: document.documentElement.dataset.workspace === 'cloudflare' ? 'cloudflare' : 'local',
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
        features: {
            mode: 'classic',
            classic_enabled: true,
            oauth_enabled: false,
            local: { tunnel_runner: true, ddns: false, mcp: false, s3_webdav: false },
            cloudflare: { enabled: false, authenticated: false, capabilities: {}, oauth: {} },
            tunnel_manager: false,
            ddns: false,
            mcp: false,
            s3_webdav: false,
            availability: {},
        },
        oauth: {
            status: null,
            relayCheck: null,
            relayCheckLoading: false,
            relayCheckError: '',
            overview: null,
            overviewLoading: false,
            overviewError: '',
            accounts: [],
            zones: [],
            dnsRecords: [],
            tunnels: [],
            localTunnelProfiles: [],
            tunnelConfigs: {},
            tunnelConfigLoading: {},
            tunnelConfigErrors: {},
            tunnelIngressCreateTunnelId: '',
            tunnelIngressEditing: null,
            workers: [],
            workerDetail: null,
            workerMetrics: null,
            workerMetricsError: '',
            workerMetricsLoading: false,
            workerMetricsRange: '24h',
            accountUsage: null,
            accountUsageError: '',
            accountUsageLoading: false,
            zoneDetail: null,
            zoneDetailError: '',
            zoneDetailLoading: false,
            zoneDNSCount: null,
            zoneDNSCountError: '',
            zoneDNSCountLoading: false,
            zoneDNSCountZoneId: '',
            cloudflareStatus: null,
            cloudflareStatusError: '',
            cloudflareStatusLoading: false,
            workerTailSource: null,
            workerTailLines: [],
            workerTailConnecting: false,
            workerTailConnected: false,
            workerTailPaused: false,
            r2Buckets: [],
            r2Metrics: null,
            r2MetricsError: '',
            r2MetricsLoading: false,
            r2Objects: [],
            r2Cursor: '',
            r2ObjectValue: null,
            r2ObjectFilter: '',
            d1Databases: [],
            kvNamespaces: [],
            kvKeys: [],
            kvCursor: '',
            kvValue: null,
            d1Tables: [],
            d1TablesDatabaseId: '',
            d1Results: [],
            d1TableColumns: [],
            d1TableRows: [],
            d1TableOffset: 0,
            d1TableLimit: 50,
            d1TableHasMore: false,
            d1RowIDKey: '_cfui_rowid_',
            d1EditingRow: null,
            snippets: [],
            snippetRules: [],
            snippetContent: null,
            snippetContentLoading: false,
            snippetContentError: '',
            snippetContentDraft: '',
	            snippetContentMainFile: 'snippet.js',
	            wafRuleset: null,
	            wafManagedRuleset: null,
	            wafManagedOverrideRuleset: null,
	            zoneAnalytics: null,
            zoneSettings: [],
            permissionDraft: null,
            permissionDraftSource: '',
            selectedAccountId: '',
            selectedZoneId: '',
            selectedWorkerId: '',
            selectedR2BucketName: '',
            selectedR2ObjectKey: '',
            selectedKVNamespaceId: '',
            selectedKVKey: '',
            selectedD1DatabaseId: '',
            selectedD1TableName: '',
            selectedSnippetName: '',
            resource: 'overview',
            storageView: '',
            r2CreateOpen: false,
            r2ObjectCreateOpen: false,
            kvCreateOpen: false,
            tunnelCreateOpen: false,
            snippetCreateOpen: false,
	            snippetRuleCreateOpen: false,
	            wafCreateOpen: false,
	            wafEditingId: '',
	            wafManagedExceptionCreateOpen: false,
	            wafManagedExceptionEditingId: '',
	            wafManagedOverrideCreateOpen: false,
	            wafManagedOverrideEditingId: '',
	            analyticsRange: '24h',
            d1Sql: "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '_cf_%' ORDER BY name;",
            dnsFormMode: '',
            dnsEditingId: '',
            dnsFilter: '',
            loading: false,
        },
        tunnelManager: { settings: {}, config: null, zones: [], zonesLoaded: false, selectedTunnelKey: '' },
        selectedTunnelKey: '',
        mcp: { status: null, tokens: [] },
        ddns: { config: null, status: null, zones: [], zonesLoaded: false },
        s3: { settings: null, buckets: [], path: '/', files: [], loading: false },
        pendingConfigSave: null,
        pendingConfigSignature: '',
        localConfigSignature: '',
        activeDialog: null,
        dialogStack: [],
        lastFocused: null,
        confirmResolver: null,
        nextLogId: 1,
        logFilter: localStorage.getItem('logFilter') || 'all',
        logsAtBottom: true,
        statusFailCount: 0,
        tunnelProtocol: '',
        lastError: '',
        tunnelAlertDismissed: null,
        /** @type {Record<string,{running:boolean,status:string,protocol:string,error?:string}>} */
        tunnelStatuses: {},
        /** Per-tunnel config signature captured while running (restart hint). */
        runningSigs: {},
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
            const label = t(visible ? 'token_hide' : 'token_show');
            btn.setAttribute('aria-label', label);
            btn.title = label;
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
