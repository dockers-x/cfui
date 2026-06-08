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
        if (!state.isRunning || !state.runningSig) return false;
        if (selectedTunnelKey() !== activeTunnelKey()) return false;
        const cur = configSignature(readConfigFromForm());
        return cur !== state.runningSig;
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
            const selectedIsActive = selectedTunnelKey() === activeTunnelKey();
            btn.setAttribute('data-action', state.isRunning ? 'stop' : 'start');
            btn.classList.toggle('btn--danger', state.isRunning);
            btn.classList.toggle('btn--primary', !state.isRunning);
            btn.querySelector('.text').textContent = t(state.isRunning ? 'stop_tunnel' : 'start_tunnel');
            btn.disabled = !selectedIsActive;
            btn.title = selectedIsActive ? (btn.getAttribute('data-default-title') || '') : t('activate_tunnel_first');
        }

        /* Record running signature for restart-hint detection */
        if (state.isRunning) state.runningSig = configSignature(readConfigFromForm());
        updateRestartHint();
        updateTunnelProfileUI();
    }

    /* ---- Start / Stop ---- */

    async function onActionClick() {
        const btn = $('action-btn');
        if (selectedTunnelKey() !== activeTunnelKey()) {
            toast.warn(t('activate_tunnel_first'));
            return;
        }
        const action = btn.getAttribute('data-action');
        if (action === 'start') {
            if (!$('token-input').value.trim()) {
                toast.err(t('error_token_required'));
                $('token-input').focus();
                return;
            }
            const cfg = readConfigFromForm();
            const needsSave = isLocalConfigDirty(cfg);
            if (needsSave || state.pendingConfigSave) {
                setBusy(btn, true, t('saving'));
                try { await (needsSave ? saveConfig({ showFeedback: false, source: 'button' }) : state.pendingConfigSave); } catch { /* */ }
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

    function appendTunnelProfileOptionGroups(select, profiles, activeKey) {
        select.innerHTML = '';
        const appendGroup = (label, items) => {
            if (!items.length) return;
            const group = document.createElement('optgroup');
            group.label = label;
            for (const profile of items) {
                const opt = document.createElement('option');
                opt.value = profile.key;
                opt.textContent = profile.name || profile.key;
                group.appendChild(opt);
            }
            select.appendChild(group);
        };
        appendGroup(t('active_local_tunnel'), profiles.filter((profile) => profile.key === activeKey));
        appendGroup(t('other_tunnel_profiles'), profiles.filter((profile) => profile.key !== activeKey));
    }

    function renderTunnelProfileSelector() {
        const select = $('tunnel-profile-select');
        const profiles = tunnelProfiles();
        const current = selectedTunnelKey();
        if (select) {
            appendTunnelProfileOptionGroups(select, profiles, activeTunnelKey());
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
        const meta = document.createElement('span');
        meta.className = 'tunnel-profile-item__meta';
        meta.textContent = profile.key || '';
        copy.append(name, meta);

        const badges = document.createElement('span');
        badges.className = 'tunnel-profile-badges';
        badges.append(
            tunnelProfileBadge('runner', t('active_local_tunnel')),
            tunnelProfileBadge('editing', t('editing_tunnel_profile'))
        );
        summary.append(copy, badges);

        const actions = document.createElement('div');
        actions.className = 'tunnel-profile-item__actions';
        const activate = document.createElement('button');
        activate.type = 'button';
        activate.className = 'btn btn--sm tunnel-profile-runner-btn';
        const spinner = document.createElement('span');
        spinner.className = 'spinner';
        spinner.setAttribute('aria-hidden', 'true');
        const text = document.createElement('span');
        text.className = 'text';
        text.textContent = t('use_for_local_runner');
        activate.append(spinner, text);
        activate.addEventListener('click', (e) => {
            e.stopPropagation();
            activateSelectedTunnel(e.currentTarget, profile.key);
        });
        actions.appendChild(activate);

        item.append(summary, actions);
        return item;
    }

    function tunnelProfileBadge(role, text) {
        const badge = document.createElement('span');
        badge.className = 'tunnel-profile-badge';
        badge.dataset.role = role;
        badge.textContent = text;
        return badge;
    }

    function updateTunnelProfileUI() {
        const active = activeTunnelProfile();
        const selected = selectedTunnelProfile();
        const isActive = !!selected && selected.key === active?.key;
        const pill = $('active-tunnel-pill');
        if (pill) {
            pill.setAttribute('data-state', state.isRunning ? 'ok' : 'neutral');
            const text = pill.querySelector('.text');
            if (text) text.textContent = `${t('active_local_tunnel')}: ${active?.name || active?.key || 'default'}`;
        }
        const activate = $('activate-tunnel-profile');
        if (activate) {
            activate.hidden = isActive;
            activate.disabled = !selected || isActive;
        }
        const del = $('delete-tunnel-profile');
        if (del) {
            del.disabled = isActive || tunnelProfiles().length <= 1;
            del.title = isActive ? t('cannot_delete_active_tunnel') : '';
        }
        const action = $('action-btn');
        if (action && selected) {
            action.disabled = !isActive;
            action.title = isActive ? (action.getAttribute('data-default-title') || '') : t('activate_tunnel_first');
        }
        syncTunnelProfileListState(active, selected);
    }

    function syncTunnelProfileListState(active = activeTunnelProfile(), selected = selectedTunnelProfile()) {
        const activeKey = active?.key || activeTunnelKey();
        const selectedKey = selected?.key || selectedTunnelKey();
        document.querySelectorAll('.tunnel-profile-item').forEach((item) => {
            const key = item.dataset.key;
            const isRunner = key === activeKey;
            const isSelected = key === selectedKey;
            item.dataset.activeRunner = String(isRunner);
            item.dataset.selected = String(isSelected);

            const summary = item.querySelector('.tunnel-profile-item__summary');
            if (summary) summary.setAttribute('aria-pressed', String(isSelected));

            const runner = item.querySelector('.tunnel-profile-badge[data-role="runner"]');
            if (runner) {
                runner.hidden = !isRunner;
                runner.dataset.state = state.isRunning ? 'ok' : 'info';
            }

            const editing = item.querySelector('.tunnel-profile-badge[data-role="editing"]');
            if (editing) {
                editing.hidden = !isSelected;
                editing.dataset.state = 'neutral';
            }

            const activate = item.querySelector('.tunnel-profile-runner-btn');
            if (activate) {
                activate.hidden = isRunner;
                activate.disabled = isRunner;
            }
        });
    }

    async function onTunnelProfileChange(keyOverride) {
        const nextKey = typeof keyOverride === 'string' ? keyOverride : $('tunnel-profile-select')?.value;
        state.selectedTunnelKey = nextKey || activeTunnelKey();
        writeConfigToForm(state.config);
        state.localConfigSignature = localConfigSignature(readConfigFromForm());
        updateRestartHint();
    }

    function selectTunnelProfile(key) {
        const select = $('tunnel-profile-select');
        if (select) select.value = key;
        onTunnelProfileChange(key);
    }

    async function activateSelectedTunnel(control, keyOverride = '') {
        const key = selectExistingTunnelKey(keyOverride || selectedTunnelKey());
        if (!key || key === activeTunnelKey()) return;
        state.selectedTunnelKey = key;
        setBusy(control, true, t('saving'));
        try {
            await apiSend(`/tunnels/${encodeURIComponent(key)}/activate-local`, 'POST', {});
            await fetchConfig();
            toast.ok(t('local_runner_updated'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(control, false);
        }
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
        if (!selected || selected.key === activeTunnelKey()) return;
        const ok = await window.cfui.confirm({
            title: t('delete_tunnel_profile'),
            message: t('delete_tunnel_profile_message', { name: selected.name || selected.key }),
            okText: t('delete'),
        });
        if (!ok) return;
        try {
            await apiSend(`/tunnels/${encodeURIComponent(selected.key)}`, 'DELETE');
            state.selectedTunnelKey = activeTunnelKey();
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
        $('action-btn')?.addEventListener('click', onActionClick);
        $('tunnel-profile-select')?.addEventListener('change', onTunnelProfileChange);
        $('activate-tunnel-profile')?.addEventListener('click', (e) => activateSelectedTunnel(e.currentTarget));
        $('add-tunnel-profile')?.addEventListener('click', addTunnelProfile);
        $('delete-tunnel-profile')?.addEventListener('click', deleteSelectedTunnel);
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

        /* Keyboard shortcut */
        document.addEventListener('keydown', (e) => {
            if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') { e.preventDefault(); onActionClick(); }
        });

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
