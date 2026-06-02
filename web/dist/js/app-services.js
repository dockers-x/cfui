/* =========================================================================
   CloudFlared UI — Features, Tunnel Manager, DDNS, MCP
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, t, apiGet, apiSend, toast, setBusy, flashField, setTokenVisible } = window.cfui;

    /* ====================================================================
       Features
       ==================================================================== */

    async function fetchFeatures() {
        try {
            const data = await apiGet('/features');
            state.features = data;
            applyFeatureVisibility(data);
            if ($('feature-manager-toggle')) $('feature-manager-toggle').checked = !!data.tunnel_manager;
            if ($('feature-ddns-toggle')) { $('feature-ddns-toggle').checked = !!data.ddns; $('feature-ddns-toggle').disabled = !data.tunnel_manager; }
            if ($('feature-mcp-toggle')) $('feature-mcp-toggle').checked = !!data.mcp;
        } catch (err) { console.error('features fetch failed', err); }
    }

    function applyFeatureVisibility(data) {
        const show = (id, on) => { const el = $(id); if (el) el.hidden = !on; };
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
            if ($('feature-ddns-toggle')) { $('feature-ddns-toggle').checked = !!data.ddns; $('feature-ddns-toggle').disabled = !data.tunnel_manager; }
            if ($('feature-mcp-toggle')) $('feature-mcp-toggle').checked = !!data.mcp;
            if (key === 'tunnel_manager') await fetchTunnelManagerSettings();
            if (key === 'ddns') await fetchDDNSConfig();
            toast.ok(t('feature_updated'));
        } catch (err) {
            toast.err(err.message);
            await fetchFeatures();
        }
    }

    /* ====================================================================
       Tunnel Manager
       ==================================================================== */

    async function fetchTunnelManagerSettings() {
        try {
            const data = await apiGet('/tunnel-manager/settings');
            state.tunnelManager.settings = data;
            renderTunnelManagerSettings(data);
        } catch (err) {
            setManagerStatus('error', t('error_generic', { err: err.message }));
            window.cfui.addLog({ key: 'tunnel_manager_settings_failed', params: { err: err.message } }, 'error');
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
    }

    function updateManagerAuthMode() {
        const keyMode = $('manager-auth-mode')?.value === 'key';
        if ($('manager-token-field')) $('manager-token-field').hidden = keyMode;
        if ($('manager-key-fields')) $('manager-key-fields').hidden = !keyMode;
    }

    function setManagerStatus(s, text) { const el = $('manager-status'); if (el) { el.setAttribute('data-state', s); el.querySelector('.text').textContent = text; } }

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
            if (showFeedback) { toast.ok(t('manager_settings_saved')); flashField('manager-save-settings'); }
            if (canLoadTunnelManagerZones(data)) await loadTunnelManagerZones(true);
            else { state.tunnelManager.zones = []; renderTunnelManagerZones(); }
        } catch (err) {
            setManagerStatus('error', err.message);
            if (showFeedback) toast.err(t('manager_settings_save_failed') + ': ' + err.message);
        } finally { if (showFeedback) setBusy(btn, false); }
    }

    function canLoadTunnelManagerZones(s = state.tunnelManager.settings) { return !!(s?.enabled && s?.account_id && (s?.api_token_set || s?.api_key_set)); }

    async function loadTunnelManagerZones(silent = false) {
        const btn = silent ? null : $('manager-refresh-zones');
        if (btn) setBusy(btn, true);
        try {
            if (!silent) setManagerStatus('loading', t('manager_status_loading_zones'));
            const data = await apiGet('/tunnel-manager/zones');
            state.tunnelManager.zones = data.zones || [];
            state.tunnelManager.zonesLoaded = true;
            renderTunnelManagerZones();
            if (!silent) { setManagerStatus('ok', t('manager_status_zones_loaded')); toast.ok(t('manager_status_zones_loaded')); }
        } catch (err) { if (!silent) { setManagerStatus('error', err.message); toast.err(t('zone_load_failed') + ': ' + err.message); } }
        finally { if (btn) setBusy(btn, false); }
    }

    async function maybeLoadTunnelManagerZones(quiet = true) {
        if (!canLoadTunnelManagerZones()) { state.tunnelManager.zones = []; renderTunnelManagerZones(); return; }
        if (state.tunnelManager.zonesLoaded) { renderTunnelManagerZones(); return; }
        await loadTunnelManagerZones(quiet);
    }

    function renderTunnelManagerZones() {
        const sel = $('manager-entry-domain-select');
        if (!sel) return;
        const current = $('manager-entry-domain').value.trim() || sel.value;
        sel.innerHTML = '';
        const manual = document.createElement('option'); manual.value = ''; manual.textContent = t('manual_domain_option'); sel.appendChild(manual);
        for (const z of (state.tunnelManager.zones || [])) { const opt = document.createElement('option'); opt.value = z.name; opt.textContent = z.status ? `${z.name} (${z.status})` : z.name; sel.appendChild(opt); }
        const names = new Set((state.tunnelManager.zones || []).map((z) => z.name));
        if (current && names.has(current)) sel.value = current;
        else if (!current && state.tunnelManager.zones.length) sel.value = state.tunnelManager.zones[0].name;
        else sel.value = '';
        updateDomainInputMode();
    }

    function updateDomainInputMode() {
        const sel = $('manager-entry-domain-select'), input = $('manager-entry-domain');
        if (!sel || !input) return;
        const manual = !sel.value;
        input.hidden = !manual; input.disabled = !manual;
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
        } catch (err) { setManagerStatus('error', err.message); if (!silent) toast.err(t('manager_config_load_failed') + ': ' + err.message); }
        finally { if (btn) setBusy(btn, false); }
    }

    function renderTunnelManagerConfig(cfg) {
        $('manager-config-panel').hidden = false;
        const meta = $('manager-config-meta');
        meta.innerHTML = '';
        const parts = [t('tunnel_label') + ' ' + (cfg.tunnel_id || $('manager-tunnel-id').value), t('version_label') + ' ' + (cfg.version || 0), (cfg.entries?.length || 0) + ' ' + t('rules_label')];
        parts.forEach((p, i) => { if (i > 0) { const sep = document.createElement('span'); sep.className = 'sep'; meta.appendChild(sep); } const span = document.createElement('span'); span.textContent = p; meta.appendChild(span); });
        const list = $('manager-rules-list');
        list.innerHTML = '';
        if (!cfg.entries?.length) { const empty = document.createElement('div'); empty.className = 'empty'; empty.textContent = t('no_ingress_rules'); list.appendChild(empty); return; }
        for (const entry of cfg.entries) {
            const row = document.createElement('div'); row.className = 'rule';
            const body = document.createElement('div'); body.className = 'body';
            const title = document.createElement('div'); title.className = 'title'; title.textContent = entry.hostname || t('catch_all_rule');
            const detail = document.createElement('div'); detail.className = 'detail';
            const noTls = entry.no_tls_verify ? ` · ${t('no_tls_verify_detail')}` : '';
            detail.textContent = `${entry.path || '/'} → ${entry.service}${noTls}`;
            body.append(title, detail);
            const actions = document.createElement('div'); actions.className = 'actions';
            const edit = document.createElement('button'); edit.className = 'btn btn--sm'; edit.type = 'button'; edit.textContent = t('edit'); edit.addEventListener('click', () => openTunnelEntryDialog(entry));
            const del = document.createElement('button'); del.className = 'btn btn--sm btn--ghost'; del.type = 'button'; del.textContent = t('delete'); del.addEventListener('click', () => confirmDeleteEntry(entry));
            actions.append(edit, del); row.append(body, actions); list.appendChild(row);
        }
    }

    async function confirmDeleteEntry(entry) { const { confirm } = window.cfui; const ok = await confirm({ title: t('delete_rule_title'), message: t('delete_rule_message', { hostname: entry.hostname || t('catch_all_rule'), path: entry.path || '/' }), okText: t('delete') }); if (ok) await deleteTunnelManagerEntry(entry.index); }

    function openTunnelEntryDialog(entry = null) {
        const dialog = $('manager-entry-dialog'); if (!dialog) return;
        if (entry) fillTunnelEntryForm(entry); else resetTunnelEntryForm();
        const editing = $('manager-entry-index').value !== '';
        $('manager-entry-dialog-title').textContent = t(editing ? 'edit_published_app_title' : 'published_app_title');
        $('manager-entry-submit').querySelector('.text').textContent = t(editing ? 'update_rule' : 'add_rule');
        window.cfui.openDialog(dialog);
    }

    function fillTunnelEntryForm(entry) {
        const host = splitHostname(entry.hostname || ''), svc = splitService(entry.service || '');
        $('manager-entry-index').value = String(entry.index);
        $('manager-entry-subdomain').value = host.subdomain; $('manager-entry-domain').value = host.domain;
        renderTunnelManagerZones();
        $('manager-entry-path').value = entry.path || '';
        $('manager-entry-service-type').value = svc.type; $('manager-entry-service').value = svc.value;
        $('manager-entry-http-host-header').value = entry.http_host_header || '';
        $('manager-entry-origin-server-name').value = entry.origin_server_name || '';
        $('manager-entry-no-tls').checked = !!entry.no_tls_verify;
        updateServicePlaceholder();
    }

    function resetTunnelEntryForm() {
        $('manager-entry-index').value = ''; $('manager-entry-subdomain').value = ''; $('manager-entry-domain').value = '';
        renderTunnelManagerZones();
        $('manager-entry-path').value = ''; $('manager-entry-service-type').value = 'http'; $('manager-entry-service').value = '';
        $('manager-entry-http-host-header').value = ''; $('manager-entry-origin-server-name').value = ''; $('manager-entry-no-tls').checked = false;
        updateServicePlaceholder();
    }

    function updateServicePlaceholder() {
        const sel = $('manager-entry-service-type'), input = $('manager-entry-service');
        if (!sel || !input) return;
        const ph = { http: 'localhost:8080', https: 'localhost:8443', ssh: 'localhost:22', rdp: 'localhost:3389', tcp: 'localhost:5432', unix: '/var/run/app.sock', http_status: '404', raw: 'http://localhost:8080' };
        input.placeholder = ph[sel.value] || ph.http;
    }

    function splitHostname(h) { h = (h || '').trim(); if (!h || !h.includes('.')) return { subdomain: h, domain: '' }; const p = h.split('.'); return { subdomain: p.shift(), domain: p.join('.') }; }
    function splitService(s) { s = (s || '').trim(); if (s.startsWith('http_status:')) return { type: 'http_status', value: s.slice(12) }; const m = s.match(/^([a-z_]+):\/\/(.+)$/i); if (m) return { type: ['http','https','ssh','rdp','tcp','unix'].includes(m[1]) ? m[1] : 'raw', value: m[2] }; return { type: 'raw', value: s }; }
    function buildHostname(sub, dom) { sub = (sub || '').trim().replace(/^\.+|\.+$/g, ''); dom = (dom || '').trim().replace(/^\.+|\.+$/g, ''); return sub ? (dom ? `${sub}.${dom}` : sub) : dom; }
    function buildService(type, value) { value = (value || '').trim(); if (type === 'raw') return value; if (type === 'http_status') return value.startsWith('http_status:') ? value : `http_status:${value || '404'}`; return value.startsWith(`${type}:`) ? value : `${type}://${value}`; }

    async function submitTunnelManagerEntry(e) {
        e.preventDefault();
        const index = $('manager-entry-index').value;
        const entry = { hostname: buildHostname($('manager-entry-subdomain').value, $('manager-entry-domain').value), path: $('manager-entry-path').value.trim(), service: buildService($('manager-entry-service-type').value, $('manager-entry-service').value), no_tls_verify: $('manager-entry-no-tls').checked, http_host_header: $('manager-entry-http-host-header').value.trim(), origin_server_name: $('manager-entry-origin-server-name').value.trim() };
        if (!entry.service) { toast.err(t('service_required')); return; }
        const btn = $('manager-entry-submit'); setBusy(btn, true);
        const url = index === '' ? '/tunnel-manager/entries' : `/tunnel-manager/entries/${index}`;
        try { const data = await apiSend(url, index === '' ? 'POST' : 'PUT', entry); state.tunnelManager.config = data; renderTunnelManagerConfig(data); resetTunnelEntryForm(); window.cfui.closeDialog($('manager-entry-dialog')); toast.ok(t(index === '' ? 'tunnel_rule_added' : 'tunnel_rule_updated')); }
        catch (err) { toast.err(t('tunnel_rule_save_failed') + ': ' + err.message); }
        finally { setBusy(btn, false); }
    }

    async function deleteTunnelManagerEntry(index) {
        try { const data = await apiSend(`/tunnel-manager/entries/${index}`, 'DELETE'); state.tunnelManager.config = data; renderTunnelManagerConfig(data); toast.ok(t('tunnel_rule_deleted')); }
        catch (err) { toast.err(t('tunnel_rule_delete_failed') + ': ' + err.message); }
    }

    async function verifyTokenPermissions() {
        const btn = $('manager-verify-permissions'), result = $('manager-verify-result');
        const authMode = $('manager-auth-mode')?.value || 'token';
        const payload = { auth_mode: authMode, api_token: authMode === 'token' ? $('manager-api-token')?.value.trim() : '', api_email: authMode === 'key' ? $('manager-api-email')?.value.trim() : '', api_key: authMode === 'key' ? $('manager-api-key')?.value.trim() : '' };
        if (authMode === 'token' && !payload.api_token && !state.tunnelManager.settings?.api_token_set) {
            result.hidden = false; result.innerHTML = '';
            const span = document.createElement('span'); span.className = 'pill'; span.setAttribute('data-state', 'error'); span.textContent = t('verify_enter_token'); result.appendChild(span);
            toast.err(t('verify_enter_token')); return;
        }
        setBusy(btn, true, t('verify_checking')); result.hidden = false; result.innerHTML = '';
        const loading = document.createElement('span'); loading.className = 'pill'; loading.setAttribute('data-state', 'loading'); loading.textContent = t('verify_checking'); result.appendChild(loading);
        try {
            const data = await apiSend('/tunnel-manager/verify-token', 'POST', payload);
            result.innerHTML = '';
            if (data.token_status === 'inactive' || data.token_status === 'revoked') { const s = document.createElement('span'); s.className = 'pill'; s.setAttribute('data-state', 'error'); s.textContent = t('verify_token_status') + ': ' + data.token_status; result.appendChild(s); return; }
            for (const p of (data.permissions || [])) { const s = document.createElement('span'); s.className = 'pill'; s.setAttribute('data-state', p.granted ? 'ok' : (p.required ? 'error' : 'ok')); const dot = document.createElement('span'); dot.className = 'dot'; const txt = document.createElement('span'); txt.textContent = ' ' + p.description; s.append(dot, txt); result.appendChild(s); }
            toast[data.valid ? 'ok' : 'err'](t(data.valid ? 'verify_permissions_passed' : 'verify_permissions_failed'));
        } catch (err) { result.innerHTML = ''; const s = document.createElement('span'); s.className = 'pill'; s.setAttribute('data-state', 'error'); s.textContent = err.message; result.appendChild(s); toast.err(t('verify_failed') + ': ' + err.message); }
        finally { setBusy(btn, false); }
    }

    /* ====================================================================
       MCP
       ==================================================================== */

    async function fetchMCPStatus() {
        try { const data = await apiGet('/mcp/status'); state.mcp.status = data; state.mcp.tokens = data.tokens || []; renderMCPStatus(data); renderMCPTokens(); }
        catch (err) { setMCPStatus('error', err.message); }
    }

    function setMCPStatus(s, text) { const el = $('mcp-status'); if (el) { el.setAttribute('data-state', s); el.querySelector('.text').textContent = text; } }

    function renderMCPStatus(status) {
        const endpoint = status.endpoint || '/mcp';
        const absolute = `${window.location.origin}${endpoint}`;
        $('mcp-endpoint').value = absolute;
        const example = $('mcp-config-example');
        if (example) example.textContent = `{\n  "mcpServers": {\n    "cfui": {\n      "url": "${absolute}",\n      "headers": {\n        "Authorization": "Bearer YOUR_TOKEN"\n      }\n    }\n  }\n}`;
        setMCPStatus(status.enabled ? 'ok' : 'disabled', t(status.enabled ? 'mcp_status_enabled' : 'mcp_status_disabled'));
    }

    function renderMCPTokens() {
        const list = $('mcp-token-list'); if (!list) return; list.innerHTML = '';
        const tokens = state.mcp.tokens || [];
        if (!tokens.length) { const empty = document.createElement('div'); empty.className = 'empty'; empty.textContent = t('mcp_no_tokens'); list.appendChild(empty); return; }
        for (const token of tokens) {
            const row = document.createElement('div'); row.className = 'rule';
            const body = document.createElement('div'); body.className = 'body';
            const title = document.createElement('div'); title.className = 'title'; title.textContent = token.name || t('mcp_token');
            const detail = document.createElement('div'); detail.className = 'detail'; detail.textContent = token.created_at ? `${token.masked} · ${new Date(token.created_at).toLocaleString()}` : token.masked;
            body.append(title, detail);
            const actions = document.createElement('div'); actions.className = 'actions';
            const del = document.createElement('button'); del.className = 'btn btn--sm btn--ghost'; del.type = 'button'; del.textContent = t('delete'); del.addEventListener('click', () => confirmDeleteMCPToken(token));
            actions.append(del); row.append(body, actions); list.appendChild(row);
        }
    }

    async function confirmDeleteMCPToken(token) { const { confirm } = window.cfui; const ok = await confirm({ title: t('delete_mcp_token_title'), message: t('delete_mcp_token_message', { name: token.name || t('mcp_token') }), okText: t('delete') }); if (ok) await deleteMCPToken(token.id); }

    async function deleteMCPToken(id) {
        try { await apiSend(`/mcp/tokens/${encodeURIComponent(id)}`, 'DELETE'); await fetchMCPStatus(); toast.ok(t('mcp_token_deleted')); }
        catch (err) { toast.err(t('mcp_token_delete_failed') + ': ' + err.message); }
    }

    function showCreatedMCPToken(token) {
        const box = $('mcp-created-token'), code = $('mcp-created-value');
        if (!box || !code) return;
        code.textContent = token || ''; box.hidden = !token;
        if (token) navigator.clipboard?.writeText(token).then(() => toast.ok(t('copied_to_clipboard')), () => {});
    }

    async function createMCPToken(e) {
        e.preventDefault();
        const name = $('mcp-token-name').value.trim(), token = $('mcp-token-input').value.trim();
        if (token && token.length < 16) { const { confirm } = window.cfui; const ok = await confirm({ title: t('weak_token_title'), message: t('weak_token_message'), okText: t('continue'), okClass: 'btn--primary' }); if (!ok) return; }
        const btn = $('mcp-token-create'); setBusy(btn, true, t('creating'));
        try { const data = await apiSend('/mcp/tokens', 'POST', { name, token }); $('mcp-token-name').value = ''; $('mcp-token-input').value = ''; showCreatedMCPToken(data.token); await fetchMCPStatus(); toast.ok(t('mcp_token_created')); }
        catch (err) { toast.err(t('mcp_token_create_failed') + ': ' + err.message); }
        finally { setBusy(btn, false); }
    }

    /* ====================================================================
       DDNS
       ==================================================================== */

    async function refreshDDNS() { await fetchDDNSConfig(); await fetchDDNSStatus(); }

    async function fetchDDNSConfig() { try { const data = await apiGet('/ddns/config'); state.ddns.config = data; renderDDNSConfig(data); } catch (err) { setDDNSStatus('error', err.message); } }

    function renderDDNSConfig(cfg) {
        const credsMissing = !cfg.has_credentials;
        $('ddns-no-creds').hidden = !credsMissing;
        $('ddns-main').hidden = credsMissing;
        if (!credsMissing) {
            const v4 = (cfg.ip_sources || []).filter((s) => s.ip_type === 'ipv4').map((s) => s.url).join('\n');
            const v6 = (cfg.ip_sources || []).filter((s) => s.ip_type === 'ipv6').map((s) => s.url).join('\n');
            $('ddns-ipv4-textarea').value = v4; $('ddns-ipv6-textarea').value = v6;
            $('ddns-interval').value = String(cfg.interval_mins || 5); $('ddns-max-retries').value = String(cfg.max_retries || 3);
            $('ddns-only-on-change').checked = cfg.only_on_change !== false;
            renderDDNSRecords(cfg.records || []);
        }
        setDDNSStatus(cfg.enabled ? 'ok' : 'disabled', t(cfg.enabled ? 'ddns_status_running' : 'ddns_status_disabled'));
    }

    async function fetchDDNSStatus() { try { const data = await apiGet('/ddns/status'); state.ddns.status = data; renderDDNSStatus(data); } catch { /* */ } }

    function renderDDNSStatus(data) {
        if ($('ddns-ipv4-value')) $('ddns-ipv4-value').textContent = data.current_v4 || t('ddns_unknown');
        if ($('ddns-ipv6-value')) $('ddns-ipv6-value').textContent = data.current_v6 || t('ddns_unknown');
        if ($('ddns-last-check')) $('ddns-last-check').textContent = data.last_check ? `${t('ddns_last_check')}: ${new Date(data.last_check).toLocaleString()}` : '—';
        if ($('ddns-sync-log-list') && data.results) {
            const list = $('ddns-sync-log-list'); list.innerHTML = '';
            const results = data.results.slice().reverse();
            if (!results.length) { const empty = document.createElement('div'); empty.className = 'empty'; empty.textContent = t('ddns_no_sync_history'); list.appendChild(empty); }
            else for (const r of results) {
                const row = document.createElement('div'); row.className = 'item';
                const ts = document.createElement('span'); ts.className = 'ts'; ts.textContent = new Date(r.time).toLocaleTimeString();
                const ind = document.createElement('span'); ind.className = `indicator ${r.success ? 'ok' : 'err'}`; ind.textContent = r.success ? '✓' : '✗';
                const host = document.createElement('span'); host.className = 'host'; host.textContent = r.hostname || '';
                const ip = document.createElement('span'); ip.className = 'ip'; ip.textContent = r.ip || '';
                const msg = document.createElement('span'); msg.className = 'msg'; msg.textContent = r.message || '';
                row.append(ts, ind, host, ip, msg); list.appendChild(row);
            }
        }
    }

    function setDDNSStatus(s, text) { const el = $('ddns-status'); if (el) { el.setAttribute('data-state', s); el.querySelector('.text').textContent = text; } }

    async function ddnsSaveSettings() {
        const btn = $('ddns-save-settings'); setBusy(btn, true, t('saving'));
        const v4 = $('ddns-ipv4-textarea').value.split('\n').map((l) => l.trim()).filter(Boolean);
        const v6 = $('ddns-ipv6-textarea').value.split('\n').map((l) => l.trim()).filter(Boolean);
        const sources = [...v4.map((url) => ({ url, ip_type: 'ipv4' })), ...v6.map((url) => ({ url, ip_type: 'ipv6' }))];
        const payload = { enabled: state.ddns.config?.enabled ?? false, ip_sources: sources, interval_mins: parseInt($('ddns-interval').value, 10) || 5, max_retries: parseInt($('ddns-max-retries').value, 10) || 3, only_on_change: $('ddns-only-on-change').checked };
        try { const data = await apiSend('/ddns/config', 'POST', payload); state.ddns.config = data; renderDDNSConfig(data); toast.ok(t('ddns_settings_saved')); }
        catch (err) { toast.err(t('ddns_save_failed') + ': ' + err.message); }
        finally { setBusy(btn, false); }
    }

    async function ddnsSyncNow() {
        const btn = $('ddns-sync-now'); setBusy(btn, true, t('ddns_status_syncing'));
        try { const data = await apiSend('/ddns/sync-now', 'POST'); state.ddns.status = data; renderDDNSStatus(data); setDDNSStatus('ok', t('ddns_status_running')); toast.ok(t('ddns_sync_triggered')); }
        catch (err) { setDDNSStatus('error', err.message); toast.err(t('ddns_sync_failed') + ': ' + err.message); }
        finally { setBusy(btn, false); }
    }

    function defaultDDNSRecordValue(type) { return type === 'AAAA' ? '{IPV6}' : '{IPV4}'; }
    function normalizeDDNSRecordValue(type, value) { const trimmed = (value || '').trim(); return trimmed || defaultDDNSRecordValue(type); }
    function formatDDNSRecordValue(rec) { const n = normalizeDDNSRecordValue(rec.type, rec.value); if (n === '{IPV4}') return `{IPV4} · ${t('ddns_record_value_auto_ipv4')}`; if (n === '{IPV6}') return `{IPV6} · ${t('ddns_record_value_auto_ipv6')}`; return n; }

    function syncDDNSRecordValueFields() {
        const g4 = $('ddns-record-ipv4-value-group'), g6 = $('ddns-record-ipv6-value-group');
        if (!g4 || !g6) return;
        const show4 = $('ddns-record-ipv4')?.checked, show6 = $('ddns-record-ipv6')?.checked;
        g4.hidden = !show4; g6.hidden = !show6;
        if ($('ddns-record-ipv4-value')) { if (!$('ddns-record-ipv4-value').value.trim()) $('ddns-record-ipv4-value').value = defaultDDNSRecordValue('A'); $('ddns-record-ipv4-value').disabled = !show4; }
        if ($('ddns-record-ipv6-value')) { if (!$('ddns-record-ipv6-value').value.trim()) $('ddns-record-ipv6-value').value = defaultDDNSRecordValue('AAAA'); $('ddns-record-ipv6-value').disabled = !show6; }
    }

    function renderDDNSRecords(records) {
        const list = $('ddns-records-list'); if (!list) return; list.innerHTML = '';
        if (!records?.length) { const empty = document.createElement('div'); empty.className = 'empty'; empty.textContent = t('ddns_no_records'); list.appendChild(empty); return; }
        records.forEach((rec, i) => {
            const row = document.createElement('div'); row.className = 'rule';
            const body = document.createElement('div'); body.className = 'body';
            const title = document.createElement('div'); title.className = 'title'; title.textContent = rec.name || '—';
            const detail = document.createElement('div'); detail.className = 'detail';
            const ttlText = rec.ttl === 1 ? t('ddns_ttl_auto') : rec.ttl + 's';
            detail.textContent = `${rec.type} · ${t('ddns_record_value')}: ${formatDDNSRecordValue(rec)} · ${t('ddns_record_ttl')}: ${ttlText}${rec.proxied ? ` · ${t('ddns_record_proxied')}` : ''}`;
            body.append(title, detail);
            const actions = document.createElement('div'); actions.className = 'actions';
            const editBtn = document.createElement('button'); editBtn.type = 'button'; editBtn.className = 'btn btn--sm'; editBtn.textContent = t('edit'); editBtn.addEventListener('click', () => openDDNSRecordDialog(i, rec));
            const delBtn = document.createElement('button'); delBtn.type = 'button'; delBtn.className = 'btn btn--sm btn--ghost'; delBtn.textContent = t('delete'); delBtn.addEventListener('click', () => { const { confirm } = window.cfui; confirm({ title: t('delete_ddns_record_title'), message: t('delete_ddns_record_message', { name: rec.name || '' }), okText: t('delete') }).then((ok) => { if (ok) deleteDDNSRecord(i); }); });
            actions.append(editBtn, delBtn); row.append(body, actions); list.appendChild(row);
        });
    }

    function openDDNSRecordDialog(index = null, rec = null) {
        const dialog = $('ddns-record-dialog'); if (!dialog) return;
        if (rec) fillDDNSRecordForm(index, rec); else resetDDNSRecordForm();
        const editing = rec != null;
        $('ddns-record-dialog-title').textContent = t(editing ? 'ddns_edit_record' : 'ddns_add_record');
        $('ddns-record-submit').querySelector('.text').textContent = t(editing ? 'update_rule' : 'ddns_add_record');
        window.cfui.openDialog(dialog);
        loadDDNSZones().then(() => { if (rec?.zone_id && $('ddns-record-zone-select')) $('ddns-record-zone-select').value = rec.zone_id; });
    }

    function fillDDNSRecordForm(index, rec) {
        $('ddns-record-subdomain').value = rec.subdomain || '';
        $('ddns-record-ipv4').checked = rec.type === 'A'; $('ddns-record-ipv6').checked = rec.type === 'AAAA';
        $('ddns-record-ipv4-value').value = normalizeDDNSRecordValue('A', rec.type === 'A' ? rec.value : '');
        $('ddns-record-ipv6-value').value = normalizeDDNSRecordValue('AAAA', rec.type === 'AAAA' ? rec.value : '');
        $('ddns-record-ttl-select').value = String(rec.ttl || 1);
        $('ddns-record-proxied').checked = rec.proxied !== false;
        $('ddns-record-form').dataset.editIndex = String(index);
        $('ddns-record-ipv4').disabled = true; $('ddns-record-ipv6').disabled = true;
        syncDDNSRecordValueFields();
    }

    function resetDDNSRecordForm() {
        $('ddns-record-subdomain').value = '';
        $('ddns-record-ipv4').checked = true; $('ddns-record-ipv6').checked = true;
        $('ddns-record-ipv4-value').value = defaultDDNSRecordValue('A'); $('ddns-record-ipv6-value').value = defaultDDNSRecordValue('AAAA');
        $('ddns-record-ttl-select').value = '1'; $('ddns-record-proxied').checked = true;
        delete $('ddns-record-form').dataset.editIndex;
        $('ddns-record-ipv4').disabled = false; $('ddns-record-ipv6').disabled = false;
        syncDDNSRecordValueFields();
    }

    async function ddnsSubmitRecord(e) {
        e.preventDefault();
        const editing = $('ddns-record-form').dataset.editIndex;
        const sel = $('ddns-record-zone-select');
        const zoneName = sel?.selectedOptions[0]?.textContent?.replace(/ \(.*\)/, '') || '';
        const entry = { subdomain: $('ddns-record-subdomain').value.trim(), zone_id: sel?.value, zone_name: zoneName, ipv4: $('ddns-record-ipv4').checked, ipv6: $('ddns-record-ipv6').checked, ipv4_value: normalizeDDNSRecordValue('A', $('ddns-record-ipv4-value').value), ipv6_value: normalizeDDNSRecordValue('AAAA', $('ddns-record-ipv6-value').value), proxied: $('ddns-record-proxied').checked, ttl: parseInt($('ddns-record-ttl-select').value, 10) || 1 };
        if (editing) entry.value = entry.ipv4 ? normalizeDDNSRecordValue('A', $('ddns-record-ipv4-value').value) : normalizeDDNSRecordValue('AAAA', $('ddns-record-ipv6-value').value);
        if (!entry.ipv4 && !entry.ipv6) { toast.err(t('ddns_record_ip_required')); return; }
        const btn = $('ddns-record-submit'); setBusy(btn, true);
        try { const url = editing ? `/ddns/records/${editing}` : '/ddns/records'; const data = await apiSend(url, editing ? 'PUT' : 'POST', entry); state.ddns.config = data; renderDDNSConfig(data); resetDDNSRecordForm(); window.cfui.closeDialog($('ddns-record-dialog')); toast.ok(t(editing ? 'ddns_record_updated' : 'ddns_record_added')); }
        catch (err) { toast.err(t('ddns_record_save_failed') + ': ' + err.message); }
        finally { setBusy(btn, false); }
    }

    async function deleteDDNSRecord(index) { try { const data = await apiSend(`/ddns/records/${index}`, 'DELETE'); state.ddns.config = data; renderDDNSConfig(data); toast.ok(t('ddns_record_deleted')); } catch (err) { toast.err(t('ddns_record_delete_failed') + ': ' + err.message); } }

    async function loadDDNSZones() {
        if (state.ddns.zonesLoaded) { renderDDNSZones(); return; }
        try { const data = await apiGet('/ddns/zones'); state.ddns.zones = data.zones || []; state.ddns.zonesLoaded = true; renderDDNSZones(); }
        catch (err) { toast.err(t('zone_load_failed') + ': ' + err.message); }
    }

    function renderDDNSZones() { const sel = $('ddns-record-zone-select'); if (!sel) return; sel.innerHTML = ''; for (const z of state.ddns.zones) { const opt = document.createElement('option'); opt.value = z.id; opt.textContent = z.name + (z.status ? ` (${z.status})` : ''); sel.appendChild(opt); } if (!sel.value && state.ddns.zones.length) sel.value = state.ddns.zones[0].id; }

    function switchDDNSSubTab(name) {
        const sources = $('ddns-subtab-sources'), auto = $('ddns-subtab-auto');
        const isSrc = name === 'sources';
        if (sources) { sources.setAttribute('aria-selected', String(isSrc)); sources.tabIndex = isSrc ? 0 : -1; }
        if (auto) { auto.setAttribute('aria-selected', String(!isSrc)); auto.tabIndex = isSrc ? -1 : 0; }
        $('ddns-panel-sources').hidden = !isSrc; $('ddns-panel-auto').hidden = isSrc;
    }

    /* ====================================================================
       Wire all service event listeners
       ==================================================================== */

    function wireServices() {
        /* Features */
        $('feature-manager-toggle')?.addEventListener('change', (e) => saveFeature('tunnel_manager', e.target.checked));
        $('feature-ddns-toggle')?.addEventListener('change', (e) => saveFeature('ddns', e.target.checked));
        $('feature-mcp-toggle')?.addEventListener('change', (e) => saveFeature('mcp', e.target.checked));

        /* Manager */
        $('manager-auth-mode')?.addEventListener('change', updateManagerAuthMode);
        $('manager-save-settings')?.addEventListener('click', () => saveTunnelManagerSettings({ showFeedback: true }));
        $('manager-load-config')?.addEventListener('click', () => loadTunnelManagerConfig(false));
        $('manager-refresh-zones')?.addEventListener('click', () => loadTunnelManagerZones(false));
        $('manager-add-entry-btn')?.addEventListener('click', () => openTunnelEntryDialog());
        $('manager-entry-form')?.addEventListener('submit', submitTunnelManagerEntry);
        $('manager-entry-domain-select')?.addEventListener('change', updateDomainInputMode);
        $('manager-entry-service-type')?.addEventListener('change', updateServicePlaceholder);
        $('manager-verify-permissions')?.addEventListener('click', verifyTokenPermissions);
        $('manager-api-token-toggle')?.addEventListener('click', () => setTokenVisible($('manager-api-token'), $('manager-api-token-toggle'), $('manager-api-token').type === 'password'));
        $('manager-api-key-toggle')?.addEventListener('click', () => setTokenVisible($('manager-api-key'), $('manager-api-key-toggle'), $('manager-api-key').type === 'password'));

        /* MCP */
        $('mcp-help-toggle')?.addEventListener('click', () => { const panel = $('mcp-help-panel'); const hidden = panel.hidden; panel.hidden = !hidden; $('mcp-help-toggle').setAttribute('aria-expanded', String(hidden)); });
        $('mcp-token-form')?.addEventListener('submit', createMCPToken);
        $('mcp-copy-created')?.addEventListener('click', () => { const v = $('mcp-created-value')?.textContent || ''; navigator.clipboard?.writeText(v).then(() => toast.ok(t('copied_to_clipboard')), () => toast.err(t('copy_failed'))); });

        /* DDNS */
        $('ddns-sync-now')?.addEventListener('click', ddnsSyncNow);
        $('ddns-save-settings')?.addEventListener('click', ddnsSaveSettings);
        $('ddns-add-record-btn')?.addEventListener('click', () => openDDNSRecordDialog());
        $('ddns-record-form')?.addEventListener('submit', ddnsSubmitRecord);
        $('ddns-record-ipv4')?.addEventListener('change', syncDDNSRecordValueFields);
        $('ddns-record-ipv6')?.addEventListener('change', syncDDNSRecordValueFields);
        $('ddns-subtab-sources')?.addEventListener('click', () => switchDDNSSubTab('sources'));
        $('ddns-subtab-auto')?.addEventListener('click', () => switchDDNSSubTab('auto'));

        /* Help popover dismiss on outside click */
        document.addEventListener('click', (e) => { window.cfui.$$('details.help-popover[open]').forEach((p) => { if (!p.contains(e.target)) p.removeAttribute('open'); }); });

        /* Tab-change data refresh */
        document.addEventListener('tabchange', (e) => {
            const name = e.detail?.name;
            if (name === 'manager' && state.features?.tunnel_manager) fetchTunnelManagerSettings();
            else if (name === 'ddns' && state.features?.ddns) refreshDDNS();
        });
    }

    /* ---- Export ---- */
    const ns = window.cfui;
    ns.fetchFeatures = fetchFeatures;
    ns.saveFeature = saveFeature;
    ns.fetchTunnelManagerSettings = fetchTunnelManagerSettings;
    ns.maybeLoadTunnelManagerZones = maybeLoadTunnelManagerZones;
    ns.loadTunnelManagerConfig = loadTunnelManagerConfig;
    ns.canLoadTunnelManagerZones = canLoadTunnelManagerZones;
    ns.fetchMCPStatus = fetchMCPStatus;
    ns.refreshDDNS = refreshDDNS;
    ns.fetchDDNSStatus = fetchDDNSStatus;
    ns.wireServices = wireServices;
})();
