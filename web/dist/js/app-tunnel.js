/* =========================================================================
   CloudFlared UI — Config, status, tunnel controls, inline alerts
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, t, apiGet, apiSend, toast, setBusy, flashField, sleep } = window.cfui;

    /* ---- Config form helpers ---- */

    function configSignature(cfg) {
        return [
            cfg.token || '', cfg.protocol || 'auto', cfg.region || '',
            cfg.custom_tag || '', cfg.software_name || '',
            cfg.grace_period || '30s', String(cfg.retries ?? 5),
            cfg.edge_bind_address || '', String(cfg.no_tls_verify || false),
        ].join('\x1f');
    }

    function fieldsChangedWhileRunning() {
        if (!state.isRunning || !state.runningSig) return false;
        const cur = configSignature(readConfigFromForm());
        return cur !== state.runningSig;
    }

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

    function updateMetricsVisibility() {
        const on = $('metrics-enable-toggle')?.checked;
        if ($('metrics-port-field')) $('metrics-port-field').hidden = !on;
        if ($('metrics-port-input')) $('metrics-port-input').disabled = !on;
    }

    /* ---- Config fetch / save ---- */

    async function fetchConfig() {
        try {
            const data = await apiGet('/config');
            state.config = data;
            writeConfigToForm(data);
        } catch (err) {
            window.cfui.addLog({ key: 'config_load_failed', params: { err: err.message } }, 'error');
        }
    }

    let saveSeq = 0;
    function saveConfig({ showFeedback = true, source = 'auto' } = {}) {
        const seq = ++saveSeq;
        const cfg = readConfigFromForm();
        const prev = state.pendingConfigSave;
        const p = (async () => {
            try { await prev; } catch { /* ignore */ }
            if (seq !== saveSeq) return;
            try {
                const data = await apiSend('/config', 'POST', cfg);
                state.config = data;
                if (showFeedback) {
                    ['token-input','custom-version-input','software-name-input',
                     'autostart-toggle','autorestart-toggle','protocol-select',
                     'grace-period-input','region-select','retries-input',
                     'metrics-enable-toggle','metrics-port-input','edge-bind-address-input',
                     'no-tls-verify-toggle'].forEach((id) => $(id)?.classList.remove('field-saved'));
                    if (source !== 'button' && cfg.token !== undefined) flashField('token-input');
                    if (source !== 'button') toast.ok(t('config_saved'));
                }
                window.cfui.addLog({ key: 'config_saved' }, 'system');
                /* Restart-required hint */
                updateRestartHint();
            } catch (err) {
                if (seq !== saveSeq) return;
                window.cfui.addLog({ key: 'config_save_failed', params: { err: err.message } }, 'error');
                toast.err(t('config_save_failed'));
            }
        })();
        state.pendingConfigSave = p;
        return p;
    }

    /* ---- Status pill ---- */

    function setStatusPill(pillState, text) {
        const pill = $('status-pill');
        if (!pill) return;
        pill.setAttribute('data-state', pillState);
        pill.querySelector('.text').textContent = text;
    }

    /* ---- Tunnel error alert ---- */

    function showTunnelAlert(message) {
        if (state.tunnelAlertDismissed === message) return;
        const el = $('tunnel-alert');
        if (!el) return;
        $('tunnel-alert-msg').textContent = message;
        el.hidden = false;
    }

    function hideTunnelAlert() {
        const el = $('tunnel-alert');
        if (el) el.hidden = true;
        state.tunnelAlertDismissed = null;
    }

    /* ---- Restart hint ---- */

    function updateRestartHint() {
        const hint = $('restart-hint');
        if (!hint) return;
        hint.hidden = !fieldsChangedWhileRunning();
    }

    async function restartTunnel() {
        const btn = $('restart-now');
        setBusy(btn, true, t('stopping'));
        try {
            await apiSend('/control', 'POST', { action: 'stop' });
            await sleep(1200);
            setBusy(btn, true, t('starting'));
            /* Re-save current config before restart */
            await saveConfig({ showFeedback: false, source: 'button' });
            await apiSend('/control', 'POST', { action: 'start' });
            toast.ok(t('tunnel_restarted'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(btn, false);
            $('restart-hint').hidden = true;
            setTimeout(fetchStatus, 800);
        }
    }

    /* ---- Status fetch (polls every 2s) ---- */

    async function fetchStatus() {
        try {
            const data = await apiGet('/status');
            state.statusFailCount = 0;
            const prev = state.status;
            state.status = data.status;
            state.isRunning = data.running;
            state.tunnelProtocol = data.protocol || '';
            state.lastError = data.error || '';
            if (prev !== state.status && prev !== 'unknown') {
                window.cfui.addLog({ key: 'status_changed', params: { status: data.status } }, 'system');
            }
            updateStatusUI();
        } catch {
            state.statusFailCount++;
            if (state.statusFailCount >= 3) updateStatusUI();
        }
    }

    function updateStatusUI() {
        const protoText = state.tunnelProtocol && state.tunnelProtocol !== 'auto'
            ? ` · ${state.tunnelProtocol.toUpperCase()}`
            : '';

        if (state.statusFailCount >= 3) {
            setStatusPill('offline', t('status_offline'));
        } else if (state.isRunning) {
            setStatusPill('ok', t('status_running') + protoText);
            hideTunnelAlert();
        } else if (state.status === 'error') {
            setStatusPill('error', t('status_error'));
            if (state.lastError) showTunnelAlert(state.lastError);
        } else {
            setStatusPill('warn', t('status_stopped'));
        }

        /* Action button */
        const btn = $('action-btn');
        if (btn) {
            btn.setAttribute('data-action', state.isRunning ? 'stop' : 'start');
            btn.classList.toggle('btn--danger', state.isRunning);
            btn.classList.toggle('btn--primary', !state.isRunning);
            btn.querySelector('.text').textContent = t(state.isRunning ? 'stop_tunnel' : 'start_tunnel');
        }

        /* Record running signature for restart-hint detection */
        if (state.isRunning) state.runningSig = configSignature(readConfigFromForm());
        updateRestartHint();
    }

    /* ---- Start / Stop ---- */

    async function onActionClick() {
        const btn = $('action-btn');
        const action = btn.getAttribute('data-action');
        if (action === 'start') {
            if (!$('token-input').value.trim()) {
                toast.err(t('error_token_required'));
                $('token-input').focus();
                return;
            }
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
            if (action === 'start') hideTunnelAlert();
            setTimeout(fetchStatus, 500);
        } catch (err) {
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

    /* ---- Version ---- */

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

    /* ---- TLS confirm ---- */

    async function maybeConfirmTLS() {
        const { confirm } = window.cfui;
        const cb = $('no-tls-verify-toggle');
        if (!cb || !cb.checked) return;
        const ok = await confirm({
            title: t('tls_disable_title'),
            message: t('tls_disable_message'),
            okText: t('tls_i_understand'),
            okClass: 'btn--danger',
        });
        if (!ok) cb.checked = false;
    }

    /* ---- Wire ---- */

    function wireTunnel() {
        $('action-btn')?.addEventListener('click', onActionClick);
        $('tunnel-alert-logs')?.addEventListener('click', () => {
            const logsCard = document.querySelector('.logs-card');
            if (logsCard) logsCard.scrollIntoView({ behavior: 'smooth' });
        });
        $('tunnel-alert-dismiss')?.addEventListener('click', () => {
            state.tunnelAlertDismissed = state.lastError;
            $('tunnel-alert').hidden = true;
        });
        $('restart-now')?.addEventListener('click', restartTunnel);

        /* Config auto-save bindings */
        const sav = (source) => () => saveConfig({ source });
        $('token-input')?.addEventListener('blur', sav('input'));
        $('custom-version-input')?.addEventListener('change', sav('input'));
        $('software-name-input')?.addEventListener('change', sav('input'));
        $('protocol-select')?.addEventListener('change', sav('input'));
        $('grace-period-input')?.addEventListener('change', sav('input'));
        $('region-select')?.addEventListener('change', sav('input'));
        $('retries-input')?.addEventListener('change', sav('input'));
        $('edge-bind-address-input')?.addEventListener('change', sav('input'));
        $('metrics-port-input')?.addEventListener('change', sav('input'));
        $('metrics-enable-toggle')?.addEventListener('change', () => { updateMetricsVisibility(); saveConfig({ source: 'toggle' }); });
        $('autostart-toggle')?.addEventListener('change', sav('toggle'));
        $('autorestart-toggle')?.addEventListener('change', sav('toggle'));
        $('no-tls-verify-toggle')?.addEventListener('change', async () => { await maybeConfirmTLS(); saveConfig({ source: 'toggle' }); });

        /* Keyboard shortcut */
        document.addEventListener('keydown', (e) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); onActionClick(); }
        });
    }

    /* ---- Export ---- */
    const ns = window.cfui;
    ns.readConfigFromForm = readConfigFromForm;
    ns.writeConfigToForm = writeConfigToForm;
    ns.fetchConfig = fetchConfig;
    ns.saveConfig = saveConfig;
    ns.fetchStatus = fetchStatus;
    ns.fetchVersion = fetchVersion;
    ns.updateMetricsVisibility = updateMetricsVisibility;
    ns.wireTunnel = wireTunnel;
})();
