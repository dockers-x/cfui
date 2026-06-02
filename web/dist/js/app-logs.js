/* =========================================================================
   CloudFlared UI — Logs: keyed entries, SSE streaming with JSON parsing,
   level filter, smart auto-scroll, copy + download
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, t, formatTime, toast, API_BASE } = window.cfui;

    const LEVEL_ORDER = { debug: 0, info: 1, warn: 2, error: 3, fatal: 4 };
    const LEVEL_CLASS = { DEBUG: 'debug', INFO: 'info', WARN: 'warn', WARNING: 'warn', ERROR: 'error', FATAL: 'error', DPANIC: 'error', PANIC: 'error' };

    /* ---- Level filter helpers ---- */

    function levelVisible(level) {
        const f = state.logFilter;
        if (f === 'all') return true;
        const threshold = LEVEL_ORDER[f] ?? 1;
        return (LEVEL_ORDER[level] ?? 1) >= threshold;
    }

    function rowMatchesFilter(row) {
        const classes = row.classList;
        if (state.logFilter === 'all') return true;
        const threshold = LEVEL_ORDER[state.logFilter] ?? 1;
        if (classes.contains('error')) return threshold <= LEVEL_ORDER.error;
        if (classes.contains('warn'))  return threshold <= LEVEL_ORDER.warn;
        if (classes.contains('debug')) return threshold <= LEVEL_ORDER.debug;
        return threshold <= LEVEL_ORDER.info;   /* system + info */
    }

    function applyFilterToContainer() {
        const container = $('logs-container');
        if (!container) return;
        for (const row of container.children) {
            row.hidden = !rowMatchesFilter(row);
        }
        /* Always show "system_ready" first line */
        const first = container.firstElementChild;
        if (first?.classList.contains('system')) first.hidden = false;
    }

    /* ---- Smart auto-scroll ---- */

    function isAtBottom(container) {
        return container.scrollHeight - container.scrollTop - container.clientHeight < 40;
    }

    function maybeScroll(container) {
        if (state.logsAtBottom) {
            container.scrollTop = container.scrollHeight;
        } else {
            showJumpButton();
        }
    }

    function showJumpButton() {
        const btn = $('logs-jump');
        if (btn) btn.hidden = false;
    }

    function hideJumpButton() {
        const btn = $('logs-jump');
        if (btn) btn.hidden = true;
    }

    function jumpToLatest() {
        const container = $('logs-container');
        if (!container) return;
        state.logsAtBottom = true;
        container.scrollTop = container.scrollHeight;
        hideJumpButton();
    }

    /* ---- Keyed log line (translatable entries) ---- */

    function renderKeyedLogLine(entry) {
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
        row.hidden = !rowMatchesFilter(row);
        container.appendChild(row);
        capRenderedLines(container);
        maybeScroll(container);
    }

    /* ---- Streamed JSON line parsing ---- */

    function renderStreamLine(raw) {
        const container = $('logs-container');
        if (!container) return;

        const row = document.createElement('div');
        row.className = 'line info';
        const ts = document.createElement('span');
        ts.className = 'ts';
        const msg = document.createElement('span');
        msg.className = 'msg';

        let parsed = false;
        try {
            const obj = JSON.parse(raw);
            if (obj && typeof obj === 'object' && (obj.level || obj.msg)) {
                const levelKey = (obj.level || 'info').toUpperCase();
                row.className = `line ${LEVEL_CLASS[levelKey] || 'info'}`;
                ts.textContent = obj.time ? formatTime(new Date(obj.time).getTime()) : formatTime(Date.now());
                msg.textContent = obj.msg || raw;
                parsed = true;
            }
        } catch { /* not JSON — render as plain text */ }

        if (!parsed) {
            ts.textContent = formatTime(Date.now());
            msg.textContent = raw;
        }

        row.hidden = !rowMatchesFilter(row);
        row.append(ts, msg);
        container.appendChild(row);
        capRenderedLines(container);
        maybeScroll(container);
    }

    function capRenderedLines(container) {
        while (container.children.length > 500) container.firstElementChild?.remove();
    }

    /* ---- Keyed log storage ---- */

    function addLog(entryOrKey, level = 'info') {
        let entry;
        if (typeof entryOrKey === 'string') {
            entry = { key: entryOrKey, level, ts: Date.now(), id: state.nextLogId++ };
        } else {
            entry = { ...entryOrKey, level: entryOrKey.level || level, ts: entryOrKey.ts || Date.now(), id: state.nextLogId++ };
        }
        state.logs.push(entry);
        if (state.logs.length > 1000) state.logs.shift();
        renderKeyedLogLine(entry);
    }

    /* ---- Clear / copy / download ---- */

    function clearLogs() {
        state.logs = [];
        state.streamLines = [];
        const c = $('logs-container');
        if (c) c.innerHTML = '';
        hideJumpButton();
        toast.info(t('logs_cleared'));
    }

    function formatLogLine(e) {
        return `[${formatTime(e.ts)}] ${t(e.key, e.params)}`;
    }

    function copyLogs() {
        const keyed = state.logs.map(formatLogLine);
        const all = [...keyed, ...state.streamLines];
        navigator.clipboard?.writeText(all.join('\n')).then(
            () => toast.ok(t('logs_copied')),
            () => toast.err(t('copy_failed')),
        );
    }

    function downloadLogs() {
        const keyed = state.logs.map(formatLogLine);
        const all = [...keyed, ...state.streamLines];
        const blob = new Blob([all.join('\n') + '\n'], { type: 'text/plain;charset=utf-8' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        const ts = new Date().toISOString().replace(/[:.]/g, '-').slice(0, 19);
        a.href = url;
        a.download = `cfui-logs-${ts}.log`;
        document.body.appendChild(a);
        a.click();
        a.remove();
        URL.revokeObjectURL(url);
    }

    /* ---- SSE streaming ---- */

    function setLogConnPill(pillState, text) {
        const pill = $('log-conn-state');
        if (!pill) return;
        pill.setAttribute('data-state', pillState);
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
            /* Track raw lines for copy/download */
            state.streamLines.push(e.data);
            if (state.streamLines.length > 2000) state.streamLines.shift();
            renderStreamLine(e.data);
        };
        es.onerror = () => {
            setLogConnPill(state.isStreamConnected ? 'warn' : 'error',
                state.isStreamConnected ? t('log_status_reconnecting') : t('log_status_failed'));
            es.close();
            state.logStream = null;
            state.isStreamConnected = false;
            state.isStreamConnecting = false;
            updateStreamButton();
        };
    }

    function disconnectLogStream(silent = false) {
        if (state.logStream) { state.logStream.close(); state.logStream = null; }
        state.isStreamConnected = false;
        state.isStreamConnecting = false;
        setLogConnPill('disabled', t('log_status_disconnected'));
        updateStreamButton();
        if (!silent) toast.info(t('log_stream_disconnected'));
    }

    function updateStreamButton() {
        const btn = $('toggle-stream');
        if (!btn) return;
        btn.querySelector('.text').textContent = t(state.isStreamConnected ? 'log_stream_disable' : 'log_stream_enable');
    }

    /* ---- Wire ---- */

    function wireLogs() {
        $('toggle-stream')?.addEventListener('click', () => {
            if (state.isStreamConnected || state.isStreamConnecting) disconnectLogStream(); else connectLogStream();
        });
        $('clear-logs')?.addEventListener('click', clearLogs);
        $('copy-logs')?.addEventListener('click', copyLogs);
        $('download-logs')?.addEventListener('click', downloadLogs);
        $('logs-jump')?.addEventListener('click', jumpToLatest);

        /* Level filter */
        const levelSelect = $('log-level-filter');
        if (levelSelect) {
            levelSelect.value = state.logFilter;
            levelSelect.addEventListener('change', () => {
                state.logFilter = levelSelect.value;
                localStorage.setItem('logFilter', state.logFilter);
                applyFilterToContainer();
            });
        }

        /* Scroll tracking (throttled) */
        const container = $('logs-container');
        if (container) {
            let ticking = false;
            container.addEventListener('scroll', () => {
                if (ticking) return;
                ticking = true;
                requestAnimationFrame(() => {
                    state.logsAtBottom = isAtBottom(container);
                    if (state.logsAtBottom) hideJumpButton();
                    ticking = false;
                });
            }, { passive: true });
        }

        /* Initialise filter */
        applyFilterToContainer();
    }

    /* ---- Export ---- */
    const ns = window.cfui;
    ns.renderKeyedLogLine = renderKeyedLogLine;
    ns.addLog = addLog;
    ns.clearLogs = clearLogs;
    ns.connectLogStream = connectLogStream;
    ns.disconnectLogStream = disconnectLogStream;
    ns.wireLogs = wireLogs;
})();
