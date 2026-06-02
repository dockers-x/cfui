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
        document.title = t('app_title');
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

    function openDialog(dialog) {
        if (!dialog) return;
        if (state.activeDialog && state.activeDialog !== dialog) closeDialog(state.activeDialog);
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
            if (state.confirmResolver) { state.confirmResolver(false); state.confirmResolver = null; }
        }
        if (!$$('.modal-backdrop').some((d) => !d.hidden)) document.body.classList.remove('modal-open');
        if (state.lastFocused?.focus) state.lastFocused.focus();
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
            openDialog(dialog);
        });
    }

    /* ---- Tabs ---- */

    function activateTab(name) {
        const tab = document.querySelector(`.tab[data-tab="${name}"]`);
        if (!tab || tab.hidden) return;
        $$('.tab').forEach((t_) => {
            const sel = t_ === tab;
            t_.setAttribute('aria-selected', String(sel));
            t_.tabIndex = sel ? 0 : -1;
        });
        $$('.tab-panel').forEach((p) => { p.hidden = p.id !== `panel-${name}`; });
        localStorage.setItem('lastTab', name);
        document.dispatchEvent(new CustomEvent('tabchange', { detail: { name } }));
    }

    function restoreLastTab() {
        const last = localStorage.getItem('lastTab');
        if (last) {
            const tab = document.querySelector(`.tab[data-tab="${last}"]`);
            if (tab && !tab.hidden) activateTab(last);
        }
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
                const idx = tabs.indexOf(e.target);
                const next = e.key === 'ArrowRight' ? (idx + 1) % tabs.length : (idx - 1 + tabs.length) % tabs.length;
                tabs[next].focus();
                activateTab(tabs[next].dataset.tab);
            }
        });

        /* Header actions */
        $('theme-btn')?.addEventListener('click', toggleTheme);
        $('lang-btn')?.addEventListener('click', openLangMenu);

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
    ns.confirm = confirm;
    ns.activateTab = activateTab;
    ns.restoreLastTab = restoreLastTab;
    ns.wireUI = wireUI;
})();
