/* =========================================================================
   CloudFlared UI — Config, status, tunnel controls, inline alerts
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, t, apiGet, apiSend, toast, setBusy, flashField, setTokenVisible, sleep } = window.cfui;

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
        const key = selectedTunnelKey();
        const sig = state.runningSigs[key];
        if (!state.isRunning || !sig) return false;
        const cur = configSignature(readConfigFromForm());
        return cur !== sig;
    }

    function numberOr(value, fallback) {
        const n = Number.parseInt(value, 10);
        return Number.isFinite(n) ? n : fallback;
    }

    function readConfigFromForm() {
        const selected = selectedTunnelProfile();
        return {
            token: $('token-input').value.trim(),
            name: $('tunnel-name-input')?.value.trim() || selected?.name || 'Tunnel',
            key: selected?.key || state.selectedTunnelKey || state.config.active_tunnel_key || 'default',
            local_enabled: selected?.local_enabled !== false,
            remote_management_enabled: !!selected?.remote_management_enabled,
            account_id: selected?.account_id || '',
            tunnel_id: selected?.tunnel_id || '',
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
        };
    }

    function writeConfigToForm(cfg) {
        const profile = selectedTunnelProfile(cfg) || activeTunnelProfile(cfg) || cfg || {};
        if ($('tunnel-name-input')) $('tunnel-name-input').value = profile.name || '';
        $('token-input').value = profile.token || cfg.token || '';
        $('custom-version-input').value = profile.custom_tag || '';
        $('software-name-input').value = profile.software_name || 'cfui';
        $('autostart-toggle').checked = !!profile.auto_start;
        $('autorestart-toggle').checked = profile.auto_restart !== false;
        $('protocol-select').value = profile.protocol || 'auto';
        $('grace-period-input').value = profile.grace_period || '30s';
        $('region-select').value = profile.region || '';
        $('retries-input').value = profile.retries ?? 5;
        $('metrics-enable-toggle').checked = !!profile.metrics_enable;
        $('metrics-port-input').value = profile.metrics_port || 60123;
        $('edge-bind-address-input').value = profile.edge_bind_address || '';
        $('no-tls-verify-toggle').checked = !!profile.no_tls_verify;
        updateMetricsVisibility();
        updateTunnelProfileUI();
    }

    function comparableLocalConfig(cfg = {}) {
        return {
            name: cfg.name || '',
            token: cfg.token || '',
            custom_tag: cfg.custom_tag || '',
            software_name: cfg.software_name || 'cfui',
            auto_start: !!cfg.auto_start,
            auto_restart: cfg.auto_restart !== false,
            protocol: cfg.protocol || 'auto',
            grace_period: cfg.grace_period || '30s',
            region: cfg.region || '',
            retries: numberOr(cfg.retries, 5),
            metrics_enable: !!cfg.metrics_enable,
            metrics_port: numberOr(cfg.metrics_port, 60123),
            edge_bind_address: cfg.edge_bind_address || '',
            no_tls_verify: !!cfg.no_tls_verify,
        };
    }

    function localConfigSignature(cfg = {}) {
        return JSON.stringify(comparableLocalConfig(cfg));
    }

    function isLocalConfigDirty(cfg = readConfigFromForm()) {
        const saved = state.localConfigSignature || localConfigSignature(state.config || {});
        return localConfigSignature(cfg) !== saved;
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
            state.selectedTunnelKey = selectExistingTunnelKey(state.selectedTunnelKey || data.active_tunnel_key, data);
            if (!state.tunnelManager.selectedTunnelKey) state.tunnelManager.selectedTunnelKey = state.selectedTunnelKey;
            renderTunnelProfileSelector();
            window.cfui.renderTunnelManagerProfileSelector?.();
            writeConfigToForm(data);
            state.localConfigSignature = localConfigSignature(readConfigFromForm());
        } catch (err) {
            window.cfui.addLog({ key: 'config_load_failed', params: { err: err.message } }, 'error');
        }
    }

    let saveSeq = 0;
    function saveConfig({ showFeedback = true, source = 'auto' } = {}) {
        const cfg = readConfigFromForm();
        const sig = localConfigSignature(cfg);
        const saved = state.localConfigSignature || localConfigSignature(state.config || {});
        if (sig === saved) {
            updateRestartHint();
            return state.pendingConfigSave || Promise.resolve(state.config);
        }
        if (state.pendingConfigSave && state.pendingConfigSignature === sig) {
            return state.pendingConfigSave;
        }
        const seq = ++saveSeq;
        const prev = state.pendingConfigSave;
        state.pendingConfigSignature = sig;
        const p = (async () => {
            try { await prev; } catch { /* ignore */ }
            if (seq !== saveSeq) return;
            try {
                const key = encodeURIComponent(cfg.key || state.selectedTunnelKey || state.config.active_tunnel_key || 'default');
                await apiSend(`/tunnels/${key}`, 'PUT', cfg);
                await fetchConfig();
                state.localConfigSignature = localConfigSignature(readConfigFromForm());
                if (showFeedback) {
                    ['tunnel-name-input','token-input','custom-version-input','software-name-input',
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
            } finally {
                if (seq === saveSeq) {
                    state.pendingConfigSave = null;
                    state.pendingConfigSignature = '';
                }
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
        const textEl = pill.querySelector('.text');
        /* Drop the static i18n binding so a locale switch doesn't reset the
           live text to "Checking..." until the next poll. */
        textEl.removeAttribute('data-i18n');
        textEl.textContent = text;
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
        const key = encodeURIComponent(selectedTunnelKey());
        setBusy(btn, true, t('stopping'));
        try {
            await apiSend(`/tunnels/${key}/control`, 'POST', { action: 'stop' });
            await sleep(1200);
            setBusy(btn, true, t('starting'));
            /* Re-save current config before restart */
            await saveConfig({ showFeedback: false, source: 'button' });
            await apiSend(`/tunnels/${key}/control`, 'POST', { action: 'start' });
            toast.ok(t('tunnel_restarted'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(btn, false);
            delete state.runningSigs[selectedTunnelKey()];
            $('restart-hint').hidden = true;
            setTimeout(fetchStatus, 800);
        }
    }

    /* ---- Status fetch (polls every 2s) ---- */

    async function fetchStatus() {
        try {
            const data = await apiGet('/tunnels');
            state.statusFailCount = 0;
            state.tunnelStatuses = data.statuses || {};
            if (data.active_tunnel_key && state.config) {
                state.config.active_tunnel_key = data.active_tunnel_key;
            }
            const sel = state.tunnelStatuses[selectedTunnelKey()] || {};
            const prev = state.status;
            state.status = sel.status || 'stopped';
            state.isRunning = !!sel.running;
            state.tunnelProtocol = sel.protocol || '';
            state.lastError = sel.error || '';
            if (prev !== state.status && prev !== 'unknown') {
                window.cfui.addLog({ key: 'status_changed', params: { status: state.status } }, 'system');
            }
            updateStatusUI();
        } catch {
            state.statusFailCount++;
            if (state.statusFailCount >= 3) updateStatusUI();
        }
    }

    function runningTunnelCount() {
        return Object.values(state.tunnelStatuses || {}).filter((s) => s && s.running).length;
    }

    function updateStatusUI() {
        const selectedStatus = (state.tunnelStatuses || {})[selectedTunnelKey()] || {};
        state.status = selectedStatus.status || 'stopped';
        state.isRunning = !!selectedStatus.running;
        state.tunnelProtocol = selectedStatus.protocol || '';
        state.lastError = selectedStatus.error || '';

        const protoText = state.tunnelProtocol && state.tunnelProtocol !== 'auto'
            ? ` · ${state.tunnelProtocol.toUpperCase()}`
            : '';

        /* The alert banner reflects the selected tunnel. */
        if (state.isRunning) {
            hideTunnelAlert();
        } else if (state.status === 'error' && state.lastError) {
            showTunnelAlert(state.lastError);
        }

        /* Header pill: selected tunnel state, or an aggregate when several
           local tunnels exist. */
        const localProfiles = tunnelProfiles().filter((p) => p.local_enabled !== false);
        const running = runningTunnelCount();
        const anyError = Object.values(state.tunnelStatuses || {}).some((s) => s && s.status === 'error');
        if (state.statusFailCount >= 3) {
            setStatusPill('offline', t('status_offline'));
        } else if (localProfiles.length > 1) {
            const pillState = running > 0 ? 'ok' : (anyError ? 'error' : 'warn');
            setStatusPill(pillState, t('tunnels_running_ratio', { n: running, m: localProfiles.length }));
        } else if (state.isRunning) {
            setStatusPill('ok', t('status_running') + protoText);
        } else if (state.status === 'error') {
            setStatusPill('error', t('status_error'));
        } else {
            setStatusPill('warn', t('status_stopped'));
        }

        /* Capture the running config signature once per run so the
           restart-required hint compares against start-time values. */
        const selKey = selectedTunnelKey();
        if (state.isRunning) {
            if (!state.runningSigs[selKey]) state.runningSigs[selKey] = configSignature(readConfigFromForm());
        } else {
            delete state.runningSigs[selKey];
        }
        updateRestartHint();
        updateTunnelProfileUI();
    }

    /* ---- Start / Stop ---- */

    async function controlTunnel(btn, key, action) {
        if (action === 'start') {
            const editingThis = key === selectedTunnelKey();
            const profile = tunnelProfiles().find((p) => p.key === key);
            const token = editingThis ? $('token-input').value.trim() : (profile?.token || '');
            if (!token) {
                toast.err(t('error_token_required'));
                if (!editingThis) selectTunnelProfile(key);
                $('token-input').focus();
                return;
            }
            if (editingThis && (isLocalConfigDirty() || state.pendingConfigSave)) {
                setBusy(btn, true, t('saving'));
                try { await saveConfig({ showFeedback: false, source: 'button' }); } catch { /* */ }
                setBusy(btn, false);
            }
        }
        setBusy(btn, true, t(action === 'start' ? 'starting' : 'stopping'));
        try {
            await apiSend(`/tunnels/${encodeURIComponent(key)}/control`, 'POST', { action });
            toast.ok(t(action === 'start' ? 'tunnel_start_requested' : 'tunnel_stop_requested'));
            if (action === 'start' && key === selectedTunnelKey()) hideTunnelAlert();
            if (action === 'start') delete state.runningSigs[key];
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

    function tunnelProfiles(cfg = state.config) {
        return Array.isArray(cfg?.tunnels) ? cfg.tunnels : [];
    }

    function activeTunnelKey(cfg = state.config) {
        return cfg?.active_tunnel_key || tunnelProfiles(cfg)[0]?.key || 'default';
    }

    function selectedTunnelKey(cfg = state.config) {
        return selectExistingTunnelKey(state.selectedTunnelKey || activeTunnelKey(cfg), cfg);
    }

    function selectExistingTunnelKey(key, cfg = state.config) {
        const profiles = tunnelProfiles(cfg);
        if (profiles.some((p) => p.key === key)) return key;
        return activeTunnelKey(cfg);
    }

    function selectedTunnelProfile(cfg = state.config) {
        const key = selectedTunnelKey(cfg);
        return tunnelProfiles(cfg).find((p) => p.key === key) || null;
    }

    function activeTunnelProfile(cfg = state.config) {
        const key = activeTunnelKey(cfg);
        return tunnelProfiles(cfg).find((p) => p.key === key) || null;
    }

    function tunnelDisplayName(profile) {
        if (!profile) return activeTunnelKey();
        return profile.name || profile.key;
    }

    function appendTunnelProfileOptions(select, profiles) {
        select.innerHTML = '';
        for (const profile of profiles) {
            const opt = document.createElement('option');
            opt.value = profile.key;
            opt.textContent = profile.name || profile.key;
            select.appendChild(opt);
        }
    }

    function renderTunnelProfileSelector() {
        const select = $('tunnel-profile-select');
        const profiles = tunnelProfiles();
        const current = selectedTunnelKey();
        if (select) {
            appendTunnelProfileOptions(select, profiles);
            select.value = current;
        }
        renderTunnelProfileList(profiles);
        updateTunnelProfileUI();
    }

    function renderTunnelProfileList(profiles = tunnelProfiles()) {
        const list = $('tunnel-profile-list');
        if (!list) return;
        list.innerHTML = '';
        if (!profiles.length) return;
        for (const profile of profiles) {
            list.appendChild(tunnelProfileItem(profile));
        }
    }

    function tunnelProfileItem(profile) {
        const item = document.createElement('article');
        item.className = 'tunnel-profile-item';
        item.dataset.key = profile.key;
        item.setAttribute('role', 'listitem');

        const summary = document.createElement('button');
        summary.type = 'button';
        summary.className = 'tunnel-profile-item__summary';
        summary.addEventListener('click', () => selectTunnelProfile(profile.key));

        const copy = document.createElement('span');
        copy.className = 'tunnel-profile-item__copy';
        const name = document.createElement('span');
        name.className = 'tunnel-profile-item__name';
        name.textContent = tunnelDisplayName(profile);
        copy.append(name);

        const protocol = document.createElement('span');
        protocol.className = 'tunnel-profile-item__protocol';
        protocol.hidden = true;
        summary.append(copy, protocol);

        item.append(summary);
        return item;
    }

    function updateTunnelProfileUI() {
        const selected = selectedTunnelProfile();
        const del = $('delete-tunnel-profile');
        if (del) {
            const onlyOne = tunnelProfiles().length <= 1;
            del.disabled = onlyOne;
            del.title = onlyOne ? t('cannot_delete_only_tunnel') : '';
        }
        syncTunnelProfileListState(selected);
        updateSelectedTunnelAction(selected);
    }

    function syncTunnelProfileListState(selected = selectedTunnelProfile()) {
        const selectedKey = selected?.key || selectedTunnelKey();
        document.querySelectorAll('.tunnel-profile-item').forEach((item) => {
            const key = item.dataset.key;
            const isSelected = key === selectedKey;
            const st = (state.tunnelStatuses || {})[key] || {};
            item.dataset.selected = String(isSelected);

            const summary = item.querySelector('.tunnel-profile-item__summary');
            if (summary) summary.setAttribute('aria-pressed', String(isSelected));
            let stateName = 'neutral';
            let statusText = t('status_stopped');
            if (st.running) {
                stateName = 'ok';
                statusText = t('status_running');
            } else if (st.status === 'error') {
                stateName = 'error';
                statusText = t('status_error');
            }
            item.dataset.state = stateName;

            const protoText = st.running && st.protocol && st.protocol !== 'auto'
                ? st.protocol.toUpperCase()
                : '';
            const protocol = item.querySelector('.tunnel-profile-item__protocol');
            if (protocol) {
                protocol.textContent = protoText;
                protocol.hidden = !protoText;
            }

            if (summary) {
                const name = item.querySelector('.tunnel-profile-item__name')?.textContent || '';
                const fullStatus = protoText ? `${statusText} · ${protoText}` : statusText;
                const titleParts = [name, fullStatus];
                if (st.status === 'error' && st.error) titleParts.push(st.error);
                summary.title = titleParts.filter(Boolean).join(' · ');
                summary.setAttribute('aria-label', summary.title);
            }

        });
    }

    function updateSelectedTunnelAction(selected = selectedTunnelProfile()) {
        const actionBtn = $('selected-tunnel-action');
        if (!actionBtn || actionBtn.getAttribute('aria-busy') === 'true') return;
        const key = selected?.key || selectedTunnelKey();
        const st = (state.tunnelStatuses || {})[key] || {};
        const action = st.running ? 'stop' : 'start';
        actionBtn.dataset.action = action;
        actionBtn.dataset.key = key;
        actionBtn.classList.toggle('btn--primary', !st.running);
        actionBtn.classList.toggle('btn--danger', st.running);
        const text = actionBtn.querySelector('.text');
        if (text) text.textContent = t(st.running ? 'stop_this_tunnel' : 'start_this_tunnel');
    }

    async function onTunnelProfileChange(keyOverride) {
        const nextKey = typeof keyOverride === 'string' ? keyOverride : $('tunnel-profile-select')?.value;
        state.selectedTunnelKey = nextKey || activeTunnelKey();
        writeConfigToForm(state.config);
        state.localConfigSignature = localConfigSignature(readConfigFromForm());
        applySelectedStatus();
        updateRestartHint();
    }

    /* applySelectedStatus refreshes the header pill and action button from the
       cached per-tunnel statuses when the selection changes. */
    function applySelectedStatus() {
        const sel = (state.tunnelStatuses || {})[selectedTunnelKey()] || {};
        state.status = sel.status || 'stopped';
        state.isRunning = !!sel.running;
        state.tunnelProtocol = sel.protocol || '';
        state.lastError = sel.error || '';
        hideTunnelAlert();
        updateStatusUI();
    }

    function selectTunnelProfile(key) {
        const select = $('tunnel-profile-select');
        if (select) select.value = key;
        onTunnelProfileChange(key);
    }

    async function addTunnelProfile() {
        const profiles = tunnelProfiles();
        let index = profiles.length + 1;
        let key = `tunnel-${index}`;
        const keys = new Set(profiles.map((p) => p.key));
        while (keys.has(key)) {
            index++;
            key = `tunnel-${index}`;
        }
        const profile = {
            key,
            name: `Tunnel ${index}`,
            local_enabled: true,
            remote_management_enabled: false,
            auto_restart: true,
            software_name: 'cfui',
            protocol: 'auto',
            grace_period: '30s',
            retries: 5,
            metrics_port: 60123,
            log_level: 'info',
            edge_ip_version: 'auto',
        };
        try {
            await apiSend('/tunnels', 'POST', profile);
            state.selectedTunnelKey = key;
            await fetchConfig();
            toast.ok(t('tunnel_profile_added'));
            $('tunnel-name-input')?.focus();
        } catch (err) {
            toast.err(err.message);
        }
    }

    async function deleteSelectedTunnel() {
        const selected = selectedTunnelProfile();
        if (!selected) return;
        const st = (state.tunnelStatuses || {})[selected.key] || {};
        let message = t('delete_tunnel_profile_message', { name: selected.name || selected.key });
        if (st.running) message += ' ' + t('delete_tunnel_running_note');
        const ok = await window.cfui.confirm({
            title: t('delete_tunnel_profile'),
            message,
            okText: t('delete'),
        });
        if (!ok) return;
        try {
            await apiSend(`/tunnels/${encodeURIComponent(selected.key)}`, 'DELETE');
            await fetchConfig();
            toast.ok(t('tunnel_profile_deleted'));
        } catch (err) {
            toast.err(err.message);
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

    let tokenHideTimer = null;
    function toggleTokenVisibility(e) {
        e?.preventDefault();
        const input = $('token-input');
        const btn = $('toggle-token');
        if (!input || !btn) return;
        const visible = input.type === 'password';
        setTokenVisible(input, btn, visible);
        if (tokenHideTimer) {
            clearTimeout(tokenHideTimer);
            tokenHideTimer = null;
        }
        if (visible) {
            tokenHideTimer = setTimeout(() => {
                setTokenVisible(input, btn, false);
                tokenHideTimer = null;
            }, 10000);
        }
    }

    /* ---- Wire ---- */

    function wireTunnel() {
        $('tunnel-profile-select')?.addEventListener('change', onTunnelProfileChange);
        $('add-tunnel-profile')?.addEventListener('click', addTunnelProfile);
        $('delete-tunnel-profile')?.addEventListener('click', deleteSelectedTunnel);
        $('selected-tunnel-action')?.addEventListener('click', (e) => {
            const btn = e.currentTarget;
            controlTunnel(btn, selectedTunnelKey(), btn.dataset.action || 'start');
        });
        $('toggle-token')?.addEventListener('mousedown', (e) => e.preventDefault());
        $('toggle-token')?.addEventListener('click', toggleTokenVisibility);
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
        $('tunnel-name-input')?.addEventListener('change', sav('input'));
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

        document.addEventListener('localechange', () => {
            renderTunnelProfileSelector();
            updateTunnelProfileUI();
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
    ns.renderTunnelProfileSelector = renderTunnelProfileSelector;
    ns.updateTunnelProfileUI = updateTunnelProfileUI;
    ns.wireTunnel = wireTunnel;
})();
