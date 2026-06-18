/* =========================================================================
   CloudFlared UI — i18n, theme, dialog, tabs
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, $$, t, apiGet, toast } = window.cfui;

    /* ---- i18n ---- */

    async function loadLanguage(lang) {
        try {
            const res = await fetch(`/api/i18n/${lang}`);
            if (!res.ok) throw new Error('load failed');
            state.translations = await res.json();
        } catch (err) {
            console.error('i18n load failed', lang, err);
            if (lang !== 'en') { await loadLanguage('en'); return; }
            state.translations = {};
        }
        state.currentLang = lang;
        localStorage.setItem('lang', lang);
        document.documentElement.lang = lang;
        applyTranslations();
        applyLogTranslations();
        document.dispatchEvent(new CustomEvent('localechange', { detail: { lang } }));
    }

    function applyTranslations() {
        $$('[data-i18n]').forEach((el) => {
            const key = el.getAttribute('data-i18n');
            if (key) el.textContent = t(key);
        });
        $$('[data-i18n-attr]').forEach((el) => {
            const spec = el.getAttribute('data-i18n-attr');
            if (!spec) return;
            spec.split(';').forEach((part) => {
                const [attr, key] = part.split('|');
                if (attr && key) el.setAttribute(attr, t(key));
            });
        });
        updateWorkspaceChrome();
    }

    function applyLogTranslations() {
        const container = $('logs-container');
        if (!container) return;
        container.innerHTML = '';
        for (const entry of state.logs) {
            const renderKeyed = window.cfui.renderKeyedLogLine;
            if (renderKeyed) renderKeyed(entry); else {
                /* fallback before app-logs loads */
                const row = document.createElement('div');
                row.className = `line ${entry.level}`;
                const ts = document.createElement('span');
                ts.className = 'ts';
                ts.textContent = window.cfui.formatTime(entry.ts);
                const msg = document.createElement('span');
                msg.className = 'msg';
                msg.textContent = t(entry.key, entry.params);
                row.append(ts, msg);
                container.appendChild(row);
            }
        }
        container.scrollTop = container.scrollHeight;
    }

    /* ---- Theme ---- */

    function initTheme() {
        document.documentElement.setAttribute('data-theme', state.currentTheme);
    }

    function toggleTheme() {
        state.currentTheme = state.currentTheme === 'light' ? 'dark' : 'light';
        document.documentElement.setAttribute('data-theme', state.currentTheme);
        localStorage.setItem('theme', state.currentTheme);
        toast.info(t('theme_changed'), { duration: 1800 });
    }

    /* ---- Dialog ---- */

    function openDialog(dialog, options = {}) {
        if (!dialog) return;
        if (state.activeDialog && state.activeDialog !== dialog) {
            if (options.stack) {
                if (!Array.isArray(state.dialogStack)) state.dialogStack = [];
                state.dialogStack.push({ dialog: state.activeDialog, lastFocused: state.lastFocused });
            } else {
                closeDialog(state.activeDialog);
            }
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
        const focusTarget = state.lastFocused;
        const wasActive = state.activeDialog === dialog;
        dialog.hidden = true;
        if (wasActive) {
            state.activeDialog = null;
            if (state.confirmResolver) { state.confirmResolver(false); state.confirmResolver = null; }
            const previous = Array.isArray(state.dialogStack) ? state.dialogStack.pop() : null;
            if (previous?.dialog && !previous.dialog.hidden) {
                state.activeDialog = previous.dialog;
                state.lastFocused = previous.lastFocused || null;
            }
        }
        if (!$$('.modal-backdrop').some((d) => !d.hidden)) document.body.classList.remove('modal-open');
        if (state.activeDialog) {
            if (focusTarget?.focus && state.activeDialog.contains(focusTarget)) {
                focusTarget.focus();
            } else {
                state.activeDialog.querySelector('input, select, textarea, button')?.focus();
            }
        } else if (focusTarget?.focus) {
            focusTarget.focus();
        }
    }

    function closeAllDialogs() {
        let hadOpenDialog = false;
        $$('.modal-backdrop').forEach((dialog) => {
            if (!dialog.hidden) hadOpenDialog = true;
            dialog.hidden = true;
        });
        document.body.classList.remove('modal-open');
        state.activeDialog = null;
        state.dialogStack = [];
        state.lastFocused = null;
        if (state.confirmResolver) {
            state.confirmResolver(false);
            state.confirmResolver = null;
        }
        return hadOpenDialog;
    }

    function confirm({ title, message, okText, okClass = 'btn--danger' }) {
        return new Promise((resolve) => {
            const dialog = $('confirm-dialog');
            if (!dialog) return resolve(false);
            $('confirm-title').textContent = title || t('confirm_title');
            $('confirm-message').textContent = message || '';
            $('confirm-ok').className = `btn ${okClass}`;
            $('confirm-ok-text').textContent = okText || t('confirm');
            state.confirmResolver = resolve;
            openDialog(dialog, { stack: true });
        });
    }

    /* ---- Workspace routing + local tabs ---- */

    function normalizeWorkspace(value) {
        return value === 'cloudflare' ? 'cloudflare' : 'local';
    }

    function workspaceFromPath(pathname = window.location.pathname) {
        const path = pathname.replace(/\/+$/, '') || '/';
        if (path === '/cloudflare' || path.startsWith('/cloudflare/')) return 'cloudflare';
        if (path === '/local' || path.startsWith('/local/')) return 'local';
        return '';
    }

    function defaultWorkspace() {
        return state.features?.mode === 'oauth' ? 'cloudflare' : 'local';
    }

    function workspacePath(workspace) {
        return normalizeWorkspace(workspace) === 'cloudflare' ? '/cloudflare' : '/local';
    }

    function currentWorkspace() {
        return normalizeWorkspace(state.workspace);
    }

    function updateWorkspaceChrome() {
        const workspace = currentWorkspace();
        const titleKey = workspace === 'cloudflare' ? 'oauth_console_title' : 'app_title';
        document.title = t(titleKey);
    }

    function visibleLocalTabs() {
        return $$('.tab').filter((tab) => tab.dataset.tab !== 'oauth' && !tab.hidden);
    }

    function activateTab(name) {
        if (name === 'oauth') {
            setWorkspace('cloudflare');
            return true;
        }
        const tab = document.querySelector(`.tab[data-tab="${name}"]`);
        if (!tab || tab.hidden || tab.dataset.tab === 'oauth') return false;
        if (currentWorkspace() !== 'local') setWorkspace('local');
        $$('.tab').forEach((t_) => {
            const sel = t_ === tab;
            t_.setAttribute('aria-selected', String(sel));
            t_.tabIndex = sel ? 0 : -1;
        });
        $$('.tab-panel').forEach((p) => { p.hidden = p.id !== `panel-${name}`; });
        localStorage.setItem('lastLocalTab', name);
        localStorage.setItem('lastTab', name);
        document.dispatchEvent(new CustomEvent('tabchange', { detail: { name } }));
        return true;
    }

    function activateDefaultLocalTab() {
        const candidates = [
            localStorage.getItem('lastLocalTab'),
            localStorage.getItem('lastTab') === 'oauth' ? '' : localStorage.getItem('lastTab'),
            'local',
        ].filter(Boolean);
        for (const name of candidates) {
            if (activateTab(name)) return;
        }
        const first = visibleLocalTabs()[0];
        if (first) activateTab(first.dataset.tab);
    }

    function applyWorkspacePanels() {
        const workspace = currentWorkspace();
        document.documentElement.dataset.workspace = workspace;
        updateWorkspaceChrome();
        if (workspace === 'cloudflare') {
            $$('.tab').forEach((tab) => {
                tab.setAttribute('aria-selected', 'false');
                tab.tabIndex = -1;
            });
            $$('.tab-panel').forEach((panel) => { panel.hidden = panel.id !== 'panel-oauth'; });
            localStorage.setItem('workspace', 'cloudflare');
            document.dispatchEvent(new CustomEvent('tabchange', { detail: { name: 'oauth' } }));
            return;
        }
        localStorage.setItem('workspace', 'local');
        const oauthPanel = $('panel-oauth');
        if (oauthPanel) oauthPanel.hidden = true;
        activateDefaultLocalTab();
    }

    function setWorkspace(workspace, options = {}) {
        const next = normalizeWorkspace(workspace);
        const previous = currentWorkspace();
        state.workspace = next;
        if (previous !== next) closeAllDialogs();
        applyWorkspacePanels();
        if (options.updateRoute !== false) {
            const target = workspacePath(next);
            const currentPath = window.location.pathname.replace(/\/+$/, '') || '/';
            if (currentPath !== target) {
                const method = options.replace ? 'replaceState' : 'pushState';
                history[method]({ workspace: next }, '', target);
            }
        }
        if (previous !== next) {
            document.dispatchEvent(new CustomEvent('workspacechange', { detail: { workspace: next } }));
        }
    }

    function syncWorkspaceFromRoute(options = {}) {
        const explicit = workspaceFromPath();
        const workspace = explicit || defaultWorkspace();
        setWorkspace(workspace, { updateRoute: false });
        if (!explicit && options.replaceDefault && workspace === 'cloudflare') {
            history.replaceState({ workspace }, '', workspacePath(workspace));
        }
        return workspace;
    }

    function restoreLastTab() {
        return syncWorkspaceFromRoute({ replaceDefault: true });
    }

    /* ---- Language menu ---- */

    function openLangMenu() {
        const menu = $('lang-menu');
        if (!menu) return;
        $$('[data-lang]', menu).forEach((b) => {
            b.classList.toggle('btn--primary', b.dataset.lang === state.currentLang);
        });
        openDialog(menu);
    }

    /* ---- Wire document-level UI listeners ---- */

    function wireUI() {
        /* Dialog keyboard (Escape + Tab-trap) */
        document.addEventListener('keydown', (e) => {
            if (!state.activeDialog) return;
            if (e.key === 'Escape') { e.preventDefault(); closeDialog(state.activeDialog); return; }
            if (e.key !== 'Tab') return;
            const focusable = $$('a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])',
                state.activeDialog).filter((el) => el.offsetParent !== null);
            if (!focusable.length) return;
            const first = focusable[0], last = focusable[focusable.length - 1];
            if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
            else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
        });

        /* Dialog backdrop / close-button click */
        document.addEventListener('click', (e) => {
            const workspaceLink = e.target.closest('[data-workspace-link]');
            if (workspaceLink) {
                e.preventDefault();
                setWorkspace(workspaceLink.dataset.workspaceTarget);
                return;
            }
            const close = e.target.closest('[data-close-dialog]');
            if (close) { e.preventDefault(); const dlg = close.closest('.modal-backdrop'); if (dlg) closeDialog(dlg); return; }
            if (e.target.classList?.contains('modal-backdrop') && e.target.id !== 'lang-menu') closeDialog(e.target);
        });

        /* Confirm OK */
        $('confirm-ok')?.addEventListener('click', () => {
            const resolver = state.confirmResolver;
            state.confirmResolver = null;
            if (resolver) resolver(true);
            closeDialog($('confirm-dialog'));
        });

        /* Tabs click */
        document.addEventListener('click', (e) => {
            const tab = e.target.closest('.tab');
            if (tab && !tab.hidden) { e.preventDefault(); activateTab(tab.dataset.tab); }
        });

        /* Tabs keyboard */
        document.addEventListener('keydown', (e) => {
            if (!e.target.classList?.contains('tab')) return;
            if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
                e.preventDefault();
                const tabs = $$('.tab').filter((t_) => !t_.hidden);
                if (!tabs.length) return;
                const idx = tabs.indexOf(e.target);
                const next = e.key === 'ArrowRight' ? (idx + 1) % tabs.length : (idx - 1 + tabs.length) % tabs.length;
                tabs[next].focus();
                activateTab(tabs[next].dataset.tab);
            }
        });

        /* Header actions */
        $('theme-btn')?.addEventListener('click', toggleTheme);
        $('lang-btn')?.addEventListener('click', openLangMenu);
        window.addEventListener('popstate', () => syncWorkspaceFromRoute());

        /* Language buttons */
        $$('[data-lang]').forEach((b) => {
            b.addEventListener('click', () => { loadLanguage(b.dataset.lang); closeDialog($('lang-menu')); });
        });
    }

    /* ---- Export ---- */
    const ns = window.cfui;
    ns.loadLanguage = loadLanguage;
    ns.applyTranslations = applyTranslations;
    ns.applyLogTranslations = applyLogTranslations;
    ns.initTheme = initTheme;
    ns.toggleTheme = toggleTheme;
    ns.openDialog = openDialog;
    ns.closeDialog = closeDialog;
    ns.closeAllDialogs = closeAllDialogs;
    ns.confirm = confirm;
    ns.activateTab = activateTab;
    ns.restoreLastTab = restoreLastTab;
    ns.setWorkspace = setWorkspace;
    ns.syncWorkspaceFromRoute = syncWorkspaceFromRoute;
    ns.currentWorkspace = currentWorkspace;
    ns.wireUI = wireUI;
})();
