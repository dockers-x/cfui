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
        const profile = selectedTunnelProfile(cfg) || activeTunnelProfile(cfg);
        const source = profile || cfg || {};
        if ($('tunnel-name-input')) $('tunnel-name-input').value = source.name || '';
        $('token-input').value = profile ? (profile.token || '') : (cfg?.token || '');
        $('custom-version-input').value = source.custom_tag || '';
        $('software-name-input').value = source.software_name || 'cfui';
        $('autostart-toggle').checked = !!source.auto_start;
        $('autorestart-toggle').checked = source.auto_restart !== false;
        $('protocol-select').value = source.protocol || 'auto';
        $('grace-period-input').value = source.grace_period || '30s';
        $('region-select').value = source.region || '';
        $('retries-input').value = source.retries ?? 5;
        $('metrics-enable-toggle').checked = !!source.metrics_enable;
        $('metrics-port-input').value = source.metrics_port || 60123;
        $('edge-bind-address-input').value = source.edge_bind_address || '';
        $('no-tls-verify-toggle').checked = !!source.no_tls_verify;
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
            const handoffKey = consumeLocalTunnelHandoffKey(data);
            state.selectedTunnelKey = selectExistingTunnelKey(handoffKey || state.selectedTunnelKey || data.active_tunnel_key, data);
            if (handoffKey || !state.tunnelManager.selectedTunnelKey) state.tunnelManager.selectedTunnelKey = state.selectedTunnelKey;
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
        if (textEl.textContent !== text) textEl.textContent = text;
    }

    const protocolRotation = { labels: [], signature: '', index: 0, timer: null, fadeTimer: null, hovered: false, focused: false, paused: false, pill: null };
    const protocolMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)');

    // Shared by the header slot and cards; only the static SVG is inserted as HTML.
    const protocolLoader = '<svg class="protocol-loader" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 30" width="18" height="18" aria-hidden="true" focusable="false"><rect x="0" y="0" width="4" height="10" rx=".5"/><rect x="10" y="0" width="4" height="10" rx=".5"/><rect x="20" y="0" width="4" height="10" rx=".5"/></svg>';

    function protocolDescription(label) {
        return label === '—' ? t('protocol_pending') : label;
    }

    function renderProtocolLabel(element, label) {
        if (!element) return;
        if (element.dataset.protocolLabel !== label) {
            element.dataset.protocolLabel = label;
            if (label === '—') element.innerHTML = protocolLoader;
            else element.textContent = label;
        }
        element.setAttribute('aria-label', protocolDescription(label));
    }

    function profileProtocolLabels(profile, status = {}) {
        if (status.running || ['starting', 'stopping', 'reconnecting'].includes(status.status)) {
            const active = Array.isArray(status.active_protocols) ? status.active_protocols : [];
            const labels = [...new Set(active.filter(value => ['quic', 'http2'].includes(value)).map(value => value.toUpperCase()))].sort();
            return labels.length ? labels : ['—'];
        }
        const configured = status.protocol || profile.protocol || 'auto';
        return [['auto', 'quic', 'http2'].includes(configured) ? configured.toUpperCase() : 'AUTO'];
    }

    function protocolLabels(profiles, statuses) {
        const active = profiles.filter(profile => {
            const status = statuses[profile.key] || {};
            return status.running || ['starting', 'stopping', 'reconnecting'].includes(status.status);
        });
        return [...new Set((active.length ? active : profiles).flatMap(profile => profileProtocolLabels(profile, statuses[profile.key])))].sort();
    }

    function stopHeaderProtocolRotation() {
        if (protocolRotation.timer !== null) window.clearTimeout(protocolRotation.timer);
        if (protocolRotation.fadeTimer !== null) window.clearTimeout(protocolRotation.fadeTimer);
        protocolRotation.timer = null;
        protocolRotation.fadeTimer = null;
        protocolRotation.pill?.querySelector('.proto')?.removeAttribute('data-fading');
    }

    function renderHeaderProtocol() {
        const slot = protocolRotation.pill?.querySelector('.proto');
        if (!slot) return;
        renderProtocolLabel(slot.querySelector('.proto__value'), protocolRotation.labels[protocolRotation.index] || 'AUTO');
        slot.disabled = protocolRotation.labels.length < 2;
        if (slot.getAttribute('aria-pressed') !== String(protocolRotation.paused)) slot.setAttribute('aria-pressed', String(protocolRotation.paused));
        const action = t(protocolRotation.paused ? 'protocol_rotation_resume' : 'protocol_rotation_pause');
        const label = [t('protocol'), protocolRotation.labels.map(protocolDescription).join(' / '), slot.disabled ? '' : action].filter(Boolean).join(' · ');
        if (slot.getAttribute('aria-label') !== label) slot.setAttribute('aria-label', label);
        slot.title = slot.disabled ? '' : action;
    }

    function scheduleHeaderProtocolRotation() {
        if (protocolRotation.timer !== null || protocolRotation.fadeTimer !== null || protocolRotation.labels.length < 2 || protocolRotation.paused || protocolRotation.hovered || protocolRotation.focused || document.hidden) return;
        protocolRotation.timer = window.setTimeout(() => {
            protocolRotation.timer = null;
            const slot = protocolRotation.pill?.querySelector('.proto');
            if (!slot) return;
            if (protocolMotion?.matches) {
                protocolRotation.index = (protocolRotation.index + 1) % protocolRotation.labels.length;
                renderHeaderProtocol();
                scheduleHeaderProtocolRotation();
                return;
            }
            slot.setAttribute('data-fading', 'true');
            protocolRotation.fadeTimer = window.setTimeout(() => {
                protocolRotation.fadeTimer = null;
                protocolRotation.index = (protocolRotation.index + 1) % protocolRotation.labels.length;
                renderHeaderProtocol();
                slot.removeAttribute('data-fading');
                scheduleHeaderProtocolRotation();
            }, 160);
        }, 5000);
    }

    function updateHeaderProtocols(profiles, statuses) {
        const pill = $('status-pill');
        if (!pill) return [];
        if (protocolRotation.pill !== pill) {
            stopHeaderProtocolRotation();
            protocolRotation.pill = pill;
            const slot = pill.querySelector('.proto');
            slot?.addEventListener('click', () => {
                protocolRotation.paused = !protocolRotation.paused;
                stopHeaderProtocolRotation();
                renderHeaderProtocol();
                scheduleHeaderProtocolRotation();
            });
            pill.addEventListener('pointerenter', () => { protocolRotation.hovered = true; stopHeaderProtocolRotation(); });
            pill.addEventListener('pointerleave', () => { protocolRotation.hovered = false; scheduleHeaderProtocolRotation(); });
            pill.addEventListener('focusin', () => { protocolRotation.focused = true; stopHeaderProtocolRotation(); });
            pill.addEventListener('focusout', event => {
                if (pill.contains(event.relatedTarget)) return;
                protocolRotation.focused = false;
                scheduleHeaderProtocolRotation();
            });
        }
        const labels = protocolLabels(profiles, statuses);
        const signature = labels.join('|');
        if (signature !== protocolRotation.signature) {
            stopHeaderProtocolRotation();
            const previous = protocolRotation.labels[protocolRotation.index];
            protocolRotation.labels = labels;
            protocolRotation.signature = signature;
            protocolRotation.index = Math.max(0, labels.indexOf(previous));
        }
        renderHeaderProtocol();
        scheduleHeaderProtocolRotation();
        return labels;
    }

    protocolMotion?.addEventListener('change', () => { stopHeaderProtocolRotation(); renderHeaderProtocol(); scheduleHeaderProtocolRotation(); });
    document.addEventListener('visibilitychange', () => { stopHeaderProtocolRotation(); scheduleHeaderProtocolRotation(); });
    window.addEventListener?.('pagehide', stopHeaderProtocolRotation);

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

    // A failed browser poll says nothing about the tunnel's edge connectivity.
    function tunnelPresentation(st = {}, stale = false) {
        if (stale) return { state: 'unknown', pill: 'offline', text: t('status_stale') };
        if (['starting', 'stopping', 'reconnecting'].includes(st.status)) {
            return { state: st.status, pill: 'loading', text: tunnelStatusText(st) };
        }
        if (st.status === 'error') return { state: 'error', pill: 'error', text: t('status_error') };
        if (!st.running) return { state: 'stopped', pill: 'warn', text: t('status_stopped') };
        const health = ['connected', 'degraded', 'disconnected'].includes(st.connectivity) ? st.connectivity : 'unknown';
        return {
            state: health === 'connected' ? 'running' : health,
            pill: health === 'connected' ? 'ok' : (health === 'disconnected' ? 'error' : 'warn'),
            text: t('status_' + health),
        };
    }

    function tunnelOverview(profiles, statuses, stale = false) {
        let active = 0;
        let connected = 0;
        let anyError = false;
        const healthStates = [];
        for (const profile of profiles) {
            const st = statuses[profile.key] || {};
            const view = tunnelPresentation(st, stale);
            if (st.running || ['starting', 'stopping', 'reconnecting'].includes(st.status)) active++;
            if (view.state === 'running') connected++;
            if (view.pill === 'error') anyError = true;
            healthStates.push(healthAnimalPresentation(st, stale).health);
        }
        const health = ['error', 'disconnected', 'recovering', 'unknown', 'degraded'].find(value => healthStates.includes(value)) || 'connected';
        return { active, connected, health, pill: stale ? 'offline' : (connected === profiles.length ? 'ok' : (anyError ? 'error' : 'warn')) };
    }

    function healthAnimalPresentation(st = {}, stale = false) {
        if (stale) return { health: 'unknown', label: t('status_stale') };
        if (st.status === 'error') return { health: 'error', label: [t('status_error'), st.error].filter(Boolean).join(' · ') };
        if (['starting', 'stopping', 'reconnecting'].includes(st.status)) {
            return { health: 'recovering', label: tunnelStatusText(st) };
        }
        if (!st.running || st.status === 'stopped') return { health: 'unknown', label: t('status_unknown') };
        const health = ['connected', 'degraded', 'disconnected'].includes(st.connectivity) ? st.connectivity : 'unknown';
        return { health, label: t('status_' + health) };
    }

    function createHealthAnimal() {
        const animal = document.createElement('button');
        animal.type = 'button';
        animal.className = 'health-animal';
        animal.setAttribute('aria-expanded', 'false');
        animal.innerHTML = '<span class="health-animal__window" aria-hidden="true"><span class="health-animal__art"></span></span>';
        animal.addEventListener('click', (event) => {
            event.stopPropagation();
            animal.setAttribute('aria-expanded', String(animal.getAttribute('aria-expanded') !== 'true'));
        });
        animal.addEventListener('blur', () => animal.setAttribute('aria-expanded', 'false'));
        animal.addEventListener('keydown', (event) => {
            if (event.key === 'Escape') animal.setAttribute('aria-expanded', 'false');
        });
        return animal;
    }

    function updateHealthAnimal(animal, st, stale = false, summaryLabel = '') {
        if (!animal) return;
        const view = healthAnimalPresentation(st, stale);
        animal.dataset.health = view.health;
        const label = summaryLabel || [view.label, lastCheckedText(st), st.health_error || ''].filter(Boolean).join(' · ');
        animal.setAttribute('aria-label', label);
        animal.removeAttribute('title');
    }

    function lastCheckedText(st) {
        if (!st.last_checked_at) return '';
        return t('status_last_checked', { time: new Date(st.last_checked_at).toLocaleString() });
    }

    function updateStatusUI() {
        const selectedStatus = (state.tunnelStatuses || {})[selectedTunnelKey()] || {};
        state.status = selectedStatus.status || 'stopped';
        state.isRunning = !!selectedStatus.running;
        state.tunnelProtocol = selectedStatus.protocol || '';
        state.lastError = selectedStatus.error || '';

        /* The alert banner reflects the selected tunnel. */
        if (state.isRunning) {
            hideTunnelAlert();
        } else if (state.status === 'error' && state.lastError) {
            showTunnelAlert(state.lastError);
        }

        /* Header pill: selected tunnel state, or an aggregate when several
           local tunnels exist. */
        const localProfiles = tunnelProfiles().filter((p) => p.local_enabled !== false);
        const stale = state.statusFailCount >= 3;
        const view = tunnelPresentation(selectedStatus, stale);
        let headerLabel = view.text;
        if (localProfiles.length > 1) {
            const overview = tunnelOverview(localProfiles, state.tunnelStatuses || {}, stale);
            const activity = t('tunnels_active_ratio', { n: overview.active, m: localProfiles.length });
            const healthLabel = stale ? t('status_stale') : t('status_' + (overview.health === 'recovering' ? 'reconnecting' : overview.health));
            headerLabel = healthLabel + ' · ' + activity;
            setStatusPill(overview.pill, stale ? t('status_stale') : activity);
        } else {
            setStatusPill(view.pill, view.text);
        }
        const labels = updateHeaderProtocols(localProfiles, state.tunnelStatuses || {});
        const pill = $('status-pill');
        if (pill) {
            pill.querySelector('.health-animal')?.remove();
            const dot = pill.querySelector('.dot');
            if (dot) dot.hidden = false;
            pill.setAttribute('aria-label', headerLabel + ' · ' + t('protocol_display_set', { protocols: labels.map(protocolDescription).join(' / ') }));
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

    function consumeLocalTunnelHandoffKey(cfg = state.config) {
        const path = window.location.pathname.replace(/\/+$/, '') || '/';
        if (path !== '/local') return '';
        const params = new URLSearchParams(window.location.search);
        const raw = String(params.get('tunnel_key') || '').trim();
        if (!raw) return '';
        const key = selectExistingTunnelKey(raw, cfg);
        params.delete('tunnel_key');
        const query = params.toString();
        const target = query ? `/local?${query}` : '/local';
        history.replaceState({ workspace: 'local' }, '', target);
        return key;
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

    function shortIdentifier(value) {
        const text = String(value || '').trim();
        if (text.length <= 12) return text;
        return `${text.slice(0, 8)}...${text.slice(-4)}`;
    }

    function remoteTunnelProfileLabel(profile) {
        if (!profile?.remote_management_enabled && !profile?.tunnel_id && !profile?.account_id) return '';
        const id = shortIdentifier(profile.tunnel_id || profile.account_id || '');
        return id ? t('tunnel_profile_cloudflare_linked_id', { id }) : t('tunnel_profile_cloudflare_linked');
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

        const animal = createHealthAnimal();

        const copy = document.createElement('span');
        copy.className = 'tunnel-profile-item__copy';
        const name = document.createElement('span');
        name.className = 'tunnel-profile-item__name';
        name.textContent = tunnelDisplayName(profile);
        copy.append(name);
        const remoteLabel = remoteTunnelProfileLabel(profile);
        if (remoteLabel) {
            const meta = document.createElement('span');
            meta.className = 'tunnel-profile-item__meta';
            meta.textContent = remoteLabel;
            copy.append(meta);
        }

        const protocol = document.createElement('span');
        protocol.className = 'tunnel-profile-item__protocol';
        protocol.hidden = true;
        const runtime = document.createElement('span');
        runtime.className = 'tunnel-profile-item__runtime';
        copy.append(runtime);
        summary.append(copy, protocol);

        item.append(summary, animal);
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
            const view = tunnelPresentation(st, state.statusFailCount >= 3);
            const statusText = state.statusFailCount >= 3 ? t('status_stale') : tunnelStatusText(st);
            updateHealthAnimal(item.querySelector('.health-animal'), st, state.statusFailCount >= 3);
            item.dataset.state = view.state;

            const runtime = item.querySelector('.tunnel-profile-item__runtime');
            if (runtime) runtime.textContent = statusText;

            const profile = tunnelProfiles().find(profile => profile.key === key) || { key };
            const protoText = profileProtocolLabels(profile, st).join(' / ');
            const protocol = item.querySelector('.tunnel-profile-item__protocol');
            if (protocol) {
                renderProtocolLabel(protocol, protoText);
                protocol.hidden = !protoText;
            }

            if (summary) {
                const name = item.querySelector('.tunnel-profile-item__name')?.textContent || '';
                const remote = item.querySelector('.tunnel-profile-item__meta')?.textContent || '';
                const fullStatus = protoText ? `${statusText} · ${protocolDescription(protoText)}` : statusText;
                const titleParts = [name, remote, fullStatus];
                if (st.status === 'error' && st.error) titleParts.push(st.error);
                if (st.health_error) titleParts.push(st.health_error);
                titleParts.push(lastCheckedText(st));
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
        const active = st.running || ['starting', 'stopping', 'reconnecting'].includes(st.status);
        const action = active ? 'stop' : 'start';
        actionBtn.dataset.action = action;
        actionBtn.dataset.key = key;
        actionBtn.classList.toggle('btn--primary', !active);
        actionBtn.classList.toggle('btn--danger', active);
        const text = actionBtn.querySelector('.text');
        if (text) text.textContent = t(active ? 'stop_this_tunnel' : 'start_this_tunnel');
    }

    function tunnelStatusText(st = {}) {
        const key = {
            starting: 'status_starting', stopping: 'status_stopping', reconnecting: 'status_reconnecting',
            running: 'status_running', error: 'status_error', stopped: 'status_stopped',
        }[st.status] || 'status_stopped';
        let text = t(key);
        if (st.status === 'reconnecting') {
            const retry = Number(st.retry_count || 0);
            const when = st.next_retry_at ? new Date(st.next_retry_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' }) : '—';
            text = t('status_reconnecting_detail', { n: retry, time: when });
        }
        return text;
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
            state.tunnelManager.selectedTunnelKey = key;
            await fetchConfig();
            if (state.features?.tunnel_manager && document.querySelector('.tab[aria-selected="true"]')?.dataset.tab === 'manager') {
                await window.cfui.fetchTunnelManagerSettings?.();
            }
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
    ns.updateHeaderProtocols = updateHeaderProtocols;
    ns.stopHeaderProtocolRotation = stopHeaderProtocolRotation;
    ns.protocolLabels = protocolLabels;
    ns.healthAnimalPresentation = healthAnimalPresentation;
    ns.createHealthAnimal = createHealthAnimal;
    ns.updateHealthAnimal = updateHealthAnimal;
    ns.tunnelPresentation = tunnelPresentation;
    ns.tunnelOverview = tunnelOverview;
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
