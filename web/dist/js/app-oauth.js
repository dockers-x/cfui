/* =========================================================================
   CloudFlared UI — OAuth Cloudflare console
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, $$, t, API_BASE, apiGet, apiSend, toast, setBusy, sleep } = window.cfui;
    const defaultOAuthRelayCallbackURL = 'https://oauth.omarchy.qzz.io/oauth/callback';

    const {
        resourceDefinitions,
        dnsTypes,
        wafActions,
        wafManagedOverrideActions,
        wafManagedSensitivityLevels,
        wafSkipProducts,
        wafSkipPhases,
        maxR2ObjectUploadBytes,
        maxR2ChunkedUploadBytes,
        r2ObjectUploadChunkBytes,
        maxR2InlinePreviewBytes,
        maxKVValueUploadBytes,
        analyticsRanges,
        overviewMetricDefinitions,
        oauthPermissionDefinitions,
        oauthMinimumSetupScopes,
        oauthFullConsoleSetupScopes,
        securityLevels,
        zoneSettingToggles,
        cacheLevels,
        sslModes,
        browserCacheTTLs,
        writableZoneSettings,
    } = window.cfui.oauthData;

    async function fetchOAuthStatus() {
        if (!state.features?.oauth_enabled) return null;
        try {
            const previousCurrentID = state.oauth.status?.current?.id || '';
            const status = await apiGet('/oauth/status');
            state.oauth.status = status;
            const nextCurrentID = status?.current?.id || '';
            if (previousCurrentID && previousCurrentID !== nextCurrentID) {
                resetOAuthResourceState({ keepStatus: true });
            }
            renderOAuthStatus(status);
            return status;
        } catch (err) {
            setOAuthStatus('error', err.message);
            return null;
        }
    }

    async function saveOAuthRelayCallback(relayURL, button) {
        relayURL = String(relayURL || '').trim();
        if (!relayURL) {
            toast.err(t('oauth_relay_required'));
            return;
        }
        setBusy(button, true, t('saving'));
        try {
            const status = await apiSend('/oauth/config', 'PATCH', { relay_callback_url: relayURL });
            state.oauth.status = status;
            state.oauth.relayCheck = null;
            state.oauth.relayCheckError = '';
            renderOAuthStatus(status);
            toast.ok(t('oauth_relay_saved'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function rawAPIError(res) {
        try {
            const data = await res.json();
            return data.error || res.statusText;
        } catch {
            return res.statusText;
        }
    }

    async function startOAuthLogin() {
        const btn = $('oauth-login');
        setBusy(btn, true, t('oauth_signing_in'));
        try {
            const resp = await apiSend('/oauth/login', 'POST', {
                scope: selectedOAuthScopeString(),
                fresh_login: !!state.oauth.status?.logged_in,
            });
            if (!resp.url) throw new Error(t('oauth_login_url_missing'));
            window.location.href = resp.url;
        } catch (err) {
            toast.err(err.message);
            setBusy(btn, false);
        }
    }

    async function logoutOAuth() {
        const sessionID = state.oauth.status?.current?.id || '';
        try {
            const status = await apiSend('/oauth/logout', 'POST', { session_id: sessionID, revoke: true });
            state.oauth.status = status;
            resetOAuthResourceState({ keepStatus: true });
            renderOAuthStatus(status);
            renderOAuthAccounts();
            renderOAuthResource();
            if (status?.logged_in) await loadOAuthOverview();
            toast.ok(t('oauth_identity_removed'));
        } catch (err) {
            toast.err(err.message);
        }
    }

    async function switchOAuthSession(sessionID, button) {
        if (!sessionID || sessionID === state.oauth.status?.current?.id) return;
        setBusy(button, true, t('oauth_identity_switching'));
        try {
            const status = await apiSend('/oauth/session', 'POST', { session_id: sessionID });
            state.oauth.status = status;
            resetOAuthResourceState({ keepStatus: true });
            renderOAuthStatus(status);
            renderOAuthAccounts();
            renderOAuthResource();
            if (status?.logged_in) await loadOAuthOverview();
            toast.ok(t('oauth_identity_switched'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function renameOAuthSession(sessionID, currentLabel) {
        if (!sessionID) return;
        const fallback = currentLabel || t('oauth_account');
        const nextLabel = window.prompt(t('oauth_identity_rename_prompt'), fallback);
        if (nextLabel == null) return;
        const label = nextLabel.trim();
        if (!label || label === fallback) return;
        try {
            const status = await apiSend('/oauth/session', 'PATCH', { session_id: sessionID, label });
            state.oauth.status = status;
            renderOAuthStatus(status);
            renderOAuthIdentities(status);
            toast.ok(t('oauth_identity_renamed'));
        } catch (err) {
            toast.err(err.message);
        }
    }

    async function removeOAuthSession(sessionID, label) {
        if (!sessionID) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_identity_remove_title'),
            message: t('oauth_identity_remove_message', { label: label || t('oauth_account') }),
            okText: t('delete'),
        });
        if (!ok) return;
        const previousCurrentID = state.oauth.status?.current?.id || '';
        try {
            const status = await apiSend('/oauth/logout', 'POST', { session_id: sessionID, revoke: true });
            state.oauth.status = status;
            const nextCurrentID = status?.current?.id || '';
            if (previousCurrentID !== nextCurrentID || previousCurrentID === sessionID) {
                resetOAuthResourceState({ keepStatus: true });
            }
            renderOAuthStatus(status);
            renderOAuthAccounts();
            renderOAuthResource();
            if (status?.logged_in && previousCurrentID !== nextCurrentID) await loadOAuthOverview();
            toast.ok(t('oauth_identity_removed'));
        } catch (err) {
            toast.err(err.message);
        }
    }

    async function loadOAuthOverview() {
        if (!state.features?.oauth_enabled || !state.oauth.status?.logged_in) {
            renderOAuthAccounts();
            renderOAuthResource();
            return;
        }
        await loadOAuthAccounts();
        if (canRead('zones')) await loadOAuthZones();
        ensureVisibleResource();
        await loadOAuthCurrentResource();
        renderOAuthResource();
    }

    async function loadOAuthOverviewSummary() {
        if (!state.oauth.status?.logged_in) return;
        const params = new URLSearchParams();
        if (state.oauth.selectedAccountId) params.set('account_id', state.oauth.selectedAccountId);
        state.oauth.overviewLoading = true;
        state.oauth.overviewError = '';
        renderOAuthResource();
        try {
            state.oauth.overview = await apiGet('/cf/overview' + (params.toString() ? `?${params.toString()}` : ''));
            state.oauth.overviewError = '';
        } catch (err) {
            state.oauth.overview = null;
            state.oauth.overviewError = err.message;
        } finally {
            state.oauth.overviewLoading = false;
        }
    }

    async function loadOAuthAccounts() {
        try {
            const resp = await apiGet('/cf/accounts');
            state.oauth.accounts = Array.isArray(resp.data) ? resp.data : [];
            if (!state.oauth.selectedAccountId && state.oauth.accounts.length) {
                state.oauth.selectedAccountId = state.oauth.accounts[0].id;
            }
            renderOAuthAccounts();
        } catch (err) {
            renderOAuthError(err.message);
        }
    }

    async function loadOAuthZones() {
        try {
            const query = state.oauth.selectedAccountId ? `?account_id=${encodeURIComponent(state.oauth.selectedAccountId)}` : '';
            const resp = await apiGet('/cf/zones' + query);
            state.oauth.zones = Array.isArray(resp.data) ? resp.data : [];
            if (!state.oauth.selectedZoneId && state.oauth.zones.length) {
                state.oauth.selectedZoneId = state.oauth.zones[0].id;
            }
            if (state.oauth.selectedZoneId && !state.oauth.zones.some((zone) => zone.id === state.oauth.selectedZoneId)) {
                state.oauth.selectedZoneId = state.oauth.zones[0]?.id || '';
                resetZoneDetail();
            }
        } catch (err) {
            renderOAuthError(err.message);
        }
    }

    async function loadOAuthZoneDetail(zoneID = state.oauth.selectedZoneId) {
        if (!zoneID || !canRead('zones')) {
            resetZoneDetail();
            return;
        }
        state.oauth.zoneDetailLoading = true;
        state.oauth.zoneDetailError = '';
        if (state.oauth.zoneDetail?.zone?.id !== zoneID) state.oauth.zoneDetail = null;
        renderOAuthResource();
        try {
            state.oauth.zoneDetail = await apiGet('/cf/zones/' + encodeURIComponent(zoneID));
            state.oauth.zoneDetailError = '';
        } catch (err) {
            state.oauth.zoneDetail = null;
            state.oauth.zoneDetailError = err.message;
        } finally {
            state.oauth.zoneDetailLoading = false;
        }
        if (canRead('dns')) await loadZoneDNSCount(zoneID);
        else resetZoneDNSCount();
    }

    async function loadZoneDNSCount(zoneID = state.oauth.selectedZoneId) {
        if (!zoneID || !canRead('dns')) {
            resetZoneDNSCount();
            return;
        }
        state.oauth.zoneDNSCountZoneId = zoneID;
        state.oauth.zoneDNSCount = null;
        state.oauth.zoneDNSCountError = '';
        state.oauth.zoneDNSCountLoading = true;
        try {
            const resp = await apiGet('/cf/dns/count?zone_id=' + encodeURIComponent(zoneID));
            if (state.oauth.selectedZoneId === zoneID) {
                state.oauth.zoneDNSCount = Number.isFinite(Number(resp.count)) ? Number(resp.count) : null;
                state.oauth.zoneDNSCountError = '';
                state.oauth.dnsSession = resp.session || state.oauth.dnsSession || null;
                state.oauth.dnsCapabilities = resp.capabilities || state.oauth.dnsCapabilities || null;
            }
        } catch (err) {
            if (state.oauth.selectedZoneId === zoneID) {
                state.oauth.zoneDNSCount = null;
                state.oauth.zoneDNSCountError = err.message;
            }
        } finally {
            if (state.oauth.zoneDNSCountZoneId === zoneID) {
                state.oauth.zoneDNSCountLoading = false;
            }
        }
    }

    async function loadOAuthDNS() {
        if (!state.oauth.selectedZoneId) return;
        try {
            const resp = await apiGet('/cf/dns?zone_id=' + encodeURIComponent(state.oauth.selectedZoneId));
            state.oauth.dnsRecords = Array.isArray(resp.data) ? resp.data : [];
            state.oauth.dnsSession = resp.session || state.oauth.dnsSession || null;
            state.oauth.dnsCapabilities = resp.capabilities || state.oauth.dnsCapabilities || null;
            state.oauth.dnsRecordsError = '';
        } catch (err) {
            state.oauth.dnsRecords = [];
            state.oauth.dnsRecordsError = err.message || String(err);
        }
    }

    async function loadOAuthTunnels() {
        if (!state.oauth.selectedAccountId) return;
        try {
            const resp = await apiGet('/cf/tunnels?account_id=' + encodeURIComponent(state.oauth.selectedAccountId));
            state.oauth.tunnels = Array.isArray(resp.data) ? resp.data : [];
            state.oauth.localTunnelProfiles = Array.isArray(resp.local_profiles) ? resp.local_profiles : [];
            pruneOAuthTunnelConfigs();
        } catch (err) {
            renderOAuthError(err.message);
        }
    }

    async function createOAuthTunnel(payload, button) {
        if (!state.oauth.selectedAccountId) return;
        try {
            setBusy(button, true);
            const resp = await apiSend('/cf/tunnels?account_id=' + encodeURIComponent(state.oauth.selectedAccountId), 'POST', payload);
            state.oauth.tunnelCreateOpen = false;
            await loadOAuthTunnels();
            renderOAuthResource();
            const profile = resp.local_profile;
            if (profile?.key) {
                const activeText = profile.active ? t('oauth_tunnel_local_profile_activated') : t('oauth_tunnel_local_profile_saved');
                toast.ok(t('oauth_tunnel_create_success_with_profile', { name: resp.tunnel?.name || payload.name, profile: profile.name || profile.key, state: activeText }));
            } else {
                toast.ok(t('oauth_tunnel_create_success', { name: resp.tunnel?.name || payload.name }));
            }
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function activateOAuthLocalTunnelProfile(profile, button) {
        const key = String(profile?.key || '').trim();
        if (!key) return;
        try {
            setBusy(button, true, t('saving'));
            const resp = await apiSend('/tunnels/' + encodeURIComponent(key) + '/activate-local', 'POST');
            const activeKey = applyOAuthLocalTunnelActivation(resp, key);
            await window.cfui.fetchConfig?.();
            syncOAuthLocalTunnelActiveState(state.config?.active_tunnel_key || activeKey);
            renderOAuthResource();
            toast.ok(t('oauth_tunnel_local_profile_activated_toast', { name: profile.name || key }));
        } catch (err) {
            toast.err(t('oauth_tunnel_local_profile_activate_failed', { error: err.message }));
        } finally {
            setBusy(button, false);
        }
    }

    function applyOAuthLocalTunnelActivation(resp, fallbackKey) {
        const activeKey = String(resp?.active_tunnel_key || fallbackKey || '').trim();
        if (resp && typeof resp === 'object') {
            state.config = { ...(state.config || {}) };
            if (Array.isArray(resp.tunnels)) state.config.tunnels = resp.tunnels;
            if (activeKey) state.config.active_tunnel_key = activeKey;
            if (activeKey) state.selectedTunnelKey = activeKey;
        }
        syncOAuthLocalTunnelActiveState(activeKey);
        window.cfui.renderTunnelProfileSelector?.();
        window.cfui.updateTunnelProfileUI?.();
        return activeKey;
    }

    function syncOAuthLocalTunnelActiveState(activeKey) {
        activeKey = String(activeKey || '').trim();
        if (!activeKey) return;
        state.oauth.localTunnelProfiles = (state.oauth.localTunnelProfiles || []).map((profile) => ({
            ...profile,
            active: String(profile?.key || '').trim() === activeKey,
        }));
    }

    async function deleteOAuthTunnel(tunnel, localProfile, button) {
        const tunnelID = String(tunnel?.id || '').trim();
        if (!state.oauth.selectedAccountId || !tunnelID) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_tunnel_delete_title'),
            message: localProfile
                ? t('oauth_tunnel_delete_message_with_profile', { name: tunnel.name || tunnelID, profile: localProfile.name || localProfile.key })
                : t('oauth_tunnel_delete_message', { name: tunnel.name || tunnelID }),
            okText: t('delete'),
        });
        if (!ok) return;
        try {
            setBusy(button, true);
            const params = new URLSearchParams({
                account_id: state.oauth.selectedAccountId,
                delete_local_profile: localProfile ? 'true' : 'false',
            });
            await apiSend(`/cf/tunnels/${encodeURIComponent(tunnelID)}?${params.toString()}`, 'DELETE');
            delete state.oauth.tunnelConfigs?.[tunnelID];
            delete state.oauth.tunnelConfigLoading?.[tunnelID];
            delete state.oauth.tunnelConfigErrors?.[tunnelID];
            if (state.oauth.tunnelIngressCreateTunnelId === tunnelID) state.oauth.tunnelIngressCreateTunnelId = '';
            if (state.oauth.tunnelIngressEditing?.tunnel_id === tunnelID) state.oauth.tunnelIngressEditing = null;
            await loadOAuthTunnels();
            renderOAuthResource();
            toast.ok(localProfile
                ? t('oauth_tunnel_delete_success_with_profile', { name: tunnel.name || tunnelID })
                : t('oauth_tunnel_delete_success', { name: tunnel.name || tunnelID }));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    function ensureTunnelConfigState() {
        if (!state.oauth.tunnelConfigs || typeof state.oauth.tunnelConfigs !== 'object') state.oauth.tunnelConfigs = {};
        if (!state.oauth.tunnelConfigLoading || typeof state.oauth.tunnelConfigLoading !== 'object') state.oauth.tunnelConfigLoading = {};
        if (!state.oauth.tunnelConfigErrors || typeof state.oauth.tunnelConfigErrors !== 'object') state.oauth.tunnelConfigErrors = {};
    }

    function pruneOAuthTunnelConfigs() {
        ensureTunnelConfigState();
        const live = new Set((state.oauth.tunnels || []).map((tunnel) => String(tunnel?.id || '').trim()).filter(Boolean));
        for (const key of Object.keys(state.oauth.tunnelConfigs)) if (!live.has(key)) delete state.oauth.tunnelConfigs[key];
        for (const key of Object.keys(state.oauth.tunnelConfigLoading)) if (!live.has(key)) delete state.oauth.tunnelConfigLoading[key];
        for (const key of Object.keys(state.oauth.tunnelConfigErrors)) if (!live.has(key)) delete state.oauth.tunnelConfigErrors[key];
    }

    async function loadOAuthTunnelConfig(tunnelID, button, silent = false) {
        tunnelID = String(tunnelID || '').trim();
        if (!state.oauth.selectedAccountId || !tunnelID) return null;
        ensureTunnelConfigState();
        state.oauth.tunnelConfigLoading[tunnelID] = true;
        state.oauth.tunnelConfigErrors[tunnelID] = '';
        if (!silent) renderOAuthResource();
        if (button) setBusy(button, true);
        try {
            const params = new URLSearchParams({ account_id: state.oauth.selectedAccountId });
            const config = await apiGet(`/cf/tunnels/${encodeURIComponent(tunnelID)}/config?${params.toString()}`);
            state.oauth.tunnelConfigs[tunnelID] = config;
            state.oauth.tunnelConfigErrors[tunnelID] = '';
            return config;
        } catch (err) {
            state.oauth.tunnelConfigErrors[tunnelID] = err.message;
            if (!silent) toast.err(t('oauth_tunnel_ingress_load_failed') + ': ' + err.message);
            return null;
        } finally {
            state.oauth.tunnelConfigLoading[tunnelID] = false;
            if (button) setBusy(button, false);
            renderOAuthResource();
        }
    }

    async function submitOAuthTunnelIngress(tunnelID, index, payload, button) {
        tunnelID = String(tunnelID || '').trim();
        if (!state.oauth.selectedAccountId || !tunnelID) return;
        if (!String(payload?.service || '').trim()) {
            toast.err(t('service_required'));
            return;
        }
        try {
            setBusy(button, true);
            const params = new URLSearchParams({ account_id: state.oauth.selectedAccountId });
            const editing = Number.isInteger(index);
            const path = editing
                ? `/cf/tunnels/${encodeURIComponent(tunnelID)}/config/entries/${index}?${params.toString()}`
                : `/cf/tunnels/${encodeURIComponent(tunnelID)}/config/entries?${params.toString()}`;
            const config = await apiSend(path, editing ? 'PUT' : 'POST', payload);
            ensureTunnelConfigState();
            state.oauth.tunnelConfigs[tunnelID] = config;
            state.oauth.tunnelConfigErrors[tunnelID] = '';
            state.oauth.tunnelIngressCreateTunnelId = '';
            state.oauth.tunnelIngressEditing = null;
            renderOAuthResource();
            toast.ok(tunnelIngressMutationMessage(tunnelID, 'oauth_tunnel_ingress_saved'));
        } catch (err) {
            toast.err(t('oauth_tunnel_ingress_save_failed') + ': ' + err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function deleteOAuthTunnelIngress(tunnelID, entry, button) {
        tunnelID = String(tunnelID || '').trim();
        const index = Number(entry?.index);
        if (!state.oauth.selectedAccountId || !tunnelID || !Number.isInteger(index)) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_tunnel_ingress_delete_title'),
            message: t('oauth_tunnel_ingress_delete_message', { hostname: entry.hostname || t('catch_all_rule'), path: entry.path || '/' }),
            okText: t('delete'),
        });
        if (!ok) return;
        try {
            setBusy(button, true);
            const params = new URLSearchParams({ account_id: state.oauth.selectedAccountId });
            const config = await apiSend(`/cf/tunnels/${encodeURIComponent(tunnelID)}/config/entries/${index}?${params.toString()}`, 'DELETE');
            ensureTunnelConfigState();
            state.oauth.tunnelConfigs[tunnelID] = config;
            state.oauth.tunnelConfigErrors[tunnelID] = '';
            if (state.oauth.tunnelIngressEditing?.tunnel_id === tunnelID && state.oauth.tunnelIngressEditing.index === index) {
                state.oauth.tunnelIngressEditing = null;
            }
            renderOAuthResource();
            toast.ok(tunnelIngressMutationMessage(tunnelID, 'oauth_tunnel_ingress_deleted'));
        } catch (err) {
            toast.err(t('oauth_tunnel_ingress_delete_failed') + ': ' + err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function reorderOAuthTunnelIngress(tunnelID, order) {
        tunnelID = String(tunnelID || '').trim();
        if (!state.oauth.selectedAccountId || !tunnelID || !Array.isArray(order) || !order.length) return;
        try {
            const params = new URLSearchParams({ account_id: state.oauth.selectedAccountId });
            const config = await apiSend(`/cf/tunnels/${encodeURIComponent(tunnelID)}/config/entries/reorder?${params.toString()}`, 'POST', { order });
            ensureTunnelConfigState();
            state.oauth.tunnelConfigs[tunnelID] = config;
            state.oauth.tunnelConfigErrors[tunnelID] = '';
            renderOAuthResource();
            toast.ok(tunnelIngressMutationMessage(tunnelID, 'oauth_tunnel_ingress_reordered'));
        } catch (err) {
            toast.err(t('oauth_tunnel_ingress_reorder_failed') + ': ' + err.message);
            renderOAuthResource();
        }
    }

    async function moveOAuthTunnelIngress(tunnelID, index, delta) {
        const config = state.oauth.tunnelConfigs?.[tunnelID];
        const entries = config?.entries || [];
        const movable = entries.filter((entry) => !isOAuthTunnelCatchAllRule(entry, entries));
        const from = movable.findIndex((entry) => entry.index === index);
        if (from < 0) return;
        const to = from + delta;
        if (to < 0 || to >= movable.length) return;
        const next = movable.slice();
        const [entry] = next.splice(from, 1);
        next.splice(to, 0, entry);
        const catchAll = entries.find((entry) => isOAuthTunnelCatchAllRule(entry, entries));
        const order = next.map((entry) => entry.index);
        if (catchAll) order.push(catchAll.index);
        await reorderOAuthTunnelIngress(tunnelID, order);
    }

    async function loadOAuthWorkers() {
        if (!state.oauth.selectedAccountId) return;
        try {
            const resp = await apiGet('/cf/workers?account_id=' + encodeURIComponent(state.oauth.selectedAccountId));
            state.oauth.workers = Array.isArray(resp.data) ? resp.data : [];
            if (state.oauth.selectedWorkerId && !state.oauth.workers.some((worker) => worker.id === state.oauth.selectedWorkerId)) {
                resetWorkerDetail();
            }
        } catch (err) {
            renderOAuthError(err.message);
        }
    }

    async function loadWorkerDetail(scriptName = state.oauth.selectedWorkerId) {
        if (!state.oauth.selectedAccountId || !scriptName) return;
        try {
            state.oauth.workerMetrics = null;
            state.oauth.workerMetricsError = '';
            state.oauth.workerDetail = await apiGet(`/cf/workers/${encodeURIComponent(scriptName)}?account_id=${encodeURIComponent(state.oauth.selectedAccountId)}`);
            state.oauth.selectedWorkerId = state.oauth.workerDetail?.worker?.id || scriptName;
            if (canRead('analytics')) await loadWorkerMetrics(state.oauth.selectedWorkerId);
        } catch (err) {
            toast.err(err.message);
        }
    }

    async function loadWorkerMetrics(scriptName = state.oauth.selectedWorkerId) {
        if (!state.oauth.selectedAccountId || !scriptName || !canRead('analytics')) return;
        state.oauth.workerMetricsLoading = true;
        try {
            const params = new URLSearchParams({
                account_id: state.oauth.selectedAccountId,
                range: state.oauth.workerMetricsRange || '24h',
            });
            state.oauth.workerMetrics = await apiGet(`/cf/workers/${encodeURIComponent(scriptName)}/metrics?${params.toString()}`);
            state.oauth.workerMetricsError = '';
        } catch (err) {
            state.oauth.workerMetrics = null;
            state.oauth.workerMetricsError = err.message;
        } finally {
            state.oauth.workerMetricsLoading = false;
        }
    }

    async function loadOAuthAccountUsage() {
        if (!state.oauth.selectedAccountId) {
            resetUsageDetail();
            return;
        }
        if (!canRead('analytics')) return;
        state.oauth.accountUsageLoading = true;
        state.oauth.accountUsageError = '';
        state.oauth.accountUsage = null;
        renderOAuthResource();
        try {
            const params = new URLSearchParams({ account_id: state.oauth.selectedAccountId });
            state.oauth.accountUsage = await apiGet('/cf/usage/account?' + params.toString());
        } catch (err) {
            state.oauth.accountUsageError = err.message;
        } finally {
            state.oauth.accountUsageLoading = false;
        }
    }

    async function loadCloudflareStatus() {
        state.oauth.cloudflareStatusLoading = true;
        state.oauth.cloudflareStatusError = '';
        renderOAuthResource();
        try {
            state.oauth.cloudflareStatus = await apiGet('/cf/status');
        } catch (err) {
            state.oauth.cloudflareStatus = null;
            state.oauth.cloudflareStatusError = err.message;
        } finally {
            state.oauth.cloudflareStatusLoading = false;
        }
    }

    function startWorkerTail(button) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedWorkerId || state.oauth.workerTailConnecting || state.oauth.workerTailConnected) return;
        closeWorkerTailStream();
        state.oauth.workerTailConnecting = true;
        state.oauth.workerTailLines = [];
        appendWorkerTailLine('info', t('oauth_worker_tail_connecting'));
        renderOAuthResource();

        const params = new URLSearchParams({ account_id: state.oauth.selectedAccountId });
        const source = new EventSource(`${API_BASE}/cf/workers/${encodeURIComponent(state.oauth.selectedWorkerId)}/tail?${params.toString()}`);
        state.oauth.workerTailSource = source;
        setBusy(button, true, t('oauth_worker_tail_connecting'));

        source.addEventListener('tail_open', (event) => {
            if (state.oauth.workerTailSource !== source) return;
            state.oauth.workerTailConnecting = false;
            state.oauth.workerTailConnected = true;
            const payload = parseSSEPayload(event.data);
            appendWorkerTailLine('info', t('oauth_worker_tail_connected', { id: payload.id || '' }));
            renderOAuthResource();
        });
        source.addEventListener('tail_message', (event) => {
            if (state.oauth.workerTailSource !== source || state.oauth.workerTailPaused) return;
            const payload = parseSSEPayload(event.data);
            appendWorkerTailData(payload.data || '');
            renderOAuthResource();
        });
        source.addEventListener('tail_dropped', (event) => {
            if (state.oauth.workerTailSource !== source) return;
            const payload = parseSSEPayload(event.data);
            appendWorkerTailLine('warn', t('oauth_worker_tail_dropped', { count: payload.count || 0 }));
            renderOAuthResource();
        });
        source.addEventListener('tail_error', (event) => {
            if (state.oauth.workerTailSource !== source) return;
            const payload = parseSSEPayload(event.data);
            appendWorkerTailLine('error', payload.message || t('oauth_worker_tail_failed'));
            closeWorkerTailStream();
            renderOAuthResource();
        });
        source.onerror = () => {
            if (state.oauth.workerTailSource !== source) return;
            appendWorkerTailLine('error', t('oauth_worker_tail_disconnected'));
            closeWorkerTailStream();
            renderOAuthResource();
        };
    }

    function stopWorkerTail() {
        closeWorkerTailStream();
        appendWorkerTailLine('info', t('oauth_worker_tail_stopped'));
        renderOAuthResource();
    }

    async function loadOAuthStorage() {
        if (!state.oauth.selectedAccountId) return;
        const account = encodeURIComponent(state.oauth.selectedAccountId);
        const loads = [];
        if (canRead('r2')) {
            loads.push(apiGet('/cf/r2/buckets?account_id=' + account).then((resp) => {
                state.oauth.r2Buckets = Array.isArray(resp.data) ? resp.data : [];
                state.oauth.r2Session = resp.session || state.oauth.r2Session;
                state.oauth.r2Capabilities = resp.capabilities || state.oauth.r2Capabilities;
            }));
            loads.push(loadR2Metrics());
        }
        if (canRead('d1')) loads.push(loadD1Databases());
        if (canRead('kv')) loads.push(loadKVNamespaces());
        try {
            await Promise.all(loads);
        } catch (err) {
            renderOAuthError(err.message);
        }
    }

    async function loadD1Databases() {
        if (!state.oauth.selectedAccountId || !canRead('d1')) return;
        const account = encodeURIComponent(state.oauth.selectedAccountId);
        let databases = [];
        try {
            const resp = await apiGet('/cf/d1/databases?account_id=' + account);
            databases = Array.isArray(resp.data) ? resp.data : [];
            state.oauth.d1Databases = databases;
            state.oauth.d1Session = resp.session || state.oauth.d1Session || null;
            state.oauth.d1Capabilities = resp.capabilities || state.oauth.d1Capabilities || null;
            state.oauth.d1DatabasesError = '';
        } catch (err) {
            state.oauth.d1Databases = [];
            state.oauth.d1DatabasesError = err.message || String(err);
            return;
        }
        if (!databases.length) {
            state.oauth.d1DetailsError = '';
            return;
        }

        const detailResults = await Promise.allSettled(databases
            .filter((database) => database.uuid)
            .map((database) => apiGet(`/cf/d1/databases/${encodeURIComponent(database.uuid)}?account_id=${account}`)));
        const details = new Map();
        const detailErrors = [];
        for (const result of detailResults) {
            if (result.status !== 'fulfilled') {
                detailErrors.push(result.reason?.message || String(result.reason || ''));
                continue;
            }
            state.oauth.d1Session = result.value?.session || state.oauth.d1Session || null;
            state.oauth.d1Capabilities = result.value?.capabilities || state.oauth.d1Capabilities || null;
            const database = result.value?.database;
            if (database?.uuid) details.set(database.uuid, database);
        }
        state.oauth.d1DetailsError = detailErrors.filter(Boolean).join('; ');
        if (details.size) {
            state.oauth.d1Databases = databases.map((database) => details.get(database.uuid) || database);
        }
    }

    async function loadKVNamespaces() {
        if (!state.oauth.selectedAccountId || !canRead('kv')) return;
        const account = encodeURIComponent(state.oauth.selectedAccountId);
        const resp = await apiGet('/cf/kv/namespaces?account_id=' + account);
        state.oauth.kvNamespaces = Array.isArray(resp.data) ? resp.data : [];
        state.oauth.kvSession = resp.session || state.oauth.kvSession || null;
        state.oauth.kvCapabilities = resp.capabilities || state.oauth.kvCapabilities || null;
    }

    async function createD1Database(payload, button) {
        if (!state.oauth.selectedAccountId || !canWrite('d1')) return;
        try {
            setBusy(button, true, t('creating'));
            const account = encodeURIComponent(state.oauth.selectedAccountId);
            const resp = await apiSend('/cf/d1/databases?account_id=' + account, 'POST', payload);
            state.oauth.d1Session = resp.session || state.oauth.d1Session || null;
            state.oauth.d1Capabilities = resp.capabilities || state.oauth.d1Capabilities || null;
            state.oauth.d1CreateOpen = false;
            await loadD1Databases();
            if (resp?.database?.uuid) {
                const created = resp.database;
                state.oauth.selectedD1DatabaseId = created.uuid;
                state.oauth.storageView = 'd1';
                state.oauth.selectedD1TableName = '';
                state.oauth.d1Tables = [];
                state.oauth.d1TablesDatabaseId = '';
                state.oauth.d1TablesError = '';
                state.oauth.d1TableColumns = [];
                state.oauth.d1TableRows = [];
                state.oauth.d1TableRowsError = '';
                state.oauth.d1EditingRow = null;
                state.oauth.d1Results = [];
                state.oauth.d1QueryError = '';
                await loadD1Tables(created.uuid);
            }
            renderOAuthResource();
            toast.ok(t('oauth_d1_database_created'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function deleteD1Database(database, button) {
        const databaseID = String(database?.uuid || '').trim();
        if (!state.oauth.selectedAccountId || !databaseID || !canWrite('d1')) return;
        const name = database.name || databaseID;
        const ok = await window.cfui.confirm({
            title: t('oauth_d1_delete_database_title'),
            message: t('oauth_d1_delete_database_message', { name }),
            okText: t('delete'),
        });
        if (!ok) return;
        try {
            setBusy(button, true, t('delete'));
            const account = encodeURIComponent(state.oauth.selectedAccountId);
            const resp = await apiSend('/cf/d1/databases/' + encodeURIComponent(databaseID) + '?account_id=' + account, 'DELETE');
            state.oauth.d1Session = resp.session || state.oauth.d1Session || null;
            state.oauth.d1Capabilities = resp.capabilities || state.oauth.d1Capabilities || null;
            if (state.oauth.selectedD1DatabaseId === databaseID) resetD1DetailSelection();
            await loadD1Databases();
            renderOAuthResource();
            toast.ok(t('oauth_d1_database_deleted'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    function resetD1DetailSelection() {
        state.oauth.storageView = '';
        state.oauth.selectedD1DatabaseId = '';
        state.oauth.selectedD1TableName = '';
        state.oauth.d1Tables = [];
        state.oauth.d1TablesDatabaseId = '';
        state.oauth.d1TablesError = '';
        state.oauth.d1TableColumns = [];
        state.oauth.d1TableRows = [];
        state.oauth.d1TableRowsError = '';
        state.oauth.d1TableOffset = 0;
        state.oauth.d1TableHasMore = false;
        state.oauth.d1EditingRow = null;
        state.oauth.d1Results = [];
        state.oauth.d1QueryError = '';
    }

    async function loadR2Metrics() {
        if (!state.oauth.selectedAccountId || !canRead('r2')) return;
        state.oauth.r2Metrics = null;
        state.oauth.r2MetricsError = '';
        state.oauth.r2MetricsLoading = true;
        try {
            state.oauth.r2Metrics = await apiGet('/cf/r2/metrics?account_id=' + encodeURIComponent(state.oauth.selectedAccountId));
            state.oauth.r2Session = state.oauth.r2Metrics?.session || state.oauth.r2Session;
            state.oauth.r2Capabilities = state.oauth.r2Metrics?.capabilities || state.oauth.r2Capabilities;
        } catch (err) {
            state.oauth.r2MetricsError = err.message;
        } finally {
            state.oauth.r2MetricsLoading = false;
        }
    }

    async function createR2Bucket(payload, button) {
        if (!state.oauth.selectedAccountId) return;
        setBusy(button, true, t('saving'));
        try {
            await apiSend('/cf/r2/buckets?account_id=' + encodeURIComponent(state.oauth.selectedAccountId), 'POST', payload);
            state.oauth.r2CreateOpen = false;
            await loadOAuthStorage();
            renderOAuthResource();
            toast.ok(t('oauth_r2_created'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function deleteR2Bucket(bucketName) {
        if (!state.oauth.selectedAccountId || !bucketName) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_r2_delete_title'),
            message: t('oauth_r2_delete_message', { name: bucketName }),
            okText: t('delete'),
        });
        if (!ok) return;
        try {
            await apiSend(`/cf/r2/buckets/${encodeURIComponent(bucketName)}?account_id=${encodeURIComponent(state.oauth.selectedAccountId)}`, 'DELETE');
            await loadOAuthStorage();
            renderOAuthResource();
            toast.ok(t('oauth_r2_deleted'));
        } catch (err) {
            toast.err(err.message);
        }
    }

    async function loadR2Objects(bucketName = state.oauth.selectedR2BucketName, append = false) {
        if (!state.oauth.selectedAccountId || !bucketName) return;
        if (!append) state.oauth.r2ObjectsError = '';
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            bucket: bucketName,
            limit: '100',
        });
        if (append && state.oauth.r2Cursor) params.set('cursor', state.oauth.r2Cursor);
        try {
            const resp = await apiGet('/cf/r2/objects?' + params.toString());
            const objects = Array.isArray(resp.data) ? resp.data : [];
            state.oauth.r2Objects = append ? state.oauth.r2Objects.concat(objects) : objects;
            state.oauth.r2Cursor = resp.cursor || '';
            state.oauth.r2ObjectsError = '';
            state.oauth.r2Session = resp.session || state.oauth.r2Session;
            state.oauth.r2Capabilities = resp.capabilities || state.oauth.r2Capabilities;
        } catch (err) {
            state.oauth.r2ObjectsError = err.message;
            toast.err(err.message);
        }
    }

    async function loadR2ObjectValue(key) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedR2BucketName || !key) return;
        state.oauth.r2ObjectValueError = '';
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            bucket: state.oauth.selectedR2BucketName,
            key,
        });
        try {
            state.oauth.r2ObjectValue = await apiGet('/cf/r2/object?' + params.toString());
            state.oauth.r2ObjectValueError = '';
        } catch (err) {
            state.oauth.r2ObjectValueError = err.message;
            toast.err(err.message);
        }
    }

    async function saveR2Object(key, value, contentType, button) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedR2BucketName || !key) return;
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            bucket: state.oauth.selectedR2BucketName,
            key,
        });
        setBusy(button, true, t('saving'));
        try {
            state.oauth.r2ObjectValue = await apiSend('/cf/r2/object?' + params.toString(), 'PUT', {
                value,
                content_type: contentType || 'text/plain; charset=utf-8',
            });
            state.oauth.selectedR2ObjectKey = key;
            state.oauth.r2ObjectCreateOpen = false;
            await loadR2Objects(state.oauth.selectedR2BucketName);
            renderOAuthResource();
            toast.ok(t('oauth_r2_object_saved'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function uploadR2ObjectFile(key, file, contentType, button) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedR2BucketName) return;
        if (!file) {
            toast.err(t('oauth_r2_object_file_required'));
            return;
        }
        key = (key || file.name || '').trim();
        if (!key) {
            toast.err(t('oauth_r2_object_key_required'));
            return;
        }
        if (file.size > maxR2ChunkedUploadBytes) {
            toast.err(t('oauth_r2_object_upload_too_large', {
                size: formatBytes(file.size),
                max: formatBytes(maxR2ChunkedUploadBytes),
            }));
            return;
        }
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            bucket: state.oauth.selectedR2BucketName,
            key,
        });
        const chunked = file.size > maxR2ObjectUploadBytes;
        setBusy(button, true, t(chunked ? 'oauth_r2_object_uploading_chunked' : 'upload'));
        try {
            if (chunked) {
                state.oauth.r2ObjectValue = await uploadR2ObjectFileChunked(key, file, contentType || file.type || 'application/octet-stream');
            } else {
                setR2UploadProgress({
                    fileName: file.name || key,
                    uploaded: 0,
                    total: file.size,
                    chunkIndex: 0,
                    totalChunks: 1,
                    mode: t('oauth_r2_object_upload_mode_direct'),
                });
                const res = await fetch(`${API_BASE}/cf/r2/object/upload?${params.toString()}`, {
                    method: 'POST',
                    headers: { 'Content-Type': contentType || file.type || 'application/octet-stream' },
                    body: file,
                });
                if (!res.ok) throw new Error(await rawAPIError(res));
                state.oauth.r2ObjectValue = await res.json();
            }
            state.oauth.selectedR2ObjectKey = key;
            state.oauth.r2ObjectCreateOpen = false;
            await loadR2Objects(state.oauth.selectedR2BucketName);
            state.oauth.r2UploadProgress = null;
            renderOAuthResource();
            toast.ok(t('oauth_r2_object_uploaded'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            state.oauth.r2UploadProgress = null;
            updateR2UploadProgressNode();
            setBusy(button, false);
        }
    }

    async function uploadR2ObjectFileChunked(key, file, contentType) {
        const start = await apiSend('/cf/r2/object/upload-session', 'POST', {
            account_id: state.oauth.selectedAccountId,
            bucket: state.oauth.selectedR2BucketName,
            key,
            content_type: contentType || 'application/octet-stream',
            size: file.size,
            chunk_size: r2ObjectUploadChunkBytes,
        });
        const uploadID = start.upload_id;
        if (!uploadID) throw new Error(t('oauth_r2_object_upload_session_missing'));
        const totalChunks = start.total_chunks || Math.max(1, Math.ceil(file.size / r2ObjectUploadChunkBytes));
        let uploaded = 0;
        setR2UploadProgress({
            fileName: file.name || key,
            uploaded,
            total: file.size,
            chunkIndex: 0,
            totalChunks,
            mode: t('oauth_r2_object_upload_mode_chunked'),
        });
        try {
            for (let index = 0; index < totalChunks; index += 1) {
                const offset = index * r2ObjectUploadChunkBytes;
                const chunk = file.slice(offset, Math.min(file.size, offset + r2ObjectUploadChunkBytes));
                const status = await uploadR2ChunkWithRetry(uploadID, index, chunk);
                uploaded = status.received_bytes ?? Math.min(file.size, uploaded + chunk.size);
                setR2UploadProgress({
                    fileName: file.name || key,
                    uploaded,
                    total: file.size,
                    chunkIndex: index + 1,
                    totalChunks,
                    mode: t('oauth_r2_object_upload_mode_chunked'),
                });
            }
            const complete = await fetch(`${API_BASE}/cf/r2/object/upload-session/${encodeURIComponent(uploadID)}/complete`, { method: 'POST' });
            if (!complete.ok) throw new Error(await rawAPIError(complete));
            return await complete.json();
        } catch (err) {
            fetch(`${API_BASE}/cf/r2/object/upload-session/${encodeURIComponent(uploadID)}`, { method: 'DELETE' }).catch(() => {});
            throw err;
        }
    }

    async function uploadR2ChunkWithRetry(uploadID, index, chunk) {
        let lastError = null;
        for (let attempt = 0; attempt < 3; attempt += 1) {
            try {
                const res = await fetch(`${API_BASE}/cf/r2/object/upload-session/${encodeURIComponent(uploadID)}/chunks/${index}`, {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/octet-stream' },
                    body: chunk,
                });
                if (!res.ok) throw new Error(await rawAPIError(res));
                return await res.json();
            } catch (err) {
                lastError = err;
                if (attempt < 2) await sleep(400 * (attempt + 1));
            }
        }
        throw lastError || new Error(t('oauth_r2_object_upload_failed'));
    }

    function setR2UploadProgress(progress) {
        state.oauth.r2UploadProgress = progress;
        updateR2UploadProgressNode();
    }

    function updateR2UploadProgressNode() {
        const node = $('oauth-r2-upload-progress');
        if (!node) return;
        const progress = state.oauth.r2UploadProgress;
        node.hidden = !progress;
        if (!progress) return;
        const title = node.querySelector('[data-role="title"]');
        const percent = node.querySelector('[data-role="percent"]');
        const bar = node.querySelector('[data-role="bar"]');
        const bytes = node.querySelector('[data-role="bytes"]');
        const ratio = progress.total > 0 ? Math.min(1, progress.uploaded / progress.total) : 1;
        const percentText = `${Math.round(ratio * 100)}%`;
        if (title) title.textContent = t('oauth_r2_object_upload_progress_title', { name: progress.fileName || progress.mode || '' });
        if (percent) percent.textContent = percentText;
        if (bar) bar.style.width = percentText;
        if (bytes) {
            bytes.textContent = [
                progress.mode || '',
                t('oauth_r2_object_upload_progress', {
                    uploaded: formatBytes(progress.uploaded || 0),
                    total: formatBytes(progress.total || 0),
                }),
                progress.totalChunks ? `${progress.chunkIndex || 0}/${progress.totalChunks}` : '',
            ].filter(Boolean).join(' · ');
        }
    }

    function r2ObjectDownloadURL(key = state.oauth.selectedR2ObjectKey, preview = false) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedR2BucketName || !key) return '';
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            bucket: state.oauth.selectedR2BucketName,
            key,
        });
        if (preview) params.set('preview', '1');
        return `${API_BASE}/cf/r2/object/download?${params.toString()}`;
    }

    function downloadR2Object(key = state.oauth.selectedR2ObjectKey) {
        const url = r2ObjectDownloadURL(key);
        if (!url) return;
        const link = document.createElement('a');
        link.href = url;
        link.download = key.split('/').filter(Boolean).pop() || 'download';
        document.body.appendChild(link);
        link.click();
        link.remove();
    }

    function kvValueDownloadURL(key = state.oauth.selectedKVKey) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedKVNamespaceId || !key) return '';
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            namespace_id: state.oauth.selectedKVNamespaceId,
            key,
        });
        return `${API_BASE}/cf/kv/value/download?${params.toString()}`;
    }

    function downloadKVValue(key = state.oauth.selectedKVKey) {
        const url = kvValueDownloadURL(key);
        if (!url) return;
        const link = document.createElement('a');
        link.href = url;
        link.download = key.split('/').filter(Boolean).pop() || 'kv-value.bin';
        document.body.appendChild(link);
        link.click();
        link.remove();
    }

    function suggestedR2CopyKey(key) {
        const value = String(key || '').trim();
        if (!value) return '';
        const slash = value.lastIndexOf('/');
        if (slash < 0) return `copy-${value}`;
        return `${value.slice(0, slash + 1)}copy-${value.slice(slash + 1)}`;
    }

    async function copyOrMoveR2Object(key, move = false, button = null) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedR2BucketName || !key) return;
        let destinationBucket = state.oauth.selectedR2BucketName;
        if (state.oauth.r2Buckets.length > 1) {
            const bucketTarget = window.prompt(t(move ? 'oauth_r2_object_move_bucket_prompt' : 'oauth_r2_object_copy_bucket_prompt'), destinationBucket);
            if (bucketTarget == null) return;
            destinationBucket = bucketTarget.trim();
            if (!destinationBucket) {
                toast.err(t('oauth_r2_bucket_required'));
                return;
            }
        }
        const fallback = move ? key : suggestedR2CopyKey(key);
        const target = window.prompt(t(move ? 'oauth_r2_object_move_prompt' : 'oauth_r2_object_copy_prompt'), fallback || key);
        if (target == null) return;
        const destinationKey = target.trim();
        if (!destinationKey) {
            toast.err(t('oauth_r2_object_key_required'));
            return;
        }
        if (destinationBucket === state.oauth.selectedR2BucketName && destinationKey === key) {
            toast.err(t('oauth_r2_object_destination_diff'));
            return;
        }
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            bucket: state.oauth.selectedR2BucketName,
        });
        setBusy(button, true, t(move ? 'oauth_r2_object_moving' : 'oauth_r2_object_copying'));
        try {
            await apiSend('/cf/r2/object/copy?' + params.toString(), 'POST', {
                source_key: key,
                destination_bucket: destinationBucket,
                destination_key: destinationKey,
                delete_source: move,
            });
            state.oauth.selectedR2BucketName = destinationBucket;
            state.oauth.selectedR2ObjectKey = destinationKey;
            state.oauth.r2ObjectCreateOpen = false;
            state.oauth.r2ObjectValue = null;
            await loadR2Objects(destinationBucket);
            await loadR2ObjectValue(destinationKey);
            renderOAuthResource();
            toast.ok(t(move ? 'oauth_r2_object_moved' : 'oauth_r2_object_copied'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    function r2ObjectPreviewKind(object) {
        const contentType = String(object?.content_type || '').split(';')[0].trim().toLowerCase();
        if (['image/png', 'image/jpeg', 'image/gif', 'image/webp', 'image/avif'].includes(contentType)) return 'image';
        if (['audio/aac', 'audio/flac', 'audio/mpeg', 'audio/mp4', 'audio/ogg', 'audio/wav', 'audio/webm'].includes(contentType)) return 'audio';
        if (['video/mp4', 'video/ogg', 'video/quicktime', 'video/webm'].includes(contentType)) return 'video';
        if (contentType === 'application/pdf') return 'pdf';
        return '';
    }

    function r2ObjectPreviewNode(object) {
        const kind = r2ObjectPreviewKind(object);
        if (!kind) return null;
        if (r2ObjectPreviewTooLarge(object)) return null;
        const key = object.key || state.oauth.selectedR2ObjectKey;
        const url = r2ObjectDownloadURL(key, true);
        if (!url) return null;

        const wrapper = document.createElement('div');
        wrapper.className = 'oauth-media-preview';
        const label = document.createElement('div');
        label.className = 'oauth-media-label';
        label.textContent = t('oauth_r2_object_preview');
        wrapper.appendChild(label);

        let media;
        if (kind === 'image') {
            media = document.createElement('img');
            media.alt = key;
            media.loading = 'lazy';
            media.decoding = 'async';
        } else if (kind === 'audio') {
            media = document.createElement('audio');
            media.controls = true;
            media.preload = 'metadata';
        } else if (kind === 'video') {
            media = document.createElement('video');
            media.controls = true;
            media.preload = 'metadata';
            media.playsInline = true;
        } else {
            media = document.createElement('iframe');
            media.title = key;
            media.loading = 'lazy';
        }
        media.className = `oauth-media-frame oauth-media-frame--${kind}`;
        media.src = url;

        const status = document.createElement('div');
        status.className = 'oauth-row-meta';
        status.textContent = object.content_type || '';
        media.addEventListener('error', () => {
            status.textContent = t('oauth_r2_object_preview_failed');
        });
        wrapper.append(media, status);
        return wrapper;
    }

    function binaryPreviewNode(object, titleText, metaText) {
        const preview = object?.binary_preview;
        if (!preview?.hexdump) return null;
        const wrapper = document.createElement('div');
        wrapper.className = 'oauth-binary-preview';

        const label = document.createElement('div');
        label.className = 'oauth-media-label';
        label.textContent = titleText;

        const meta = document.createElement('div');
        meta.className = 'oauth-row-meta';
        meta.textContent = [
            metaText,
            preview.truncated ? t('oauth_r2_object_truncated') : '',
        ].filter(Boolean).join(' · ');

        const dump = document.createElement('pre');
        dump.className = 'oauth-binary-hexdump';
        dump.textContent = preview.hexdump;

        wrapper.append(label, meta, dump);
        return wrapper;
    }

    function r2BinaryPreviewNode(object) {
        return binaryPreviewNode(
            object,
            t('oauth_r2_object_binary_preview'),
            t('oauth_r2_object_binary_preview_meta', { bytes: formatBytes(object?.binary_preview?.bytes || 0) }),
        );
    }

    function kvBinaryPreviewNode(value) {
        return binaryPreviewNode(
            value,
            t('oauth_kv_value_binary_preview'),
            t('oauth_kv_value_binary_preview_meta', { bytes: formatBytes(value?.binary_preview?.bytes || 0) }),
        );
    }

    async function deleteR2Object(key) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedR2BucketName || !key) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_r2_object_delete_title'),
            message: t('oauth_r2_object_delete_message', { key }),
            okText: t('delete'),
        });
        if (!ok) return;
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            bucket: state.oauth.selectedR2BucketName,
            key,
        });
        try {
            await apiSend('/cf/r2/object?' + params.toString(), 'DELETE');
            if (state.oauth.selectedR2ObjectKey === key) {
                state.oauth.selectedR2ObjectKey = '';
                state.oauth.r2ObjectValue = null;
            }
            await loadR2Objects(state.oauth.selectedR2BucketName);
            renderOAuthResource();
            toast.ok(t('oauth_r2_object_deleted'));
        } catch (err) {
            toast.err(err.message);
        }
    }

    async function loadKVKeys(namespaceId, append = false) {
        if (!state.oauth.selectedAccountId || !namespaceId) return;
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            namespace_id: namespaceId,
            limit: '100',
        });
        if (append && state.oauth.kvCursor) params.set('cursor', state.oauth.kvCursor);
        try {
            const resp = await apiGet('/cf/kv/keys?' + params.toString());
            const keys = Array.isArray(resp.data) ? resp.data : [];
            state.oauth.kvKeys = append ? state.oauth.kvKeys.concat(keys) : keys;
            state.oauth.kvCursor = resp.cursor || '';
            state.oauth.kvSession = resp.session || state.oauth.kvSession || null;
            state.oauth.kvCapabilities = resp.capabilities || state.oauth.kvCapabilities || null;
            state.oauth.kvKeysError = '';
            pruneKVSelectedKeys();
        } catch (err) {
            state.oauth.kvKeysError = err.message || String(err);
            toast.err(err.message);
        }
    }

    async function loadKVValue(key) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedKVNamespaceId || !key) return;
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            namespace_id: state.oauth.selectedKVNamespaceId,
            key,
        });
        try {
            state.oauth.kvValue = await apiGet('/cf/kv/value?' + params.toString());
            state.oauth.kvValueError = '';
        } catch (err) {
            state.oauth.kvValueError = err.message || String(err);
            toast.err(err.message);
        }
    }

    async function saveKVValue(key, value, button) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedKVNamespaceId || !key) return;
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            namespace_id: state.oauth.selectedKVNamespaceId,
            key,
        });
        setBusy(button, true, t('saving'));
        try {
            state.oauth.kvValue = await apiSend('/cf/kv/value?' + params.toString(), 'PUT', { value });
            state.oauth.selectedKVKey = key;
            state.oauth.kvValueError = '';
            state.oauth.kvCreateOpen = false;
            await loadKVKeys(state.oauth.selectedKVNamespaceId);
            renderOAuthResource();
            toast.ok(t('oauth_kv_saved'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function uploadKVValueFile(key, file, button) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedKVNamespaceId) return;
        if (!file) {
            toast.err(t('oauth_kv_file_required'));
            return;
        }
        key = (key || file.name || '').trim();
        if (!key) {
            toast.err(t('oauth_kv_key_required'));
            return;
        }
        if (file.size > maxKVValueUploadBytes) {
            toast.err(t('oauth_kv_upload_too_large', {
                size: formatBytes(file.size),
                max: formatBytes(maxKVValueUploadBytes),
            }));
            return;
        }
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            namespace_id: state.oauth.selectedKVNamespaceId,
            key,
        });
        setBusy(button, true, t('upload'));
        try {
            const res = await fetch(`${API_BASE}/cf/kv/value/upload?${params.toString()}`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/octet-stream' },
                body: file,
            });
            if (!res.ok) throw new Error(await rawAPIError(res));
            state.oauth.kvValue = await res.json();
            state.oauth.selectedKVKey = key;
            state.oauth.kvValueError = '';
            state.oauth.kvCreateOpen = false;
            await loadKVKeys(state.oauth.selectedKVNamespaceId);
            renderOAuthResource();
            toast.ok(t('oauth_kv_uploaded'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function deleteKVValue(key) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedKVNamespaceId || !key) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_kv_delete_title'),
            message: t('oauth_kv_delete_message', { key }),
            okText: t('delete'),
        });
        if (!ok) return;
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            namespace_id: state.oauth.selectedKVNamespaceId,
            key,
        });
        try {
            await apiSend('/cf/kv/value?' + params.toString(), 'DELETE');
            state.oauth.selectedKVKey = '';
            state.oauth.kvValue = null;
            state.oauth.kvValueError = '';
            state.oauth.kvSelectedKeys = kvSelectedKeys().filter((item) => item !== key);
            await loadKVKeys(state.oauth.selectedKVNamespaceId);
            renderOAuthResource();
            toast.ok(t('oauth_kv_deleted'));
        } catch (err) {
            toast.err(err.message);
        }
    }

    async function deleteSelectedKVKeys(button) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedKVNamespaceId || !canWrite('kv')) return;
        const keys = kvSelectedKeys();
        if (!keys.length) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_kv_bulk_delete_title'),
            message: t('oauth_kv_bulk_delete_message', { count: keys.length }),
            okText: t('delete'),
        });
        if (!ok) return;
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            namespace_id: state.oauth.selectedKVNamespaceId,
        });
        try {
            setBusy(button, true, t('delete'));
            await apiSend('/cf/kv/keys/bulk-delete?' + params.toString(), 'POST', { keys });
            const deleted = new Set(keys);
            state.oauth.kvSelectedKeys = [];
            if (deleted.has(state.oauth.selectedKVKey)) {
                state.oauth.selectedKVKey = '';
                state.oauth.kvValue = null;
                state.oauth.kvValueError = '';
            }
            await loadKVKeys(state.oauth.selectedKVNamespaceId);
            renderOAuthResource();
            toast.ok(t('oauth_kv_bulk_deleted', { count: keys.length }));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function createKVNamespace(payload, button) {
        if (!state.oauth.selectedAccountId || !canWrite('kv')) return;
        try {
            setBusy(button, true, t('creating'));
            const account = encodeURIComponent(state.oauth.selectedAccountId);
            const resp = await apiSend('/cf/kv/namespaces?account_id=' + account, 'POST', payload);
            state.oauth.kvNamespaceCreateOpen = false;
            await loadKVNamespaces();
            if (resp?.namespace?.id) {
                state.oauth.storageView = 'kv';
                state.oauth.selectedKVNamespaceId = resp.namespace.id;
                state.oauth.selectedKVKey = '';
                state.oauth.kvSelectedKeys = [];
                state.oauth.kvKeys = [];
                state.oauth.kvCursor = '';
                state.oauth.kvValue = null;
                state.oauth.kvKeysError = '';
                state.oauth.kvValueError = '';
                state.oauth.kvCreateOpen = false;
                await loadKVKeys(resp.namespace.id);
            }
            renderOAuthResource();
            toast.ok(t('oauth_kv_namespace_created'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function updateKVNamespace(namespaceID, payload, button) {
        if (!state.oauth.selectedAccountId || !namespaceID || !canWrite('kv')) return;
        try {
            setBusy(button, true, t('saving'));
            const account = encodeURIComponent(state.oauth.selectedAccountId);
            await apiSend('/cf/kv/namespaces/' + encodeURIComponent(namespaceID) + '?account_id=' + account, 'PUT', payload);
            state.oauth.kvNamespaceEditingId = '';
            await loadKVNamespaces();
            renderOAuthResource();
            toast.ok(t('oauth_kv_namespace_renamed'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function deleteKVNamespace(namespace, button) {
        const namespaceID = String(namespace?.id || '').trim();
        if (!state.oauth.selectedAccountId || !namespaceID || !canWrite('kv')) return;
        const title = namespace.title || namespaceID;
        const ok = await window.cfui.confirm({
            title: t('oauth_kv_delete_namespace_title'),
            message: t('oauth_kv_delete_namespace_message', { title }),
            okText: t('delete'),
        });
        if (!ok) return;
        try {
            setBusy(button, true, t('delete'));
            const account = encodeURIComponent(state.oauth.selectedAccountId);
            await apiSend('/cf/kv/namespaces/' + encodeURIComponent(namespaceID) + '?account_id=' + account, 'DELETE');
            if (state.oauth.selectedKVNamespaceId === namespaceID) resetKVDetailSelection();
            if (state.oauth.kvNamespaceEditingId === namespaceID) state.oauth.kvNamespaceEditingId = '';
            await loadKVNamespaces();
            renderOAuthResource();
            toast.ok(t('oauth_kv_namespace_deleted'));
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function runD1Query(sql, button) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedD1DatabaseId) return;
        setBusy(button, true, t('oauth_d1_running'));
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            database_id: state.oauth.selectedD1DatabaseId,
        });
        try {
            const resp = await apiSend('/cf/d1/query?' + params.toString(), 'POST', { sql });
            state.oauth.d1Results = Array.isArray(resp.data) ? resp.data : [];
            state.oauth.d1Session = resp.session || state.oauth.d1Session || null;
            state.oauth.d1Capabilities = resp.capabilities || state.oauth.d1Capabilities || null;
            state.oauth.d1QueryError = '';
            toast.ok(t('oauth_d1_query_done'));
        } catch (err) {
            state.oauth.d1Results = [];
            state.oauth.d1QueryError = err.message || String(err);
            toast.err(err.message);
        } finally {
            setBusy(button, false);
            renderOAuthResource();
        }
    }

    async function loadD1Tables(databaseId = state.oauth.selectedD1DatabaseId) {
        if (!state.oauth.selectedAccountId || !databaseId) return;
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            database_id: databaseId,
        });
        try {
            const resp = await apiGet('/cf/d1/tables?' + params.toString());
            state.oauth.d1Tables = Array.isArray(resp.data) ? resp.data : [];
            state.oauth.d1TablesDatabaseId = databaseId;
            state.oauth.d1Session = resp.session || state.oauth.d1Session || null;
            state.oauth.d1Capabilities = resp.capabilities || state.oauth.d1Capabilities || null;
            state.oauth.d1TablesError = '';
        } catch (err) {
            state.oauth.d1Tables = [];
            state.oauth.d1TablesDatabaseId = databaseId;
            state.oauth.d1TablesError = err.message || String(err);
            toast.err(err.message);
        }
    }

    async function loadD1TableRows(tableName, append = false) {
        if (!state.oauth.selectedAccountId || !state.oauth.selectedD1DatabaseId || !tableName) return;
        const offset = append ? state.oauth.d1TableOffset + state.oauth.d1TableLimit : 0;
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            database_id: state.oauth.selectedD1DatabaseId,
            table: tableName,
            limit: String(state.oauth.d1TableLimit || 50),
            offset: String(offset),
        });
        try {
            state.oauth.selectedD1TableName = tableName;
            const resp = await apiGet('/cf/d1/table?' + params.toString());
            state.oauth.selectedD1TableName = resp.table || tableName;
            state.oauth.d1TableColumns = Array.isArray(resp.columns) ? resp.columns : [];
            state.oauth.d1TableRows = append ? state.oauth.d1TableRows.concat(resp.rows || []) : (Array.isArray(resp.rows) ? resp.rows : []);
            state.oauth.d1TableOffset = resp.offset || 0;
            state.oauth.d1TableLimit = resp.limit || 50;
            state.oauth.d1TableHasMore = !!resp.has_more;
            state.oauth.d1RowIDKey = resp.rowid_key || '_cfui_rowid_';
            state.oauth.d1EditingRow = null;
            state.oauth.d1Session = resp.session || state.oauth.d1Session || null;
            state.oauth.d1Capabilities = resp.capabilities || state.oauth.d1Capabilities || null;
            state.oauth.d1TableRowsError = '';
        } catch (err) {
            state.oauth.selectedD1TableName = tableName;
            if (!append) {
                state.oauth.d1TableColumns = [];
                state.oauth.d1TableRows = [];
                state.oauth.d1TableOffset = 0;
                state.oauth.d1TableHasMore = false;
            }
            state.oauth.d1TableRowsError = err.message || String(err);
            toast.err(err.message);
        }
    }

    async function updateD1Row(changes, button) {
        const row = state.oauth.d1EditingRow;
        const rowid = row?.[state.oauth.d1RowIDKey];
        if (!state.oauth.selectedAccountId || !state.oauth.selectedD1DatabaseId || !state.oauth.selectedD1TableName || rowid == null) return;
        setBusy(button, true, t('saving'));
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            database_id: state.oauth.selectedD1DatabaseId,
            table: state.oauth.selectedD1TableName,
        });
        try {
            const resp = await apiSend('/cf/d1/table?' + params.toString(), 'PATCH', { rowid: String(rowid), changes });
            state.oauth.d1Session = resp.session || state.oauth.d1Session || null;
            state.oauth.d1Capabilities = resp.capabilities || state.oauth.d1Capabilities || null;
            await loadD1TableRows(state.oauth.selectedD1TableName);
            state.oauth.d1TableRowsError = '';
            toast.ok(t('oauth_d1_row_saved'));
        } catch (err) {
            state.oauth.d1TableRowsError = err.message || String(err);
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function deleteD1Row(row) {
        const rowid = row?.[state.oauth.d1RowIDKey];
        if (!state.oauth.selectedAccountId || !state.oauth.selectedD1DatabaseId || !state.oauth.selectedD1TableName || rowid == null) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_d1_delete_row_title'),
            message: t('oauth_d1_delete_row_message', { rowid: String(rowid) }),
            okText: t('delete'),
        });
        if (!ok) return;
        const params = new URLSearchParams({
            account_id: state.oauth.selectedAccountId,
            database_id: state.oauth.selectedD1DatabaseId,
            table: state.oauth.selectedD1TableName,
        });
        try {
            const resp = await apiSend('/cf/d1/table?' + params.toString(), 'DELETE', { rowid: String(rowid) });
            state.oauth.d1Session = resp.session || state.oauth.d1Session || null;
            state.oauth.d1Capabilities = resp.capabilities || state.oauth.d1Capabilities || null;
            await loadD1TableRows(state.oauth.selectedD1TableName);
            state.oauth.d1TableRowsError = '';
            toast.ok(t('oauth_d1_row_deleted'));
        } catch (err) {
            state.oauth.d1TableRowsError = err.message || String(err);
            toast.err(err.message);
        }
    }

    async function loadOAuthSnippets() {
        if (!state.oauth.selectedZoneId) return;
        state.oauth.snippetsError = '';
        try {
            const resp = await apiGet('/cf/snippets?zone_id=' + encodeURIComponent(state.oauth.selectedZoneId));
            state.oauth.snippets = Array.isArray(resp.data) ? resp.data : [];
            state.oauth.snippetSession = resp.session || state.oauth.snippetSession || null;
            state.oauth.snippetCapabilities = resp.capabilities || state.oauth.snippetCapabilities || null;
            if (state.oauth.selectedSnippetName && !state.oauth.snippets.some((snippet) => snippet.name === state.oauth.selectedSnippetName)) {
                resetSnippetDetail();
            }
            if (state.oauth.selectedSnippetName) {
                await Promise.all([
                    loadSnippetRules(state.oauth.selectedSnippetName),
                    loadSnippetContent(state.oauth.selectedSnippetName),
                ]);
            }
        } catch (err) {
            state.oauth.snippetsError = err.message || String(err);
            state.oauth.snippets = [];
        }
    }

    async function createSnippet(payload, button) {
        if (!state.oauth.selectedZoneId) return;
        setBusy(button, true, t('saving'));
        state.oauth.snippetMutationError = '';
        try {
            await apiSend('/cf/snippets?zone_id=' + encodeURIComponent(state.oauth.selectedZoneId), 'POST', payload);
            state.oauth.snippetCreateOpen = false;
            state.oauth.selectedSnippetName = payload.name;
            state.oauth.snippetContent = {
                name: payload.name,
                main_file: payload.main_file || 'snippet.js',
                value: payload.code || '',
                encoding: 'utf-8',
                bytes: new Blob([payload.code || '']).size,
                truncated: false,
            };
            state.oauth.snippetContentDraft = payload.code || '';
            state.oauth.snippetContentMainFile = payload.main_file || 'snippet.js';
            state.oauth.snippetContentError = '';
            await loadOAuthSnippets();
            renderOAuthResource();
            toast.ok(t('oauth_snippet_saved'));
        } catch (err) {
            state.oauth.snippetMutationError = err.message || String(err);
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function deleteSnippet(snippetName) {
        if (!state.oauth.selectedZoneId || !snippetName) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_snippet_delete_title'),
            message: t('oauth_snippet_delete_message', { name: snippetName }),
            okText: t('delete'),
        });
        if (!ok) return;
        state.oauth.snippetMutationError = '';
        try {
            await apiSend(`/cf/snippets/${encodeURIComponent(snippetName)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'DELETE');
            resetSnippetDetail();
            await loadOAuthSnippets();
            renderOAuthResource();
            toast.ok(t('oauth_snippet_deleted'));
        } catch (err) {
            state.oauth.snippetMutationError = err.message || String(err);
            toast.err(err.message);
        }
    }

    async function loadSnippetRules(snippetName = state.oauth.selectedSnippetName) {
        if (!state.oauth.selectedZoneId || !snippetName) return;
        const params = new URLSearchParams({
            zone_id: state.oauth.selectedZoneId,
            snippet_name: snippetName,
        });
        state.oauth.snippetRulesError = '';
        try {
            const resp = await apiGet('/cf/snippets/rules?' + params.toString());
            state.oauth.snippetRules = Array.isArray(resp.data) ? resp.data : [];
            state.oauth.snippetSession = resp.session || state.oauth.snippetSession || null;
            state.oauth.snippetCapabilities = resp.capabilities || state.oauth.snippetCapabilities || null;
        } catch (err) {
            state.oauth.snippetRulesError = err.message || String(err);
            state.oauth.snippetRules = [];
        }
    }

    async function loadSnippetContent(snippetName = state.oauth.selectedSnippetName) {
        if (!state.oauth.selectedZoneId || !snippetName) return;
        state.oauth.snippetContentLoading = true;
        state.oauth.snippetContentError = '';
        state.oauth.snippetContent = null;
        const params = new URLSearchParams({ zone_id: state.oauth.selectedZoneId });
        try {
            const content = await apiGet(`/cf/snippets/${encodeURIComponent(snippetName)}/content?${params.toString()}`);
            if (state.oauth.selectedSnippetName !== snippetName) return;
            state.oauth.snippetContent = content;
            state.oauth.snippetSession = content.session || state.oauth.snippetSession || null;
            state.oauth.snippetCapabilities = content.capabilities || state.oauth.snippetCapabilities || null;
            state.oauth.snippetContentDraft = content.value || '';
            state.oauth.snippetContentMainFile = content.main_file || 'snippet.js';
        } catch (err) {
            if (state.oauth.selectedSnippetName !== snippetName) return;
            state.oauth.snippetContentError = err.message;
            state.oauth.snippetContent = null;
            state.oauth.snippetContentDraft = '';
            state.oauth.snippetContentMainFile = 'snippet.js';
        } finally {
            if (state.oauth.selectedSnippetName === snippetName) {
                state.oauth.snippetContentLoading = false;
            }
        }
    }

    async function saveSnippetContent(mainFile, code, button) {
        if (!state.oauth.selectedZoneId || !state.oauth.selectedSnippetName) return;
        const snippetName = state.oauth.selectedSnippetName;
        setBusy(button, true, t('saving'));
        const params = new URLSearchParams({ zone_id: state.oauth.selectedZoneId });
        state.oauth.snippetMutationError = '';
        try {
            const content = await apiSend(`/cf/snippets/${encodeURIComponent(snippetName)}/content?${params.toString()}`, 'PUT', {
                main_file: mainFile,
                code,
            });
            state.oauth.snippetContent = content;
            state.oauth.snippetSession = content.session || state.oauth.snippetSession || null;
            state.oauth.snippetCapabilities = content.capabilities || state.oauth.snippetCapabilities || null;
            state.oauth.snippetContentDraft = content.value || code || '';
            state.oauth.snippetContentMainFile = content.main_file || mainFile || 'snippet.js';
            state.oauth.snippetContentError = '';
            await loadOAuthSnippets();
            renderOAuthResource();
            toast.ok(t('oauth_snippet_content_saved'));
        } catch (err) {
            state.oauth.snippetMutationError = err.message || String(err);
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function createSnippetRule(payload, button) {
        if (!state.oauth.selectedZoneId || !state.oauth.selectedSnippetName) return;
        setBusy(button, true, t('saving'));
        state.oauth.snippetMutationError = '';
        try {
            const resp = await apiSend('/cf/snippets/rules?zone_id=' + encodeURIComponent(state.oauth.selectedZoneId), 'POST', payload);
            state.oauth.snippetRules = Array.isArray(resp.data) ? resp.data : state.oauth.snippetRules;
            state.oauth.snippetSession = resp.session || state.oauth.snippetSession || null;
            state.oauth.snippetCapabilities = resp.capabilities || state.oauth.snippetCapabilities || null;
            state.oauth.snippetRuleCreateOpen = false;
            await loadOAuthSnippets();
            await loadSnippetRules(state.oauth.selectedSnippetName);
            renderOAuthResource();
            toast.ok(t('oauth_snippet_rule_saved'));
        } catch (err) {
            state.oauth.snippetMutationError = err.message || String(err);
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function setSnippetRuleEnabled(rule, enabled, control) {
        if (!state.oauth.selectedZoneId || !rule?.id) return;
        if (control) control.disabled = true;
        state.oauth.snippetMutationError = '';
        try {
            const resp = await apiSend(`/cf/snippets/rules/${encodeURIComponent(rule.id)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'PATCH', { enabled });
            state.oauth.snippetRules = Array.isArray(resp.data) ? resp.data : state.oauth.snippetRules;
            state.oauth.snippetSession = resp.session || state.oauth.snippetSession || null;
            state.oauth.snippetCapabilities = resp.capabilities || state.oauth.snippetCapabilities || null;
            await loadOAuthSnippets();
            await loadSnippetRules(state.oauth.selectedSnippetName);
            renderOAuthResource();
        } catch (err) {
            state.oauth.snippetMutationError = err.message || String(err);
            toast.err(err.message);
            if (control) control.checked = !enabled;
        } finally {
            if (control) control.disabled = false;
        }
    }

    async function deleteSnippetRule(rule) {
        if (!state.oauth.selectedZoneId || !rule?.id) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_snippet_rule_delete_title'),
            message: t('oauth_snippet_rule_delete_message'),
            okText: t('delete'),
        });
        if (!ok) return;
        state.oauth.snippetMutationError = '';
        try {
            const resp = await apiSend(`/cf/snippets/rules/${encodeURIComponent(rule.id)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'DELETE');
            state.oauth.snippetRules = Array.isArray(resp.data) ? resp.data : state.oauth.snippetRules;
            state.oauth.snippetSession = resp.session || state.oauth.snippetSession || null;
            state.oauth.snippetCapabilities = resp.capabilities || state.oauth.snippetCapabilities || null;
            await loadOAuthSnippets();
            await loadSnippetRules(state.oauth.selectedSnippetName);
            renderOAuthResource();
            toast.ok(t('oauth_snippet_rule_deleted'));
        } catch (err) {
            state.oauth.snippetMutationError = err.message || String(err);
            toast.err(err.message);
        }
    }

	async function loadOAuthWAF() {
		if (!state.oauth.selectedZoneId) return;
		state.oauth.wafError = '';
		try {
			const zoneID = encodeURIComponent(state.oauth.selectedZoneId);
			const [ruleset, managedRuleset, managedOverrideRuleset] = await Promise.all([
				apiGet('/cf/waf?zone_id=' + zoneID),
				apiGet('/cf/waf/managed-exceptions?zone_id=' + zoneID),
				apiGet('/cf/waf/managed-overrides?zone_id=' + zoneID),
			]);
			applyWAFRulesetResponse('custom', ruleset);
			applyWAFRulesetResponse('managed_exceptions', managedRuleset);
			applyWAFRulesetResponse('managed_overrides', managedOverrideRuleset);
		} catch (err) {
			state.oauth.wafError = err.message || String(err);
			state.oauth.wafRuleset = null;
			state.oauth.wafManagedRuleset = null;
			state.oauth.wafManagedOverrideRuleset = null;
		}
	}

	function applyWAFRulesetResponse(kind, ruleset) {
		if (kind === 'custom') state.oauth.wafRuleset = ruleset || null;
		else if (kind === 'managed_exceptions') state.oauth.wafManagedRuleset = ruleset || null;
		else if (kind === 'managed_overrides') state.oauth.wafManagedOverrideRuleset = ruleset || null;
		state.oauth.wafSession = ruleset?.session || state.oauth.wafSession || null;
		state.oauth.wafCapabilities = ruleset?.capabilities || state.oauth.wafCapabilities || null;
	}

    async function loadOAuthAnalytics() {
        if (!state.oauth.selectedZoneId) return;
        state.oauth.zoneAnalyticsLoading = true;
        state.oauth.zoneAnalyticsError = '';
        try {
            const params = new URLSearchParams({
                zone_id: state.oauth.selectedZoneId,
                range: state.oauth.analyticsRange || '24h',
            });
            state.oauth.zoneAnalytics = await apiGet('/cf/analytics/zone?' + params.toString());
            state.oauth.zoneAnalyticsError = '';
        } catch (err) {
            state.oauth.zoneAnalytics = null;
            state.oauth.zoneAnalyticsError = err.message;
        } finally {
            state.oauth.zoneAnalyticsLoading = false;
        }
    }

    async function createWAFRule(payload, button) {
        if (!state.oauth.selectedZoneId) return;
        setBusy(button, true, t('saving'));
        state.oauth.wafMutationError = '';
        try {
            applyWAFRulesetResponse('custom', await apiSend('/cf/waf/rules?zone_id=' + encodeURIComponent(state.oauth.selectedZoneId), 'POST', payload));
            state.oauth.wafCreateOpen = false;
            renderOAuthResource();
            toast.ok(t('oauth_waf_rule_saved'));
        } catch (err) {
            state.oauth.wafMutationError = err.message || String(err);
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function updateWAFRule(rule, payload, button) {
        if (!state.oauth.selectedZoneId || !rule?.id) return;
        setBusy(button, true, t('saving'));
        state.oauth.wafMutationError = '';
        try {
            applyWAFRulesetResponse('custom', await apiSend(`/cf/waf/rules/${encodeURIComponent(rule.id)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'PATCH', payload));
            state.oauth.wafEditingId = '';
            renderOAuthResource();
            toast.ok(t('oauth_waf_rule_updated'));
        } catch (err) {
            state.oauth.wafMutationError = err.message || String(err);
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function setWAFRuleEnabled(rule, enabled, control) {
        if (!state.oauth.selectedZoneId || !rule?.id) return;
        if (control) control.disabled = true;
        state.oauth.wafMutationError = '';
        try {
            applyWAFRulesetResponse('custom', await apiSend(`/cf/waf/rules/${encodeURIComponent(rule.id)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'PATCH', { enabled }));
            renderOAuthResource();
        } catch (err) {
            state.oauth.wafMutationError = err.message || String(err);
            toast.err(err.message);
            if (control) control.checked = !enabled;
        } finally {
            if (control) control.disabled = false;
        }
    }

	async function deleteWAFRule(rule) {
		if (!state.oauth.selectedZoneId || !rule?.id) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_waf_delete_rule_title'),
            message: t('oauth_waf_delete_rule_message'),
            okText: t('delete'),
        });
        if (!ok) return;
        state.oauth.wafMutationError = '';
        try {
            applyWAFRulesetResponse('custom', await apiSend(`/cf/waf/rules/${encodeURIComponent(rule.id)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'DELETE'));
            renderOAuthResource();
            toast.ok(t('oauth_waf_rule_deleted'));
        } catch (err) {
            state.oauth.wafMutationError = err.message || String(err);
            toast.err(err.message);
		}
	}

	async function createWAFManagedException(payload, button) {
		if (!state.oauth.selectedZoneId) return;
		setBusy(button, true, t('saving'));
		state.oauth.wafMutationError = '';
		try {
			applyWAFRulesetResponse('managed_exceptions', await apiSend('/cf/waf/managed-exceptions/rules?zone_id=' + encodeURIComponent(state.oauth.selectedZoneId), 'POST', payload));
			state.oauth.wafManagedExceptionCreateOpen = false;
			renderOAuthResource();
			toast.ok(t('oauth_waf_managed_exception_saved'));
		} catch (err) {
			state.oauth.wafMutationError = err.message || String(err);
			toast.err(err.message);
		} finally {
			setBusy(button, false);
		}
	}

	async function updateWAFManagedException(rule, payload, button) {
		if (!state.oauth.selectedZoneId || !rule?.id) return;
		setBusy(button, true, t('saving'));
		state.oauth.wafMutationError = '';
		try {
			applyWAFRulesetResponse('managed_exceptions', await apiSend(`/cf/waf/managed-exceptions/rules/${encodeURIComponent(rule.id)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'PATCH', payload));
			state.oauth.wafManagedExceptionEditingId = '';
			renderOAuthResource();
			toast.ok(t('oauth_waf_managed_exception_updated'));
		} catch (err) {
			state.oauth.wafMutationError = err.message || String(err);
			toast.err(err.message);
		} finally {
			setBusy(button, false);
		}
	}

	async function setWAFManagedExceptionEnabled(rule, enabled, control) {
		if (!state.oauth.selectedZoneId || !rule?.id) return;
		if (control) control.disabled = true;
		state.oauth.wafMutationError = '';
		try {
			applyWAFRulesetResponse('managed_exceptions', await apiSend(`/cf/waf/managed-exceptions/rules/${encodeURIComponent(rule.id)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'PATCH', { enabled }));
			renderOAuthResource();
		} catch (err) {
			state.oauth.wafMutationError = err.message || String(err);
			toast.err(err.message);
			if (control) control.checked = !enabled;
		} finally {
			if (control) control.disabled = false;
		}
	}

	async function deleteWAFManagedException(rule) {
		if (!state.oauth.selectedZoneId || !rule?.id) return;
		const ok = await window.cfui.confirm({
			title: t('oauth_waf_delete_managed_exception_title'),
			message: t('oauth_waf_delete_managed_exception_message'),
			okText: t('delete'),
		});
		if (!ok) return;
		state.oauth.wafMutationError = '';
		try {
			applyWAFRulesetResponse('managed_exceptions', await apiSend(`/cf/waf/managed-exceptions/rules/${encodeURIComponent(rule.id)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'DELETE'));
			renderOAuthResource();
			toast.ok(t('oauth_waf_managed_exception_deleted'));
		} catch (err) {
			state.oauth.wafMutationError = err.message || String(err);
			toast.err(err.message);
		}
	}

	async function createWAFManagedOverride(payload, button) {
		if (!state.oauth.selectedZoneId) return;
		setBusy(button, true, t('saving'));
		state.oauth.wafMutationError = '';
		try {
			applyWAFRulesetResponse('managed_overrides', await apiSend('/cf/waf/managed-overrides/rules?zone_id=' + encodeURIComponent(state.oauth.selectedZoneId), 'POST', payload));
			state.oauth.wafManagedOverrideCreateOpen = false;
			renderOAuthResource();
			toast.ok(t('oauth_waf_managed_override_saved'));
		} catch (err) {
			state.oauth.wafMutationError = err.message || String(err);
			toast.err(err.message);
		} finally {
			setBusy(button, false);
		}
	}

	async function updateWAFManagedOverride(rule, payload, button) {
		if (!state.oauth.selectedZoneId || !rule?.id) return;
		setBusy(button, true, t('saving'));
		state.oauth.wafMutationError = '';
		try {
			applyWAFRulesetResponse('managed_overrides', await apiSend(`/cf/waf/managed-overrides/rules/${encodeURIComponent(rule.id)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'PATCH', payload));
			state.oauth.wafManagedOverrideEditingId = '';
			renderOAuthResource();
			toast.ok(t('oauth_waf_managed_override_updated'));
		} catch (err) {
			state.oauth.wafMutationError = err.message || String(err);
			toast.err(err.message);
		} finally {
			setBusy(button, false);
		}
	}

	async function setWAFManagedOverrideEnabled(rule, enabled, control) {
		if (!state.oauth.selectedZoneId || !rule?.id) return;
		if (control) control.disabled = true;
		state.oauth.wafMutationError = '';
		try {
			applyWAFRulesetResponse('managed_overrides', await apiSend(`/cf/waf/managed-overrides/rules/${encodeURIComponent(rule.id)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'PATCH', { enabled }));
			renderOAuthResource();
		} catch (err) {
			state.oauth.wafMutationError = err.message || String(err);
			toast.err(err.message);
			if (control) control.checked = !enabled;
		} finally {
			if (control) control.disabled = false;
		}
	}

	async function deleteWAFManagedOverride(rule) {
		if (!state.oauth.selectedZoneId || !rule?.id) return;
		const ok = await window.cfui.confirm({
			title: t('oauth_waf_delete_managed_override_title'),
			message: t('oauth_waf_delete_managed_override_message'),
			okText: t('delete'),
		});
		if (!ok) return;
		state.oauth.wafMutationError = '';
		try {
			applyWAFRulesetResponse('managed_overrides', await apiSend(`/cf/waf/managed-overrides/rules/${encodeURIComponent(rule.id)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'DELETE'));
			renderOAuthResource();
			toast.ok(t('oauth_waf_managed_override_deleted'));
		} catch (err) {
			state.oauth.wafMutationError = err.message || String(err);
			toast.err(err.message);
		}
	}

	async function loadOAuthZoneSettings() {
        if (!state.oauth.selectedZoneId) return;
        state.oauth.zoneSettingsError = '';
        try {
            const resp = await apiGet('/cf/zone-settings?zone_id=' + encodeURIComponent(state.oauth.selectedZoneId));
            state.oauth.zoneSettings = Array.isArray(resp.data) ? resp.data : [];
            state.oauth.zoneSettingsSession = resp.session || state.oauth.zoneSettingsSession || null;
            state.oauth.zoneSettingsCapabilities = resp.capabilities || state.oauth.zoneSettingsCapabilities || null;
        } catch (err) {
            state.oauth.zoneSettingsError = err.message || String(err);
            state.oauth.zoneSettings = [];
        }
    }

    async function submitDNSRecord(record, payload, button) {
        if (!state.oauth.selectedZoneId) return;
        const zone = encodeURIComponent(state.oauth.selectedZoneId);
        const editing = !!record?.id;
        const path = editing
            ? `/cf/dns/${encodeURIComponent(record.id)}?zone_id=${zone}`
            : `/cf/dns?zone_id=${zone}`;
        setBusy(button, true, t('saving'));
        try {
            await apiSend(path, editing ? 'PUT' : 'POST', payload);
            state.oauth.dnsFormMode = '';
            state.oauth.dnsEditingId = '';
            state.oauth.dnsMutationError = '';
            await loadOAuthDNS();
            renderOAuthResource();
            toast.ok(t(editing ? 'oauth_dns_updated' : 'oauth_dns_created'));
        } catch (err) {
            state.oauth.dnsMutationError = err.message || String(err);
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function deleteDNSRecord(record) {
        if (!record?.id || !state.oauth.selectedZoneId) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_dns_delete_title'),
            message: t('oauth_dns_delete_message', { name: record.name || record.id }),
            okText: t('delete'),
        });
        if (!ok) return;
        try {
            await apiSend(`/cf/dns/${encodeURIComponent(record.id)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'DELETE');
            state.oauth.dnsMutationError = '';
            await loadOAuthDNS();
            renderOAuthResource();
            toast.ok(t('oauth_dns_deleted'));
        } catch (err) {
            state.oauth.dnsMutationError = err.message || String(err);
            toast.err(err.message);
        }
    }

    async function updateZoneSetting(settingID, value, control) {
        if (!state.oauth.selectedZoneId) return;
        if (control) control.disabled = true;
        state.oauth.zoneSettingsMutationError = '';
        try {
            await apiSend(`/cf/zone-settings/${encodeURIComponent(settingID)}?zone_id=${encodeURIComponent(state.oauth.selectedZoneId)}`, 'PATCH', { value });
            await loadOAuthZoneSettings();
            renderOAuthResource();
            toast.ok(t('oauth_zone_setting_saved'));
        } catch (err) {
            state.oauth.zoneSettingsMutationError = err.message || String(err);
            toast.err(err.message);
            await loadOAuthZoneSettings();
            renderOAuthResource();
        } finally {
            if (control) control.disabled = false;
        }
    }

    async function purgeZoneCache(button) {
        if (!state.oauth.selectedZoneId || !canWrite('cache_purge')) return;
        const ok = await window.cfui.confirm({
            title: t('oauth_cache_purge_title'),
            message: t('oauth_cache_purge_message'),
            okText: t('oauth_cache_purge'),
        });
        if (!ok) return;
        setBusy(button, true, t('saving'));
        state.oauth.zoneCachePurgeError = '';
        try {
            await apiSend('/cf/cache/purge?zone_id=' + encodeURIComponent(state.oauth.selectedZoneId), 'POST', {});
            toast.ok(t('oauth_cache_purged'));
        } catch (err) {
            state.oauth.zoneCachePurgeError = err.message || String(err);
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function loadOAuthCurrentResource() {
        if (state.oauth.resource === 'status') {
            await loadCloudflareStatus();
            return;
        }
        if (!state.oauth.status?.logged_in) return;
        if (state.oauth.resource === 'overview') await loadOAuthOverviewSummary();
        else if (state.oauth.resource === 'zones') await loadOAuthZoneDetail();
        else if (state.oauth.resource === 'dns') await loadOAuthDNS();
        else if (state.oauth.resource === 'tunnels') await loadOAuthTunnels();
        else if (state.oauth.resource === 'workers') await loadOAuthWorkers();
        else if (state.oauth.resource === 'storage') await loadOAuthStorage();
        else if (state.oauth.resource === 'usage') await loadOAuthAccountUsage();
        else if (state.oauth.resource === 'snippets') await loadOAuthSnippets();
        else if (state.oauth.resource === 'waf') await loadOAuthWAF();
        else if (state.oauth.resource === 'analytics') await loadOAuthAnalytics();
        else if (state.oauth.resource === 'settings') await loadOAuthZoneSettings();
    }

    function configuredOAuthScopeSet(status = state.oauth.status) {
        return new Set(String(status?.config?.scopes || '').split(/\s+/).map((scope) => scope.trim().toLowerCase()).filter(Boolean));
    }

    function ensurePermissionDraft(status = state.oauth.status) {
        if (!status?.config?.configured) {
            state.oauth.permissionDraft = null;
            state.oauth.permissionDraftSource = '';
            return null;
        }
        const source = String(status.config.scopes || '');
        if (state.oauth.permissionDraft && state.oauth.permissionDraftSource === source) {
            return state.oauth.permissionDraft;
        }
        const configured = configuredOAuthScopeSet(status);
        const draft = {};
        for (const definition of oauthPermissionDefinitions) {
            const hasRead = definition.readScopes.some((scope) => configured.has(scope.toLowerCase()));
            const writeScopes = definition.acceptedWriteScopes || definition.writeScopes;
            const hasWrite = writeScopes.some((scope) => configured.has(scope.toLowerCase()));
            draft[definition.id] = {
                enabled: !!definition.required || hasRead || hasWrite,
                write: hasWrite,
            };
        }
        state.oauth.permissionDraft = draft;
        state.oauth.permissionDraftSource = source;
        return draft;
    }

    function selectedOAuthScopes() {
        const draft = ensurePermissionDraft();
        if (!draft) return [];
        const scopes = new Set();
        for (const definition of oauthPermissionDefinitions) {
            const item = draft[definition.id] || {};
            if (!item.enabled && !definition.required) continue;
            definition.readScopes.forEach((scope) => scopes.add(scope));
            if (definition.writeOnly || item.write) definition.writeScopes.forEach((scope) => scopes.add(scope));
        }
        return Array.from(scopes).sort((a, b) => a.localeCompare(b));
    }

    function selectedOAuthScopeString() {
        return selectedOAuthScopes().join(' ');
    }

    function setPermissionFeatureEnabled(id, enabled) {
        const draft = ensurePermissionDraft();
        if (!draft) return;
        const definition = oauthPermissionDefinitions.find((item) => item.id === id);
        if (!definition || definition.required) return;
        draft[id] = draft[id] || { enabled: false, write: false };
        draft[id].enabled = !!enabled;
        if (definition.writeOnly) draft[id].write = !!enabled;
        if (!draft[id].enabled) draft[id].write = false;
        renderOAuthScopePanel(state.oauth.status);
        renderOAuthScopeDialog(state.oauth.status);
    }

    function setPermissionWriteEnabled(id, enabled) {
        const draft = ensurePermissionDraft();
        if (!draft) return;
        const definition = oauthPermissionDefinitions.find((item) => item.id === id);
        if (!definition || !definition.writeScopes.length) return;
        draft[id] = draft[id] || { enabled: true, write: false };
        draft[id].enabled = true;
        draft[id].write = !!enabled;
        renderOAuthScopePanel(state.oauth.status);
        renderOAuthScopeDialog(state.oauth.status);
    }

    function renderOAuthStatus(status) {
        const relay = $('oauth-relay-url');
        if (relay) {
            relay.innerHTML = '';
            relay.appendChild(oauthRelayCallbackNode(status));
        }
        const localCallback = $('oauth-local-callback-url');
        if (localCallback) {
            const callbackPath = status?.config?.local_callback_path || '/oauth/callback';
            localCallback.textContent = window.location.origin + callbackPath;
        }
        const login = $('oauth-login');
        const logout = $('oauth-logout');
        if (login) {
            login.hidden = !status?.config?.configured;
            const text = login.querySelector('.text');
            if (text) text.textContent = status?.logged_in ? t('oauth_add_identity') : t('oauth_sign_in');
            login.title = status?.logged_in ? t('oauth_add_identity_fresh_hint') : t('oauth_sign_in');
            login.setAttribute('aria-label', login.title);
        }
        if (logout) {
            logout.hidden = !status?.logged_in;
            logout.textContent = t('oauth_sign_out_current');
        }

        if (!status?.config?.configured) {
            setOAuthStatus('warn', t('oauth_not_configured'));
        } else if (status.logged_in) {
            const label = status.current?.label || t('oauth_account');
            setOAuthStatus('ok', t('oauth_logged_in_as', { label }));
        } else {
            setOAuthStatus('warn', t('oauth_not_logged_in'));
        }
        renderOAuthSetupGuide(status);
        renderOAuthScopePanel(status);
        renderOAuthIdentities(status);
        updateOAuthResourceTabs();
    }

    function oauthRelayCallbackNode(status) {
        const savedRelay = status?.config?.relay_callback_url || '';
        const configuredRelay = savedRelay || defaultOAuthRelayCallbackURL;
        const isDefaultRelay = configuredRelay === defaultOAuthRelayCallbackURL;
        const form = document.createElement('form');
        form.className = 'oauth-relay-editor';
        form.dataset.mode = isDefaultRelay ? 'default' : 'custom';
        const field = document.createElement('div');
        field.className = 'oauth-relay-field';
        const inputRow = document.createElement('div');
        inputRow.className = 'oauth-relay-input-row';
        const input = document.createElement('input');
        input.className = 'input oauth-relay-input mono';
        input.id = 'oauth-relay-callback-input';
        input.type = 'url';
        input.required = true;
        input.spellcheck = false;
        input.autocomplete = 'off';
        input.placeholder = defaultOAuthRelayCallbackURL;
        input.setAttribute('aria-label', t('oauth_relay_callback'));
        input.setAttribute('aria-describedby', 'oauth-relay-callback-help oauth-relay-callback-assist');
        input.value = configuredRelay;
        const save = smallButton(t('save'), 'btn btn--sm btn--primary oauth-relay-save');
        save.type = 'submit';
        inputRow.append(input, save);

        const helper = document.createElement('div');
        helper.className = 'oauth-relay-helper';
        const helperCopy = document.createElement('div');
        helperCopy.className = 'oauth-relay-helper-copy';
        const helperText = document.createElement('span');
        helperText.className = 'oauth-relay-helper-text';
        helperText.id = 'oauth-relay-callback-help';
        helperText.textContent = t('oauth_relay_config_hint');
        const assistText = document.createElement('span');
        assistText.className = 'oauth-relay-assist-text';
        assistText.id = 'oauth-relay-callback-assist';
        assistText.textContent = t('oauth_relay_assist_text');
        helperCopy.append(helperText, assistText);
        const assistActions = document.createElement('span');
        assistActions.className = 'oauth-relay-assist-actions';
        const useDefault = smallButton(t('oauth_relay_use_default'), 'btn btn--xs btn--text oauth-relay-inline-action oauth-relay-text-action', (event) => {
            input.value = defaultOAuthRelayCallbackURL;
            if (savedRelay === defaultOAuthRelayCallbackURL) {
                input.focus();
                input.select();
                return;
            }
            saveOAuthRelayCallback(input.value, event.currentTarget);
        });
        useDefault.title = t('oauth_relay_use_default_title');
        useDefault.setAttribute('aria-label', t('oauth_relay_use_default_title'));
        const selfHost = smallButton(t('oauth_relay_self_host'), 'btn btn--xs btn--text oauth-relay-inline-action oauth-relay-text-action', () => openOAuthWorkerScriptDialog());
        selfHost.title = t('oauth_relay_self_host_title');
        selfHost.setAttribute('aria-label', t('oauth_relay_self_host_title'));
        assistActions.append(useDefault, selfHost);
        helper.append(helperCopy, assistActions);

        field.append(inputRow, helper);
        form.appendChild(field);
        form.addEventListener('submit', (event) => {
            event.preventDefault();
            saveOAuthRelayCallback(input.value, save);
        });
        return form;
    }

    function oauthWorkerScriptURL() {
        return window.location.origin + '/cloudflare-oauth-worker.js';
    }

    function openOAuthWorkerScript() {
        window.open('/cloudflare-oauth-worker.js', '_blank', 'noopener');
    }

    async function openOAuthWorkerScriptDialog() {
        const dialog = $('oauth-worker-script-dialog');
        if (!dialog) {
            openOAuthWorkerScript();
            return;
        }
        window.cfui.openDialog?.(dialog);
        await loadOAuthWorkerScript();
    }

    async function loadOAuthWorkerScript(force = false) {
        if (state.oauth.workerScriptContent && !force) {
            renderOAuthWorkerScriptDialog();
            return;
        }
        state.oauth.workerScriptLoading = true;
        state.oauth.workerScriptError = '';
        renderOAuthWorkerScriptDialog();
        try {
            const resp = await fetch('/cloudflare-oauth-worker.js', { cache: 'no-store' });
            if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
            state.oauth.workerScriptContent = await resp.text();
        } catch (err) {
            state.oauth.workerScriptError = err.message || String(err);
        } finally {
            state.oauth.workerScriptLoading = false;
            renderOAuthWorkerScriptDialog();
        }
    }

    function renderOAuthWorkerScriptDialog() {
        const code = $('oauth-worker-script-content');
        if (!code) return;
        const defaultRelay = $('oauth-worker-default-relay');
        if (defaultRelay) defaultRelay.textContent = defaultOAuthRelayCallbackURL;
        if (state.oauth.workerScriptLoading) {
            code.textContent = t('oauth_worker_script_loading');
        } else if (state.oauth.workerScriptError) {
            code.textContent = t('oauth_worker_script_load_failed', { error: state.oauth.workerScriptError });
        } else {
            code.textContent = state.oauth.workerScriptContent || '';
        }
        const copy = $('oauth-worker-script-copy');
        if (copy) copy.disabled = !state.oauth.workerScriptContent;
    }

    function focusOAuthRelayInput() {
        const input = $('oauth-relay-url')?.querySelector('input');
        if (!input) return;
        input.focus();
        input.select();
    }

    function setOAuthStatus(kind, text) {
        const el = $('oauth-status');
        if (!el) return;
        el.setAttribute('data-state', kind);
        const textEl = el.querySelector('.text');
        if (textEl) textEl.textContent = text;
    }

    function renderOAuthSetupGuide(status) {
        const guide = $('oauth-setup-guide');
        if (!guide) return;
        guide.innerHTML = '';
        const configured = !!status?.config?.configured;
        guide.hidden = configured;
        if (configured) return;

        const relayURL = status?.config?.relay_callback_url || '';
        const minimumScopeList = oauthMinimumSetupScopes.join(' ');
        const fullConsoleScopeList = oauthFullConsoleSetupScopes.join(' ');
        const envSnippet = [
            `CFUI_OAUTH_CLIENT_ID=${t('oauth_setup_client_id_placeholder')}`,
            `CFUI_OAUTH_RELAY_URL=${relayURL || t('oauth_setup_relay_url_placeholder')}`,
            'CFUI_RUN_MODE=oauth',
        ].join('\n');

        const title = document.createElement('div');
        title.className = 'oauth-setup-title';
        title.textContent = t('oauth_setup_title');
        const subtitle = document.createElement('div');
        subtitle.className = 'oauth-setup-subtitle';
        subtitle.textContent = t('oauth_setup_subtitle');
        guide.append(title, subtitle);

        const steps = document.createElement('div');
        steps.className = 'oauth-setup-steps';
        steps.append(
            setupGuideStep(
                '1',
                t('oauth_setup_relay_title'),
                t('oauth_setup_relay_desc'),
                [setupGuideNote(t('oauth_setup_relay_input_note'))]
            ),
            setupGuideStep(
                '2',
                t('oauth_setup_oauth_app_title'),
                t('oauth_setup_oauth_app_desc'),
                [
                    setupGuideCodeRow(t('oauth_setup_cloudflare_path'), t('oauth_setup_cloudflare_path_value'), { copy: false }),
                    setupGuideCodeRow(t('oauth_setup_client_name'), 'cfui'),
                    setupGuideCodeRow(t('oauth_setup_response_type'), t('oauth_setup_response_type_value'), { copy: false }),
                    setupGuideCodeRow(t('oauth_setup_grant_type'), t('oauth_setup_grant_type_value'), { copy: false }),
                    setupGuideCodeRow(t('oauth_setup_token_auth_method'), t('oauth_setup_token_auth_method_value'), { copy: false }),
                    setupGuideCodeRow(t('oauth_setup_redirect_uri'), relayURL || defaultOAuthRelayCallbackURL, {
                        actionLabel: t('oauth_relay_configure'),
                        actionTitle: t('oauth_relay_edit'),
                        action: focusOAuthRelayInput,
                    }),
                    setupGuideNote(t('oauth_setup_redirect_uri_note')),
                    setupGuideCodeRow(t('oauth_setup_client_url'), t('oauth_setup_client_url_value'), { copy: false }),
                ]
            ),
            setupGuideStep(
                '3',
                t('oauth_setup_permissions_title'),
                t('oauth_setup_permissions_desc'),
                [
                    setupGuideCodeRow(t('oauth_setup_permissions_minimum'), minimumScopeList),
                    setupGuideCodeRow(t('oauth_setup_permissions_full'), fullConsoleScopeList),
                    setupGuideNote(t('oauth_setup_permissions_scope_model')),
                    setupGuideNote(t('oauth_setup_permissions_categories')),
                    setupGuideNote(t('oauth_setup_permissions_write_note')),
                ]
            ),
            setupGuideStep(
                '4',
                t('oauth_setup_env_title'),
                t('oauth_setup_env_desc'),
                [setupGuideCodeRow(t('oauth_setup_env_vars'), envSnippet)]
            ),
        );
        guide.appendChild(steps);
    }

    function setupGuideStep(index, titleText, descText, rows = []) {
        const step = document.createElement('section');
        step.className = 'oauth-setup-step';
        const badge = document.createElement('div');
        badge.className = 'oauth-setup-index';
        badge.textContent = index;
        const body = document.createElement('div');
        const title = document.createElement('div');
        title.className = 'oauth-setup-step-title';
        title.textContent = titleText;
        const desc = document.createElement('p');
        desc.className = 'oauth-setup-step-desc';
        desc.textContent = descText;
        body.append(title, desc);
        for (const row of rows) body.appendChild(row);
        step.append(badge, body);
        return step;
    }

    function setupGuideCodeRow(labelText, value, options = {}) {
        const row = document.createElement('div');
        row.className = 'oauth-setup-code-row';
        const label = document.createElement('div');
        label.className = 'oauth-setup-code-label';
        label.textContent = labelText;
        const code = document.createElement('pre');
        code.className = 'oauth-setup-code mono';
        code.textContent = value || '';
        row.append(label, code);
        if (options.action && options.actionLabel) {
            const action = smallButton(options.actionLabel, 'btn btn--sm btn--ghost', options.action);
            if (options.actionTitle) {
                action.title = options.actionTitle;
                action.setAttribute('aria-label', options.actionTitle);
            }
            row.appendChild(action);
            return row;
        }
        const actions = document.createElement('div');
        actions.className = 'oauth-config-actions';
        if (Array.isArray(options.actions)) {
            for (const item of options.actions) {
                const action = smallButton(item.label, 'btn btn--sm btn--ghost', item.action);
                if (item.title) {
                    action.title = item.title;
                    action.setAttribute('aria-label', item.title);
                }
                actions.appendChild(action);
            }
        }
        if (options.copy !== false) {
            const copy = smallButton(t('copy'), 'btn btn--sm btn--ghost', () => copyOAuthText(value || ''));
            actions.appendChild(copy);
        }
        if (actions.childElementCount) row.appendChild(actions);
        return row;
    }

    function setupGuideNote(text) {
        const note = document.createElement('div');
        note.className = 'oauth-setup-note';
        note.textContent = text;
        return note;
    }

    function copyOAuthText(value) {
        if (!value) return;
        navigator.clipboard?.writeText(value).then(
            () => toast.ok(t('copied_to_clipboard')),
            () => toast.err(t('copy_failed')),
        );
    }

    function renderOAuthScopePanel(status) {
        const list = $('oauth-scope-list');
        if (!list) return;
        list.innerHTML = '';
        if (!status?.config?.configured) return;
        list.appendChild(scopeSummaryNode(status));
        renderOAuthScopeDialog(status);
    }

    function scopeSummaryNode(status) {
        const summary = document.createElement('div');
        summary.className = 'oauth-scope-summary';

        const copy = document.createElement('div');
        copy.className = 'oauth-scope-summary-copy';
        const title = document.createElement('div');
        title.className = 'oauth-scope-panel-title';
        title.textContent = t('oauth_authorization_scopes');
        const meta = document.createElement('div');
        meta.className = 'oauth-scope-summary-text mono';
        const scopeText = selectedOAuthScopeString();
        meta.textContent = scopeText || t('oauth_scope_summary_empty');
        meta.title = scopeText;
        copy.append(title, meta);

        const actions = document.createElement('div');
        actions.className = 'oauth-scope-summary-actions';
        const count = document.createElement('span');
        count.className = 'oauth-badge';
        count.textContent = t('oauth_scope_count', { n: selectedOAuthScopes().length });
        const edit = iconButton(t('oauth_scope_edit'), iconEditSVG(), () => openOAuthScopeDialog(status));
        actions.append(count, edit);
        summary.append(copy, actions);
        return summary;
    }

    function openOAuthScopeDialog(status = state.oauth.status) {
        renderOAuthScopeDialog(status);
        window.cfui.openDialog?.($('oauth-scope-dialog'));
    }

    function renderOAuthScopeDialog(status = state.oauth.status) {
        const body = $('oauth-scope-dialog-body');
        if (!body) return;
        body.innerHTML = '';
        if (!status?.config?.configured) {
            body.appendChild(empty(t('oauth_not_configured')));
            return;
        }
        if (status?.logged_in) {
            body.appendChild(scopePillSection(t('oauth_current_scopes'), status.current?.scopes || []));
        }
        body.appendChild(permissionSelectorNode(status));
    }

    function scopePillSection(titleText, scopes) {
        const section = document.createElement('div');
        section.className = 'oauth-scope-panel';
        const title = document.createElement('div');
        title.className = 'oauth-scope-panel-title';
        title.textContent = titleText;
        section.appendChild(title);
        const pills = document.createElement('div');
        pills.className = 'oauth-scope-pills';
        if (!scopes.length) {
            pills.appendChild(empty(t('oauth_no_scopes')));
        }
        for (const scope of scopes) {
            const item = document.createElement('span');
            item.className = 'oauth-scope mono';
            item.textContent = scope;
            pills.appendChild(item);
        }
        section.appendChild(pills);
        return section;
    }

    function permissionSelectorNode(status) {
        const draft = ensurePermissionDraft(status);
        const section = document.createElement('div');
        section.className = 'oauth-permission-panel';

        const header = document.createElement('div');
        header.className = 'oauth-permission-header';
        const copy = document.createElement('div');
        const title = document.createElement('div');
        title.className = 'oauth-scope-panel-title';
        title.textContent = t('oauth_authorization_scopes');
        const hint = document.createElement('div');
        hint.className = 'oauth-row-meta';
        hint.textContent = t('oauth_authorization_scopes_hint');
        copy.append(title, hint);
        const actions = document.createElement('div');
        actions.className = 'oauth-permission-header-actions';
        const count = document.createElement('div');
        count.className = 'oauth-badge';
        count.textContent = t('oauth_scope_count', { n: selectedOAuthScopes().length });
        const copyMatrix = smallButton(t('oauth_scope_copy_matrix'), 'btn btn--sm btn--ghost', () => copyOAuthText(scopeMatrixText(status)));
        copyMatrix.title = t('oauth_scope_copy_matrix_title');
        copyMatrix.setAttribute('aria-label', t('oauth_scope_copy_matrix_title'));
        actions.append(count, copyMatrix);
        header.append(copy, actions);
        section.appendChild(header);

        for (const definition of oauthPermissionDefinitions) {
            const item = draft?.[definition.id] || { enabled: !!definition.required, write: false };
            section.appendChild(permissionRowNode(definition, item));
        }

        const preview = document.createElement('div');
        preview.className = 'oauth-scope-preview mono';
        preview.setAttribute('aria-label', t('oauth_scope_preview'));
        preview.textContent = selectedOAuthScopeString();
        section.appendChild(preview);
        return section;
    }

    function scopeMatrixText(status = state.oauth.status) {
        const draft = ensurePermissionDraft(status) || {};
        const configuredScopes = String(status?.config?.scopes || '').split(/\s+/).map((scope) => scope.trim()).filter(Boolean);
        const requestedScopes = selectedOAuthScopes();
        const currentScopes = Array.isArray(status?.current?.scopes) ? status.current.scopes : [];
        const capabilities = status?.current?.capabilities || status?.capabilities || {};
        const rows = oauthPermissionDefinitions.map((definition) => {
            const item = draft[definition.id] || {};
            const capability = capabilities[definition.id] || {};
            const enabled = !!item.enabled || !!definition.required;
            const requestedWrite = !!definition.writeOnly || !!item.write;
            return {
                id: definition.id,
                label: definition.title(),
                required: !!definition.required,
                next_login_enabled: enabled,
                next_login_write: enabled && requestedWrite,
                read_scopes: definition.readScopes,
                write_scopes: definition.writeScopes,
                accepted_write_scopes: definition.acceptedWriteScopes || definition.writeScopes,
                current_read: !!capability.read,
                current_write: !!capability.write,
            };
        });
        return JSON.stringify({
            type: 'cfui_oauth_scope_matrix',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            oauth_configured: !!status?.config?.configured,
            identity: {
                label: status?.current?.label || '',
                expires_at: status?.current?.expires_at || '',
                scopes: currentScopes,
            },
            configured_scope_template: configuredScopes,
            next_login_requested_scopes: requestedScopes,
            capabilities: rows,
        }, null, 2);
    }

    function permissionRowNode(definition, item) {
        const row = document.createElement('div');
        row.className = 'oauth-permission-row';
        row.setAttribute('data-enabled', String(!!item.enabled || !!definition.required));

        const label = document.createElement('label');
        label.className = 'oauth-permission-copy';
        const checkbox = document.createElement('input');
        checkbox.type = 'checkbox';
        checkbox.checked = !!item.enabled || !!definition.required;
        checkbox.disabled = !!definition.required;
        checkbox.addEventListener('change', () => setPermissionFeatureEnabled(definition.id, checkbox.checked));
        const text = document.createElement('span');
        const title = document.createElement('span');
        title.className = 'oauth-permission-title';
        title.textContent = definition.title();
        if (definition.required) {
            const required = document.createElement('span');
            required.className = 'oauth-permission-required';
            required.textContent = t('oauth_permission_required');
            title.appendChild(required);
        }
        const desc = document.createElement('span');
        desc.className = 'oauth-permission-desc';
        desc.textContent = definition.description();
        text.append(title, desc);
        label.append(checkbox, text);
        row.appendChild(label);

        if (definition.writeScopes.length && !definition.writeOnly) {
            const select = document.createElement('select');
            select.className = 'select select--sm oauth-permission-level';
            select.setAttribute('aria-label', t('oauth_permission_level'));
            const read = document.createElement('option');
            read.value = 'read';
            read.textContent = t('oauth_permission_readonly');
            const write = document.createElement('option');
            write.value = 'write';
            write.textContent = t('oauth_permission_readwrite');
            select.append(read, write);
            select.value = item.write ? 'write' : 'read';
            select.disabled = !item.enabled && !definition.required;
            select.addEventListener('change', () => setPermissionWriteEnabled(definition.id, select.value === 'write'));
            row.appendChild(select);
        }
        return row;
    }

    function renderOAuthIdentities(status) {
        const list = $('oauth-identity-list');
        if (!list) return;
        list.innerHTML = '';
        const sessions = Array.isArray(status?.sessions) ? status.sessions : [];
        const divider = document.querySelector('.oauth-sidebar-divider');
        if (!status?.config?.configured) {
            list.hidden = true;
            if (divider) divider.hidden = true;
            return;
        }
        list.hidden = false;
        if (divider) divider.hidden = false;

        const title = document.createElement('div');
        title.className = 'oauth-identity-title';
        title.textContent = t('oauth_identities');
        list.appendChild(title);

        if (!sessions.length) {
            list.appendChild(empty(t('oauth_no_identities')));
            return;
        }
        for (const session of sessions) {
            const item = document.createElement('div');
            item.className = 'oauth-identity-item';
            item.setAttribute('data-current', String(!!session.current));

            const main = document.createElement('button');
            main.type = 'button';
            main.className = 'oauth-identity-main';
            main.disabled = !!session.current;
            if (session.current) main.setAttribute('aria-current', 'true');
            main.setAttribute('aria-label', session.current ? t('oauth_current_identity') : t('oauth_identity_switch_to', { label: session.label || t('oauth_account') }));
            const name = document.createElement('div');
            name.className = 'oauth-list-title';
            name.textContent = session.label || t('oauth_account');
            const meta = document.createElement('div');
            meta.className = 'oauth-list-meta';
            const scopeCount = Array.isArray(session.scopes) ? session.scopes.length : 0;
            meta.textContent = [session.current ? t('oauth_identity_current') : '', t('oauth_scope_count', { n: scopeCount }), formatDate(session.expires_at)].filter(Boolean).join(' · ');
            main.append(name, meta);
            if (!session.current) main.addEventListener('click', (event) => switchOAuthSession(session.id, event.currentTarget));

            const actions = document.createElement('div');
            actions.className = 'oauth-identity-actions';
            const renameButton = iconButton(t('oauth_identity_rename'), iconEditSVG(), () => renameOAuthSession(session.id, session.label));
            const removeButton = iconButton(t('oauth_identity_remove'), iconTrashSVG(), () => removeOAuthSession(session.id, session.label), 'oauth-icon-danger');
            actions.append(renameButton, removeButton);

            item.append(main, actions);
            list.appendChild(item);
        }
    }

    function renderOAuthAccounts() {
        const list = $('oauth-account-list');
        if (!list) return;
        list.innerHTML = '';
        if (!state.oauth.status?.logged_in) {
            list.appendChild(empty(t('oauth_login_required')));
            return;
        }
        if (!state.oauth.accounts.length) {
            list.appendChild(empty(t('oauth_no_accounts')));
            return;
        }
        for (const account of state.oauth.accounts) {
            const button = document.createElement('button');
            button.type = 'button';
            button.className = 'oauth-list-item';
            button.setAttribute('aria-pressed', String(account.id === state.oauth.selectedAccountId));
            const title = document.createElement('div');
            title.className = 'oauth-list-title';
            title.textContent = account.name || account.id;
            const meta = document.createElement('div');
            meta.className = 'oauth-list-meta mono';
            meta.textContent = account.id;
            button.append(title, meta);
            button.addEventListener('click', async () => {
                state.oauth.selectedAccountId = account.id;
                state.oauth.selectedZoneId = '';
                resetOverview();
                resetZoneDetail();
                resetTunnelDetail();
                resetWorkerDetail();
                resetStorageDetail();
                resetSnippetDetail();
                resetWAFDetail();
                resetAnalyticsDetail();
                resetUsageDetail();
                state.oauth.dnsFormMode = '';
                state.oauth.dnsEditingId = '';
                renderOAuthAccounts();
                if (canRead('zones')) await loadOAuthZones();
                await loadOAuthCurrentResource();
                renderOAuthResource();
            });
            list.appendChild(button);
        }
    }

    function renderOAuthResource() {
        const body = $('oauth-resource-body');
        if (!body) return;
        body.innerHTML = '';
        ensureVisibleResource();
        updateOAuthResourceTabs();
        if (state.oauth.resource === 'status') {
            renderCloudflareStatus(body);
            return;
        }
        if (!state.oauth.status?.logged_in) {
            body.appendChild(empty(t('oauth_login_required')));
            return;
        }
        if (state.oauth.resource === 'overview') renderOverview(body);
        else if (state.oauth.resource === 'dns') renderDNS(body);
        else if (state.oauth.resource === 'tunnels') renderTunnels(body);
        else if (state.oauth.resource === 'workers') renderWorkers(body);
        else if (state.oauth.resource === 'storage') renderStorage(body);
        else if (state.oauth.resource === 'usage') renderUsage(body);
        else if (state.oauth.resource === 'snippets') renderSnippets(body);
        else if (state.oauth.resource === 'waf') renderWAF(body);
        else if (state.oauth.resource === 'analytics') renderAnalytics(body);
        else if (state.oauth.resource === 'settings') renderZoneSettings(body);
        else renderZones(body);
    }

    async function switchOAuthResource(resource) {
        state.oauth.resource = resource || 'zones';
        resetOAuthResourceDetail(state.oauth.resource);
        await loadOAuthCurrentResource();
        renderOAuthResource();
    }

    function resetOAuthResourceDetail(resource) {
        if (resource !== 'workers') resetWorkerDetail();
        if (resource !== 'storage') resetStorageDetail();
        if (resource !== 'tunnels') {
            state.oauth.tunnelCreateOpen = false;
            state.oauth.tunnelIngressCreateTunnelId = '';
            state.oauth.tunnelIngressEditing = null;
        }
        state.oauth.dnsFormMode = '';
        state.oauth.dnsEditingId = '';
        if (resource !== 'snippets') resetSnippetDetail();
        if (resource !== 'waf') {
            state.oauth.wafCreateOpen = false;
            state.oauth.wafEditingId = '';
            state.oauth.wafManagedExceptionCreateOpen = false;
            state.oauth.wafManagedExceptionEditingId = '';
            state.oauth.wafManagedOverrideCreateOpen = false;
            state.oauth.wafManagedOverrideEditingId = '';
        }
        if (resource !== 'analytics') resetAnalyticsDetail();
        if (resource !== 'usage') resetUsageDetail();
    }

    function renderOverview(body) {
        if (state.oauth.overviewLoading) {
            body.appendChild(empty(t('oauth_overview_loading')));
            return;
        }
        if (state.oauth.overviewError) {
            body.appendChild(empty(state.oauth.overviewError));
            return;
        }
        const overview = state.oauth.overview;
        if (!overview) {
            body.appendChild(empty(t('oauth_overview_empty')));
            return;
        }

        const metrics = new Map((Array.isArray(overview.metrics) ? overview.metrics : []).map((metric) => [metric.id, metric]));
        const context = document.createElement('section');
        context.className = 'oauth-section';
        const contextHead = document.createElement('div');
        contextHead.className = 'oauth-section-head';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_overview_context');
        const diagnostics = smallButton(t('oauth_overview_copy_diagnostics'), 'btn btn--sm btn--ghost', () => copyOAuthText(overviewDiagnosticsText(overview, metrics)));
        diagnostics.title = t('oauth_overview_copy_diagnostics_title');
        diagnostics.setAttribute('aria-label', t('oauth_overview_copy_diagnostics_title'));
        contextHead.append(heading, diagnostics);
        const account = overview.account || {};
        const zone = overview.zone || {};
        context.appendChild(contextHead);
        context.appendChild(rowNode(
            account.name || selectedAccountName() || account.id || t('oauth_account'),
            [
                account.id ? `${t('oauth_account')} ${account.id}` : '',
                zone.name ? `${t('oauth_zones')} ${zone.name}` : '',
            ].filter(Boolean).join(' · ') || t('oauth_overview_no_context'),
        ));
        body.appendChild(context);

        const quickActions = overviewQuickActionsNode(metrics);
        if (quickActions) body.appendChild(quickActions);

        const status = overview.status || null;
        if (status) {
            const statusNode = document.createElement('section');
            statusNode.className = 'oauth-status-summary';
            statusNode.dataset.state = statusIndicatorState(status.indicator);
            const title = document.createElement('div');
            title.className = 'oauth-status-summary-title';
            title.textContent = statusIndicatorLabel(status.indicator, status.description);
            const meta = document.createElement('div');
            meta.className = 'oauth-status-summary-meta';
            meta.textContent = t('oauth_overview_status');
            statusNode.append(title, meta);
            body.appendChild(statusNode);
        }

        const section = document.createElement('section');
        section.className = 'oauth-section';
        const metricHeading = document.createElement('h4');
        metricHeading.className = 'oauth-section-title';
        metricHeading.textContent = t('oauth_overview_metrics');
        const grid = document.createElement('div');
        grid.className = 'oauth-metric-grid';
        for (const [id, labelKey] of overviewMetricDefinitions) {
            const metric = metrics.get(id);
            const node = metricNode(t(labelKey), overviewMetricValue(metric));
            if (!metric?.available) node.dataset.state = 'unavailable';
            const label = node.querySelector('.oauth-metric-label');
            if (label && metric && !metric.available) {
                label.textContent = `${t(labelKey)} · ${overviewMetricErrorLabel(metric.error)}`;
            }
            if (label && metric?.limited) {
                label.textContent = `${t(labelKey)} · ${t('oauth_overview_limited', { n: metric.limit || 0 })}`;
            }
            grid.appendChild(node);
        }
        section.append(metricHeading, grid);
        body.appendChild(section);
    }

    function overviewQuickActionsNode(metrics) {
        const actions = [
            { resource: 'zones', label: t('oauth_zones'), metric: 'zones' },
            { resource: 'dns', label: t('oauth_dns'), metric: 'dns_records' },
            { resource: 'tunnels', label: t('oauth_tunnels'), metric: 'tunnels' },
            { resource: 'workers', label: t('oauth_workers'), metric: 'workers' },
            { resource: 'storage', label: t('oauth_storage'), metrics: [['r2_buckets', 'R2'], ['d1_databases', 'D1'], ['kv_namespaces', 'KV']] },
            { resource: 'usage', label: t('oauth_usage') },
            { resource: 'analytics', label: t('oauth_analytics') },
            { resource: 'waf', label: t('oauth_waf'), metric: 'waf_rules' },
            { resource: 'settings', label: t('oauth_zone_settings') },
            { resource: 'status', label: t('oauth_cloudflare_status') },
        ];
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_overview_quick_actions');
        const wrap = document.createElement('div');
        wrap.className = 'oauth-overview-actions';
        for (const action of actions) {
            const resourceDef = resourceDefinitions.find((definition) => definition.id === action.resource);
            if (!resourceDef || !canSeeResource(resourceDef)) continue;
            const disabledReason = overviewQuickActionDisabledReason(resourceDef);
            const button = document.createElement('button');
            button.type = 'button';
            button.className = 'oauth-overview-action';
            button.disabled = !!disabledReason;
            if (disabledReason) button.title = disabledReason;
            button.addEventListener('click', async () => {
                await switchOAuthResource(action.resource);
            });
            const title = document.createElement('span');
            title.className = 'oauth-overview-action-title';
            title.textContent = action.label;
            const meta = document.createElement('span');
            meta.className = 'oauth-overview-action-meta';
            meta.textContent = overviewQuickActionMeta(action, metrics, disabledReason);
            button.append(title, meta);
            wrap.appendChild(button);
        }
        if (!wrap.childElementCount) return null;
        section.append(heading, wrap);
        return section;
    }

    function overviewQuickActionDisabledReason(resourceDef) {
        if (resourceDef.needsAccount && !state.oauth.selectedAccountId) return t('oauth_overview_action_select_account');
        if (resourceDef.needsZone && !state.oauth.selectedZoneId) return t('oauth_overview_action_select_zone');
        return '';
    }

    function overviewQuickActionMeta(action, metrics, disabledReason) {
        if (disabledReason) return disabledReason;
        if (action.metric) {
            const metric = metrics.get(action.metric);
            if (metric?.available) return overviewMetricValue(metric);
            if (metric && !metric.available) return overviewMetricErrorLabel(metric.error);
        }
        if (Array.isArray(action.metrics)) {
            const parts = action.metrics.map(([id, label]) => {
                const metric = metrics.get(id);
                return metric?.available ? `${label} ${overviewMetricValue(metric)}` : '';
            }).filter(Boolean);
            if (parts.length) return parts.join(' · ');
        }
        return '';
    }

    function overviewDiagnosticsText(overview, metrics) {
        const status = state.oauth.status || {};
        const current = overview?.session || status.current || {};
        const selectedAccount = selectedAccountName();
        const selectedZone = selectedZoneName();
        const metricRows = {};
        for (const [id, labelKey] of overviewMetricDefinitions) {
            const metric = metrics.get(id) || {};
            metricRows[id] = {
                label: t(labelKey),
                feature: metric.feature || '',
                available: !!metric.available,
                value: metric.available ? Number(metric.value || 0) : null,
                error: metric.available ? '' : (metric.error || 'unavailable'),
                error_label: metric.available ? '' : overviewMetricErrorLabel(metric.error),
                limited: !!metric.limited,
                limit: metric.limit || 0,
            };
        }
        const capabilities = {};
        Object.keys(overview?.capabilities || status.capabilities || {}).sort().forEach((key) => {
            const capability = overview?.capabilities?.[key] || status.capabilities?.[key] || {};
            capabilities[key] = { read: !!capability.read, write: !!capability.write };
        });
        return JSON.stringify({
            type: 'cfui_oauth_overview_diagnostics',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            relay_callback_url: status.config?.relay_callback_url || '',
            oauth_configured: !!status.config?.configured,
            identity: {
                label: current.label || '',
                expires_at: current.expires_at || '',
                scopes: Array.isArray(current.scopes) ? current.scopes : [],
            },
            selected: {
                account_id: state.oauth.selectedAccountId || '',
                account_name: selectedAccount,
                zone_id: state.oauth.selectedZoneId || '',
                zone_name: selectedZone,
            },
            overview_context: {
                fetched_at: overview?.fetched_at || '',
                account_id: overview?.account?.id || '',
                account_name: overview?.account?.name || '',
                zone_id: overview?.zone?.id || '',
                zone_name: overview?.zone?.name || '',
                cloudflare_status_indicator: overview?.status?.indicator || '',
                cloudflare_status_description: overview?.status?.description || '',
            },
            capabilities,
            metrics: metricRows,
        }, null, 2);
    }

    function renderZones(body) {
        if (!state.oauth.zones.length) {
            body.appendChild(empty(t('oauth_no_zones')));
            return;
        }
        body.appendChild(zoneOverviewNode());
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_zones');
        section.appendChild(heading);
        for (const zone of state.oauth.zones) {
            const actions = canRead('dns') ? [
                smallButton(t('oauth_dns'), 'btn btn--sm btn--ghost', async () => {
                    const changed = state.oauth.selectedZoneId !== zone.id;
                    state.oauth.selectedZoneId = zone.id;
                    state.oauth.resource = 'dns';
                    if (changed) {
                        resetZoneDetail();
                        resetSelectedZoneResources();
                    }
                    await loadOAuthDNS();
                    renderOAuthResource();
                }),
            ] : [];
            const row = rowNode(zone.name, `${zone.status || ''} ${zone.id || ''}`.trim(), actions);
            row.setAttribute('data-selected', String(zone.id === state.oauth.selectedZoneId));
            row.addEventListener('click', async () => {
                const changed = state.oauth.selectedZoneId !== zone.id;
                state.oauth.selectedZoneId = zone.id;
                if (changed) {
                    resetZoneDetail();
                    resetSelectedZoneResources();
                }
                await loadOAuthZoneDetail(zone.id);
                renderOAuthResource();
            });
            section.appendChild(row);
        }
        body.appendChild(section);
    }

    function zoneOverviewNode() {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_zone_overview');
        section.appendChild(heading);
        if (!state.oauth.selectedZoneId) {
            section.appendChild(empty(t('oauth_select_zone')));
            return section;
        }
        if (state.oauth.zoneDetailLoading) {
            section.appendChild(empty(t('oauth_zone_loading')));
            return section;
        }
        if (state.oauth.zoneDetailError) {
            section.appendChild(empty(state.oauth.zoneDetailError));
            return section;
        }
        const zone = selectedZoneDetail();
        if (!zone) {
            section.appendChild(empty(t('oauth_zone_unavailable')));
            return section;
        }
        const grid = document.createElement('div');
        grid.className = 'oauth-metric-grid';
        const metrics = [
            metricNode(t('oauth_zone_status'), zone.status || t('oauth_zone_unavailable')),
            metricNode(t('oauth_zone_plan'), zone.plan?.name || zone.plan?.legacy_id || t('oauth_zone_unavailable')),
            metricNode(t('oauth_zone_type'), zone.type || t('oauth_zone_unavailable')),
            metricNode(t('oauth_zone_account'), zone.account?.name || selectedAccountName() || zone.account_id || t('oauth_zone_unavailable')),
        ];
        if (canRead('dns')) metrics.push(metricNode(t('oauth_zone_dns_records'), zoneDNSCountText()));
        grid.append(...metrics);
        section.appendChild(grid);
        const rows = [
            [t('oauth_zone_id'), zone.id],
            [t('oauth_zone_name_servers'), Array.isArray(zone.name_servers) ? zone.name_servers.join(' · ') : ''],
            [t('oauth_zone_original_name_servers'), Array.isArray(zone.original_name_servers) ? zone.original_name_servers.join(' · ') : ''],
            [t('oauth_zone_created'), formatDate(zone.created_on)],
            [t('oauth_zone_modified'), formatDate(zone.modified_on)],
            [t('oauth_zone_paused'), zone.paused ? t('yes') : t('no')],
        ].filter(([, value]) => value);
        for (const [label, value] of rows) section.appendChild(rowNode(label, value));
        return section;
    }

    function zoneDNSCountText() {
        if (state.oauth.zoneDNSCountLoading && state.oauth.zoneDNSCountZoneId === state.oauth.selectedZoneId) {
            return t('oauth_zone_dns_loading');
        }
        if (state.oauth.zoneDNSCountError) return t('oauth_zone_unavailable');
        if (state.oauth.zoneDNSCount != null) return formatNumber(state.oauth.zoneDNSCount);
        return t('oauth_zone_unavailable');
    }

    function dnsDiagnosticsText() {
        const status = state.oauth.status || {};
        const session = state.oauth.dnsSession || status.current || {};
        const zone = state.oauth.zoneDetail?.zone?.id === state.oauth.selectedZoneId
            ? state.oauth.zoneDetail.zone
            : state.oauth.zones.find((item) => item.id === state.oauth.selectedZoneId);
        const filtered = filteredDNSRecords();
        return JSON.stringify({
            type: 'cfui_oauth_dns_diagnostics',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            browser_path: window.location.pathname,
            contains_oauth_token: false,
            contains_refresh_token: false,
            contains_record_content: false,
            contains_record_comment: false,
            sensitive_fields_omitted: [
                'oauth_access_token',
                'oauth_refresh_token',
                'dns_record_content',
                'dns_record_comment',
            ],
            selected: {
                account_id: state.oauth.selectedAccountId || '',
                account_name: selectedAccountName(),
                zone_id: state.oauth.selectedZoneId || '',
                zone_name: selectedZoneName(),
                resource: state.oauth.resource || '',
            },
            zone: {
                id: zone?.id || '',
                name: zone?.name || '',
                status: zone?.status || '',
                paused: !!zone?.paused,
                type: zone?.type || '',
                name_servers: Array.isArray(zone?.name_servers) ? zone.name_servers : [],
                plan_name: zone?.plan?.name || '',
            },
            identity: {
                label: session.label || '',
                expires_at: session.expires_at || '',
                scopes: Array.isArray(session.scopes) ? session.scopes : [],
            },
            capability: {
                dns_read: canRead('dns'),
                dns_write: canWrite('dns'),
            },
            state: {
                records_loaded: state.oauth.dnsRecords.length,
                records_filtered: filtered.length,
                records_error: state.oauth.dnsRecordsError || '',
                mutation_error: state.oauth.dnsMutationError || '',
                filter_active: !!String(state.oauth.dnsFilter || '').trim(),
                form_mode: state.oauth.dnsFormMode || '',
                editing_record_id: state.oauth.dnsEditingId || '',
                count_loaded: state.oauth.zoneDNSCount != null,
                count_value: state.oauth.zoneDNSCount == null ? null : Number(state.oauth.zoneDNSCount),
                count_error: state.oauth.zoneDNSCountError || '',
                count_loading: !!state.oauth.zoneDNSCountLoading,
                count_zone_id: state.oauth.zoneDNSCountZoneId || '',
                scope_ready: canRead('dns'),
            },
            records: {
                loaded_count: state.oauth.dnsRecords.length,
                filtered_count: filtered.length,
                items: filtered.map(dnsRecordDiagnostics),
            },
            capabilities: oauthCapabilityDiagnostics(state.oauth.dnsCapabilities || status.capabilities || {}),
        }, null, 2);
    }

    function dnsRecordDiagnostics(record) {
        return {
            id: record?.id || '',
            type: record?.type || '',
            name: record?.name || '',
            ttl: Number(record?.ttl || 0),
            proxied: record?.proxied == null ? null : !!record.proxied,
            proxiable: !!record?.proxiable,
            content_included: false,
            content_length: String(record?.content || '').length,
            comment_included: false,
            comment_present: !!record?.comment,
            created_on: record?.created_on || '',
            modified_on: record?.modified_on || '',
        };
    }

    function renderDNS(body) {
        if (!state.oauth.selectedZoneId) {
            body.appendChild(empty(t('oauth_select_zone')));
            return;
        }
        const canEdit = canWrite('dns');
        const actions = [];
        if (canEdit) {
            actions.push({
                text: state.oauth.dnsFormMode === 'create' ? t('cancel') : t('oauth_dns_create'),
                className: state.oauth.dnsFormMode === 'create' ? 'btn btn--sm btn--ghost' : 'btn btn--sm',
                onClick: () => {
                    state.oauth.dnsEditingId = '';
                    state.oauth.dnsFormMode = state.oauth.dnsFormMode === 'create' ? '' : 'create';
                    renderOAuthResource();
                },
            });
        }
        actions.push({
            text: t('oauth_dns_copy_diagnostics'),
            className: 'btn btn--sm btn--ghost',
            title: t('oauth_dns_copy_diagnostics_title'),
            onClick: () => copyOAuthText(dnsDiagnosticsText()),
        });
        body.appendChild(resourceActionBar(selectedZoneName(), actions));
        if (canEdit && state.oauth.dnsFormMode === 'create') {
            body.appendChild(dnsFormNode(null));
        }
        if (state.oauth.dnsMutationError) {
            body.appendChild(empty(state.oauth.dnsMutationError));
        }
        if (state.oauth.dnsRecordsError) {
            body.appendChild(empty(state.oauth.dnsRecordsError));
            return;
        }
        if (!state.oauth.dnsRecords.length) {
            body.appendChild(empty(t('oauth_no_dns')));
            return;
        }
        body.appendChild(dnsFilterNode());
        const filteredRecords = filteredDNSRecords();
        if (!filteredRecords.length) {
            body.appendChild(empty(t('oauth_dns_no_matches')));
            return;
        }
        for (const record of filteredRecords) {
            const proxied = record.proxied == null ? '' : (record.proxied ? t('proxied') : t('dns_only'));
            if (canEdit && state.oauth.dnsEditingId === record.id) {
                body.appendChild(dnsFormNode(record));
                continue;
            }
            const actions = [];
            if (canEdit && dnsRecordSupportsProxy(record)) {
                actions.push(smallButton(
                    record.proxied ? t('oauth_dns_set_dns_only') : t('oauth_dns_set_proxied'),
                    'btn btn--sm btn--ghost',
                    (event) => toggleDNSProxy(record, event.currentTarget)
                ));
            }
            if (canEdit) {
                actions.push(smallButton(t('edit'), 'btn btn--sm btn--ghost', () => {
                    state.oauth.dnsFormMode = '';
                    state.oauth.dnsEditingId = record.id;
                    renderOAuthResource();
                }));
                actions.push(smallButton(t('delete'), 'btn btn--sm btn--ghost', () => deleteDNSRecord(record)));
            }
            body.appendChild(rowNode(`${record.type} ${record.name}`, `${record.content} · TTL ${record.ttl || 1} ${proxied}`.trim(), actions));
        }
    }

    function renderTunnels(body) {
        if (!state.oauth.selectedAccountId) {
            body.appendChild(empty(t('oauth_select_account')));
            return;
        }
        ensureTunnelConfigState();
        const canCreate = canWrite('tunnels');
        body.appendChild(resourceActionBar(t('oauth_tunnels'), canCreate ? {
            text: state.oauth.tunnelCreateOpen ? t('cancel') : t('oauth_tunnel_create'),
            className: state.oauth.tunnelCreateOpen ? 'btn btn--sm btn--ghost' : 'btn btn--sm btn--primary',
            onClick: () => {
                state.oauth.tunnelCreateOpen = !state.oauth.tunnelCreateOpen;
                renderOAuthResource();
            },
        } : null));
        if (!canCreate) {
            body.appendChild(empty(t('oauth_tunnels_readonly')));
        }
        if (canCreate && state.oauth.tunnelCreateOpen) {
            body.appendChild(tunnelCreateFormNode());
        }
        if (!state.oauth.tunnels.length) {
            body.appendChild(empty(t('oauth_no_tunnels')));
            return;
        }
        for (const tunnel of state.oauth.tunnels) {
            const localProfile = localProfileForTunnel(tunnel);
            const actions = [];
            if (localProfile && !localProfile.active) {
                const activate = smallButton(t('oauth_tunnel_activate_local_profile_action'), 'btn btn--sm btn--ghost', (event) => {
                    activateOAuthLocalTunnelProfile(localProfile, event.currentTarget);
                });
                activate.title = t('oauth_tunnel_activate_local_profile_action_hint');
                activate.setAttribute('aria-label', t('oauth_tunnel_activate_local_profile_action_hint'));
                actions.push(activate);
            }
            if (canCreate) {
                actions.push(iconButton(t('delete'), iconTrashSVG(), (event) => deleteOAuthTunnel(tunnel, localProfile, event.currentTarget), 'oauth-icon-action--danger'));
            }
            const connectionCount = tunnelConnectionCount(tunnel);
            const meta = [
                tunnelStatusLabel(tunnel.status),
                t('oauth_tunnel_connector_count', { n: connectionCount }),
                localProfile ? t('oauth_tunnel_linked_local_profile', { name: localProfile.name || localProfile.key }) : '',
                localProfile ? localTunnelRunnerLabel(localProfile) : '',
                tunnel.type || '',
                tunnel.id || '',
            ].filter(Boolean).join(' · ');
            const row = rowNode(tunnel.name || tunnel.id, meta, actions);
            const detail = tunnelDetailNode(tunnel, localProfile);
            if (detail) row.appendChild(detail);
            row.appendChild(tunnelIngressPanelNode(tunnel));
            body.appendChild(row);
        }
    }

    function tunnelCreateFormNode() {
        const form = document.createElement('form');
        form.className = 'oauth-form';
        const title = document.createElement('h4');
        title.className = 'oauth-section-title';
        title.textContent = t('oauth_tunnel_create');
        form.appendChild(title);

        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid oauth-form-grid--tunnel';
        const nameInput = textInput('', 'text');
        nameInput.required = true;
        nameInput.maxLength = 128;
        nameInput.placeholder = t('oauth_tunnel_name_placeholder');
        grid.appendChild(formField(t('oauth_tunnel_name'), nameInput));
        form.appendChild(grid);

        const options = document.createElement('div');
        options.className = 'oauth-form-options oauth-form-options--stacked';
        const saveOption = toggleOption(t('oauth_tunnel_save_local_profile'), true);
        const activateOption = toggleOption(t('oauth_tunnel_activate_local_profile'), false);
        const syncActivation = () => {
            activateOption.input.disabled = !saveOption.input.checked;
            if (!saveOption.input.checked) activateOption.input.checked = false;
        };
        saveOption.input.addEventListener('change', syncActivation);
        syncActivation();
        options.append(saveOption.node, activateOption.node);
        form.appendChild(options);

        const hint = document.createElement('div');
        hint.className = 'oauth-row-meta';
        hint.textContent = t('oauth_tunnel_create_hint');
        form.appendChild(hint);

        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        const cancelBtn = smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
            state.oauth.tunnelCreateOpen = false;
            renderOAuthResource();
        });
        const submitBtn = smallButton(t('oauth_tunnel_create'), 'btn btn--sm btn--primary');
        submitBtn.type = 'submit';
        actions.append(cancelBtn, submitBtn);
        form.appendChild(actions);

        form.addEventListener('submit', (event) => {
            event.preventDefault();
            createOAuthTunnel({
                name: nameInput.value.trim(),
                save_local_profile: saveOption.input.checked,
                activate_local: saveOption.input.checked && activateOption.input.checked,
            }, submitBtn);
        });
        return form;
    }

    function tunnelConnectionCount(tunnel) {
        if (Number.isFinite(Number(tunnel?.connection_count))) return Number(tunnel.connection_count);
        if (Array.isArray(tunnel?.connections)) return tunnel.connections.length;
        return Number(tunnel?.connections || 0) || 0;
    }

    function localProfileForTunnel(tunnel) {
        const tunnelID = String(tunnel?.id || '').trim();
        if (!tunnelID) return null;
        const accountID = String(state.oauth.selectedAccountId || '').trim();
        return (state.oauth.localTunnelProfiles || []).find((profile) => {
            if (String(profile?.tunnel_id || '').trim() !== tunnelID) return false;
            const profileAccount = String(profile?.account_id || '').trim();
            return !accountID || !profileAccount || profileAccount === accountID;
        }) || null;
    }

    function localTunnelRunnerLabel(profile) {
        if (!profile?.local_enabled) return t('oauth_tunnel_local_runner_disabled');
        if (profile.running) {
            return [t('oauth_tunnel_local_runner_running'), profile.protocol || ''].filter(Boolean).join(' · ');
        }
        const status = String(profile.status || '').trim();
        if (status === 'unavailable') return t('oauth_tunnel_local_runner_unavailable');
        if (status === 'error') return t('oauth_tunnel_local_runner_error');
        return t('oauth_tunnel_local_runner_stopped');
    }

    function tunnelIngressMutationMessage(tunnelID, key) {
        const base = t(key);
        const localProfile = localProfileForTunnel({ id: tunnelID });
        if (!localProfile) return base;
        if (localProfile.running) return `${base} ${t('oauth_tunnel_ingress_local_running_hint')}`;
        if (String(localProfile.status || '').trim() === 'unavailable') return `${base} ${t('oauth_tunnel_ingress_local_unknown_hint')}`;
        return `${base} ${t('oauth_tunnel_ingress_local_stopped_hint')}`;
    }

    function tunnelDiagnosticsText(tunnel, localProfile) {
        const tunnelID = String(tunnel?.id || '').trim();
        ensureTunnelConfigState();
        const config = state.oauth.tunnelConfigs?.[tunnelID] || null;
        const entries = Array.isArray(config?.entries) ? config.entries : [];
        const configError = state.oauth.tunnelConfigErrors?.[tunnelID] || '';
        const configLoading = !!state.oauth.tunnelConfigLoading?.[tunnelID];
        return JSON.stringify({
            type: 'cfui_oauth_tunnel_diagnostics',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            browser_path: window.location.pathname,
            contains_oauth_token: false,
            contains_connector_token: false,
            config_loaded: !!config,
            config_loading: configLoading,
            config_error: configError,
            sensitive_fields_omitted: [
                'oauth_access_token',
                'oauth_refresh_token',
                'connector_token',
                'tunnel_secret',
            ],
            selected: {
                account_id: state.oauth.selectedAccountId || '',
                account_name: selectedAccountName(),
            },
            capability: {
                tunnels_read: canRead('tunnels'),
                tunnels_write: canWrite('tunnels'),
            },
            tunnel: {
                id: tunnelID,
                name: tunnel?.name || '',
                status: tunnel?.status || '',
                status_label: tunnelStatusLabel(tunnel?.status),
                type: tunnel?.type || '',
                remote_config: !!tunnel?.remote_config,
                created_at: tunnel?.created_at || '',
                deleted_at: tunnel?.deleted_at || '',
                connections_active_at: tunnel?.connections_active_at || '',
                connections_inactive_at: tunnel?.connections_inactive_at || '',
                connection_count: tunnelConnectionCount(tunnel),
                connections: tunnelConnectionsDiagnostics(tunnel),
            },
            local_profile: localProfileDiagnostics(localProfile),
            config_state: {
                loaded: !!config,
                loading: configLoading,
                error: configError,
            },
            remote_config: {
                tunnel_id: config?.tunnel_id || tunnelID,
                version: Number.isFinite(Number(config?.version)) ? Number(config.version) : 0,
                warp_routing_enabled: !!config?.warp_routing_enabled,
                ingress_count: entries.length,
                ingress: entries.map((entry) => tunnelIngressDiagnostics(entry, entries)),
            },
        }, null, 2);
    }

    function tunnelConnectionsDiagnostics(tunnel) {
        const connections = Array.isArray(tunnel?.connections) ? tunnel.connections : [];
        return connections.map((connection, index) => ({
            index,
            id: connection?.id || '',
            colo_name: connection?.colo_name || '',
            client_version: connection?.client_version || '',
            opened_at: connection?.opened_at || '',
            origin_ip: connection?.origin_ip || '',
            is_pending_reconnect: !!connection?.is_pending_reconnect,
            summary: tunnelConnectionSummary(connection || {}),
        }));
    }

    function localProfileDiagnostics(profile) {
        if (!profile) return null;
        return {
            key: profile.key || '',
            name: profile.name || '',
            account_id: profile.account_id || '',
            tunnel_id: profile.tunnel_id || '',
            local_enabled: !!profile.local_enabled,
            remote_management_enabled: !!profile.remote_management_enabled,
            active: !!profile.active,
            running: !!profile.running,
            status: profile.status || '',
            protocol: profile.protocol || '',
            runner_label: localTunnelRunnerLabel(profile),
        };
    }

    function tunnelIngressDiagnostics(entry, entries) {
        return {
            index: Number.isFinite(Number(entry?.index)) ? Number(entry.index) : 0,
            hostname: entry?.hostname || '',
            path: entry?.path || '',
            service: entry?.service || '',
            no_tls_verify: !!entry?.no_tls_verify,
            http_host_header: entry?.http_host_header || '',
            origin_server_name: entry?.origin_server_name || '',
            catch_all: isOAuthTunnelCatchAllRule(entry || {}, entries),
        };
    }

    function tunnelDetailNode(tunnel, localProfile) {
        const rows = [];
        const add = (label, value) => {
            if (value == null || value === '') return;
            rows.push([label, value]);
        };
        add(t('oauth_tunnel_id'), tunnel.id || '');
        add(t('oauth_tunnel_status'), tunnelStatusLabel(tunnel.status));
        add(t('oauth_tunnel_type'), tunnel.type || '');
        add(t('oauth_tunnel_remote_config'), tunnel.remote_config ? t('oauth_enabled_state') : t('oauth_disabled'));
        add(t('oauth_tunnel_created'), formatDate(tunnel.created_at));
        add(t('oauth_tunnel_deleted'), formatDate(tunnel.deleted_at));
        add(t('oauth_tunnel_connections_active_at'), formatDate(tunnel.connections_active_at));
        add(t('oauth_tunnel_connections_inactive_at'), formatDate(tunnel.connections_inactive_at));
        if (localProfile) {
            add(t('oauth_tunnel_local_profile'), localProfile.name || localProfile.key);
            add(t('oauth_tunnel_local_state'), localProfile.active ? t('oauth_tunnel_local_profile_activated') : t('oauth_tunnel_local_profile_saved'));
            add(t('oauth_tunnel_local_runner'), localTunnelRunnerLabel(localProfile));
        }
        const connections = Array.isArray(tunnel.connections) ? tunnel.connections : [];
        add(t('oauth_tunnel_connections'), connections.length ? connections.map(tunnelConnectionSummary).join('\n') : t('oauth_tunnel_no_active_connections'));
        if (!rows.length) return null;

        const detail = document.createElement('dl');
        detail.className = 'oauth-row-detail';
        for (const [label, value] of rows) {
            const item = document.createElement('div');
            item.className = 'oauth-row-detail-item';
            const term = document.createElement('dt');
            term.textContent = label;
            const desc = document.createElement('dd');
            desc.textContent = value;
            item.append(term, desc);
            detail.appendChild(item);
        }
        return detail;
    }

    function tunnelIngressPanelNode(tunnel) {
        const tunnelID = String(tunnel?.id || '').trim();
        const section = document.createElement('section');
        section.className = 'oauth-section oauth-tunnel-ingress';
        const header = document.createElement('div');
        header.className = 'oauth-action-bar oauth-action-bar--subtle';
        const copy = document.createElement('div');
        const title = document.createElement('div');
        title.className = 'oauth-action-title';
        title.textContent = t('oauth_tunnel_ingress');
        const meta = document.createElement('div');
        meta.className = 'oauth-action-meta';
        const config = state.oauth.tunnelConfigs?.[tunnelID];
        const loading = !!state.oauth.tunnelConfigLoading?.[tunnelID];
        const error = state.oauth.tunnelConfigErrors?.[tunnelID] || '';
        meta.textContent = config ? t('oauth_tunnel_ingress_rules_meta', {
            n: (config.entries || []).length,
            version: config.version || 0,
        }) : t('oauth_tunnel_ingress_not_loaded');
        copy.append(title, meta);
        header.appendChild(copy);

        const actions = document.createElement('div');
        actions.className = 'oauth-row-actions';
        const diagnostics = smallButton(t('oauth_tunnel_copy_diagnostics'), 'btn btn--sm btn--ghost', () => copyOAuthText(tunnelDiagnosticsText(tunnel, localProfileForTunnel(tunnel))));
        diagnostics.title = t('oauth_tunnel_copy_diagnostics_title');
        diagnostics.setAttribute('aria-label', t('oauth_tunnel_copy_diagnostics_title'));
        actions.appendChild(diagnostics);
        actions.appendChild(smallButton(
            config ? t('oauth_tunnel_ingress_refresh') : t('oauth_tunnel_ingress_load'),
            'btn btn--sm btn--ghost',
            (event) => loadOAuthTunnelConfig(tunnelID, event.currentTarget)
        ));
        if (config && canWrite('tunnels')) {
            actions.appendChild(smallButton(
                state.oauth.tunnelIngressCreateTunnelId === tunnelID ? t('cancel') : t('oauth_tunnel_ingress_create'),
                state.oauth.tunnelIngressCreateTunnelId === tunnelID ? 'btn btn--sm btn--ghost' : 'btn btn--sm btn--primary',
                () => {
                    state.oauth.tunnelIngressCreateTunnelId = state.oauth.tunnelIngressCreateTunnelId === tunnelID ? '' : tunnelID;
                    state.oauth.tunnelIngressEditing = null;
                    renderOAuthResource();
                }
            ));
        }
        header.appendChild(actions);
        section.appendChild(header);

        if (!canWrite('tunnels')) section.appendChild(empty(t('oauth_tunnel_ingress_readonly')));
        if (loading) {
            section.appendChild(empty(t('oauth_tunnel_ingress_loading')));
            return section;
        }
        if (error) section.appendChild(empty(t('oauth_tunnel_ingress_error', { error })));
        if (!config) return section;
        if (canWrite('tunnels') && state.oauth.tunnelIngressCreateTunnelId === tunnelID) {
            section.appendChild(tunnelIngressFormNode(tunnelID, null));
        }

        const entries = Array.isArray(config.entries) ? config.entries : [];
        if (!entries.length) {
            section.appendChild(empty(t('oauth_tunnel_ingress_empty')));
            return section;
        }
        const list = document.createElement('div');
        list.className = 'oauth-ingress-list';
        list.dataset.tunnelId = tunnelID;
        for (const entry of entries) {
            const editing = state.oauth.tunnelIngressEditing?.tunnel_id === tunnelID && state.oauth.tunnelIngressEditing.index === entry.index;
            if (editing && canWrite('tunnels')) {
                list.appendChild(tunnelIngressFormNode(tunnelID, entry));
            } else {
                list.appendChild(tunnelIngressRuleNode(tunnelID, entry, entries));
            }
        }
        section.appendChild(list);
        if (canWrite('tunnels')) bindOAuthTunnelIngressDragSort(list);
        return section;
    }

    function tunnelIngressRuleNode(tunnelID, entry, entries) {
        const catchAll = isOAuthTunnelCatchAllRule(entry, entries);
        const row = document.createElement('div');
        row.className = catchAll ? 'oauth-ingress-rule rule--fixed' : 'oauth-ingress-rule rule--draggable';
        row.dataset.ruleIndex = String(entry.index);
        if (catchAll) row.dataset.catchAll = 'true';
        if (canWrite('tunnels') && !catchAll) {
            row.dataset.draggable = 'true';
            row.draggable = true;
            row.title = t('tunnel_rule_reorder_handle');
        }

        const body = document.createElement('div');
        body.className = 'oauth-ingress-rule__body';
        const title = document.createElement('div');
        title.className = 'oauth-ingress-rule__title';
        title.textContent = entry.hostname || t('catch_all_rule');
        const detail = document.createElement('div');
        detail.className = 'oauth-row-meta';
        detail.textContent = tunnelIngressRuleSummary(entry);
        body.append(title, detail);

        const actions = document.createElement('div');
        actions.className = 'oauth-row-actions';
        if (canWrite('tunnels') && !catchAll) {
            actions.appendChild(iconTextButton(t('tunnel_rule_move_up'), '↑', () => moveOAuthTunnelIngress(tunnelID, entry.index, -1)));
            actions.appendChild(iconTextButton(t('tunnel_rule_move_down'), '↓', () => moveOAuthTunnelIngress(tunnelID, entry.index, 1)));
            actions.appendChild(smallButton(t('edit'), 'btn btn--sm btn--ghost', () => {
                state.oauth.tunnelIngressCreateTunnelId = '';
                state.oauth.tunnelIngressEditing = { tunnel_id: tunnelID, index: entry.index };
                renderOAuthResource();
            }));
            actions.appendChild(smallButton(t('delete'), 'btn btn--sm btn--danger', (event) => deleteOAuthTunnelIngress(tunnelID, entry, event.currentTarget)));
        }
        row.append(oauthTunnelIngressDragHandle(catchAll), body, actions);
        return row;
    }

    function tunnelIngressFormNode(tunnelID, entry) {
        const form = document.createElement('form');
        form.className = 'oauth-form oauth-tunnel-ingress-form';
        form.dataset.ruleIndex = entry ? String(entry.index) : '';
        const title = document.createElement('h4');
        title.className = 'oauth-section-title';
        title.textContent = entry ? t('oauth_tunnel_ingress_edit') : t('oauth_tunnel_ingress_create');
        form.appendChild(title);

        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid oauth-form-grid--ingress';
        const hostInput = textInput(entry?.hostname || '', 'text');
        hostInput.placeholder = t('oauth_tunnel_ingress_hostname_placeholder');
        grid.appendChild(formField(t('oauth_tunnel_ingress_hostname'), hostInput));

        const pathInput = textInput(entry?.path || '', 'text');
        pathInput.placeholder = t('oauth_tunnel_ingress_path_placeholder');
        grid.appendChild(formField(t('oauth_tunnel_ingress_path'), pathInput));

        const service = splitOAuthTunnelService(entry?.service || '');
        const typeSelect = document.createElement('select');
        typeSelect.className = 'form-select';
        for (const value of ['http', 'https', 'ssh', 'rdp', 'tcp', 'unix', 'http_status', 'raw']) {
            const option = document.createElement('option');
            option.value = value;
            option.textContent = tunnelServiceTypeLabel(value);
            typeSelect.appendChild(option);
        }
        typeSelect.value = service.type;
        grid.appendChild(formField(t('oauth_tunnel_ingress_service_type'), typeSelect));

        const serviceInput = textInput(service.value, 'text');
        serviceInput.required = true;
        serviceInput.placeholder = tunnelServicePlaceholder(typeSelect.value);
        typeSelect.addEventListener('change', () => { serviceInput.placeholder = tunnelServicePlaceholder(typeSelect.value); });
        grid.appendChild(formField(t('oauth_tunnel_ingress_service'), serviceInput));
        form.appendChild(grid);

        const options = document.createElement('div');
        options.className = 'oauth-form-options oauth-form-options--stacked';
        const noTLS = toggleOption(t('oauth_tunnel_ingress_no_tls_verify'), !!entry?.no_tls_verify);
        options.appendChild(noTLS.node);
        form.appendChild(options);

        const advanced = document.createElement('details');
        advanced.className = 'oauth-form-section';
        const summary = document.createElement('summary');
        summary.className = 'oauth-form-section-title';
        summary.textContent = t('oauth_tunnel_ingress_advanced');
        advanced.appendChild(summary);
        const advancedGrid = document.createElement('div');
        advancedGrid.className = 'oauth-form-grid oauth-form-grid--ingress-advanced';
        const hostHeaderInput = textInput(entry?.http_host_header || '', 'text');
        hostHeaderInput.placeholder = 'origin.internal';
        advancedGrid.appendChild(formField(t('oauth_tunnel_ingress_http_host_header'), hostHeaderInput));
        const originServerNameInput = textInput(entry?.origin_server_name || '', 'text');
        originServerNameInput.placeholder = 'origin.example.com';
        advancedGrid.appendChild(formField(t('oauth_tunnel_ingress_origin_server_name'), originServerNameInput));
        advanced.appendChild(advancedGrid);
        form.appendChild(advanced);

        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        actions.appendChild(smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
            if (entry) state.oauth.tunnelIngressEditing = null;
            else state.oauth.tunnelIngressCreateTunnelId = '';
            renderOAuthResource();
        }));
        const submitBtn = smallButton(entry ? t('update_rule') : t('add_rule'), 'btn btn--sm btn--primary');
        submitBtn.type = 'submit';
        actions.appendChild(submitBtn);
        form.appendChild(actions);

        form.addEventListener('submit', (event) => {
            event.preventDefault();
            const payload = {
                hostname: hostInput.value.trim(),
                path: pathInput.value.trim(),
                service: buildOAuthTunnelService(typeSelect.value, serviceInput.value),
                no_tls_verify: noTLS.input.checked,
                http_host_header: hostHeaderInput.value.trim(),
                origin_server_name: originServerNameInput.value.trim(),
            };
            submitOAuthTunnelIngress(tunnelID, entry ? Number(entry.index) : null, payload, submitBtn);
        });
        return form;
    }

    function iconTextButton(label, text, onClick) {
        const button = smallButton(text, 'btn btn--sm btn--ghost btn--square', onClick);
        button.title = label;
        button.setAttribute('aria-label', label);
        return button;
    }

    function oauthTunnelIngressDragHandle(disabled = false) {
        const handle = document.createElement('span');
        handle.className = 'rule-drag-handle';
        handle.title = disabled ? '' : t('tunnel_rule_reorder_handle');
        handle.setAttribute('aria-hidden', 'true');
        handle.innerHTML = '<svg viewBox="0 0 16 20" fill="currentColor" aria-hidden="true"><circle cx="5" cy="4" r="1.4"></circle><circle cx="11" cy="4" r="1.4"></circle><circle cx="5" cy="10" r="1.4"></circle><circle cx="11" cy="10" r="1.4"></circle><circle cx="5" cy="16" r="1.4"></circle><circle cx="11" cy="16" r="1.4"></circle></svg>';
        return handle;
    }

    function isOAuthTunnelCatchAllRule(entry, entries = []) {
        const last = entries[entries.length - 1];
        return !!last && entry.index === last.index && !String(entry.hostname || '').trim() && !String(entry.path || '').trim() && !!String(entry.service || '').trim();
    }

    function tunnelIngressRuleSummary(entry) {
        const parts = [
            `${entry.path || '/'} -> ${entry.service || ''}`,
            entry.no_tls_verify ? t('no_tls_verify_detail') : '',
            entry.http_host_header ? `${t('oauth_tunnel_ingress_http_host_header')} ${entry.http_host_header}` : '',
            entry.origin_server_name ? `${t('oauth_tunnel_ingress_origin_server_name')} ${entry.origin_server_name}` : '',
        ];
        return parts.filter(Boolean).join(' · ');
    }

    function bindOAuthTunnelIngressDragSort(list) {
        list.ondragstart = (event) => {
            if (event.target.closest('button, input, select, textarea, details')) {
                event.preventDefault();
                return;
            }
            const row = event.target.closest('.oauth-ingress-rule[data-draggable="true"]');
            if (!row) return;
            event.dataTransfer.effectAllowed = 'move';
            event.dataTransfer.setData('text/plain', row.dataset.ruleIndex || '');
            row.classList.add('rule--dragging');
        };
        list.ondragover = (event) => {
            const dragging = list.querySelector('.rule--dragging');
            if (!dragging) return;
            event.preventDefault();
            const before = oauthTunnelIngressDragTarget(list, event.clientY);
            const catchAll = list.querySelector('.oauth-ingress-rule[data-catch-all="true"]');
            if (before) list.insertBefore(dragging, before);
            else if (catchAll) list.insertBefore(dragging, catchAll);
            else list.appendChild(dragging);
        };
        list.ondrop = async (event) => {
            const dragging = list.querySelector('.rule--dragging');
            if (!dragging) return;
            event.preventDefault();
            dragging.classList.remove('rule--dragging');
            await reorderOAuthTunnelIngress(list.dataset.tunnelId, oauthTunnelIngressOrderFromDOM(list));
        };
        list.ondragend = () => {
            list.querySelector('.rule--dragging')?.classList.remove('rule--dragging');
        };
    }

    function oauthTunnelIngressDragTarget(list, y) {
        const rows = [...list.querySelectorAll('.oauth-ingress-rule[data-draggable="true"]:not(.rule--dragging)')];
        return rows.reduce((closest, row) => {
            const box = row.getBoundingClientRect();
            const offset = y - box.top - box.height / 2;
            if (offset < 0 && offset > closest.offset) return { offset, row };
            return closest;
        }, { offset: Number.NEGATIVE_INFINITY, row: null }).row;
    }

    function oauthTunnelIngressOrderFromDOM(list) {
        return [...list.querySelectorAll('.oauth-ingress-rule[data-rule-index]')]
            .map((row) => Number(row.dataset.ruleIndex))
            .filter((index) => Number.isInteger(index));
    }

    function splitOAuthTunnelService(value) {
        const service = String(value || '').trim();
        if (service.startsWith('http_status:')) return { type: 'http_status', value: service.slice(12) };
        const match = service.match(/^([a-z_]+):\/\/(.+)$/i);
        if (!match) return { type: 'raw', value: service };
        const type = ['http', 'https', 'ssh', 'rdp', 'tcp', 'unix'].includes(match[1]) ? match[1] : 'raw';
        return type === 'raw' ? { type, value: service } : { type, value: match[2] };
    }

    function buildOAuthTunnelService(type, value) {
        value = String(value || '').trim();
        if (type === 'raw') return value;
        if (type === 'http_status') return value.startsWith('http_status:') ? value : `http_status:${value || '404'}`;
        return value.startsWith(`${type}:`) ? value : `${type}://${value}`;
    }

    function tunnelServicePlaceholder(type) {
        return {
            http: 'localhost:8080',
            https: 'localhost:8443',
            ssh: 'localhost:22',
            rdp: 'localhost:3389',
            tcp: 'localhost:5432',
            unix: '/var/run/app.sock',
            http_status: '404',
            raw: 'http://localhost:8080',
        }[type] || 'localhost:8080';
    }

    function tunnelServiceTypeLabel(type) {
        if (type === 'http_status') return 'HTTP status';
        if (type === 'raw') return 'Raw';
        return String(type || '').toUpperCase();
    }

    function tunnelConnectionSummary(connection) {
        const parts = [
            connection.colo_name || connection.id || '',
            connection.client_version ? t('oauth_tunnel_client_version', { version: connection.client_version }) : '',
            connection.opened_at ? t('oauth_tunnel_opened_at', { time: formatDate(connection.opened_at) }) : '',
            connection.origin_ip || '',
            connection.is_pending_reconnect ? t('oauth_tunnel_pending_reconnect') : '',
        ];
        return parts.filter(Boolean).join(' · ');
    }

    function tunnelStatusLabel(status) {
        const normalized = String(status || 'unknown').replaceAll('-', '_');
        const key = `oauth_tunnel_status_${normalized}`;
        const label = t(key);
        return label === key ? (status || t('oauth_tunnel_status_unknown')) : label;
    }

    function renderWorkers(body) {
        if (!state.oauth.selectedAccountId) {
            body.appendChild(empty(t('oauth_select_account')));
            return;
        }
        if (state.oauth.selectedWorkerId) {
            renderWorkerDetail(body);
            return;
        }
        if (!state.oauth.workers.length) {
            body.appendChild(empty(t('oauth_no_workers')));
            return;
        }
        body.appendChild(resourceActionBar(t('oauth_workers'), null));
        for (const worker of state.oauth.workers) {
            const details = [
                worker.size ? `${formatBytes(worker.size)}` : '',
                worker.logpush == null ? '' : (worker.logpush ? 'Logpush' : t('oauth_logpush_off')),
                worker.modified_on ? `${t('oauth_modified')} ${formatDate(worker.modified_on)}` : '',
            ].filter(Boolean).join(' · ');
            const row = rowNode(worker.id, details);
            row.addEventListener('click', async () => {
                state.oauth.selectedWorkerId = worker.id;
                state.oauth.workerDetail = null;
                renderOAuthResource();
                await loadWorkerDetail(worker.id);
                renderOAuthResource();
            });
            body.appendChild(row);
        }
    }

    function renderWorkerDetail(body) {
        const detail = state.oauth.workerDetail;
        const title = detail?.worker?.id || state.oauth.selectedWorkerId || t('oauth_worker_detail');
        body.appendChild(workerBackHeader(title, state.oauth.selectedAccountId));
        if (!detail) {
            body.appendChild(empty(t('oauth_worker_loading')));
            return;
        }

        const worker = detail.worker || {};
        const settings = detail.settings || {};
        const metaRows = [
            [t('oauth_worker_size'), worker.size ? formatBytes(worker.size) : ''],
            [t('oauth_worker_created'), formatDate(worker.created_on)],
            [t('oauth_worker_modified'), formatDate(worker.modified_on)],
            [t('oauth_worker_deployment'), worker.deployment_id || ''],
            [t('oauth_worker_source'), worker.last_deployed_from || ''],
            [t('oauth_worker_etag'), settings.etag || ''],
        ].filter((row) => row[1]);
        body.appendChild(workerInfoSection(t('oauth_worker_metadata'), metaRows));

        const settingsRows = [
            [t('oauth_worker_logpush'), settings.logpush == null ? t('oauth_readonly') : (settings.logpush ? t('oauth_enabled_state') : t('oauth_disabled'))],
            [t('oauth_worker_placement'), [settings.placement_mode, settings.placement_status].filter(Boolean).join(' · ')],
        ].filter((row) => row[1]);
        body.appendChild(workerInfoSection(t('oauth_worker_settings'), settingsRows));

        body.appendChild(workerMetricsSection());

        const consumers = Array.isArray(settings.tail_consumers) ? settings.tail_consumers : [];
        const tailSection = document.createElement('section');
        tailSection.className = 'oauth-section';
        const tailHeading = document.createElement('h4');
        tailHeading.className = 'oauth-section-title';
        tailHeading.textContent = t('oauth_worker_tail_consumers');
        tailSection.appendChild(tailHeading);
        if (!consumers.length) {
            tailSection.appendChild(empty(t('oauth_worker_no_tail_consumers')));
        } else {
            for (const consumer of consumers) {
                tailSection.appendChild(rowNode(consumer.service, [consumer.environment, consumer.namespace].filter(Boolean).join(' · ')));
            }
        }
        body.appendChild(tailSection);

        body.appendChild(workerTailSection());
        body.appendChild(workerScriptSection(detail.content || {}));
    }

    function workerDiagnosticsText() {
        const status = state.oauth.status || {};
        const detail = state.oauth.workerDetail || null;
        const metrics = state.oauth.workerMetrics || null;
        const session = detail?.session || metrics?.session || status.current || {};
        const worker = detail?.worker || {};
        const settings = detail?.settings || {};
        const content = detail?.content || {};
        return JSON.stringify({
            type: 'cfui_oauth_worker_diagnostics',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            browser_path: window.location.pathname,
            contains_oauth_token: false,
            contains_refresh_token: false,
            contains_worker_script: false,
            contains_tail_events: false,
            sensitive_fields_omitted: [
                'oauth_access_token',
                'oauth_refresh_token',
                'worker_script_content',
                'worker_tail_websocket_url',
                'tail_event_text',
            ],
            selected: {
                account_id: state.oauth.selectedAccountId || '',
                account_name: selectedAccountName(),
                script_name: state.oauth.selectedWorkerId || worker.id || '',
            },
            identity: {
                label: session.label || '',
                expires_at: session.expires_at || '',
                scopes: Array.isArray(session.scopes) ? session.scopes : [],
            },
            capability: {
                workers_read: canRead('workers'),
                analytics_read: canRead('analytics'),
                workers_tail_read: canRead('workers_tail'),
            },
            detail_state: {
                loaded: !!detail,
            },
            worker: detail ? {
                id: worker.id || '',
                size: Number(worker.size || 0),
                created_on: worker.created_on || '',
                modified_on: worker.modified_on || '',
                logpush: worker.logpush == null ? null : !!worker.logpush,
                last_deployed_from: worker.last_deployed_from || '',
                deployment_id: worker.deployment_id || '',
            } : null,
            settings: detail ? {
                etag: settings.etag || '',
                logpush: settings.logpush == null ? null : !!settings.logpush,
                placement_mode: settings.placement_mode || '',
                placement_status: settings.placement_status || '',
                tail_consumers: workerTailConsumersDiagnostics(settings.tail_consumers),
            } : null,
            script_preview: detail ? {
                encoding: content.encoding || '',
                bytes: Number(content.bytes || 0),
                truncated: !!content.truncated,
                value_included: false,
            } : null,
            metrics_state: {
                loaded: !!metrics,
                loading: !!state.oauth.workerMetricsLoading,
                error: state.oauth.workerMetricsError || '',
                range: state.oauth.workerMetricsRange || '',
                scope_ready: canRead('analytics'),
            },
            metrics: metrics ? workerMetricsDiagnostics(metrics) : null,
            tail_state: workerTailDiagnostics(),
            capabilities: oauthCapabilityDiagnostics(detail?.capabilities || metrics?.capabilities || status.capabilities || {}),
        }, null, 2);
    }

    function workerTailConsumersDiagnostics(consumers) {
        return (Array.isArray(consumers) ? consumers : []).map((consumer) => ({
            service: consumer?.service || '',
            environment: consumer?.environment || '',
            namespace: consumer?.namespace || '',
        }));
    }

    function workerMetricsDiagnostics(metrics) {
        const summary = metrics?.summary || {};
        return {
            range: metrics?.range || '',
            since: metrics?.since || '',
            until: metrics?.until || '',
            summary: {
                requests: Number(summary.requests || 0),
                errors: Number(summary.errors || 0),
                subrequests: Number(summary.subrequests || 0),
                cpu_time_us: summary.cpu_time_us == null ? null : Number(summary.cpu_time_us),
                cpu_time_p50_us: summary.cpu_time_p50_us == null ? null : Number(summary.cpu_time_p50_us),
                cpu_time_p99_us: summary.cpu_time_p99_us == null ? null : Number(summary.cpu_time_p99_us),
            },
            status_breakdown: (Array.isArray(metrics?.status_breakdown) ? metrics.status_breakdown : []).map((item) => ({
                status: item?.status || '',
                status_label: workerStatusLabel(item?.status || ''),
                requests: Number(item?.requests || 0),
            })),
            series: (Array.isArray(metrics?.series) ? metrics.series : []).map((point) => ({
                time: point?.time || '',
                requests: Number(point?.requests || 0),
                errors: Number(point?.errors || 0),
            })),
        };
    }

    function workerTailDiagnostics() {
        const lines = Array.isArray(state.oauth.workerTailLines) ? state.oauth.workerTailLines : [];
        const levels = {};
        for (const line of lines) {
            const level = String(line?.level || 'info');
            levels[level] = (levels[level] || 0) + 1;
        }
        return {
            can_read: canRead('workers_tail'),
            connecting: !!state.oauth.workerTailConnecting,
            connected: !!state.oauth.workerTailConnected,
            paused: !!state.oauth.workerTailPaused,
            line_count: lines.length,
            levels,
            last_event_at: lines.length ? new Date(lines[lines.length - 1].ts || Date.now()).toISOString() : '',
            event_text_included: false,
        };
    }

    function workerBackHeader(title, metaText) {
        const bar = document.createElement('div');
        bar.className = 'oauth-action-bar';
        const copy = document.createElement('div');
        const label = document.createElement('div');
        label.className = 'oauth-action-title';
        label.textContent = title || '';
        const meta = document.createElement('div');
        meta.className = 'oauth-action-meta mono';
        meta.textContent = metaText || '';
        copy.append(label, meta);
        const actions = document.createElement('div');
        actions.className = 'oauth-row-actions';
        const diagnostics = smallButton(t('oauth_worker_copy_diagnostics'), 'btn btn--sm btn--ghost', () => copyOAuthText(workerDiagnosticsText()));
        diagnostics.title = t('oauth_worker_copy_diagnostics_title');
        diagnostics.setAttribute('aria-label', t('oauth_worker_copy_diagnostics_title'));
        const back = smallButton(t('back'), 'btn btn--sm btn--ghost', () => {
            resetWorkerDetail();
            renderOAuthResource();
        });
        actions.append(diagnostics, back);
        bar.append(copy, actions);
        return bar;
    }

    function workerInfoSection(title, rows) {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = title;
        section.appendChild(heading);
        if (!rows.length) {
            section.appendChild(empty(t('oauth_worker_no_metadata')));
            return section;
        }
        for (const [label, value] of rows) section.appendChild(rowNode(label, value));
        return section;
    }

    function workerMetricsSection() {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const header = document.createElement('div');
        header.className = 'oauth-action-bar';
        const copy = document.createElement('div');
        const title = document.createElement('div');
        title.className = 'oauth-action-title';
        title.textContent = t('oauth_worker_metrics');
        const meta = document.createElement('div');
        meta.className = 'oauth-action-meta';
        meta.textContent = state.oauth.workerMetrics ? formatDateRange(state.oauth.workerMetrics.since, state.oauth.workerMetrics.until) : '';
        copy.append(title, meta);
        header.appendChild(copy);
        if (canRead('analytics')) {
            const select = document.createElement('select');
            select.className = 'select select--sm';
            select.setAttribute('aria-label', t('oauth_worker_metrics_range'));
            for (const value of analyticsRanges) {
                const option = document.createElement('option');
                option.value = value;
                option.textContent = analyticsRangeLabel(value);
                option.selected = value === state.oauth.workerMetricsRange;
                select.appendChild(option);
            }
            select.addEventListener('change', async () => {
                state.oauth.workerMetricsRange = select.value;
                state.oauth.workerMetrics = null;
                state.oauth.workerMetricsError = '';
                renderOAuthResource();
                await loadWorkerMetrics();
                renderOAuthResource();
            });
            header.appendChild(select);
        }
        section.appendChild(header);

        if (!canRead('analytics')) {
            section.appendChild(empty(t('oauth_worker_metrics_scope_required')));
            return section;
        }
        if (state.oauth.workerMetricsLoading) {
            section.appendChild(empty(t('oauth_worker_metrics_loading')));
            return section;
        }
        if (state.oauth.workerMetricsError) {
            section.appendChild(empty(state.oauth.workerMetricsError));
            return section;
        }
        const metrics = state.oauth.workerMetrics;
        if (!metrics) {
            section.appendChild(empty(t('oauth_worker_metrics_empty')));
            return section;
        }
        const summary = metrics.summary || {};
        const requests = Number(summary.requests || 0);
        const errors = Number(summary.errors || 0);
        const grid = document.createElement('div');
        grid.className = 'oauth-metric-grid';
        grid.append(
            metricNode(t('oauth_worker_metrics_requests'), formatNumber(requests)),
            metricNode(t('oauth_worker_metrics_errors'), formatNumber(errors)),
            metricNode(t('oauth_worker_metrics_subrequests'), formatNumber(summary.subrequests)),
            metricNode(t('oauth_worker_metrics_error_rate'), requests > 0 ? formatPercent((errors / requests) * 100) : '0%'),
        );
        if (summary.cpu_time_us != null) {
            grid.appendChild(metricNode(t('oauth_worker_metrics_cpu_total'), formatCPUTime(summary.cpu_time_us)));
        }
        if (summary.cpu_time_p50_us != null || summary.cpu_time_p99_us != null) {
            grid.appendChild(metricNode(t('oauth_worker_metrics_cpu_single'), [
                summary.cpu_time_p50_us == null ? '' : `P50 ${formatCPUTime(summary.cpu_time_p50_us)}`,
                summary.cpu_time_p99_us == null ? '' : `P99 ${formatCPUTime(summary.cpu_time_p99_us)}`,
            ].filter(Boolean).join(' · ')));
        }
        section.appendChild(grid);

        const statuses = Array.isArray(metrics.status_breakdown) ? metrics.status_breakdown : [];
        if (statuses.length) {
            const heading = document.createElement('h4');
            heading.className = 'oauth-section-title';
            heading.textContent = t('oauth_worker_metrics_status');
            section.appendChild(heading);
            for (const item of statuses) section.appendChild(rowNode(workerStatusLabel(item.status), formatNumber(item.requests)));
        }

        const series = Array.isArray(metrics.series) ? metrics.series : [];
        if (series.length) {
            const heading = document.createElement('h4');
            heading.className = 'oauth-section-title';
            heading.textContent = t('oauth_worker_metrics_timeseries');
            section.appendChild(heading);
            for (const point of series.slice(-12)) {
                section.appendChild(rowNode(formatDate(point.time), [
                    `${t('oauth_worker_metrics_requests')} ${formatNumber(point.requests)}`,
                    `${t('oauth_worker_metrics_errors')} ${formatNumber(point.errors)}`,
                ].join(' · ')));
            }
        }
        return section;
    }

    function workerScriptSection(content) {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_worker_script');
        section.appendChild(heading);
        if (content.encoding !== 'utf-8') {
            section.appendChild(empty(t('oauth_worker_script_binary', { bytes: formatBytes(content.bytes || 0) })));
            return section;
        }
        const meta = document.createElement('div');
        meta.className = 'oauth-row-meta';
        meta.textContent = [
            formatBytes(content.bytes || 0),
            content.truncated ? t('oauth_worker_script_truncated') : '',
        ].filter(Boolean).join(' · ');
        section.appendChild(meta);
        const code = document.createElement('textarea');
        code.className = 'oauth-code-editor';
        code.readOnly = true;
        code.spellcheck = false;
        code.value = content.value || '';
        section.appendChild(code);
        return section;
    }

    function workerTailSection() {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const header = document.createElement('div');
        header.className = 'oauth-action-bar';
        const copy = document.createElement('div');
        const title = document.createElement('div');
        title.className = 'oauth-action-title';
        title.textContent = t('oauth_worker_tail_live');
        const meta = document.createElement('div');
        meta.className = 'oauth-action-meta';
        meta.textContent = workerTailStatusText();
        copy.append(title, meta);
        header.appendChild(copy);
        const actions = document.createElement('div');
        actions.className = 'oauth-row-actions';
        if (canRead('workers_tail')) {
            if (state.oauth.workerTailConnected || state.oauth.workerTailConnecting) {
                actions.appendChild(smallButton(state.oauth.workerTailPaused ? t('oauth_worker_tail_resume') : t('oauth_worker_tail_pause'), 'btn btn--sm btn--ghost', () => {
                    state.oauth.workerTailPaused = !state.oauth.workerTailPaused;
                    renderOAuthResource();
                }));
                actions.appendChild(smallButton(t('oauth_worker_tail_stop'), 'btn btn--sm btn--danger', stopWorkerTail));
            } else {
                actions.appendChild(smallButton(t('oauth_worker_tail_start'), 'btn btn--sm btn--primary', (event) => startWorkerTail(event.currentTarget)));
            }
            actions.appendChild(smallButton(t('oauth_worker_tail_clear'), 'btn btn--sm btn--ghost', () => {
                state.oauth.workerTailLines = [];
                renderOAuthResource();
            }));
        }
        header.appendChild(actions);
        section.appendChild(header);
        if (!canRead('workers_tail')) {
            section.appendChild(empty(t('oauth_worker_tail_scope_required')));
            return section;
        }
        const log = document.createElement('div');
        log.className = 'oauth-tail-log';
        if (!state.oauth.workerTailLines.length) {
            log.appendChild(empty(t('oauth_worker_tail_empty')));
        } else {
            for (const line of state.oauth.workerTailLines) {
                const row = document.createElement('div');
                row.className = 'oauth-tail-line';
                row.setAttribute('data-level', line.level || 'info');
                const ts = document.createElement('span');
                ts.className = 'oauth-tail-time mono';
                ts.textContent = formatTimeOnly(line.ts);
                const level = document.createElement('span');
                level.className = 'oauth-tail-level mono';
                level.textContent = line.level || 'info';
                const text = document.createElement('span');
                text.className = 'oauth-tail-text';
                text.textContent = line.text || '';
                row.append(ts, level, text);
                log.appendChild(row);
            }
        }
        section.appendChild(log);
        return section;
    }

    function renderStorage(body) {
        if (!state.oauth.selectedAccountId) {
            body.appendChild(empty(t('oauth_select_account')));
            return;
        }
        if (state.oauth.storageView === 'kv') {
            renderKVDetail(body);
            return;
        }
        if (state.oauth.storageView === 'd1') {
            renderD1Detail(body);
            return;
        }
        if (state.oauth.storageView === 'r2') {
            renderR2Detail(body);
            return;
        }
        let rendered = 0;
        if (canRead('r2')) {
            body.appendChild(r2MetricsSectionNode());
            const r2Actions = [];
            if (canWrite('r2')) {
                r2Actions.push({
                    text: state.oauth.r2CreateOpen ? t('cancel') : t('oauth_r2_create_bucket'),
                    className: state.oauth.r2CreateOpen ? 'btn btn--sm btn--ghost' : 'btn btn--sm',
                    onClick: () => {
                        state.oauth.r2CreateOpen = !state.oauth.r2CreateOpen;
                        if (state.oauth.r2CreateOpen) {
                            state.oauth.d1CreateOpen = false;
                            state.oauth.kvNamespaceCreateOpen = false;
                            state.oauth.kvNamespaceEditingId = '';
                        }
                        renderOAuthResource();
                    },
                });
            }
            r2Actions.push({
                text: t('oauth_r2_copy_diagnostics'),
                className: 'btn btn--sm btn--ghost',
                title: t('oauth_r2_copy_diagnostics_title'),
                onClick: () => copyOAuthText(r2DiagnosticsText()),
            });
            body.appendChild(resourceActionBar(t('oauth_r2_buckets'), r2Actions));
            if (canWrite('r2') && state.oauth.r2CreateOpen) body.appendChild(r2BucketFormNode());
            rendered += renderSection(body, t('oauth_r2_buckets'), state.oauth.r2Buckets, (bucket) => {
                const meta = [bucket.location, bucket.creation_date ? formatDate(bucket.creation_date) : ''].filter(Boolean).join(' · ');
                const actions = canWrite('r2') ? [
                    smallButton(t('delete'), 'btn btn--sm btn--danger', () => deleteR2Bucket(bucket.name)),
                ] : [];
                const row = rowNode(bucket.name, meta, actions);
                row.addEventListener('click', async () => {
                    state.oauth.storageView = 'r2';
                    state.oauth.selectedR2BucketName = bucket.name;
                    state.oauth.selectedR2ObjectKey = '';
                    state.oauth.r2Objects = [];
                    state.oauth.r2Cursor = '';
                    state.oauth.r2ObjectValue = null;
                    state.oauth.r2ObjectFilter = '';
                    state.oauth.r2ObjectCreateOpen = false;
                    await loadR2Objects(bucket.name);
                    renderOAuthResource();
                });
                return row;
            }, t('oauth_no_r2_buckets'));
        }
        if (canRead('d1')) {
            const d1Actions = [];
            if (canWrite('d1')) {
                d1Actions.push({
                    text: state.oauth.d1CreateOpen ? t('cancel') : t('oauth_d1_create_database'),
                    className: state.oauth.d1CreateOpen ? 'btn btn--sm btn--ghost' : 'btn btn--sm',
                    onClick: () => {
                        state.oauth.d1CreateOpen = !state.oauth.d1CreateOpen;
                        if (state.oauth.d1CreateOpen) {
                            state.oauth.r2CreateOpen = false;
                            state.oauth.kvNamespaceCreateOpen = false;
                            state.oauth.kvNamespaceEditingId = '';
                        }
                        renderOAuthResource();
                    },
                });
            }
            d1Actions.push({
                text: t('oauth_d1_copy_diagnostics'),
                className: 'btn btn--sm btn--ghost',
                title: t('oauth_d1_copy_diagnostics_title'),
                onClick: () => copyOAuthText(d1DiagnosticsText()),
            });
            body.appendChild(resourceActionBar(t('oauth_d1_databases'), d1Actions));
            if (canWrite('d1') && state.oauth.d1CreateOpen) body.appendChild(d1DatabaseFormNode());
            if (state.oauth.d1DatabasesError) {
                body.appendChild(empty(state.oauth.d1DatabasesError));
                rendered += 1;
            } else {
                if (state.oauth.d1DetailsError) body.appendChild(empty(state.oauth.d1DetailsError));
                rendered += renderSection(body, t('oauth_d1_databases'), state.oauth.d1Databases, (database) => {
                    const meta = [`${database.num_tables || 0} ${t('oauth_tables')}`, formatBytes(database.file_size || 0)].join(' · ');
                    const actions = canWrite('d1') ? [
                        smallButton(t('delete'), 'btn btn--sm btn--danger', (event) => deleteD1Database(database, event.currentTarget)),
                    ] : [];
                    const row = rowNode(database.name || database.uuid, meta, actions);
                    row.addEventListener('click', async () => {
                        state.oauth.storageView = 'd1';
                        state.oauth.selectedD1DatabaseId = database.uuid;
                        state.oauth.selectedD1TableName = '';
                        state.oauth.d1Tables = [];
                        state.oauth.d1TablesDatabaseId = '';
                        state.oauth.d1TablesError = '';
                        state.oauth.d1TableColumns = [];
                        state.oauth.d1TableRows = [];
                        state.oauth.d1TableRowsError = '';
                        state.oauth.d1EditingRow = null;
                        state.oauth.d1Results = [];
                        state.oauth.d1QueryError = '';
                        state.oauth.d1Sql = "SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE '_cf_%' ORDER BY name;";
                        await loadD1Tables(database.uuid);
                        renderOAuthResource();
                    });
                    return row;
                }, t('oauth_no_d1_databases'));
            }
        }
        if (canRead('kv')) {
            const kvActions = [];
            if (canWrite('kv')) {
                kvActions.push({
                    text: state.oauth.kvNamespaceCreateOpen ? t('cancel') : t('oauth_kv_create_namespace'),
                    className: state.oauth.kvNamespaceCreateOpen ? 'btn btn--sm btn--ghost' : 'btn btn--sm',
                    onClick: () => {
                        state.oauth.kvNamespaceCreateOpen = !state.oauth.kvNamespaceCreateOpen;
                        if (state.oauth.kvNamespaceCreateOpen) {
                            state.oauth.r2CreateOpen = false;
                            state.oauth.d1CreateOpen = false;
                            state.oauth.kvNamespaceEditingId = '';
                        }
                        renderOAuthResource();
                    },
                });
            }
            kvActions.push({
                text: t('oauth_kv_copy_diagnostics'),
                className: 'btn btn--sm btn--ghost',
                title: t('oauth_kv_copy_diagnostics_title'),
                onClick: () => copyOAuthText(kvDiagnosticsText()),
            });
            body.appendChild(resourceActionBar(t('oauth_kv_namespaces'), kvActions));
            if (canWrite('kv') && state.oauth.kvNamespaceCreateOpen) body.appendChild(kvNamespaceFormNode('', true));
            rendered += renderSection(body, t('oauth_kv_namespaces'), state.oauth.kvNamespaces, (namespace) => {
                if (state.oauth.kvNamespaceEditingId === namespace.id) {
                    return kvNamespaceFormNode(namespace.title || '', false, namespace.id);
                }
                const actions = canWrite('kv') ? [
                    smallButton(t('rename'), 'btn btn--sm btn--ghost', () => {
                        state.oauth.kvNamespaceEditingId = namespace.id;
                        state.oauth.kvNamespaceCreateOpen = false;
                        state.oauth.r2CreateOpen = false;
                        state.oauth.d1CreateOpen = false;
                        renderOAuthResource();
                    }),
                    smallButton(t('delete'), 'btn btn--sm btn--danger', (event) => deleteKVNamespace(namespace, event.currentTarget)),
                ] : [];
                const row = rowNode(namespace.title || namespace.id, namespace.id, actions);
                row.addEventListener('click', async () => {
                    state.oauth.storageView = 'kv';
                    state.oauth.selectedKVNamespaceId = namespace.id;
                    state.oauth.selectedKVKey = '';
                    state.oauth.kvSelectedKeys = [];
                    state.oauth.kvValue = null;
                    state.oauth.kvKeysError = '';
                    state.oauth.kvValueError = '';
                    state.oauth.kvCreateOpen = false;
                    await loadKVKeys(namespace.id);
                    renderOAuthResource();
                });
                return row;
            }, t('oauth_no_kv_namespaces'));
        }
        if (!rendered) body.appendChild(empty(t('oauth_storage_scope_required')));
    }

    function r2MetricsSectionNode() {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_r2_metrics');
        section.appendChild(heading);

        if (state.oauth.r2MetricsLoading) {
            section.appendChild(empty(t('oauth_r2_metrics_loading')));
            return section;
        }
        if (state.oauth.r2MetricsError) {
            section.appendChild(empty(t('oauth_r2_metrics_error', { error: state.oauth.r2MetricsError })));
            return section;
        }

        const metrics = state.oauth.r2Metrics || {};
        const classes = [
            [t('oauth_r2_metrics_standard'), metrics.standard],
            [t('oauth_r2_metrics_infrequent_access'), metrics.infrequent_access],
        ].filter(([, item]) => item);
        if (!classes.length) {
            section.appendChild(empty(t('oauth_r2_metrics_empty')));
            return section;
        }

        for (const [label, item] of classes) {
            section.appendChild(r2ClassMetricsNode(label, item));
        }
        return section;
    }

    function r2ClassMetricsNode(label, metrics) {
        const group = document.createElement('div');
        group.className = 'oauth-metric-group';
        const title = document.createElement('div');
        title.className = 'oauth-row-title';
        title.textContent = label;
        group.appendChild(title);
        group.appendChild(r2SnapshotMetricsGrid(t('oauth_r2_metrics_published'), metrics.published));
        group.appendChild(r2SnapshotMetricsGrid(t('oauth_r2_metrics_uploaded'), metrics.uploaded));
        return group;
    }

    function r2SnapshotMetricsGrid(label, snapshot) {
        const wrap = document.createElement('div');
        wrap.className = 'oauth-metric-stack';
        const title = document.createElement('div');
        title.className = 'oauth-row-meta';
        title.textContent = label;
        const grid = document.createElement('div');
        grid.className = 'oauth-metric-grid';
        const item = snapshot || {};
        grid.append(
            metricNode(t('oauth_r2_metrics_objects'), formatNumber(item.objects)),
            metricNode(t('oauth_r2_metrics_payload'), formatBytes(item.payload_size)),
            metricNode(t('oauth_r2_metrics_metadata'), formatBytes(item.metadata_size)),
            metricNode(t('oauth_r2_metrics_total'), formatBytes(item.total_bytes))
        );
        wrap.append(title, grid);
        return wrap;
    }

    function r2DiagnosticsText() {
        const status = state.oauth.status || {};
        const metrics = state.oauth.r2Metrics || null;
        const session = state.oauth.r2Session || metrics?.session || status.current || {};
        const selectedObject = state.oauth.r2ObjectValue || null;
        const listObject = selectedR2ListObject(selectedObject);
        const filtered = filteredR2Objects();
        return JSON.stringify({
            type: 'cfui_oauth_r2_diagnostics',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            browser_path: window.location.pathname,
            contains_oauth_token: false,
            contains_refresh_token: false,
            contains_object_value: false,
            contains_binary_preview: false,
            contains_download_url: false,
            contains_local_file_path: false,
            sensitive_fields_omitted: [
                'oauth_access_token',
                'oauth_refresh_token',
                'r2_object_value',
                'r2_binary_preview',
                'r2_download_url',
                'local_file_name',
                'upload_session_id',
            ],
            selected: {
                account_id: state.oauth.selectedAccountId || '',
                account_name: selectedAccountName(),
                bucket: state.oauth.selectedR2BucketName || '',
                object_key: state.oauth.selectedR2ObjectKey || '',
                storage_view: state.oauth.storageView || '',
            },
            identity: {
                label: session.label || '',
                expires_at: session.expires_at || '',
                scopes: Array.isArray(session.scopes) ? session.scopes : [],
            },
            capability: {
                r2_read: canRead('r2'),
                r2_write: canWrite('r2'),
            },
            state: {
                bucket_count: state.oauth.r2Buckets.length,
                metrics_loaded: !!metrics,
                metrics_loading: !!state.oauth.r2MetricsLoading,
                metrics_error: state.oauth.r2MetricsError || '',
                objects_loaded: state.oauth.r2Objects.length,
                objects_filtered: filtered.length,
                objects_has_more: !!state.oauth.r2Cursor,
                objects_error: state.oauth.r2ObjectsError || '',
                object_loaded: !!selectedObject,
                object_error: state.oauth.r2ObjectValueError || '',
                object_filter_active: !!String(state.oauth.r2ObjectFilter || '').trim(),
                upload_active: !!state.oauth.r2UploadProgress,
                scope_ready: canRead('r2'),
            },
            limits: {
                direct_upload_bytes: maxR2ObjectUploadBytes,
                chunked_upload_bytes: maxR2ChunkedUploadBytes,
                chunk_size_bytes: r2ObjectUploadChunkBytes,
                inline_preview_bytes: maxR2InlinePreviewBytes,
            },
            metrics: metrics ? r2MetricsDiagnostics(metrics) : null,
            buckets: state.oauth.r2Buckets.map(r2BucketDiagnostics),
            objects: {
                loaded_count: state.oauth.r2Objects.length,
                filtered_count: filtered.length,
                has_more: !!state.oauth.r2Cursor,
                cursor_present: !!state.oauth.r2Cursor,
                items: filtered.map(r2ObjectListDiagnostics),
            },
            selected_object: selectedObject ? r2SelectedObjectDiagnostics(selectedObject, listObject) : null,
            upload: r2UploadDiagnostics(state.oauth.r2UploadProgress),
            capabilities: oauthCapabilityDiagnostics(state.oauth.r2Capabilities || metrics?.capabilities || status.capabilities || {}),
        }, null, 2);
    }

    function r2BucketDiagnostics(bucket) {
        return {
            name: bucket?.name || '',
            location: bucket?.location || '',
            creation_date: bucket?.creation_date || '',
        };
    }

    function r2ObjectListDiagnostics(object) {
        return {
            key: object?.key || '',
            size: object?.size == null ? null : Number(object.size),
            etag: object?.etag || '',
            last_modified: object?.last_modified || '',
            storage_class: object?.storage_class || '',
            content_type: object?.http_metadata?.contentType || '',
        };
    }

    function r2SelectedObjectDiagnostics(object, listObject) {
        return {
            key: object?.key || state.oauth.selectedR2ObjectKey || '',
            encoding: object?.encoding || '',
            bytes: object?.bytes == null ? null : Number(object.bytes),
            content_type: object?.content_type || listObject?.http_metadata?.contentType || '',
            truncated: !!object?.truncated,
            value_included: false,
            binary_preview_included: false,
            list_metadata: r2ObjectListDiagnostics(listObject || {}),
        };
    }

    function r2MetricsDiagnostics(metrics) {
        return {
            standard: r2ClassMetricsDiagnostics(metrics?.standard),
            infrequent_access: r2ClassMetricsDiagnostics(metrics?.infrequent_access),
        };
    }

    function r2ClassMetricsDiagnostics(metrics) {
        if (!metrics) return null;
        return {
            published: r2SnapshotDiagnostics(metrics.published),
            uploaded: r2SnapshotDiagnostics(metrics.uploaded),
        };
    }

    function r2SnapshotDiagnostics(snapshot) {
        if (!snapshot) return null;
        return {
            objects: Number(snapshot.objects || 0),
            payload_size: Number(snapshot.payload_size || 0),
            metadata_size: Number(snapshot.metadata_size || 0),
            total_bytes: Number(snapshot.total_bytes || 0),
        };
    }

    function r2UploadDiagnostics(progress) {
        if (!progress) return { active: false };
        return {
            active: true,
            file_name_included: false,
            uploaded: Number(progress.uploaded || 0),
            total: Number(progress.total || 0),
            chunk_index: Number(progress.chunkIndex || 0),
            total_chunks: Number(progress.totalChunks || 0),
            mode: progress.mode || '',
        };
    }

    function d1DiagnosticsText() {
        const status = state.oauth.status || {};
        const session = state.oauth.d1Session || status.current || {};
        const database = selectedD1Database();
        return JSON.stringify({
            type: 'cfui_oauth_d1_diagnostics',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            browser_path: window.location.pathname,
            contains_oauth_token: false,
            contains_refresh_token: false,
            contains_sql_text: false,
            contains_sql_parameters: false,
            contains_query_rows: false,
            contains_table_rows: false,
            contains_editing_row_values: false,
            sensitive_fields_omitted: [
                'oauth_access_token',
                'oauth_refresh_token',
                'd1_sql_text',
                'd1_sql_parameters',
                'd1_query_result_rows',
                'd1_table_row_values',
                'd1_editing_row_values',
            ],
            selected: {
                account_id: state.oauth.selectedAccountId || '',
                account_name: selectedAccountName(),
                database_id: state.oauth.selectedD1DatabaseId || '',
                database_name: database?.name || '',
                table: state.oauth.selectedD1TableName || '',
                storage_view: state.oauth.storageView || '',
            },
            identity: {
                label: session.label || '',
                expires_at: session.expires_at || '',
                scopes: Array.isArray(session.scopes) ? session.scopes : [],
            },
            capability: {
                d1_read: canRead('d1'),
                d1_write: canWrite('d1'),
            },
            state: {
                database_count: state.oauth.d1Databases.length,
                databases_error: state.oauth.d1DatabasesError || '',
                detail_error: state.oauth.d1DetailsError || '',
                tables_loaded: state.oauth.d1Tables.length,
                tables_database_id: state.oauth.d1TablesDatabaseId || '',
                tables_error: state.oauth.d1TablesError || '',
                table_rows_loaded: state.oauth.d1TableRows.length,
                table_rows_error: state.oauth.d1TableRowsError || '',
                table_has_more: !!state.oauth.d1TableHasMore,
                query_result_count: state.oauth.d1Results.length,
                query_error: state.oauth.d1QueryError || '',
                query_text_included: false,
                query_text_length: String(state.oauth.d1Sql || '').length,
                editing_row_active: !!state.oauth.d1EditingRow,
                editing_row_values_included: false,
                create_database_open: !!state.oauth.d1CreateOpen,
                scope_ready: canRead('d1'),
            },
            pagination: {
                limit: Number(state.oauth.d1TableLimit || 0),
                offset: Number(state.oauth.d1TableOffset || 0),
                has_more: !!state.oauth.d1TableHasMore,
            },
            databases: state.oauth.d1Databases.map(d1DatabaseDiagnostics),
            tables: {
                loaded_count: state.oauth.d1Tables.length,
                names: state.oauth.d1Tables.slice(),
            },
            selected_table: state.oauth.selectedD1TableName ? d1SelectedTableDiagnostics() : null,
            query_results: state.oauth.d1Results.map(d1QueryResultDiagnostics),
            capabilities: oauthCapabilityDiagnostics(state.oauth.d1Capabilities || status.capabilities || {}),
        }, null, 2);
    }

    function d1DatabaseDiagnostics(database) {
        return {
            uuid: database?.uuid || '',
            name: database?.name || '',
            version: database?.version || '',
            num_tables: Number(database?.num_tables || 0),
            file_size: Number(database?.file_size || 0),
            created_at: database?.created_at || '',
        };
    }

    function d1SelectedTableDiagnostics() {
        return {
            name: state.oauth.selectedD1TableName || '',
            columns: state.oauth.d1TableColumns.map(d1ColumnDiagnostics),
            row_count_loaded: state.oauth.d1TableRows.length,
            row_values_included: false,
            rowid_key: state.oauth.d1RowIDKey || '',
            limit: Number(state.oauth.d1TableLimit || 0),
            offset: Number(state.oauth.d1TableOffset || 0),
            has_more: !!state.oauth.d1TableHasMore,
        };
    }

    function d1ColumnDiagnostics(column) {
        return {
            name: column?.name || '',
            type: column?.type || '',
            not_null: !!column?.not_null,
            primary_key: !!column?.primary_key,
        };
    }

    function d1QueryResultDiagnostics(result) {
        const rows = Array.isArray(result?.results) ? result.results : [];
        return {
            success: result?.success ?? null,
            row_count: rows.length,
            row_values_included: false,
            columns: d1ResultColumnNames(rows),
            meta: d1ResultMetaDiagnostics(result?.meta || {}),
        };
    }

    function d1ResultColumnNames(rows) {
        const columns = new Set();
        for (const row of rows) Object.keys(row || {}).forEach((key) => columns.add(key));
        return Array.from(columns).sort();
    }

    function d1ResultMetaDiagnostics(meta) {
        return {
            duration: meta.duration == null ? null : Number(meta.duration),
            rows_read: meta.rows_read == null ? null : Number(meta.rows_read),
            rows_written: meta.rows_written == null ? null : Number(meta.rows_written),
            changes: meta.changes == null ? null : Number(meta.changes),
            changed_db: meta.changed_db == null ? null : !!meta.changed_db,
        };
    }

    function kvDiagnosticsText() {
        const status = state.oauth.status || {};
        const session = state.oauth.kvSession || status.current || {};
        const namespace = selectedKVNamespace();
        const selectedValue = state.oauth.kvValue || null;
        const selectedListKey = selectedKVListKey(selectedValue?.key || state.oauth.selectedKVKey);
        return JSON.stringify({
            type: 'cfui_oauth_kv_diagnostics',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            browser_path: window.location.pathname,
            contains_oauth_token: false,
            contains_refresh_token: false,
            contains_kv_value: false,
            contains_binary_preview: false,
            contains_download_url: false,
            contains_local_file_path: false,
            sensitive_fields_omitted: [
                'oauth_access_token',
                'oauth_refresh_token',
                'kv_value',
                'kv_binary_preview',
                'kv_download_url',
                'local_file_name',
            ],
            selected: {
                account_id: state.oauth.selectedAccountId || '',
                account_name: selectedAccountName(),
                namespace_id: state.oauth.selectedKVNamespaceId || '',
                namespace_title: namespace?.title || '',
                key: state.oauth.selectedKVKey || '',
                storage_view: state.oauth.storageView || '',
            },
            identity: {
                label: session.label || '',
                expires_at: session.expires_at || '',
                scopes: Array.isArray(session.scopes) ? session.scopes : [],
            },
            capability: {
                kv_read: canRead('kv'),
                kv_write: canWrite('kv'),
            },
            state: {
                namespace_count: state.oauth.kvNamespaces.length,
                keys_loaded: state.oauth.kvKeys.length,
                keys_has_more: !!state.oauth.kvCursor,
                keys_error: state.oauth.kvKeysError || '',
                value_loaded: !!selectedValue,
                value_error: state.oauth.kvValueError || '',
                create_namespace_open: !!state.oauth.kvNamespaceCreateOpen,
                create_key_open: !!state.oauth.kvCreateOpen,
                editing_namespace_id: state.oauth.kvNamespaceEditingId || '',
                scope_ready: canRead('kv'),
            },
            limits: {
                value_upload_bytes: maxKVValueUploadBytes,
            },
            namespaces: state.oauth.kvNamespaces.map(kvNamespaceDiagnostics),
            keys: {
                loaded_count: state.oauth.kvKeys.length,
                has_more: !!state.oauth.kvCursor,
                cursor_present: !!state.oauth.kvCursor,
                items: state.oauth.kvKeys.map(kvKeyDiagnostics),
            },
            selected_value: selectedValue ? kvSelectedValueDiagnostics(selectedValue, selectedListKey) : null,
            bulk_selection: kvBulkSelectionDiagnostics(),
            capabilities: oauthCapabilityDiagnostics(state.oauth.kvCapabilities || status.capabilities || {}),
        }, null, 2);
    }

    function kvNamespaceDiagnostics(namespace) {
        return {
            id: namespace?.id || '',
            title: namespace?.title || '',
        };
    }

    function kvKeyDiagnostics(key) {
        const expiration = Number(key?.expiration || 0);
        return {
            name: key?.name || '',
            expiration: expiration || null,
            expiration_at: expiration ? new Date(expiration * 1000).toISOString() : '',
        };
    }

    function kvSelectedValueDiagnostics(value, listKey) {
        return {
            key: value?.key || state.oauth.selectedKVKey || '',
            encoding: value?.encoding || '',
            bytes: value?.bytes == null ? null : Number(value.bytes),
            value_included: false,
            binary_preview_included: false,
            download_url_included: false,
            binary_preview_available: !!value?.binary_preview,
            list_metadata: kvKeyDiagnostics(listKey || {}),
        };
    }

    function kvBulkSelectionDiagnostics() {
        const selected = kvSelectedKeys();
        return {
            selected_count: selected.length,
            loaded_count: state.oauth.kvKeys.length,
            keys: selected,
        };
    }

    function renderR2Detail(body) {
        const bucket = selectedR2Bucket();
        if (!bucket) {
            resetStorageDetail();
            body.appendChild(empty(t('oauth_select_account')));
            return;
        }
        body.appendChild(storageBackHeader(bucket.name, bucket.location || 'R2', [{
            text: t('oauth_r2_copy_diagnostics'),
            className: 'btn btn--sm btn--ghost',
            title: t('oauth_r2_copy_diagnostics_title'),
            onClick: () => copyOAuthText(r2DiagnosticsText()),
        }]));
        const canEdit = canWrite('r2');
        if (canEdit) {
            body.appendChild(resourceActionBar(t('oauth_r2_objects'), {
                text: state.oauth.r2ObjectCreateOpen ? t('cancel') : t('oauth_r2_object_create'),
                className: state.oauth.r2ObjectCreateOpen ? 'btn btn--sm btn--ghost' : 'btn btn--sm',
                onClick: () => {
                    state.oauth.r2ObjectCreateOpen = !state.oauth.r2ObjectCreateOpen;
                    if (state.oauth.r2ObjectCreateOpen) {
                        state.oauth.selectedR2ObjectKey = '';
                        state.oauth.r2ObjectValue = null;
                        state.oauth.r2ObjectValueError = '';
                    }
                    renderOAuthResource();
                },
            }));
            if (state.oauth.r2ObjectCreateOpen) body.appendChild(r2ObjectFormNode('', '', 'text/plain; charset=utf-8', true));
            body.appendChild(r2ObjectUploadFormNode());
        } else {
            body.appendChild(empty(t('oauth_r2_objects_readonly')));
        }

        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_r2_objects');
        section.appendChild(heading);
        section.appendChild(r2ObjectFilterNode());
        const filteredObjects = filteredR2Objects();
        if (state.oauth.r2ObjectsError) {
            section.appendChild(empty(state.oauth.r2ObjectsError));
        } else if (!state.oauth.r2Objects.length) {
            section.appendChild(empty(t('oauth_r2_no_objects')));
        } else if (!filteredObjects.length) {
            section.appendChild(empty(t('oauth_r2_no_object_matches')));
        } else {
            for (const object of filteredObjects) {
                const meta = [
                    object.size == null ? '' : formatBytes(object.size),
                    object.http_metadata?.contentType || '',
                    object.last_modified ? formatDate(object.last_modified) : '',
                ].filter(Boolean).join(' · ');
                const actions = [
                    smallButton(t('oauth_r2_object_copy_key'), 'btn btn--sm btn--ghost', () => copyOAuthText(object.key)),
                    smallButton(t('download'), 'btn btn--sm btn--ghost', () => downloadR2Object(object.key)),
                ];
                if (canEdit) {
                    actions.push(
                        smallButton(t('oauth_r2_object_copy'), 'btn btn--sm btn--ghost', (event) => copyOrMoveR2Object(object.key, false, event.currentTarget)),
                        smallButton(t('oauth_r2_object_move'), 'btn btn--sm btn--ghost', (event) => copyOrMoveR2Object(object.key, true, event.currentTarget)),
                        smallButton(t('delete'), 'btn btn--sm btn--danger', () => deleteR2Object(object.key)),
                    );
                }
                const row = rowNode(object.key, meta, actions);
                row.setAttribute('data-selected', String(object.key === state.oauth.selectedR2ObjectKey));
                row.addEventListener('click', async () => {
                    state.oauth.selectedR2ObjectKey = object.key;
                    state.oauth.r2ObjectCreateOpen = false;
                    state.oauth.r2ObjectValue = null;
                    state.oauth.r2ObjectValueError = '';
                    await loadR2ObjectValue(object.key);
                    renderOAuthResource();
                });
                section.appendChild(row);
            }
            if (state.oauth.r2Cursor) {
                section.appendChild(smallButton(t('oauth_r2_load_more_objects'), 'btn btn--sm btn--ghost', async () => {
                    await loadR2Objects(bucket.name, true);
                    renderOAuthResource();
                }));
            }
        }
        body.appendChild(section);

        if (!state.oauth.selectedR2ObjectKey) {
            body.appendChild(empty(t('oauth_r2_select_object')));
            return;
        }
        if (state.oauth.r2ObjectValueError) {
            body.appendChild(empty(state.oauth.r2ObjectValueError));
            return;
        }
        body.appendChild(r2ObjectPanelNode());
    }

    function renderKVDetail(body) {
        const namespace = selectedKVNamespace();
        if (!namespace) {
            resetStorageDetail();
            body.appendChild(empty(t('oauth_select_account')));
            return;
        }
        body.appendChild(storageBackHeader(namespace.title || namespace.id, namespace.id, [{
            text: t('oauth_kv_copy_diagnostics'),
            className: 'btn btn--sm btn--ghost',
            title: t('oauth_kv_copy_diagnostics_title'),
            onClick: () => copyOAuthText(kvDiagnosticsText()),
        }]));
        const canEdit = canWrite('kv');
        if (canEdit) {
            body.appendChild(resourceActionBar(t('oauth_kv_keys'), {
                text: state.oauth.kvCreateOpen ? t('cancel') : t('oauth_kv_create_key'),
                className: state.oauth.kvCreateOpen ? 'btn btn--sm btn--ghost' : 'btn btn--sm',
                onClick: () => {
                    state.oauth.kvCreateOpen = !state.oauth.kvCreateOpen;
                    renderOAuthResource();
                },
            }));
            if (state.oauth.kvCreateOpen) body.appendChild(kvValueFormNode('', '', true));
        } else {
            body.appendChild(empty(t('oauth_kv_readonly')));
        }

        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_kv_keys');
        section.appendChild(heading);
        if (state.oauth.kvKeysError) {
            section.appendChild(empty(state.oauth.kvKeysError));
        } else if (!state.oauth.kvKeys.length) {
            section.appendChild(empty(t('oauth_kv_no_keys')));
        } else {
            if (canEdit) section.appendChild(kvBulkActionsNode());
            for (const key of state.oauth.kvKeys) {
                const meta = key.expiration ? `${t('oauth_kv_expiration')} ${formatDate(key.expiration * 1000)}` : '';
                const actions = canEdit ? [kvKeySelectCheckbox(key.name)] : [];
                const row = rowNode(key.name, meta, actions);
                row.setAttribute('data-selected', String(key.name === state.oauth.selectedKVKey));
                row.addEventListener('click', async () => {
                    state.oauth.selectedKVKey = key.name;
                    state.oauth.kvCreateOpen = false;
                    state.oauth.kvValue = null;
                    state.oauth.kvValueError = '';
                    await loadKVValue(key.name);
                    renderOAuthResource();
                });
                section.appendChild(row);
            }
            if (state.oauth.kvCursor) {
                section.appendChild(smallButton(t('oauth_kv_load_more'), 'btn btn--sm btn--ghost', async () => {
                    await loadKVKeys(namespace.id, true);
                    renderOAuthResource();
                }));
            }
        }
        body.appendChild(section);

        if (!state.oauth.selectedKVKey) {
            body.appendChild(empty(t('oauth_kv_select_key')));
            return;
        }
        body.appendChild(kvValuePanelNode());
    }

    function kvBulkActionsNode() {
        const node = document.createElement('div');
        node.className = 'oauth-bulk-actions';
        const selected = kvSelectedKeys();
        const meta = document.createElement('div');
        meta.className = 'oauth-row-meta';
        meta.textContent = t('oauth_kv_selected_keys', { count: selected.length, loaded: state.oauth.kvKeys.length });
        const actions = document.createElement('div');
        actions.className = 'oauth-row-actions';
        actions.appendChild(smallButton(t('oauth_kv_select_loaded'), 'btn btn--sm btn--ghost', () => selectLoadedKVKeys()));
        const clear = smallButton(t('clear'), 'btn btn--sm btn--ghost', () => {
            state.oauth.kvSelectedKeys = [];
            renderOAuthResource();
        });
        clear.disabled = selected.length === 0;
        actions.appendChild(clear);
        const deleteButton = smallButton(t('oauth_kv_delete_selected'), 'btn btn--sm btn--danger', (event) => deleteSelectedKVKeys(event.currentTarget));
        deleteButton.disabled = selected.length === 0;
        actions.appendChild(deleteButton);
        node.append(meta, actions);
        return node;
    }

    function kvKeySelectCheckbox(keyName) {
        const label = document.createElement('label');
        label.className = 'oauth-checkbox-action';
        label.title = t('oauth_kv_select_key');
        label.setAttribute('aria-label', t('oauth_kv_select_key'));
        const input = document.createElement('input');
        input.type = 'checkbox';
        input.checked = kvSelectedKeySet().has(keyName);
        input.addEventListener('click', (event) => event.stopPropagation());
        input.addEventListener('change', (event) => {
            event.stopPropagation();
            setKVKeySelected(keyName, input.checked);
            renderOAuthResource();
        });
        label.addEventListener('click', (event) => event.stopPropagation());
        label.appendChild(input);
        return label;
    }

    function renderD1Detail(body) {
        const database = selectedD1Database();
        if (!database) {
            resetStorageDetail();
            body.appendChild(empty(t('oauth_select_account')));
            return;
        }
        body.appendChild(storageBackHeader(database.name || database.uuid, database.uuid, [{
            text: t('oauth_d1_copy_diagnostics'),
            className: 'btn btn--sm btn--ghost',
            title: t('oauth_d1_copy_diagnostics_title'),
            onClick: () => copyOAuthText(d1DiagnosticsText()),
        }]));
        body.appendChild(d1DatabaseInfoNode(database));
        if (!canWrite('d1')) body.appendChild(empty(t('oauth_d1_readonly_warning')));

        body.appendChild(d1TablesSectionNode());
        if (state.oauth.selectedD1TableName) {
            body.appendChild(d1TablePanelNode());
            if (state.oauth.d1EditingRow) body.appendChild(d1RowEditorNode(state.oauth.d1EditingRow));
        }

        const form = document.createElement('form');
        form.className = 'oauth-form';
        const label = document.createElement('label');
        label.className = 'oauth-form-field';
        const span = document.createElement('span');
        span.textContent = t('oauth_d1_sql');
        const textarea = document.createElement('textarea');
        textarea.className = 'oauth-code-editor';
        textarea.value = state.oauth.d1Sql || '';
        textarea.spellcheck = false;
        textarea.addEventListener('input', () => { state.oauth.d1Sql = textarea.value; });
        label.append(span, textarea);
        form.appendChild(label);
        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        const runButton = smallButton(t('oauth_d1_run'), 'btn btn--sm btn--primary');
        runButton.type = 'submit';
        actions.appendChild(runButton);
        form.appendChild(actions);
        form.addEventListener('submit', (event) => {
            event.preventDefault();
            state.oauth.d1Sql = textarea.value;
            runD1Query(textarea.value, runButton);
        });
        body.appendChild(form);

        const resultSection = document.createElement('section');
        resultSection.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_d1_results');
        resultSection.appendChild(heading);
        if (state.oauth.d1QueryError) {
            resultSection.appendChild(empty(state.oauth.d1QueryError));
        } else if (!state.oauth.d1Results.length) {
            resultSection.appendChild(empty(t('oauth_d1_no_results')));
        } else {
            state.oauth.d1Results.forEach((result, index) => {
                resultSection.appendChild(d1ResultNode(result, index, state.oauth.d1Results.length));
            });
        }
        body.appendChild(resultSection);
    }

    function d1DatabaseInfoNode(database) {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_d1_database_detail');
        section.appendChild(heading);
        const grid = document.createElement('div');
        grid.className = 'oauth-metric-grid';
        grid.append(
            metricNode(t('oauth_tables'), formatNumber(database.num_tables || 0)),
            metricNode(t('oauth_d1_file_size'), formatBytes(database.file_size || 0)),
            metricNode(t('oauth_d1_version'), database.version || t('oauth_d1_unavailable')),
            metricNode(t('oauth_d1_created'), formatDate(database.created_at) || t('oauth_d1_unavailable')),
        );
        section.appendChild(grid);
        return section;
    }

    function d1TablesSectionNode() {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_d1_tables');
        section.appendChild(heading);

        if (state.oauth.d1TablesDatabaseId !== state.oauth.selectedD1DatabaseId) {
            section.appendChild(smallButton(t('oauth_d1_load_tables'), 'btn btn--sm btn--ghost', async () => {
                await loadD1Tables();
                renderOAuthResource();
            }));
            return section;
        }
        if (state.oauth.d1TablesError) {
            section.appendChild(empty(state.oauth.d1TablesError));
            return section;
        }
        if (!state.oauth.d1Tables.length) {
            section.appendChild(empty(t('oauth_d1_no_tables')));
            return section;
        }
        for (const table of state.oauth.d1Tables) {
            const row = rowNode(table, '', []);
            row.setAttribute('data-selected', String(table === state.oauth.selectedD1TableName));
            row.addEventListener('click', async () => {
                await loadD1TableRows(table);
                renderOAuthResource();
            });
            section.appendChild(row);
        }
        return section;
    }

    function d1TablePanelNode() {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const header = document.createElement('div');
        header.className = 'oauth-action-bar';
        const copy = document.createElement('div');
        const title = document.createElement('div');
        title.className = 'oauth-action-title mono';
        title.textContent = state.oauth.selectedD1TableName;
        const meta = document.createElement('div');
        meta.className = 'oauth-action-meta';
        meta.textContent = t('oauth_d1_table_meta', { rows: state.oauth.d1TableRows.length, offset: state.oauth.d1TableOffset });
        copy.append(title, meta);
        header.appendChild(copy);
        if (state.oauth.d1TableHasMore) {
            header.appendChild(smallButton(t('oauth_d1_load_more_rows'), 'btn btn--sm btn--ghost', async () => {
                await loadD1TableRows(state.oauth.selectedD1TableName, true);
                renderOAuthResource();
            }));
        }
        section.appendChild(header);

        if (state.oauth.d1TableRowsError) {
            section.appendChild(empty(state.oauth.d1TableRowsError));
            return section;
        }
        if (!state.oauth.d1TableRows.length) {
            section.appendChild(empty(t('oauth_d1_empty_table')));
            return section;
        }

        const tableWrap = document.createElement('div');
        tableWrap.className = 'oauth-table-wrap';
        const table = document.createElement('table');
        table.className = 'oauth-data-table';
        const thead = document.createElement('thead');
        const headRow = document.createElement('tr');
        for (const column of state.oauth.d1TableColumns) {
            const th = document.createElement('th');
            const name = document.createElement('div');
            name.textContent = column.name;
            const type = document.createElement('div');
            type.className = 'oauth-column-type';
            type.textContent = [column.type || '', column.primary_key ? 'PK' : '', column.not_null ? 'NOT NULL' : ''].filter(Boolean).join(' · ');
            th.append(name, type);
            headRow.appendChild(th);
        }
        if (canWrite('d1')) {
            const th = document.createElement('th');
            th.textContent = t('actions');
            headRow.appendChild(th);
        }
        thead.appendChild(headRow);
        table.appendChild(thead);
        const tbody = document.createElement('tbody');
        for (const rowData of state.oauth.d1TableRows) {
            const tr = document.createElement('tr');
            for (const column of state.oauth.d1TableColumns) {
                const td = document.createElement('td');
                td.textContent = displayValue(rowData?.[column.name]);
                tr.appendChild(td);
            }
            if (canWrite('d1')) {
                const td = document.createElement('td');
                td.className = 'oauth-table-actions';
                td.appendChild(smallButton(t('edit'), 'btn btn--sm btn--ghost', () => {
                    state.oauth.d1EditingRow = rowData;
                    renderOAuthResource();
                }));
                td.appendChild(smallButton(t('delete'), 'btn btn--sm btn--danger', () => deleteD1Row(rowData)));
                tr.appendChild(td);
            }
            tbody.appendChild(tr);
        }
        table.appendChild(tbody);
        tableWrap.appendChild(table);
        section.appendChild(tableWrap);
        return section;
    }

    function d1RowEditorNode(row) {
        const form = document.createElement('form');
        form.className = 'oauth-form';
        const title = document.createElement('h4');
        title.className = 'oauth-section-title';
        title.textContent = t('oauth_d1_edit_row_title', { rowid: String(row[state.oauth.d1RowIDKey] ?? '') });
        form.appendChild(title);

        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid oauth-form-grid--d1-row';
        const inputs = new Map();
        for (const column of state.oauth.d1TableColumns) {
            const input = textInput(fieldEditValue(row?.[column.name]), 'text');
            input.maxLength = 65536;
            inputs.set(column.name, input);
            grid.appendChild(formField(column.name, input));
        }
        form.appendChild(grid);

        const hint = document.createElement('div');
        hint.className = 'oauth-row-meta';
        hint.textContent = t('oauth_d1_edit_hint');
        form.appendChild(hint);

        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        actions.appendChild(smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
            state.oauth.d1EditingRow = null;
            renderOAuthResource();
        }));
        actions.appendChild(smallButton(t('delete'), 'btn btn--sm btn--danger', () => deleteD1Row(row)));
        const saveButton = smallButton(t('save'), 'btn btn--sm btn--primary');
        saveButton.type = 'submit';
        actions.appendChild(saveButton);
        form.appendChild(actions);

        form.addEventListener('submit', (event) => {
            event.preventDefault();
            const changes = {};
            for (const column of state.oauth.d1TableColumns) {
                const original = fieldEditValue(row?.[column.name]);
                const next = inputs.get(column.name)?.value ?? '';
                if (next !== original) changes[column.name] = next;
            }
            if (!Object.keys(changes).length) {
                toast.err(t('oauth_d1_no_changes'));
                return;
            }
            updateD1Row(changes, saveButton);
        });
        return form;
    }

    function snippetDiagnosticsText() {
        const status = state.oauth.status || {};
        const session = state.oauth.snippetSession || state.oauth.snippetContent?.session || status.current || {};
        const selectedSnippet = state.oauth.snippets.find((item) => item.name === state.oauth.selectedSnippetName) || null;
        const content = state.oauth.snippetContent || null;
        return JSON.stringify({
            type: 'cfui_oauth_snippet_diagnostics',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            browser_path: window.location.pathname,
            contains_oauth_token: false,
            contains_refresh_token: false,
            contains_snippet_code: false,
            contains_draft_code: false,
            contains_rule_expression: false,
            contains_rule_description: false,
            sensitive_fields_omitted: [
                'oauth_access_token',
                'oauth_refresh_token',
                'snippet_code',
                'snippet_draft_code',
                'snippet_rule_expression',
                'snippet_rule_description',
            ],
            selected: {
                account_id: state.oauth.selectedAccountId || '',
                account_name: selectedAccountName(),
                zone_id: state.oauth.selectedZoneId || '',
                zone_name: selectedZoneName(),
                snippet_name: state.oauth.selectedSnippetName || '',
                resource: state.oauth.resource || '',
            },
            identity: {
                label: session.label || '',
                expires_at: session.expires_at || '',
                scopes: Array.isArray(session.scopes) ? session.scopes : [],
            },
            capability: {
                snippets_read: canRead('snippets'),
                snippets_write: canWrite('snippets'),
            },
            state: {
                snippet_count: state.oauth.snippets.length,
                snippets_error: state.oauth.snippetsError || '',
                rules_loaded: state.oauth.snippetRules.length,
                rules_error: state.oauth.snippetRulesError || '',
                mutation_error: state.oauth.snippetMutationError || '',
                create_open: !!state.oauth.snippetCreateOpen,
                rule_create_open: !!state.oauth.snippetRuleCreateOpen,
                content_loading: !!state.oauth.snippetContentLoading,
                content_error: state.oauth.snippetContentError || '',
                content_loaded: !!content,
                scope_ready: canRead('snippets'),
            },
            snippets: {
                loaded_count: state.oauth.snippets.length,
                items: state.oauth.snippets.map(snippetListDiagnostics),
            },
            selected_snippet: selectedSnippet ? snippetListDiagnostics(selectedSnippet) : null,
            selected_content: content ? snippetContentDiagnostics(content) : null,
            rules: {
                loaded_count: state.oauth.snippetRules.length,
                items: state.oauth.snippetRules.map(snippetRuleDiagnostics),
            },
            capabilities: oauthCapabilityDiagnostics(state.oauth.snippetCapabilities || content?.capabilities || status.capabilities || {}),
        }, null, 2);
    }

    function snippetListDiagnostics(snippet) {
        return {
            name: snippet?.name || '',
            created_on: snippet?.created_on || '',
            modified_on: snippet?.modified_on || '',
            rule_count: Number(snippet?.rule_count || 0),
        };
    }

    function snippetContentDiagnostics(content) {
        const draft = state.oauth.snippetContentDraft || '';
        const value = content?.value || '';
        return {
            name: content?.name || state.oauth.selectedSnippetName || '',
            main_file: content?.main_file || state.oauth.snippetContentMainFile || '',
            encoding: content?.encoding || '',
            bytes: content?.bytes == null ? null : Number(content.bytes),
            truncated: !!content?.truncated,
            code_included: false,
            code_length: value.length,
            draft_code_included: false,
            draft_code_length: draft.length,
        };
    }

    function snippetRuleDiagnostics(rule) {
        return {
            id: rule?.id || '',
            snippet_name: rule?.snippet_name || '',
            enabled: !!rule?.enabled,
            expression_included: false,
            expression_length: String(rule?.expression || '').length,
            description_included: false,
            description_present: !!rule?.description,
            description_length: String(rule?.description || '').length,
        };
    }

    function renderSnippets(body) {
        if (!state.oauth.selectedZoneId) {
            body.appendChild(empty(t('oauth_select_zone')));
            return;
        }
        const actions = [];
        if (canWrite('snippets')) {
            actions.push({
                text: state.oauth.snippetCreateOpen ? t('cancel') : t('oauth_snippet_create'),
                className: state.oauth.snippetCreateOpen ? 'btn btn--sm btn--ghost' : 'btn btn--sm',
                onClick: () => {
                    state.oauth.snippetCreateOpen = !state.oauth.snippetCreateOpen;
                    renderOAuthResource();
                },
            });
        }
        actions.push({
            text: t('oauth_snippet_copy_diagnostics'),
            className: 'btn btn--sm btn--ghost',
            title: t('oauth_snippet_copy_diagnostics_title'),
            onClick: () => copyOAuthText(snippetDiagnosticsText()),
        });
        body.appendChild(resourceActionBar(t('oauth_snippets'), actions));
        if (!canWrite('snippets')) body.appendChild(empty(t('oauth_snippets_readonly')));
        if (state.oauth.snippetMutationError) body.appendChild(empty(state.oauth.snippetMutationError));
        if (state.oauth.snippetCreateOpen) body.appendChild(snippetCreateFormNode());
        if (state.oauth.snippetsError) {
            body.appendChild(empty(state.oauth.snippetsError));
            return;
        }
        if (!state.oauth.snippets.length) {
            body.appendChild(empty(t('oauth_no_snippets')));
            return;
        }
        const list = document.createElement('section');
        list.className = 'oauth-section';
        for (const snippet of state.oauth.snippets) {
            const meta = [
                t('oauth_snippet_rule_count', { n: snippet.rule_count || 0 }),
                snippet.modified_on ? `${t('oauth_modified')} ${formatDate(snippet.modified_on)}` : '',
            ].filter(Boolean).join(' · ');
            const row = rowNode(snippet.name, meta);
            row.setAttribute('data-selected', String(snippet.name === state.oauth.selectedSnippetName));
            row.addEventListener('click', async () => {
                state.oauth.selectedSnippetName = snippet.name;
                state.oauth.snippetRuleCreateOpen = false;
                resetSnippetContent();
                renderOAuthResource();
                await Promise.all([
                    loadSnippetRules(snippet.name),
                    loadSnippetContent(snippet.name),
                ]);
                renderOAuthResource();
            });
            list.appendChild(row);
        }
        body.appendChild(list);
        if (state.oauth.selectedSnippetName) body.appendChild(snippetDetailNode());
    }

    function snippetCreateFormNode() {
        const form = document.createElement('form');
        form.className = 'oauth-form';

        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid oauth-form-grid--snippet';
        const nameInput = textInput('', 'text');
        nameInput.placeholder = 'snippet_name';
        nameInput.maxLength = 128;
        nameInput.pattern = '[A-Za-z0-9_]+';
        const fileInput = textInput('snippet.js', 'text');
        fileInput.maxLength = 128;
        grid.append(
            formField(t('oauth_snippet_name'), nameInput),
            formField(t('oauth_snippet_main_file'), fileInput),
        );
        form.appendChild(grid);

        const codeArea = document.createElement('textarea');
        codeArea.className = 'oauth-code-editor';
        codeArea.value = snippetTemplate();
        codeArea.spellcheck = false;
        codeArea.maxLength = 524288;
        form.appendChild(formField(t('oauth_snippet_code'), codeArea));

        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        actions.appendChild(smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
            state.oauth.snippetCreateOpen = false;
            renderOAuthResource();
        }));
        const saveButton = smallButton(t('oauth_snippet_create'), 'btn btn--sm btn--primary');
        saveButton.type = 'submit';
        actions.appendChild(saveButton);
        form.appendChild(actions);

        form.addEventListener('submit', (event) => {
            event.preventDefault();
            createSnippet({
                name: nameInput.value.trim(),
                main_file: fileInput.value.trim(),
                code: codeArea.value,
            }, saveButton);
        });
        return form;
    }

    function snippetDetailNode() {
        const snippet = state.oauth.snippets.find((item) => item.name === state.oauth.selectedSnippetName);
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const header = document.createElement('div');
        header.className = 'oauth-action-bar';
        const copy = document.createElement('div');
        const title = document.createElement('div');
        title.className = 'oauth-action-title mono';
        title.textContent = state.oauth.selectedSnippetName;
        const meta = document.createElement('div');
        meta.className = 'oauth-action-meta';
        meta.textContent = snippet?.modified_on ? `${t('oauth_modified')} ${formatDate(snippet.modified_on)}` : t('oauth_snippet_rules');
        copy.append(title, meta);
        header.appendChild(copy);
        const actions = document.createElement('div');
        actions.className = 'oauth-row-actions';
        const diagnostics = smallButton(t('oauth_snippet_copy_diagnostics'), 'btn btn--sm btn--ghost', () => copyOAuthText(snippetDiagnosticsText()));
        diagnostics.title = t('oauth_snippet_copy_diagnostics_title');
        diagnostics.setAttribute('aria-label', t('oauth_snippet_copy_diagnostics_title'));
        actions.appendChild(diagnostics);
        if (canWrite('snippets')) {
            actions.appendChild(smallButton(state.oauth.snippetRuleCreateOpen ? t('cancel') : t('oauth_snippet_rule_add'), 'btn btn--sm btn--ghost', () => {
                state.oauth.snippetRuleCreateOpen = !state.oauth.snippetRuleCreateOpen;
                renderOAuthResource();
            }));
            actions.appendChild(smallButton(t('delete'), 'btn btn--sm btn--danger', () => deleteSnippet(state.oauth.selectedSnippetName)));
        }
        header.appendChild(actions);
        section.appendChild(header);

        section.appendChild(snippetContentNode());

        if (state.oauth.snippetRuleCreateOpen) section.appendChild(snippetRuleFormNode());
        if (state.oauth.snippetRulesError) {
            section.appendChild(empty(state.oauth.snippetRulesError));
            return section;
        }
        if (!state.oauth.snippetRules.length) {
            section.appendChild(empty(t('oauth_snippet_no_rules')));
            return section;
        }

        for (const rule of state.oauth.snippetRules) {
            const row = document.createElement('div');
            row.className = 'oauth-row';
            const titleNode = document.createElement('div');
            titleNode.className = 'oauth-row-title';
            titleNode.textContent = rule.description || rule.id || rule.expression;
            const metaNode = document.createElement('div');
            metaNode.className = 'oauth-row-meta mono';
            metaNode.textContent = rule.expression || '';
            const copyNode = document.createElement('div');
            copyNode.append(titleNode, metaNode);
            row.appendChild(copyNode);

            const rowActions = document.createElement('div');
            rowActions.className = 'oauth-row-actions';
            const stateLabel = document.createElement('span');
            stateLabel.className = 'oauth-badge';
            stateLabel.textContent = rule.enabled ? t('oauth_enabled_state') : t('oauth_disabled');
            rowActions.appendChild(stateLabel);
            if (canWrite('snippets')) {
                const toggle = document.createElement('input');
                toggle.type = 'checkbox';
                toggle.checked = !!rule.enabled;
                toggle.title = t('oauth_snippet_rule_enabled');
                toggle.setAttribute('aria-label', t('oauth_snippet_rule_enabled'));
                toggle.addEventListener('change', () => setSnippetRuleEnabled(rule, toggle.checked, toggle));
                rowActions.appendChild(toggle);
                rowActions.appendChild(smallButton(t('delete'), 'btn btn--sm btn--danger', () => deleteSnippetRule(rule)));
            }
            row.appendChild(rowActions);
            section.appendChild(row);
        }
        return section;
    }

    function snippetContentNode() {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const header = document.createElement('div');
        header.className = 'oauth-action-bar';
        const copy = document.createElement('div');
        const title = document.createElement('div');
        title.className = 'oauth-action-title';
        title.textContent = t('oauth_snippet_content');
        const meta = document.createElement('div');
        meta.className = 'oauth-action-meta mono';
        meta.textContent = state.oauth.snippetContent?.main_file || state.oauth.snippetContentMainFile || '';
        copy.append(title, meta);
        header.appendChild(copy);
        section.appendChild(header);

        if (state.oauth.snippetContentLoading) {
            section.appendChild(empty(t('oauth_snippet_content_loading')));
            return section;
        }
        if (state.oauth.snippetContentError) {
            section.appendChild(empty(state.oauth.snippetContentError));
            return section;
        }
        const content = state.oauth.snippetContent;
        if (!content) {
            section.appendChild(empty(t('oauth_snippet_content_unavailable')));
            return section;
        }
        if (content.encoding && content.encoding !== 'utf-8') {
            section.appendChild(empty(t('oauth_snippet_content_binary')));
            return section;
        }

        const form = document.createElement('form');
        form.className = 'oauth-form';
        const fileInput = textInput(state.oauth.snippetContentMainFile || content.main_file || 'snippet.js', 'text');
        fileInput.maxLength = 128;
        fileInput.disabled = !canWrite('snippets');
        fileInput.addEventListener('input', () => { state.oauth.snippetContentMainFile = fileInput.value; });
        form.appendChild(formField(t('oauth_snippet_main_file'), fileInput));

        const codeArea = document.createElement('textarea');
        codeArea.className = 'oauth-code-editor';
        codeArea.value = state.oauth.snippetContentDraft ?? content.value ?? '';
        codeArea.spellcheck = false;
        codeArea.maxLength = 524288;
        codeArea.disabled = !canWrite('snippets');
        codeArea.addEventListener('input', () => { state.oauth.snippetContentDraft = codeArea.value; });
        form.appendChild(formField(t('oauth_snippet_code'), codeArea));

        if (content.truncated) {
            form.appendChild(empty(t('oauth_snippet_content_truncated')));
        }
        if (canWrite('snippets')) {
            const actions = document.createElement('div');
            actions.className = 'oauth-form-actions';
            const saveButton = smallButton(t('save'), 'btn btn--sm btn--primary');
            saveButton.type = 'submit';
            actions.appendChild(saveButton);
            form.appendChild(actions);
            form.addEventListener('submit', (event) => {
                event.preventDefault();
                saveSnippetContent(fileInput.value.trim(), codeArea.value, saveButton);
            });
        }
        section.appendChild(form);
        return section;
    }

    function snippetRuleFormNode() {
        const form = document.createElement('form');
        form.className = 'oauth-form';
        const expressionArea = document.createElement('textarea');
        expressionArea.className = 'oauth-code-editor oauth-code-editor--compact';
        expressionArea.value = 'http.request.uri.path contains "/"';
        expressionArea.spellcheck = false;
        expressionArea.maxLength = 4096;
        const descriptionInput = textInput('', 'text');
        const enabledInput = document.createElement('input');
        enabledInput.type = 'checkbox';
        enabledInput.checked = true;
        form.append(
            formField(t('oauth_snippet_rule_expression'), expressionArea),
            expressionHelperNode(expressionArea),
            formField(t('oauth_snippet_rule_description'), descriptionInput),
            formField(t('oauth_snippet_rule_enabled'), enabledInput),
        );
        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        actions.appendChild(smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
            state.oauth.snippetRuleCreateOpen = false;
            renderOAuthResource();
        }));
        const saveButton = smallButton(t('oauth_snippet_rule_add'), 'btn btn--sm btn--primary');
        saveButton.type = 'submit';
        actions.appendChild(saveButton);
        form.appendChild(actions);
        form.addEventListener('submit', (event) => {
            event.preventDefault();
            createSnippetRule({
                snippet_name: state.oauth.selectedSnippetName,
                expression: expressionArea.value,
                description: descriptionInput.value.trim(),
                enabled: enabledInput.checked,
            }, saveButton);
        });
        return form;
    }

    function expressionHelperNode(textarea) {
        const panel = document.createElement('div');
        panel.className = 'oauth-expression-helper';

        const title = document.createElement('div');
        title.className = 'oauth-expression-helper-title';
        title.textContent = t('oauth_expression_helper_title');
        panel.appendChild(title);

        const row = document.createElement('div');
        row.className = 'oauth-expression-helper-row';

        const preset = document.createElement('select');
        preset.setAttribute('aria-label', t('oauth_expression_helper_preset'));
        const presets = [
            ['host_eq', 'oauth_expression_preset_host_eq', 'example.com'],
            ['path_starts_with', 'oauth_expression_preset_path_starts_with', '/admin'],
            ['path_contains', 'oauth_expression_preset_path_contains', '/api/'],
            ['method_eq', 'oauth_expression_preset_method_eq', 'POST'],
            ['country_eq', 'oauth_expression_preset_country_eq', 'US'],
            ['ip_src', 'oauth_expression_preset_ip_src', '203.0.113.10'],
        ];
        for (const [value, labelKey] of presets) {
            const option = document.createElement('option');
            option.value = value;
            option.textContent = t(labelKey);
            preset.appendChild(option);
        }

        const valueInput = textInput('', 'text');
        valueInput.autocomplete = 'off';
        valueInput.spellcheck = false;
        valueInput.setAttribute('aria-label', t('oauth_expression_helper_value'));
        const updatePlaceholder = () => {
            const found = presets.find((item) => item[0] === preset.value);
            valueInput.placeholder = found?.[2] || '';
        };
        preset.addEventListener('change', updatePlaceholder);
        updatePlaceholder();

        const insert = smallButton(t('oauth_expression_helper_insert'), 'btn btn--sm btn--ghost', () => {
            const expr = buildExpressionHelperSnippet(preset.value, valueInput.value);
            if (!expr) return;
            setExpressionTextareaValue(textarea, mergeExpression(textarea.value, expr));
        });
        const replace = smallButton(t('oauth_expression_helper_replace'), 'btn btn--sm btn--ghost', () => {
            const expr = buildExpressionHelperSnippet(preset.value, valueInput.value);
            if (!expr) return;
            setExpressionTextareaValue(textarea, expr);
        });

        row.append(preset, valueInput, insert, replace);
        panel.appendChild(row);
        return panel;
    }

    function buildExpressionHelperSnippet(kind, rawValue) {
        let value = String(rawValue || '').trim();
        if (!value) {
            toast.err(t('oauth_expression_helper_value_required'));
            return '';
        }
        switch (kind) {
        case 'host_eq':
            return `http.host eq ${cfExpressionString(value)}`;
        case 'path_starts_with':
            if (!value.startsWith('/')) value = '/' + value;
            return `http.request.uri.path starts_with ${cfExpressionString(value)}`;
        case 'path_contains':
            return `http.request.uri.path contains ${cfExpressionString(value)}`;
        case 'method_eq':
            return `http.request.method eq ${cfExpressionString(value.toUpperCase())}`;
        case 'country_eq':
            return `ip.geoip.country eq ${cfExpressionString(value.toUpperCase())}`;
        case 'ip_src':
            return buildIPSourceExpression(value);
        default:
            return '';
        }
    }

    function cfExpressionString(value) {
        return `"${String(value || '').replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"`;
    }

    function buildIPSourceExpression(value) {
        const cleaned = String(value || '').trim();
        if (!/^[0-9a-fA-F:.\/]+$/.test(cleaned)) {
            toast.err(t('oauth_expression_helper_ip_invalid'));
            return '';
        }
        if (cleaned.includes('/')) return `ip.src in {${cleaned}}`;
        return `ip.src eq ${cleaned}`;
    }

    function mergeExpression(current, snippet) {
        const existing = String(current || '').trim();
        if (!existing || existing === 'true') return snippet;
        return `(${existing}) and (${snippet})`;
    }

	function setExpressionTextareaValue(textarea, value) {
		textarea.value = value;
		textarea.focus();
		textarea.dispatchEvent(new Event('input', { bubbles: true }));
	}

	function wafDiagnosticsText() {
		const status = state.oauth.status || {};
		const session = state.oauth.wafSession || state.oauth.wafRuleset?.session || state.oauth.wafManagedRuleset?.session || state.oauth.wafManagedOverrideRuleset?.session || status.current || {};
		const customRules = wafRulesetRules(state.oauth.wafRuleset);
		const managedExceptions = wafRulesetRules(state.oauth.wafManagedRuleset);
		const managedOverrides = wafRulesetRules(state.oauth.wafManagedOverrideRuleset);
		return JSON.stringify({
			type: 'cfui_oauth_waf_diagnostics',
			version: 1,
			generated_at: new Date().toISOString(),
			browser_origin: window.location.origin,
			browser_path: window.location.pathname,
			contains_oauth_token: false,
			contains_refresh_token: false,
			contains_waf_expression: false,
			contains_waf_description: false,
			contains_action_parameters_raw: false,
			contains_managed_ruleset_ids: false,
			contains_managed_rule_ids: false,
			contains_rate_limit_counting_expression: false,
			contains_credential_check_expression: false,
			contains_audit_json: false,
			contains_advanced_json: false,
			sensitive_fields_omitted: [
				'oauth_access_token',
				'oauth_refresh_token',
				'waf_expression',
				'waf_description',
				'waf_action_parameters_raw',
				'waf_managed_ruleset_id',
				'waf_managed_rule_ids',
				'waf_rate_limit_counting_expression',
				'waf_credential_check_expression',
				'waf_audit_json',
				'waf_advanced_json',
			],
			selected: {
				account_id: state.oauth.selectedAccountId || '',
				account_name: selectedAccountName(),
				zone_id: state.oauth.selectedZoneId || '',
				zone_name: selectedZoneName(),
				resource: state.oauth.resource || '',
			},
			identity: {
				label: session.label || '',
				expires_at: session.expires_at || '',
				scopes: Array.isArray(session.scopes) ? session.scopes : [],
			},
			capability: {
				waf_read: canRead('waf'),
				waf_write: canWrite('waf'),
			},
			state: {
				load_error: state.oauth.wafError || '',
				mutation_error: state.oauth.wafMutationError || '',
				custom_rules_loaded: customRules.length,
				managed_exceptions_loaded: managedExceptions.length,
				managed_overrides_loaded: managedOverrides.length,
				create_rule_open: !!state.oauth.wafCreateOpen,
				editing_rule_id: state.oauth.wafEditingId || '',
				create_managed_exception_open: !!state.oauth.wafManagedExceptionCreateOpen,
				editing_managed_exception_id: state.oauth.wafManagedExceptionEditingId || '',
				create_managed_override_open: !!state.oauth.wafManagedOverrideCreateOpen,
				editing_managed_override_id: state.oauth.wafManagedOverrideEditingId || '',
				scope_ready: canRead('waf'),
			},
			rulesets: {
				custom: wafRulesetDiagnostics(state.oauth.wafRuleset, 'custom'),
				managed_exceptions: wafRulesetDiagnostics(state.oauth.wafManagedRuleset, 'managed_exceptions'),
				managed_overrides: wafRulesetDiagnostics(state.oauth.wafManagedOverrideRuleset, 'managed_overrides'),
			},
			capabilities: oauthCapabilityDiagnostics(state.oauth.wafCapabilities || state.oauth.wafRuleset?.capabilities || state.oauth.wafManagedRuleset?.capabilities || state.oauth.wafManagedOverrideRuleset?.capabilities || status.capabilities || {}),
		}, null, 2);
	}

	function wafRulesetRules(ruleset) {
		return Array.isArray(ruleset?.rules) ? ruleset.rules : [];
	}

	function wafRulesetDiagnostics(ruleset, kind) {
		const rules = wafRulesetRules(ruleset);
		return {
			kind,
			loaded: !!ruleset,
			id: ruleset?.id || '',
			name: ruleset?.name || '',
			phase: ruleset?.phase || '',
			last_updated: ruleset?.last_updated || '',
			rule_count: rules.length,
			rules: rules.map((rule) => wafRuleDiagnostics(rule, kind)),
		};
	}

	function wafRuleDiagnostics(rule, kind) {
		return {
			kind,
			id: rule?.id || '',
			ref: rule?.ref || '',
			version: rule?.version || '',
			action: rule?.action || '',
			enabled: rule?.enabled === false ? false : true,
			score_threshold: Number(rule?.score_threshold || 0),
			last_updated: rule?.last_updated || '',
			expression_included: false,
			expression_length: String(rule?.expression || '').length,
			description_included: false,
			description_present: !!rule?.description,
			description_length: String(rule?.description || '').length,
			action_parameters: wafActionParametersDiagnostics(rule?.action_parameters),
			rate_limit: wafRateLimitDiagnostics(rule?.ratelimit),
			logging: rule?.logging ? { enabled: rule.logging.enabled == null ? null : !!rule.logging.enabled } : null,
			credential_check: wafCredentialCheckDiagnostics(rule?.exposed_credential_check),
			editable: kind === 'custom'
				? isEditableWAFRule(rule)
				: (kind === 'managed_overrides' ? isEditableWAFManagedOverride(rule) : isEditableWAFManagedException(rule)),
		};
	}

	function wafActionParametersDiagnostics(params) {
		if (!params) return null;
		const raw = params.raw && typeof params.raw === 'object' ? params.raw : {};
		const rules = params.rules && typeof params.rules === 'object' ? params.rules : {};
		const rulesetRuleCounts = Object.values(rules).reduce((sum, item) => sum + (Array.isArray(item) ? item.length : 0), 0);
		return {
			id_present: !!params.id,
			id_included: false,
			id_length: String(params.id || '').length,
			ruleset_present: !!params.ruleset,
			ruleset_included: false,
			ruleset_length: String(params.ruleset || '').length,
			rulesets_count: Array.isArray(params.rulesets) ? params.rulesets.length : 0,
			rulesets_included: false,
			rules_map_ruleset_count: Object.keys(rules).length,
			rules_map_rule_count: rulesetRuleCounts,
			rules_map_included: false,
			products_count: Array.isArray(params.products) ? params.products.length : 0,
			products_included: false,
			phases_count: Array.isArray(params.phases) ? params.phases.length : 0,
			phases_included: false,
			version_present: params.version != null && String(params.version) !== '',
			version_included: false,
			overrides: wafManagedOverridesDiagnostics(params.overrides),
			raw_present: Object.keys(raw).length > 0,
			raw_included: false,
			raw_keys: Object.keys(raw).sort(),
		};
	}

	function wafManagedOverridesDiagnostics(overrides) {
		if (!overrides) return null;
		return {
			enabled: overrides.enabled == null ? null : !!overrides.enabled,
			action: overrides.action || '',
			sensitivity_level: overrides.sensitivity_level || '',
			category_count: Array.isArray(overrides.categories) ? overrides.categories.length : 0,
			rule_count: Array.isArray(overrides.rules) ? overrides.rules.length : 0,
			categories_included: false,
			rule_ids_included: false,
		};
	}

	function wafRateLimitDiagnostics(rateLimit) {
		if (!rateLimit) return null;
		return {
			characteristics_count: Array.isArray(rateLimit.characteristics) ? rateLimit.characteristics.length : 0,
			characteristics_included: false,
			requests_per_period: Number(rateLimit.requests_per_period || 0),
			score_per_period: Number(rateLimit.score_per_period || 0),
			period: Number(rateLimit.period || 0),
			mitigation_timeout: Number(rateLimit.mitigation_timeout || 0),
			requests_to_origin: !!rateLimit.requests_to_origin,
			counting_expression_included: false,
			counting_expression_length: String(rateLimit.counting_expression || '').length,
			score_response_header_name_included: false,
			score_response_header_name_present: !!rateLimit.score_response_header_name,
		};
	}

	function wafCredentialCheckDiagnostics(check) {
		if (!check) return null;
		return {
			username_expression_included: false,
			username_expression_length: String(check.username_expression || '').length,
			password_expression_included: false,
			password_expression_length: String(check.password_expression || '').length,
		};
	}

	function renderWAF(body) {
		if (!state.oauth.selectedZoneId) {
			body.appendChild(empty(t('oauth_select_zone')));
			return;
		}
		if (!canWrite('waf')) body.appendChild(empty(t('oauth_waf_readonly')));
		body.appendChild(resourceActionBar(t('oauth_waf'), {
			text: t('oauth_waf_copy_diagnostics'),
			className: 'btn btn--sm btn--ghost',
			title: t('oauth_waf_copy_diagnostics_title'),
			onClick: () => copyOAuthText(wafDiagnosticsText()),
		}));
		if (state.oauth.wafError) {
			body.appendChild(empty(state.oauth.wafError));
			return;
		}
		if (state.oauth.wafMutationError) body.appendChild(empty(state.oauth.wafMutationError));

		body.appendChild(resourceActionBar(t('oauth_waf_custom_rules'), canWrite('waf') ? {
			text: state.oauth.wafCreateOpen ? t('cancel') : t('oauth_waf_create_rule'),
			className: state.oauth.wafCreateOpen ? 'btn btn--sm btn--ghost' : 'btn btn--sm',
			onClick: () => {
				state.oauth.wafCreateOpen = !state.oauth.wafCreateOpen;
				state.oauth.wafEditingId = '';
				state.oauth.wafManagedExceptionCreateOpen = false;
				state.oauth.wafManagedExceptionEditingId = '';
				state.oauth.wafManagedOverrideCreateOpen = false;
				state.oauth.wafManagedOverrideEditingId = '';
				renderOAuthResource();
			},
		} : null));
		if (state.oauth.wafCreateOpen) body.appendChild(wafRuleFormNode());

		const rules = Array.isArray(state.oauth.wafRuleset?.rules) ? state.oauth.wafRuleset.rules : [];
		renderWAFRuleRows(body, rules, {
			emptyKey: 'oauth_no_waf_rules',
			editingId: state.oauth.wafEditingId,
			canEditRule: isEditableWAFRule,
			editLabelKey: 'oauth_waf_edit_rule',
			onEdit: (rule) => {
				state.oauth.wafEditingId = state.oauth.wafEditingId === rule.id ? '' : rule.id;
				state.oauth.wafCreateOpen = false;
				state.oauth.wafManagedExceptionCreateOpen = false;
				state.oauth.wafManagedExceptionEditingId = '';
				state.oauth.wafManagedOverrideCreateOpen = false;
				state.oauth.wafManagedOverrideEditingId = '';
			},
			onToggle: setWAFRuleEnabled,
			onDelete: deleteWAFRule,
			formNode: wafRuleFormNode,
		});

		body.appendChild(resourceActionBar(t('oauth_waf_managed_overrides'), canWrite('waf') ? {
			text: state.oauth.wafManagedOverrideCreateOpen ? t('cancel') : t('oauth_waf_create_managed_override'),
			className: state.oauth.wafManagedOverrideCreateOpen ? 'btn btn--sm btn--ghost' : 'btn btn--sm',
			onClick: () => {
				state.oauth.wafManagedOverrideCreateOpen = !state.oauth.wafManagedOverrideCreateOpen;
				state.oauth.wafManagedOverrideEditingId = '';
				state.oauth.wafCreateOpen = false;
				state.oauth.wafEditingId = '';
				state.oauth.wafManagedExceptionCreateOpen = false;
				state.oauth.wafManagedExceptionEditingId = '';
				renderOAuthResource();
			},
		} : null));
		if (state.oauth.wafManagedOverrideCreateOpen) body.appendChild(wafManagedOverrideFormNode());

		const managedOverrideRules = Array.isArray(state.oauth.wafManagedOverrideRuleset?.rules) ? state.oauth.wafManagedOverrideRuleset.rules : [];
		renderWAFRuleRows(body, managedOverrideRules, {
			emptyKey: 'oauth_no_waf_managed_overrides',
			editingId: state.oauth.wafManagedOverrideEditingId,
			canEditRule: isEditableWAFManagedOverride,
			editLabelKey: 'oauth_waf_edit_managed_override',
			onEdit: (rule) => {
				state.oauth.wafManagedOverrideEditingId = state.oauth.wafManagedOverrideEditingId === rule.id ? '' : rule.id;
				state.oauth.wafManagedOverrideCreateOpen = false;
				state.oauth.wafCreateOpen = false;
				state.oauth.wafEditingId = '';
				state.oauth.wafManagedExceptionCreateOpen = false;
				state.oauth.wafManagedExceptionEditingId = '';
			},
			onToggle: setWAFManagedOverrideEnabled,
			onDelete: deleteWAFManagedOverride,
			formNode: wafManagedOverrideFormNode,
		});

		body.appendChild(resourceActionBar(t('oauth_waf_managed_exceptions'), canWrite('waf') ? {
			text: state.oauth.wafManagedExceptionCreateOpen ? t('cancel') : t('oauth_waf_create_managed_exception'),
			className: state.oauth.wafManagedExceptionCreateOpen ? 'btn btn--sm btn--ghost' : 'btn btn--sm',
			onClick: () => {
				state.oauth.wafManagedExceptionCreateOpen = !state.oauth.wafManagedExceptionCreateOpen;
				state.oauth.wafManagedExceptionEditingId = '';
				state.oauth.wafCreateOpen = false;
				state.oauth.wafEditingId = '';
				state.oauth.wafManagedOverrideCreateOpen = false;
				state.oauth.wafManagedOverrideEditingId = '';
				renderOAuthResource();
			},
		} : null));
		if (state.oauth.wafManagedExceptionCreateOpen) body.appendChild(wafManagedExceptionFormNode());

		const managedRules = Array.isArray(state.oauth.wafManagedRuleset?.rules) ? state.oauth.wafManagedRuleset.rules : [];
		renderWAFRuleRows(body, managedRules, {
			emptyKey: 'oauth_no_waf_managed_exceptions',
			editingId: state.oauth.wafManagedExceptionEditingId,
			canEditRule: isEditableWAFManagedException,
			editLabelKey: 'oauth_waf_edit_managed_exception',
			onEdit: (rule) => {
				state.oauth.wafManagedExceptionEditingId = state.oauth.wafManagedExceptionEditingId === rule.id ? '' : rule.id;
				state.oauth.wafManagedExceptionCreateOpen = false;
				state.oauth.wafCreateOpen = false;
				state.oauth.wafEditingId = '';
				state.oauth.wafManagedOverrideCreateOpen = false;
				state.oauth.wafManagedOverrideEditingId = '';
			},
			onToggle: setWAFManagedExceptionEnabled,
			onDelete: deleteWAFManagedException,
			formNode: wafManagedExceptionFormNode,
		});
	}

	function renderWAFRuleRows(body, rules, options) {
		if (!rules.length) {
			body.appendChild(empty(t(options.emptyKey)));
			return;
		}
		for (const rule of rules) {
            const enabled = rule.enabled === false ? t('oauth_disabled') : t('oauth_enabled_state');
            const actions = [];
            const actionBadge = document.createElement('span');
            actionBadge.className = 'oauth-badge';
            actionBadge.textContent = wafActionLabel(rule.action || '');
            actions.push(actionBadge);

            const statusBadge = document.createElement('span');
            statusBadge.className = 'oauth-badge';
            statusBadge.textContent = enabled;
			actions.push(statusBadge);

			if (canWrite('waf')) {
				if (rule.id && options.canEditRule(rule)) {
					actions.push(smallButton(t(options.editLabelKey), 'btn btn--sm btn--ghost', () => {
						options.onEdit(rule);
						renderOAuthResource();
					}));
				}
                const toggle = document.createElement('input');
                toggle.type = 'checkbox';
				toggle.checked = rule.enabled !== false;
				toggle.title = t('oauth_waf_enabled');
				toggle.setAttribute('aria-label', t('oauth_waf_enabled'));
				toggle.addEventListener('change', () => options.onToggle(rule, toggle.checked, toggle));
				actions.push(toggle);
				actions.push(smallButton(t('delete'), 'btn btn--sm btn--danger', () => options.onDelete(rule)));
			}
			const auditJSON = wafRuleAuditJSON(rule);
			if (auditJSON) {
                actions.push(smallButton(t('oauth_waf_copy_audit_json'), 'btn btn--sm btn--ghost', () => copyOAuthText(auditJSON)));
            }

            const title = rule.description || rule.id || wafActionLabel(rule.action || '');
            const meta = [rule.id || '', wafActionParametersSummary(rule)].filter(Boolean).join(' · ');
			const row = rowNode(title, meta, actions);
			const detail = wafRuleDetailNode(rule);
			if (detail) row.appendChild(detail);
			if (options.editingId === rule.id && rule.id) {
				row.appendChild(options.formNode(rule));
			}
			body.appendChild(row);
		}
	}

    function wafRuleDetailNode(rule) {
        const rows = [];
        const add = (label, value) => {
            if (value == null || value === '') return;
            rows.push([label, value]);
        };
        add(t('oauth_waf_expression'), rule.expression || '');
        add(t('oauth_waf_ref'), rule.ref || '');
        add(t('oauth_waf_version'), rule.version || rule.action_parameters?.version || '');
        if (rule.score_threshold) add(t('oauth_waf_score_threshold'), String(rule.score_threshold));
        add(t('oauth_waf_last_updated'), formatDate(rule.last_updated));
        add(t('oauth_waf_action_parameters'), wafActionParametersDetail(rule.action_parameters));
        add(t('oauth_waf_rate_limit'), formatJSONPreview(rule.ratelimit));
        if (rule.logging) {
            add(t('oauth_waf_logging'), rule.logging.enabled == null ? t('oauth_unavailable') : (rule.logging.enabled === false ? t('oauth_disabled') : t('oauth_enabled_state')));
        }
        add(t('oauth_waf_credential_check'), formatJSONPreview(rule.exposed_credential_check));
        if (!rows.length) return null;

        const detail = document.createElement('dl');
        detail.className = 'oauth-row-detail';
        for (const [label, value] of rows) {
            const item = document.createElement('div');
            item.className = 'oauth-row-detail-item';
            const term = document.createElement('dt');
            term.textContent = label;
            const desc = document.createElement('dd');
            desc.textContent = value;
            item.append(term, desc);
            detail.appendChild(item);
        }
        return detail;
    }

	function wafRuleFormNode(rule = null) {
		const editing = !!rule?.id;
		const currentAction = String(rule?.action || '');
        const safeCurrentAction = !editing || wafActions.includes(currentAction);
        const safeCurrentParams = !editing || isEditableWAFActionParameters(currentAction, rule.action_parameters);
        const form = document.createElement('form');
        form.className = 'oauth-form';

        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid oauth-form-grid--snippet';

        const actionSelect = document.createElement('select');
        actionSelect.required = true;
        if (editing && currentAction && !safeCurrentAction) {
            const option = document.createElement('option');
            option.value = currentAction;
            option.textContent = wafActionLabel(currentAction);
            actionSelect.appendChild(option);
            actionSelect.value = currentAction;
            actionSelect.disabled = true;
        }
        for (const action of wafActions) {
            const option = document.createElement('option');
            option.value = action;
            option.textContent = wafActionLabel(action);
            actionSelect.appendChild(option);
        }
        if (editing && safeCurrentAction) actionSelect.value = currentAction;
        grid.appendChild(formField(t('oauth_waf_action'), actionSelect));

        const descriptionInput = textInput(editing ? (rule.description || '') : '', 'text');
        descriptionInput.maxLength = 256;
        descriptionInput.placeholder = t('oauth_waf_description');
        grid.appendChild(formField(t('oauth_waf_description'), descriptionInput));
        form.appendChild(grid);

        const skipOptions = wafSkipOptionsNode(editing ? rule.action_parameters : null, editing ? rule.id : 'create');
        form.appendChild(skipOptions.node);

        const updateSkipVisibility = () => {
            skipOptions.node.hidden = actionSelect.value !== 'skip';
        };
        actionSelect.addEventListener('change', updateSkipVisibility);
        updateSkipVisibility();

        const expressionArea = document.createElement('textarea');
        expressionArea.className = 'oauth-code-editor oauth-code-editor--compact';
        expressionArea.required = true;
        expressionArea.maxLength = 4096;
        expressionArea.spellcheck = false;
        expressionArea.placeholder = 'http.request.uri.path contains "/admin"';
        expressionArea.value = editing ? (rule.expression || '') : '';
        form.appendChild(formField(t('oauth_waf_expression'), expressionArea));
        form.appendChild(expressionHelperNode(expressionArea));

        const enabledInput = document.createElement('input');
        enabledInput.type = 'checkbox';
        enabledInput.checked = editing ? rule.enabled !== false : true;
        form.appendChild(formField(t('oauth_waf_enabled'), enabledInput));

        const rateLimit = wafRateLimitNode(editing ? rule : null, editing ? rule.id : 'create');
        form.appendChild(rateLimit.node);

        const advancedJSON = editing ? wafAdvancedJSONNode(rule) : null;
        if (advancedJSON) form.appendChild(advancedJSON.node);

        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        actions.appendChild(smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
            if (editing) state.oauth.wafEditingId = '';
            else state.oauth.wafCreateOpen = false;
            renderOAuthResource();
        }));
        const saveButton = smallButton(t(editing ? 'oauth_waf_update_rule' : 'oauth_waf_create_rule'), 'btn btn--sm btn--primary');
        saveButton.type = 'submit';
        actions.appendChild(saveButton);
        form.appendChild(actions);

        form.addEventListener('submit', (event) => {
            event.preventDefault();
            const payload = {
                expression: expressionArea.value,
                description: descriptionInput.value.trim(),
                enabled: enabledInput.checked,
            };
            const actionChanged = !editing || actionSelect.value !== currentAction;
            const shouldSubmitSafeAction = !editing || actionChanged || safeCurrentParams;
            if (shouldSubmitSafeAction && !actionSelect.disabled) {
                payload.action = actionSelect.value;
            }
            if (shouldSubmitSafeAction && actionSelect.value === 'skip') {
                payload.action_parameters = skipOptions.value();
                if (!payload.action_parameters) {
                    toast.err(t('oauth_waf_skip_requires_option'));
                    return;
                }
            }
            const rateLimitPayload = rateLimit.value();
            if (!rateLimitPayload) return;
            Object.assign(payload, rateLimitPayload);
            if (advancedJSON) {
                const advancedPayload = advancedJSON.value();
                if (!advancedPayload) return;
                Object.assign(payload, advancedPayload);
            }
            if (editing) updateWAFRule(rule, payload, saveButton);
            else createWAFRule(payload, saveButton);
        });
		return form;
	}

	function wafManagedExceptionFormNode(rule = null) {
		const editing = !!rule?.id;
		const form = document.createElement('form');
		form.className = 'oauth-form';

		const grid = document.createElement('div');
		grid.className = 'oauth-form-grid oauth-form-grid--snippet';

		const descriptionInput = textInput(editing ? (rule.description || '') : '', 'text');
		descriptionInput.maxLength = 256;
		descriptionInput.placeholder = t('oauth_waf_description');
		grid.appendChild(formField(t('oauth_waf_description'), descriptionInput));

		const enabledInput = document.createElement('input');
		enabledInput.type = 'checkbox';
		enabledInput.checked = editing ? rule.enabled !== false : true;
		grid.appendChild(formField(t('oauth_waf_enabled'), enabledInput));
		form.appendChild(grid);

		const expressionArea = document.createElement('textarea');
		expressionArea.className = 'oauth-code-editor oauth-code-editor--compact';
		expressionArea.required = true;
		expressionArea.maxLength = 4096;
		expressionArea.spellcheck = false;
		expressionArea.placeholder = 'http.request.uri.path contains "/legacy"';
		expressionArea.value = editing ? (rule.expression || '') : '';
		form.appendChild(formField(t('oauth_waf_expression'), expressionArea));
		form.appendChild(expressionHelperNode(expressionArea));

		const targets = wafManagedExceptionTargetsNode(editing ? rule.action_parameters : null, editing ? rule.id : 'create');
		form.appendChild(targets.node);

		const actions = document.createElement('div');
		actions.className = 'oauth-form-actions';
		actions.appendChild(smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
			if (editing) state.oauth.wafManagedExceptionEditingId = '';
			else state.oauth.wafManagedExceptionCreateOpen = false;
			renderOAuthResource();
		}));
		const saveButton = smallButton(t(editing ? 'oauth_waf_update_managed_exception' : 'oauth_waf_create_managed_exception'), 'btn btn--sm btn--primary');
		saveButton.type = 'submit';
		actions.appendChild(saveButton);
		form.appendChild(actions);

		form.addEventListener('submit', (event) => {
			event.preventDefault();
			const actionParameters = targets.value();
			if (actionParameters === false) return;
			if (!actionParameters) {
				toast.err(t('oauth_waf_skip_requires_option'));
				return;
			}
			const payload = {
				action: 'skip',
				expression: expressionArea.value,
				description: descriptionInput.value.trim(),
				enabled: enabledInput.checked,
				action_parameters: actionParameters,
			};
			if (editing) updateWAFManagedException(rule, payload, saveButton);
			else createWAFManagedException(payload, saveButton);
		});
		return form;
	}

	function wafManagedOverrideFormNode(rule = null) {
		const editing = !!rule?.id;
		const params = rule?.action_parameters && typeof rule.action_parameters === 'object' ? rule.action_parameters : {};
		const form = document.createElement('form');
		form.className = 'oauth-form';

		const grid = document.createElement('div');
		grid.className = 'oauth-form-grid oauth-form-grid--snippet';

		const managedRulesetInput = textInput(editing ? (params.id || '') : '', 'text');
		managedRulesetInput.required = true;
		managedRulesetInput.placeholder = 'efb7b8c949ac4650a09736fc376e9aee';
		grid.appendChild(formField(t('oauth_waf_managed_ruleset_id'), managedRulesetInput));

		const descriptionInput = textInput(editing ? (rule.description || '') : '', 'text');
		descriptionInput.maxLength = 256;
		descriptionInput.placeholder = t('oauth_waf_description');
		grid.appendChild(formField(t('oauth_waf_description'), descriptionInput));

		const enabledInput = document.createElement('input');
		enabledInput.type = 'checkbox';
		enabledInput.checked = editing ? rule.enabled !== false : true;
		grid.appendChild(formField(t('oauth_waf_enabled'), enabledInput));
		form.appendChild(grid);

		const expressionArea = document.createElement('textarea');
		expressionArea.className = 'oauth-code-editor oauth-code-editor--compact';
		expressionArea.required = true;
		expressionArea.maxLength = 4096;
		expressionArea.spellcheck = false;
		expressionArea.placeholder = 'true';
		expressionArea.value = editing ? (rule.expression || '') : 'true';
		form.appendChild(formField(t('oauth_waf_expression'), expressionArea));
		form.appendChild(expressionHelperNode(expressionArea));

		const overrides = wafManagedOverrideOverridesNode(wafManagedOverridesFromRule(rule), editing ? rule.id : 'create');
		form.appendChild(overrides.node);

		const actions = document.createElement('div');
		actions.className = 'oauth-form-actions';
		actions.appendChild(smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
			if (editing) state.oauth.wafManagedOverrideEditingId = '';
			else state.oauth.wafManagedOverrideCreateOpen = false;
			renderOAuthResource();
		}));
		const saveButton = smallButton(t(editing ? 'oauth_waf_update_managed_override' : 'oauth_waf_create_managed_override'), 'btn btn--sm btn--primary');
		saveButton.type = 'submit';
		actions.appendChild(saveButton);
		form.appendChild(actions);

		form.addEventListener('submit', (event) => {
			event.preventDefault();
			const overridePayload = overrides.value();
			if (overridePayload === false) return;
			const payload = {
				managed_ruleset_id: managedRulesetInput.value.trim(),
				expression: expressionArea.value,
				description: descriptionInput.value.trim(),
				enabled: enabledInput.checked,
			};
			if (overridePayload) payload.overrides = overridePayload;
			else if (editing) payload.overrides = null;
			if (editing) updateWAFManagedOverride(rule, payload, saveButton);
			else createWAFManagedOverride(payload, saveButton);
		});
		return form;
	}

	function wafManagedOverrideOverridesNode(initialOverrides = null, idSuffix = 'create') {
		const overrides = initialOverrides && typeof initialOverrides === 'object' ? initialOverrides : {};
		const suffix = String(idSuffix || 'create').replace(/[^a-zA-Z0-9_-]/g, '-');
		const panel = document.createElement('div');
		panel.className = 'oauth-form-section';

		const title = document.createElement('div');
		title.className = 'oauth-form-section-title';
		title.textContent = t('oauth_waf_managed_override_overrides');
		panel.appendChild(title);

		const grid = document.createElement('div');
		grid.className = 'oauth-form-grid oauth-form-grid--snippet';
		const actionSelect = wafManagedOverrideActionSelect(overrides.action || '');
		grid.appendChild(formField(t('oauth_waf_managed_override_ruleset_action'), actionSelect));
		const enabledSelect = wafBooleanOverrideSelect(overrides.enabled);
		grid.appendChild(formField(t('oauth_waf_managed_override_ruleset_enabled'), enabledSelect));
		const sensitivitySelect = wafSensitivitySelect(overrides.sensitivity_level || '');
		grid.appendChild(formField(t('oauth_waf_managed_override_sensitivity'), sensitivitySelect));
		panel.appendChild(grid);

		const categoryTitle = document.createElement('div');
		categoryTitle.className = 'oauth-form-check-title';
		categoryTitle.textContent = t('oauth_waf_managed_override_categories');
		panel.appendChild(categoryTitle);
		const categoryRows = [];
		const categoryTarget = document.createElement('div');
		categoryTarget.className = 'oauth-managed-targets';
		panel.appendChild(categoryTarget);
		const addCategoryRow = (category = {}) => {
			const row = document.createElement('div');
			row.className = 'oauth-form-grid oauth-form-grid--d1-row';
			const categoryInput = textInput(category.category || '', 'text');
			categoryInput.placeholder = 'wordpress';
			const rowActionSelect = wafManagedOverrideActionSelect(category.action || '');
			const rowEnabledSelect = wafBooleanOverrideSelect(category.enabled);
			const removeWrap = document.createElement('div');
			removeWrap.className = 'oauth-form-actions';
			removeWrap.appendChild(smallButton(t('delete'), 'btn btn--sm btn--ghost', () => row.remove()));
			row.append(
				formField(t('oauth_waf_managed_override_category'), categoryInput),
				formField(t('oauth_waf_action'), rowActionSelect),
				formField(t('oauth_waf_managed_override_enabled_override'), rowEnabledSelect),
				removeWrap,
			);
			categoryRows.push({ node: row, categoryInput, actionSelect: rowActionSelect, enabledSelect: rowEnabledSelect });
			categoryTarget.appendChild(row);
		};
		(Array.isArray(overrides.categories) ? overrides.categories : []).forEach(addCategoryRow);
		panel.appendChild(smallButton(t('oauth_waf_managed_override_add_category'), 'btn btn--sm btn--ghost', () => addCategoryRow()));

		const ruleTitle = document.createElement('div');
		ruleTitle.className = 'oauth-form-check-title';
		ruleTitle.textContent = t('oauth_waf_managed_override_rules');
		panel.appendChild(ruleTitle);
		const ruleRows = [];
		const ruleTarget = document.createElement('div');
		ruleTarget.className = 'oauth-managed-targets';
		panel.appendChild(ruleTarget);
		const addRuleRow = (rule = {}) => {
			const row = document.createElement('div');
			row.className = 'oauth-form-grid oauth-form-grid--d1-row';
			const ruleInput = textInput(rule.id || '', 'text');
			ruleInput.placeholder = '100001';
			const rowActionSelect = wafManagedOverrideActionSelect(rule.action || '');
			const rowEnabledSelect = wafBooleanOverrideSelect(rule.enabled);
			const scoreInput = wafIntegerInput(rule.score_threshold);
			scoreInput.min = '0';
			scoreInput.max = '100';
			const rowSensitivitySelect = wafSensitivitySelect(rule.sensitivity_level || '');
			const removeWrap = document.createElement('div');
			removeWrap.className = 'oauth-form-actions';
			removeWrap.appendChild(smallButton(t('delete'), 'btn btn--sm btn--ghost', () => row.remove()));
			row.append(
				formField(t('oauth_waf_managed_rule_id'), ruleInput),
				formField(t('oauth_waf_action'), rowActionSelect),
				formField(t('oauth_waf_managed_override_enabled_override'), rowEnabledSelect),
				formField(t('oauth_waf_managed_override_score_threshold'), scoreInput),
				formField(t('oauth_waf_managed_override_sensitivity'), rowSensitivitySelect),
				removeWrap,
			);
			ruleRows.push({ node: row, ruleInput, actionSelect: rowActionSelect, enabledSelect: rowEnabledSelect, scoreInput, sensitivitySelect: rowSensitivitySelect });
			ruleTarget.appendChild(row);
		};
		(Array.isArray(overrides.rules) ? overrides.rules : []).forEach(addRuleRow);
		panel.appendChild(smallButton(t('oauth_waf_managed_override_add_rule'), 'btn btn--sm btn--ghost', () => addRuleRow()));

		return {
			node: panel,
			value: () => {
				const out = {};
				if (actionSelect.value) out.action = actionSelect.value;
				const enabled = wafBooleanOverrideValue(enabledSelect);
				if (enabled !== null) out.enabled = enabled;
				if (sensitivitySelect.value) out.sensitivity_level = sensitivitySelect.value;

				const categories = [];
				for (const row of categoryRows) {
					if (!row.node.isConnected) continue;
					const category = row.categoryInput.value.trim();
					const action = row.actionSelect.value;
					const enabledOverride = wafBooleanOverrideValue(row.enabledSelect);
					if (!category && !action && enabledOverride === null) continue;
					if (!category) {
						toast.err(t('oauth_waf_managed_override_category_required'));
						row.categoryInput.focus();
						return false;
					}
					const item = { category };
					if (action) item.action = action;
					if (enabledOverride !== null) item.enabled = enabledOverride;
					categories.push(item);
				}
				if (categories.length) out.categories = categories;

				const rules = [];
				for (const row of ruleRows) {
					if (!row.node.isConnected) continue;
					const id = row.ruleInput.value.trim();
					const action = row.actionSelect.value;
					const enabledOverride = wafBooleanOverrideValue(row.enabledSelect);
					const sensitivity = row.sensitivitySelect.value;
					const scoreText = row.scoreInput.value.trim();
					if (!id && !action && enabledOverride === null && !sensitivity && !scoreText) continue;
					if (!id) {
						toast.err(t('oauth_waf_managed_override_rule_required'));
						row.ruleInput.focus();
						return false;
					}
					const item = { id };
					if (action) item.action = action;
					if (enabledOverride !== null) item.enabled = enabledOverride;
					if (sensitivity) item.sensitivity_level = sensitivity;
					if (scoreText) {
						const score = Number(scoreText);
						if (!Number.isInteger(score) || score < 0 || score > 100) {
							toast.err(t('oauth_waf_managed_override_score_invalid'));
							row.scoreInput.focus();
							return false;
						}
						item.score_threshold = score;
					}
					rules.push(item);
				}
				if (rules.length) out.rules = rules;
				return Object.keys(out).length ? out : null;
			},
		};
	}

	function wafManagedOverridesFromRule(rule) {
		const params = rule?.action_parameters && typeof rule.action_parameters === 'object' ? rule.action_parameters : {};
		if (params.overrides && typeof params.overrides === 'object') return params.overrides;
		const raw = params.raw && typeof params.raw === 'object' ? params.raw : {};
		if (raw.overrides && typeof raw.overrides === 'object') return raw.overrides;
		return null;
	}

	function wafManagedOverrideActionSelect(value = '') {
		const select = document.createElement('select');
		select.className = 'form-select';
		for (const action of wafManagedOverrideActions) {
			const option = document.createElement('option');
			option.value = action;
			option.textContent = action ? wafActionLabel(action) : t('oauth_waf_managed_override_no_action_override');
			select.appendChild(option);
		}
		select.value = wafManagedOverrideActions.includes(value) ? value : '';
		return select;
	}

	function wafSensitivitySelect(value = '') {
		const select = document.createElement('select');
		select.className = 'form-select';
		for (const level of wafManagedSensitivityLevels) {
			const option = document.createElement('option');
			option.value = level;
			option.textContent = level ? t(`oauth_waf_sensitivity_${level}`) : t('oauth_waf_managed_override_no_sensitivity_override');
			select.appendChild(option);
		}
		select.value = wafManagedSensitivityLevels.includes(value) ? value : '';
		return select;
	}

	function wafBooleanOverrideSelect(value) {
		const select = document.createElement('select');
		select.className = 'form-select';
		[
			['', 'oauth_waf_managed_override_no_enabled_override'],
			['true', 'oauth_enabled_state'],
			['false', 'oauth_disabled'],
		].forEach(([optionValue, labelKey]) => {
			const option = document.createElement('option');
			option.value = optionValue;
			option.textContent = t(labelKey);
			select.appendChild(option);
		});
		if (value === true) select.value = 'true';
		else if (value === false) select.value = 'false';
		else select.value = '';
		return select;
	}

	function wafBooleanOverrideValue(select) {
		if (!select || select.value === '') return null;
		return select.value === 'true';
	}

	function wafManagedExceptionTargetsNode(initialParams = null, idSuffix = 'create') {
		const params = initialParams && typeof initialParams === 'object' ? initialParams : {};
		const suffix = String(idSuffix || 'create').replace(/[^a-zA-Z0-9_-]/g, '-');
		const panel = document.createElement('div');
		panel.className = 'oauth-form-section';

		const title = document.createElement('div');
		title.className = 'oauth-form-section-title';
		title.textContent = t('oauth_waf_managed_exception_targets');
		panel.appendChild(title);

		const currentRuleset = checkboxOption(`waf-managed-skip-current-${suffix}`, t('oauth_waf_managed_exception_skip_current'), params.ruleset === 'current');
		panel.appendChild(currentRuleset.label);

		const rulesetsArea = document.createElement('textarea');
		rulesetsArea.className = 'oauth-code-editor oauth-code-editor--compact';
		rulesetsArea.spellcheck = false;
		rulesetsArea.placeholder = 'efb7b8c949ac4650a09736fc376e9aee';
		rulesetsArea.value = Array.isArray(params.rulesets) ? params.rulesets.join('\n') : '';
		panel.appendChild(formField(t('oauth_waf_managed_rulesets'), rulesetsArea));

		const targetRows = document.createElement('div');
		targetRows.className = 'oauth-managed-targets';
		panel.appendChild(targetRows);

		const rows = [];
		const addTargetRow = (rulesetID = '', ruleIDs = []) => {
			const row = document.createElement('div');
			row.className = 'oauth-form-grid oauth-form-grid--d1-row';
			const rulesetInput = textInput(rulesetID, 'text');
			rulesetInput.placeholder = 'efb7b8c949ac4650a09736fc376e9aee';
			const ruleIDsArea = document.createElement('textarea');
			ruleIDsArea.className = 'oauth-code-editor oauth-code-editor--compact';
			ruleIDsArea.spellcheck = false;
			ruleIDsArea.placeholder = '100001\n100002';
			ruleIDsArea.value = Array.isArray(ruleIDs) ? ruleIDs.join('\n') : '';
			const removeWrap = document.createElement('div');
			removeWrap.className = 'oauth-form-actions';
			removeWrap.appendChild(smallButton(t('delete'), 'btn btn--sm btn--ghost', () => {
				row.remove();
			}));
			row.append(
				formField(t('oauth_waf_managed_rule_ruleset_id'), rulesetInput),
				formField(t('oauth_waf_managed_rule_ids'), ruleIDsArea),
				removeWrap,
			);
			rows.push({ node: row, rulesetInput, ruleIDsArea });
			targetRows.appendChild(row);
		};

		const existingRules = params.rules && typeof params.rules === 'object' ? Object.entries(params.rules) : [];
		if (existingRules.length) {
			for (const [rulesetID, ruleIDs] of existingRules) addTargetRow(rulesetID, Array.isArray(ruleIDs) ? ruleIDs : []);
		} else {
			addTargetRow();
		}

		const addButton = smallButton(t('oauth_waf_managed_exception_add_rule_target'), 'btn btn--sm btn--ghost', () => addTargetRow());
		panel.appendChild(addButton);

		return {
			node: panel,
			value: () => {
				const out = {};
				if (currentRuleset.input.checked) out.ruleset = 'current';
				const rulesets = splitWAFList(rulesetsArea.value);
				if (rulesets.length) out.rulesets = rulesets;

				const rules = {};
				for (const row of rows) {
					if (!row.node.isConnected) continue;
					const rulesetID = row.rulesetInput.value.trim();
					const ruleIDs = splitWAFList(row.ruleIDsArea.value);
					if (!rulesetID && !ruleIDs.length) continue;
					if (!rulesetID || !ruleIDs.length) {
						toast.err(t('oauth_waf_managed_exception_rule_target_required'));
						(rulesetID ? row.ruleIDsArea : row.rulesetInput).focus();
						return false;
					}
					rules[rulesetID] = ruleIDs;
				}
				if (Object.keys(rules).length) out.rules = rules;
				return Object.keys(out).length ? out : null;
			},
		};
	}

	function wafRateLimitNode(rule = null, idSuffix = 'create') {
        const initial = rule?.ratelimit && typeof rule.ratelimit === 'object' ? rule.ratelimit : null;
        const initialJSON = canonicalWAFJSON(initial);
        const suffix = String(idSuffix || 'create').replace(/[^a-zA-Z0-9_-]/g, '-');
        const panel = document.createElement('details');
        panel.className = 'oauth-form-section';
        panel.open = !!initial;

        const title = document.createElement('summary');
        title.className = 'oauth-form-section-title';
        title.textContent = t('oauth_waf_rate_limit');
        panel.appendChild(title);

        const help = document.createElement('p');
        help.className = 'help-text';
        help.textContent = t('oauth_waf_rate_limit_help');
        panel.appendChild(help);

        const enabled = checkboxOption(`waf-rate-limit-enabled-${suffix}`, t('oauth_waf_rate_limit_enable'), !!initial);
        panel.appendChild(enabled.label);

        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid oauth-form-grid--d1-row';

        const characteristicsInput = textInput(Array.isArray(initial?.characteristics) ? initial.characteristics.join(', ') : '', 'text');
        characteristicsInput.placeholder = 'ip.src, cf.colo.id';
        grid.appendChild(formField(t('oauth_waf_rate_limit_characteristics'), characteristicsInput));

        const periodInput = wafIntegerInput(initial?.period);
        periodInput.min = '1';
        grid.appendChild(formField(t('oauth_waf_rate_limit_period'), periodInput));

        const requestsInput = wafIntegerInput(initial?.requests_per_period);
        requestsInput.min = '0';
        grid.appendChild(formField(t('oauth_waf_rate_limit_requests'), requestsInput));

        const mitigationInput = wafIntegerInput(initial?.mitigation_timeout);
        mitigationInput.min = '0';
        grid.appendChild(formField(t('oauth_waf_rate_limit_mitigation'), mitigationInput));

        const scoreInput = wafIntegerInput(initial?.score_per_period);
        scoreInput.min = '0';
        grid.appendChild(formField(t('oauth_waf_rate_limit_score'), scoreInput));

        const scoreHeaderInput = textInput(initial?.score_response_header_name || '', 'text');
        scoreHeaderInput.placeholder = 'Cf-Rate-Limit-Score';
        grid.appendChild(formField(t('oauth_waf_rate_limit_score_header'), scoreHeaderInput));

        panel.appendChild(grid);

        const countingExpression = document.createElement('textarea');
        countingExpression.className = 'oauth-code-editor oauth-code-editor--compact';
        countingExpression.spellcheck = false;
        countingExpression.placeholder = 'http.request.uri.path contains "/api"';
        countingExpression.value = initial?.counting_expression || '';
        panel.appendChild(formField(t('oauth_waf_rate_limit_counting_expression'), countingExpression));

        const requestsToOrigin = checkboxOption(`waf-rate-limit-origin-${suffix}`, t('oauth_waf_rate_limit_requests_to_origin'), initial?.requests_to_origin === true);
        panel.appendChild(requestsToOrigin.label);

        const controls = [characteristicsInput, periodInput, requestsInput, mitigationInput, scoreInput, scoreHeaderInput, countingExpression, requestsToOrigin.input];
        const syncDisabled = () => {
            const on = enabled.input.checked;
            controls.forEach((control) => { control.disabled = !on; });
        };
        enabled.input.addEventListener('change', syncDisabled);
        syncDisabled();

        return {
            node: panel,
            value: () => {
                if (!enabled.input.checked) {
                    if (!initial) return {};
                    return { ratelimit: null };
                }
                const characteristics = splitWAFList(characteristicsInput.value);
                const period = wafOptionalInteger(periodInput, t('oauth_waf_rate_limit_period'), 1);
                const requests = wafOptionalInteger(requestsInput, t('oauth_waf_rate_limit_requests'), 0);
                const mitigation = wafOptionalInteger(mitigationInput, t('oauth_waf_rate_limit_mitigation'), 0);
                const score = wafOptionalInteger(scoreInput, t('oauth_waf_rate_limit_score'), 0);
                if (period === null || requests === null || mitigation === null || score === null) return null;
                if (!characteristics.length || !period || (!requests && !score)) {
                    toast.err(t('oauth_waf_rate_limit_required'));
                    (characteristics.length ? (period ? requestsInput : periodInput) : characteristicsInput).focus();
                    return null;
                }
                const payload = {
                    characteristics,
                    period,
                };
                if (requests) payload.requests_per_period = requests;
                if (score) payload.score_per_period = score;
                if (mitigation != null) payload.mitigation_timeout = mitigation;
                const scoreHeader = scoreHeaderInput.value.trim();
                if (scoreHeader) payload.score_response_header_name = scoreHeader;
                const expression = countingExpression.value.trim();
                if (expression) payload.counting_expression = expression;
                if (requestsToOrigin.input.checked || initial?.requests_to_origin === true) {
                    payload.requests_to_origin = requestsToOrigin.input.checked;
                }
                if (canonicalWAFJSON(payload) === initialJSON) return {};
                return { ratelimit: payload };
            },
        };
    }

    function wafIntegerInput(value) {
        const input = textInput(value == null || value === 0 ? '' : String(value), 'number');
        input.step = '1';
        input.inputMode = 'numeric';
        return input;
    }

    function wafOptionalInteger(input, label, min = 0) {
        const text = input.value.trim();
        if (!text) return undefined;
        const value = Number(text);
        if (!Number.isInteger(value) || value < min) {
            toast.err(t('oauth_waf_rate_limit_invalid_number', { field: label }));
            input.focus();
            return null;
        }
        return value;
    }

    function splitWAFList(value) {
        return String(value || '')
            .split(/[,\n]+/)
            .map((part) => part.trim())
            .filter(Boolean);
    }

    function canonicalWAFJSON(value) {
        if (value == null || value === '') return 'null';
        try {
            return JSON.stringify(stableWAFJSONValue(value));
        } catch {
            return 'null';
        }
    }

    function stableWAFJSONValue(value) {
        if (Array.isArray(value)) return value.map(stableWAFJSONValue);
        if (!value || typeof value !== 'object') return value;
        return Object.keys(value).sort().reduce((out, key) => {
            const next = stableWAFJSONValue(value[key]);
            if (next !== undefined) out[key] = next;
            return out;
        }, {});
    }

    function wafAdvancedJSONNode(rule) {
        const panel = document.createElement('details');
        panel.className = 'oauth-form-section';
        const title = document.createElement('summary');
        title.className = 'oauth-form-section-title';
        title.textContent = t('oauth_waf_advanced_json');
        panel.appendChild(title);

        const help = document.createElement('p');
        help.className = 'help-text';
        help.textContent = t('oauth_waf_advanced_json_help');
        panel.appendChild(help);

        const fields = [
            {
                key: 'action_parameters_json',
                label: t('oauth_waf_action_parameters'),
                value: wafActionParametersJSONValue(rule.action_parameters),
            },
            { key: 'ratelimit', label: t('oauth_waf_rate_limit'), value: wafJSONEditorValue(rule.ratelimit) },
            { key: 'logging', label: t('oauth_waf_logging'), value: wafJSONEditorValue(rule.logging) },
            { key: 'exposed_credential_check', label: t('oauth_waf_credential_check'), value: wafJSONEditorValue(rule.exposed_credential_check) },
        ];
        const textareas = fields.map((field) => {
            const area = document.createElement('textarea');
            area.className = 'oauth-code-editor oauth-code-editor--compact';
            area.spellcheck = false;
            area.value = field.value;
            area.dataset.initialValue = field.value.trim();
            panel.appendChild(formField(field.label, area));
            return { ...field, area };
        });

        return {
            node: panel,
            value: () => {
                const payload = {};
                for (const field of textareas) {
                    const text = field.area.value.trim();
                    if (text === field.area.dataset.initialValue) continue;
                    let parsed;
                    try {
                        parsed = parseWAFAdvancedJSON(text);
                    } catch {
                        toast.err(t('oauth_waf_advanced_json_invalid', { field: field.label }));
                        field.area.focus();
                        return null;
                    }
                    payload[field.key] = parsed;
                }
                return payload;
            },
        };
    }

    function wafActionParametersJSONValue(params) {
        if (!params || typeof params !== 'object') return 'null';
        if (params.raw && typeof params.raw === 'object' && Object.keys(params.raw).length) {
            return wafJSONEditorValue(params.raw);
        }
        const compact = {};
        for (const key of ['id', 'ruleset', 'rulesets', 'rules', 'products', 'phases', 'overrides', 'version']) {
            const value = params[key];
            if (value == null) continue;
            if (Array.isArray(value) && !value.length) continue;
            if (typeof value === 'object' && !Array.isArray(value) && !Object.keys(value).length) continue;
            compact[key] = value;
        }
        return Object.keys(compact).length ? wafJSONEditorValue(compact) : 'null';
    }

    function wafJSONEditorValue(value) {
        if (value == null || value === '') return 'null';
        try {
            return JSON.stringify(value, null, 2);
        } catch {
            return 'null';
        }
    }

    function parseWAFAdvancedJSON(text) {
        if (!text) throw new Error('empty json');
        const parsed = JSON.parse(text);
        if (parsed !== null && (typeof parsed !== 'object' || Array.isArray(parsed))) {
            throw new Error('json must be object or null');
        }
        return parsed;
    }

    function wafSkipOptionsNode(initialParams = null, idSuffix = 'create') {
        const params = initialParams && typeof initialParams === 'object' ? initialParams : {};
        const suffix = String(idSuffix || 'create').replace(/[^a-zA-Z0-9_-]/g, '-');
        const panel = document.createElement('div');
        panel.className = 'oauth-form-section';

        const title = document.createElement('div');
        title.className = 'oauth-form-section-title';
        title.textContent = t('oauth_waf_skip_options');
        panel.appendChild(title);

        const currentRuleset = checkboxOption(`waf-skip-ruleset-current-${suffix}`, t('oauth_waf_skip_current_ruleset'), initialParams == null ? true : params.ruleset === 'current');
        panel.appendChild(currentRuleset.label);

        const productGroup = checkboxGroup(t('oauth_waf_skip_products'), wafSkipProducts, `waf-skip-product-${suffix}`, params.products);
        const phaseGroup = checkboxGroup(t('oauth_waf_skip_phases'), wafSkipPhases, `waf-skip-phase-${suffix}`, params.phases);
        panel.append(productGroup.node, phaseGroup.node);

        return {
            node: panel,
            value: () => {
                const params = {};
                if (currentRuleset.input.checked) params.ruleset = 'current';
                const products = productGroup.values();
                const phases = phaseGroup.values();
                if (products.length) params.products = products;
                if (phases.length) params.phases = phases;
                return Object.keys(params).length ? params : null;
            },
        };
    }

    function checkboxGroup(titleText, options, idPrefix, selected = []) {
        const group = document.createElement('div');
        group.className = 'oauth-form-check-group';
        const title = document.createElement('div');
        title.className = 'oauth-form-check-title';
        title.textContent = titleText;
        const list = document.createElement('div');
        list.className = 'oauth-form-options';
        const inputs = [];
        const selectedSet = new Set(Array.isArray(selected) ? selected : []);
        options.forEach(([value, labelKey], index) => {
            const option = checkboxOption(`${idPrefix}-${index}`, t(labelKey), selectedSet.has(value), value);
            inputs.push(option.input);
            list.appendChild(option.label);
        });
        group.append(title, list);
        return {
            node: group,
            values: () => inputs.filter((input) => input.checked).map((input) => input.value),
        };
    }

    function checkboxOption(id, labelText, checked = false, value = '1') {
        const label = document.createElement('label');
        label.className = 'oauth-check-option';
        const input = document.createElement('input');
        input.type = 'checkbox';
        input.id = id;
        input.value = value;
        input.checked = checked;
        const span = document.createElement('span');
        span.textContent = labelText;
        label.append(input, span);
        return { label, input };
    }

    function renderAnalytics(body) {
        if (!state.oauth.selectedZoneId) {
            body.appendChild(empty(t('oauth_select_zone')));
            return;
        }
        body.appendChild(analyticsActionBar());
        if (!canRead('analytics')) {
            body.appendChild(empty(t('oauth_analytics_scope_required')));
            return;
        }
        if (state.oauth.zoneAnalyticsLoading) {
            body.appendChild(empty(t('oauth_analytics_loading')));
            return;
        }
        if (state.oauth.zoneAnalyticsError) {
            body.appendChild(empty(state.oauth.zoneAnalyticsError));
            return;
        }
        const analytics = state.oauth.zoneAnalytics;
        if (!analytics) {
            body.appendChild(empty(t('oauth_no_analytics')));
            return;
        }

        const totals = analytics.totals || {};
        const summary = document.createElement('section');
        summary.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_analytics_summary');
        const grid = document.createElement('div');
        grid.className = 'oauth-metric-grid';
        grid.append(
            metricNode(t('oauth_analytics_requests'), formatNumber(totals.requests)),
            metricNode(t('oauth_analytics_bandwidth'), formatBytes(totals.bytes || 0)),
            metricNode(t('oauth_analytics_threats'), formatNumber(totals.threats)),
            metricNode(t('oauth_analytics_pageviews'), formatNumber(totals.pageviews)),
            metricNode(t('oauth_analytics_uniques'), formatNumber(totals.uniques)),
            metricNode(t('oauth_analytics_cache_hit'), formatPercent(cacheHitRate(totals))),
        );
        summary.append(heading, grid);
        body.appendChild(summary);

        const points = Array.isArray(analytics.timeseries) ? analytics.timeseries : [];
        if (!points.length) {
            body.appendChild(empty(t('oauth_no_analytics_timeseries')));
            return;
        }
        const maxRequests = points.reduce((max, point) => Math.max(max, Number(point.requests || 0)), 0);
        const rows = document.createElement('section');
        rows.className = 'oauth-section';
        const rowsHeading = document.createElement('h4');
        rowsHeading.className = 'oauth-section-title';
        rowsHeading.textContent = t('oauth_analytics_timeseries');
        rows.appendChild(rowsHeading);
        const list = document.createElement('div');
        list.className = 'oauth-analytics-series';
        for (const point of points) {
            list.appendChild(analyticsTimeSeriesRow(point, maxRequests));
        }
        rows.appendChild(list);
        body.appendChild(rows);
    }

    function analyticsTimeSeriesRow(point, maxRequests) {
        const requests = Number(point?.requests || 0);
        const percent = maxRequests > 0 ? Math.round((requests / maxRequests) * 1000) / 10 : 0;
        const row = document.createElement('div');
        row.className = 'oauth-analytics-row';

        const copy = document.createElement('div');
        copy.className = 'oauth-analytics-row-copy';
        const title = document.createElement('div');
        title.className = 'oauth-row-title';
        title.textContent = formatDateRange(point?.since, point?.until);
        const meta = document.createElement('div');
        meta.className = 'oauth-row-meta';
        meta.textContent = [
            `${t('oauth_analytics_requests')} ${formatNumber(requests)}`,
            `${t('oauth_analytics_bandwidth')} ${formatBytes(point?.bytes || 0)}`,
            `${t('oauth_analytics_threats')} ${formatNumber(point?.threats)}`,
        ].join(' · ');
        copy.append(title, meta);

        const trend = document.createElement('div');
        trend.className = 'oauth-analytics-trend';
        trend.setAttribute('aria-label', `${t('oauth_analytics_requests')} ${formatNumber(requests)}`);
        const bar = document.createElement('div');
        bar.className = 'oauth-analytics-bar';
        const fill = document.createElement('div');
        fill.className = 'oauth-analytics-bar-fill';
        fill.style.width = `${percent}%`;
        bar.appendChild(fill);
        const trendMeta = document.createElement('div');
        trendMeta.className = 'oauth-analytics-trend-meta mono';
        trendMeta.textContent = formatNumber(requests);
        trend.append(bar, trendMeta);

        row.append(copy, trend);
        return row;
    }

    function renderCloudflareStatus(body) {
        body.appendChild(cloudflareStatusActionBar());
        if (state.oauth.cloudflareStatusLoading) {
            body.appendChild(empty(t('oauth_cf_status_loading')));
            return;
        }
        if (state.oauth.cloudflareStatusError) {
            body.appendChild(empty(state.oauth.cloudflareStatusError));
            return;
        }
        const status = state.oauth.cloudflareStatus;
        if (!status) {
            body.appendChild(empty(t('oauth_cf_status_empty')));
            return;
        }

        const overall = status.overall || {};
        const affected = Array.isArray(status.affected_products) ? status.affected_products : [];
        const active = Array.isArray(status.active_incidents) ? status.active_incidents : [];
        const maintenances = Array.isArray(status.maintenances) ? status.maintenances : [];
        const regions = Array.isArray(status.regions) ? status.regions : [];
        const recent = Array.isArray(status.recent_incidents) ? status.recent_incidents : [];

        const summary = document.createElement('section');
        summary.className = 'oauth-status-summary';
        summary.dataset.state = statusIndicatorState(overall.indicator);
        const title = document.createElement('div');
        title.className = 'oauth-status-summary-title';
        title.textContent = statusIndicatorLabel(overall.indicator, overall.description);
        const meta = document.createElement('div');
        meta.className = 'oauth-status-summary-meta';
        meta.textContent = [
            status.page?.updated_at ? `${t('oauth_cf_status_updated')} ${formatDate(status.page.updated_at)}` : '',
            status.fetched_at ? `${t('oauth_cf_status_fetched')} ${formatDate(status.fetched_at)}` : '',
        ].filter(Boolean).join(' · ');
        const grid = document.createElement('div');
        grid.className = 'oauth-metric-grid';
        grid.append(
            metricNode(t('oauth_cf_status_products'), formatNumber(status.product_total)),
            metricNode(t('oauth_cf_status_affected'), formatNumber(affected.length)),
            metricNode(t('oauth_cf_status_active_incidents'), formatNumber(active.length)),
            metricNode(t('oauth_cf_status_maintenances'), formatNumber(maintenances.length)),
        );
        summary.append(title, meta, grid);
        body.appendChild(summary);

        renderSection(
            body,
            t('oauth_cf_status_affected_products'),
            affected,
            (component) => rowNode(component.name, componentStatusLabel(component.status)),
            t('oauth_cf_status_no_affected_products'),
        );

        renderSection(
            body,
            t('oauth_cf_status_regions'),
            regions,
            (region) => rowNode(regionNameLabel(region.name), region.impacted > 0
                ? t('oauth_cf_status_region_impacted', { n: region.impacted, m: region.total })
                : t('oauth_cf_status_region_ok', { n: region.total })),
            t('oauth_cf_status_no_regions'),
        );

        renderStatusIncidentSection(body, t('oauth_cf_status_active'), active, t('oauth_cf_status_no_active'));
        renderStatusIncidentSection(body, t('oauth_cf_status_maintenance'), maintenances, t('oauth_cf_status_no_maintenance'));
        renderStatusIncidentSection(body, t('oauth_cf_status_recent'), recent, t('oauth_cf_status_no_recent'));
    }

    function cloudflareStatusActionBar() {
        const bar = document.createElement('div');
        bar.className = 'oauth-action-bar';
        const copy = document.createElement('div');
        const label = document.createElement('div');
        label.className = 'oauth-action-title';
        label.textContent = t('oauth_cloudflare_status');
        const meta = document.createElement('div');
        meta.className = 'oauth-action-meta';
        meta.textContent = t('oauth_cf_status_public');
        copy.append(label, meta);
        const actions = document.createElement('div');
        actions.className = 'oauth-row-actions';
        const diagnostics = smallButton(t('oauth_cf_status_copy_diagnostics'), 'btn btn--sm btn--ghost', () => copyOAuthText(cloudflareStatusDiagnosticsText()));
        diagnostics.title = t('oauth_cf_status_copy_diagnostics_title');
        diagnostics.setAttribute('aria-label', t('oauth_cf_status_copy_diagnostics_title'));
        actions.append(
            diagnostics,
            smallButton(t('refresh'), 'btn btn--sm btn--ghost', async () => {
                await loadCloudflareStatus();
                renderOAuthResource();
            }),
            smallButton(t('open'), 'btn btn--sm btn--ghost', () => {
                window.open('https://www.cloudflarestatus.com', '_blank', 'noopener');
            }),
        );
        bar.append(copy, actions);
        return bar;
    }

    function cloudflareStatusDiagnosticsText() {
        const status = state.oauth.cloudflareStatus || null;
        const overall = status?.overall || {};
        const affected = Array.isArray(status?.affected_products) ? status.affected_products : [];
        const active = Array.isArray(status?.active_incidents) ? status.active_incidents : [];
        const maintenances = Array.isArray(status?.maintenances) ? status.maintenances : [];
        const regions = Array.isArray(status?.regions) ? status.regions : [];
        const recent = Array.isArray(status?.recent_incidents) ? status.recent_incidents : [];
        return JSON.stringify({
            type: 'cfui_oauth_cloudflare_status_diagnostics',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            browser_path: window.location.pathname,
            requires_oauth_scope: false,
            contains_oauth_token: false,
            contains_refresh_token: false,
            contains_incident_update_body: false,
            sensitive_fields_omitted: [
                'oauth_access_token',
                'oauth_refresh_token',
                'incident_updates.body',
            ],
            state: {
                loading: !!state.oauth.cloudflareStatusLoading,
                error: state.oauth.cloudflareStatusError || '',
                loaded: !!status,
            },
            page: status ? {
                id: status.page?.id || '',
                name: status.page?.name || '',
                url: status.page?.url || '',
                time_zone: status.page?.time_zone || '',
                updated_at: status.page?.updated_at || '',
                fetched_at: status.fetched_at || '',
            } : null,
            overall: status ? {
                indicator: overall.indicator || '',
                label: statusIndicatorLabel(overall.indicator, overall.description),
            } : null,
            counts: {
                product_total: Number(status?.product_total || 0),
                affected_products: affected.length,
                active_incidents: active.length,
                maintenances: maintenances.length,
                regions: regions.length,
                impacted_regions: regions.filter((region) => Number(region?.impacted || 0) > 0).length,
                recent_incidents: recent.length,
            },
            affected_products: affected.map(statusComponentDiagnostics),
            regions: regions.map(statusRegionDiagnostics),
            active_incidents: active.map(statusIncidentDiagnostics),
            maintenances: maintenances.map(statusIncidentDiagnostics),
            recent_incidents: recent.map(statusIncidentDiagnostics),
        }, null, 2);
    }

    function statusComponentDiagnostics(component) {
        return {
            id: component?.id || '',
            name: component?.name || '',
            status: component?.status || '',
            status_label: componentStatusLabel(component?.status || ''),
            group_id: component?.group_id || '',
        };
    }

    function statusRegionDiagnostics(region) {
        return {
            id: region?.id || '',
            name: region?.name || '',
            label: regionNameLabel(region?.name || ''),
            total: Number(region?.total || 0),
            impacted: Number(region?.impacted || 0),
        };
    }

    function statusIncidentDiagnostics(incident) {
        return {
            id: incident?.id || '',
            name: incident?.name || '',
            status: incident?.status || '',
            status_label: incidentStatusLabel(incident?.status || ''),
            impact: incident?.impact || '',
            impact_label: impactLabel(incident?.impact || ''),
            created_at: incident?.created_at || '',
            updated_at: incident?.updated_at || '',
            scheduled_for: incident?.scheduled_for || '',
            shortlink_present: !!incident?.shortlink,
            update_count: Array.isArray(incident?.incident_updates) ? incident.incident_updates.length : 0,
        };
    }

    function renderStatusIncidentSection(body, title, incidents, emptyText) {
        return renderSection(
            body,
            title,
            incidents,
            (incident) => rowNode(incident.name, [
                incidentStatusLabel(incident.status),
                impactLabel(incident.impact),
                formatDate(incident.scheduled_for || incident.updated_at || incident.created_at),
            ].filter(Boolean).join(' · '), incident.shortlink ? [
                smallButton(t('open'), 'btn btn--sm btn--ghost', () => window.open(incident.shortlink, '_blank', 'noopener')),
            ] : null),
            emptyText,
        );
    }

    function renderUsage(body) {
        if (!state.oauth.selectedAccountId) {
            body.appendChild(empty(t('oauth_select_account')));
            return;
        }
        body.appendChild(usageActionBar());
        if (!canRead('analytics')) {
            body.appendChild(empty(t('oauth_usage_scope_required')));
            return;
        }
        if (state.oauth.accountUsageLoading) {
            body.appendChild(empty(t('oauth_usage_loading')));
            return;
        }
        if (state.oauth.accountUsageError) {
            body.appendChild(empty(state.oauth.accountUsageError));
            return;
        }
        const usage = state.oauth.accountUsage;
        if (!usage) {
            body.appendChild(empty(t('oauth_usage_empty')));
            return;
        }

        const period = document.createElement('section');
        period.className = 'oauth-section';
        period.appendChild(rowNode(t('oauth_usage_period'), formatDateRange(usage.period_start, usage.now)));
        body.appendChild(period);
        body.appendChild(usageBillingSection(usage.billing || {}));

        const workers = usage.workers || {};
        const workerItems = [
            [t('oauth_usage_requests_today'), formatNumber(workers.requests_today)],
            [t('oauth_usage_requests_period'), formatNumber(workers.requests_period)],
            [t('oauth_usage_errors_period'), formatNumber(workers.errors_period)],
            [t('oauth_usage_subrequests'), formatNumber(workers.subrequests)],
        ];
        if (workers.errors_last_hour != null) workerItems.splice(3, 0, [t('oauth_usage_errors_last_hour'), formatNumber(workers.errors_last_hour)]);
        if (workers.cpu_time_period_us != null) workerItems.push([t('oauth_usage_cpu_period'), formatCPUTime(workers.cpu_time_period_us)]);
        if (workers.cpu_time_today_us != null) workerItems.push([t('oauth_usage_cpu_today'), formatCPUTime(workers.cpu_time_today_us)]);
        if (workers.cpu_time_p50_us != null || workers.cpu_time_p99_us != null) {
            workerItems.push([t('oauth_usage_cpu_single'), [
                workers.cpu_time_p50_us == null ? '' : `P50 ${formatCPUTime(workers.cpu_time_p50_us)}`,
                workers.cpu_time_p99_us == null ? '' : `P99 ${formatCPUTime(workers.cpu_time_p99_us)}`,
            ].filter(Boolean).join(' · ')]);
        }
        body.appendChild(usageMetricSection(t('oauth_usage_workers'), workerItems));

        const r2 = usage.r2 || {};
        body.appendChild(usageMetricSection(t('oauth_usage_r2'), [
            [t('oauth_usage_r2_class_a'), formatNumber(r2.class_a_operations)],
            [t('oauth_usage_r2_class_b'), formatNumber(r2.class_b_operations)],
            [t('oauth_usage_r2_storage'), formatBytes(r2.storage_bytes)],
            [t('oauth_usage_r2_objects'), formatNumber(r2.object_count)],
        ]));

        if (usage.d1) {
            body.appendChild(usageMetricSection(t('oauth_usage_d1'), [
                [t('oauth_usage_d1_rows_read_today'), formatNumber(usage.d1.rows_read_today)],
                [t('oauth_usage_d1_rows_written_today'), formatNumber(usage.d1.rows_written_today)],
                [t('oauth_usage_d1_rows_read_period'), formatNumber(usage.d1.rows_read_period)],
                [t('oauth_usage_d1_rows_written_period'), formatNumber(usage.d1.rows_written_period)],
                [t('oauth_usage_d1_read_queries_period'), formatNumber(usage.d1.read_queries_period)],
                [t('oauth_usage_d1_write_queries_period'), formatNumber(usage.d1.write_queries_period)],
            ]));
        }

        if (usage.kv) {
            body.appendChild(usageMetricSection(t('oauth_usage_kv'), [
                [t('oauth_usage_kv_reads_today'), formatNumber(usage.kv.reads_today)],
                [t('oauth_usage_kv_writes_today'), formatNumber(usage.kv.writes_today)],
                [t('oauth_usage_kv_reads_period'), formatNumber(usage.kv.reads_period)],
                [t('oauth_usage_kv_writes_period'), formatNumber(usage.kv.writes_period)],
                [t('oauth_usage_kv_storage'), formatBytes(usage.kv.storage_bytes)],
                [t('oauth_usage_kv_keys'), formatNumber(usage.kv.key_count)],
            ]));
        }
    }

    function usageActionBar() {
        const bar = document.createElement('div');
        bar.className = 'oauth-action-bar';
        const copy = document.createElement('div');
        const label = document.createElement('div');
        label.className = 'oauth-action-title';
        label.textContent = t('oauth_usage');
        const meta = document.createElement('div');
        meta.className = 'oauth-action-meta mono';
        meta.textContent = selectedAccountName();
        copy.append(label, meta);
        const actions = document.createElement('div');
        actions.className = 'oauth-row-actions';
        const diagnostics = smallButton(t('oauth_usage_copy_diagnostics'), 'btn btn--sm btn--ghost', () => copyOAuthText(usageDiagnosticsText()));
        diagnostics.title = t('oauth_usage_copy_diagnostics_title');
        diagnostics.setAttribute('aria-label', t('oauth_usage_copy_diagnostics_title'));
        const refresh = smallButton(t('refresh'), 'btn btn--sm btn--ghost', async () => {
            await loadOAuthAccountUsage();
            renderOAuthResource();
        });
        actions.append(diagnostics, refresh);
        bar.append(copy, actions);
        return bar;
    }

    function usageDiagnosticsText() {
        const status = state.oauth.status || {};
        const usage = state.oauth.accountUsage || null;
        const session = usage?.session || status.current || {};
        return JSON.stringify({
            type: 'cfui_oauth_account_usage_diagnostics',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            browser_path: window.location.pathname,
            contains_oauth_token: false,
            contains_refresh_token: false,
            sensitive_fields_omitted: [
                'oauth_access_token',
                'oauth_refresh_token',
                'subscription_id',
            ],
            selected: {
                account_id: state.oauth.selectedAccountId || '',
                account_name: selectedAccountName(),
            },
            identity: {
                label: session.label || '',
                expires_at: session.expires_at || '',
                scopes: Array.isArray(session.scopes) ? session.scopes : [],
            },
            capability: {
                analytics_read: canRead('analytics'),
                r2_read: canRead('r2'),
                d1_read: canRead('d1'),
                kv_read: canRead('kv'),
            },
            state: {
                loaded: !!usage,
                loading: !!state.oauth.accountUsageLoading,
                error: state.oauth.accountUsageError || '',
                scope_ready: canRead('analytics'),
            },
            period: usage ? {
                period_start: usage.period_start || '',
                today_start: usage.today_start || '',
                now: usage.now || '',
            } : null,
            billing: usage ? usageBillingDiagnostics(usage.billing || {}) : null,
            workers: usage ? usageWorkersDiagnostics(usage.workers || {}) : null,
            r2: usage ? usageR2Diagnostics(usage.r2 || {}) : null,
            d1: usage?.d1 ? usageD1Diagnostics(usage.d1) : null,
            kv: usage?.kv ? usageKVDiagnostics(usage.kv) : null,
            capabilities: oauthCapabilityDiagnostics(usage?.capabilities || status.capabilities || {}),
        }, null, 2);
    }

    function usageBillingDiagnostics(billing) {
        const subscriptions = Array.isArray(billing?.subscriptions) ? billing.subscriptions : [];
        return {
            available: !!billing?.available,
            reason: billing?.reason || '',
            reason_label: billing?.available ? '' : billingReasonLabel(billing?.reason),
            workers_paid: !!billing?.workers_paid,
            r2_paid: !!billing?.r2_paid,
            period_start: billing?.period_start || '',
            period_end: billing?.period_end || '',
            subscription_count: subscriptions.length,
            subscriptions: subscriptions.map((subscription) => ({
                state: subscription?.state || '',
                frequency: subscription?.frequency || '',
                rate_plan_id: subscription?.rate_plan_id || '',
                rate_plan_name: subscription?.rate_plan_name || '',
                active: !!subscription?.active,
                current_period_start: subscription?.current_period_start || '',
                current_period_end: subscription?.current_period_end || '',
            })),
        };
    }

    function usageWorkersDiagnostics(workers) {
        return {
            requests_today: Number(workers?.requests_today || 0),
            requests_period: Number(workers?.requests_period || 0),
            errors_period: Number(workers?.errors_period || 0),
            errors_last_hour: workers?.errors_last_hour == null ? null : Number(workers.errors_last_hour),
            subrequests: Number(workers?.subrequests || 0),
            cpu_time_period_us: workers?.cpu_time_period_us == null ? null : Number(workers.cpu_time_period_us),
            cpu_time_today_us: workers?.cpu_time_today_us == null ? null : Number(workers.cpu_time_today_us),
            cpu_time_p50_us: workers?.cpu_time_p50_us == null ? null : Number(workers.cpu_time_p50_us),
            cpu_time_p99_us: workers?.cpu_time_p99_us == null ? null : Number(workers.cpu_time_p99_us),
        };
    }

    function usageR2Diagnostics(r2) {
        return {
            class_a_operations: Number(r2?.class_a_operations || 0),
            class_b_operations: Number(r2?.class_b_operations || 0),
            storage_bytes: Number(r2?.storage_bytes || 0),
            object_count: Number(r2?.object_count || 0),
        };
    }

    function usageD1Diagnostics(d1) {
        return {
            rows_read_today: Number(d1?.rows_read_today || 0),
            rows_written_today: Number(d1?.rows_written_today || 0),
            rows_read_period: Number(d1?.rows_read_period || 0),
            rows_written_period: Number(d1?.rows_written_period || 0),
            read_queries_period: Number(d1?.read_queries_period || 0),
            write_queries_period: Number(d1?.write_queries_period || 0),
        };
    }

    function usageKVDiagnostics(kv) {
        return {
            reads_today: Number(kv?.reads_today || 0),
            writes_today: Number(kv?.writes_today || 0),
            reads_period: Number(kv?.reads_period || 0),
            writes_period: Number(kv?.writes_period || 0),
            storage_bytes: Number(kv?.storage_bytes || 0),
            key_count: Number(kv?.key_count || 0),
        };
    }

    function oauthCapabilityDiagnostics(capabilities) {
        const rows = {};
        Object.keys(capabilities || {}).sort().forEach((key) => {
            const capability = capabilities[key] || {};
            rows[key] = { read: !!capability.read, write: !!capability.write };
        });
        return rows;
    }

    function usageMetricSection(title, items) {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = title;
        const grid = document.createElement('div');
        grid.className = 'oauth-metric-grid';
        for (const [label, value] of items) grid.appendChild(metricNode(label, value));
        section.append(heading, grid);
        return section;
    }

    function usageBillingSection(billing) {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_usage_billing');
        section.appendChild(heading);

        if (!billing.available) {
            section.appendChild(rowNode(t('oauth_usage_billing_status'), billingReasonLabel(billing.reason)));
            return section;
        }

        const grid = document.createElement('div');
        grid.className = 'oauth-metric-grid';
        grid.appendChild(metricNode(t('oauth_usage_workers_paid'), billing.workers_paid ? t('yes') : t('no')));
        grid.appendChild(metricNode(t('oauth_usage_r2_paid'), billing.r2_paid ? t('yes') : t('no')));
        grid.appendChild(metricNode(t('oauth_usage_subscription_count'), formatNumber((billing.subscriptions || []).length)));
        if (billing.period_start || billing.period_end) {
            grid.appendChild(metricNode(t('oauth_usage_billing_period'), formatDateRange(billing.period_start, billing.period_end)));
        }
        section.appendChild(grid);

        const subscriptions = billing.subscriptions || [];
        if (!subscriptions.length) {
            section.appendChild(empty(t('oauth_usage_no_subscriptions')));
            return section;
        }
        for (const subscription of subscriptions) {
            const title = subscription.rate_plan_name || subscription.rate_plan_id || subscription.id || t('oauth_usage_subscription');
            const meta = [
                subscription.state || '',
                subscription.frequency || '',
                subscription.active ? t('oauth_usage_subscription_active') : t('oauth_usage_subscription_inactive'),
                formatDateRange(subscription.current_period_start, subscription.current_period_end),
            ].filter(Boolean).join(' · ');
            section.appendChild(rowNode(title, meta));
        }
        return section;
    }

    function billingReasonLabel(reason) {
        const key = `oauth_usage_billing_reason_${String(reason || 'unavailable').replaceAll('-', '_')}`;
        const translated = t(key);
        return translated === key ? t('oauth_usage_billing_reason_unavailable') : translated;
    }

    function analyticsActionBar() {
        const bar = document.createElement('div');
        bar.className = 'oauth-action-bar';
        const copy = document.createElement('div');
        const label = document.createElement('div');
        label.className = 'oauth-action-title';
        label.textContent = t('oauth_analytics');
        const meta = document.createElement('div');
        meta.className = 'oauth-action-meta mono';
        meta.textContent = selectedZoneName();
        copy.append(label, meta);

        const select = document.createElement('select');
        select.className = 'form-select';
        select.setAttribute('aria-label', t('oauth_analytics_range'));
        for (const value of analyticsRanges) {
            const option = document.createElement('option');
            option.value = value;
            option.textContent = analyticsRangeLabel(value);
            option.selected = value === state.oauth.analyticsRange;
            select.appendChild(option);
        }
        select.addEventListener('change', async () => {
            state.oauth.analyticsRange = select.value;
            await loadOAuthAnalytics();
            renderOAuthResource();
        });

        const actions = document.createElement('div');
        actions.className = 'oauth-config-actions';
        const copySummary = smallButton(t('oauth_analytics_copy_summary'), 'btn btn--sm btn--ghost', () => copyOAuthText(analyticsSummaryText()));
        copySummary.disabled = !state.oauth.zoneAnalytics;
        const diagnostics = smallButton(t('oauth_analytics_copy_diagnostics'), 'btn btn--sm btn--ghost', () => copyOAuthText(analyticsDiagnosticsText()));
        diagnostics.title = t('oauth_analytics_copy_diagnostics_title');
        diagnostics.setAttribute('aria-label', t('oauth_analytics_copy_diagnostics_title'));
        actions.append(select, copySummary, diagnostics);

        bar.append(copy, actions);
        return bar;
    }

    function analyticsDiagnosticsText() {
        const status = state.oauth.status || {};
        const analytics = state.oauth.zoneAnalytics || null;
        const session = analytics?.session || status.current || {};
        const totals = analytics?.totals || {};
        const points = Array.isArray(analytics?.timeseries) ? analytics.timeseries : [];
        return JSON.stringify({
            type: 'cfui_oauth_zone_analytics_diagnostics',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            browser_path: window.location.pathname,
            contains_oauth_token: false,
            contains_refresh_token: false,
            sensitive_fields_omitted: [
                'oauth_access_token',
                'oauth_refresh_token',
            ],
            selected: {
                account_id: state.oauth.selectedAccountId || '',
                account_name: selectedAccountName(),
                zone_id: state.oauth.selectedZoneId || '',
                zone_name: selectedZoneName(),
            },
            identity: {
                label: session.label || '',
                expires_at: session.expires_at || '',
                scopes: Array.isArray(session.scopes) ? session.scopes : [],
            },
            capability: {
                analytics_read: canRead('analytics'),
            },
            state: {
                loaded: !!analytics,
                loading: !!state.oauth.zoneAnalyticsLoading,
                error: state.oauth.zoneAnalyticsError || '',
                scope_ready: canRead('analytics'),
            },
            period: analytics ? {
                range: analytics.range || state.oauth.analyticsRange || '',
                range_label: analyticsRangeLabel(analytics.range || state.oauth.analyticsRange),
                since: analytics.since || '',
                until: analytics.until || '',
            } : {
                range: state.oauth.analyticsRange || '',
                range_label: analyticsRangeLabel(state.oauth.analyticsRange),
                since: '',
                until: '',
            },
            totals: analytics ? analyticsPointDiagnostics(totals) : null,
            timeseries: {
                count: points.length,
                points: points.map(analyticsPointDiagnostics),
            },
            capabilities: oauthCapabilityDiagnostics(analytics?.capabilities || status.capabilities || {}),
        }, null, 2);
    }

    function analyticsPointDiagnostics(point) {
        return {
            since: point?.since || '',
            until: point?.until || '',
            requests: Number(point?.requests || 0),
            cached_requests: Number(point?.cached_requests || 0),
            uncached_requests: Number(point?.uncached_requests || 0),
            bytes: Number(point?.bytes || 0),
            cached_bytes: Number(point?.cached_bytes || 0),
            threats: Number(point?.threats || 0),
            pageviews: Number(point?.pageviews || 0),
            uniques: Number(point?.uniques || 0),
            cache_hit_rate_percent: cacheHitRate(point),
        };
    }

    function analyticsSummaryText() {
        const analytics = state.oauth.zoneAnalytics;
        if (!analytics) return '';
        const totals = analytics.totals || {};
        return [
            t('oauth_analytics'),
            `${t('oauth_analytics_zone')}: ${selectedZoneName()}`,
            `${t('oauth_analytics_range')}: ${analyticsRangeLabel(analytics.range || state.oauth.analyticsRange)}`,
            formatDateRange(analytics.since, analytics.until) ? `${t('oauth_analytics_period')}: ${formatDateRange(analytics.since, analytics.until)}` : '',
            `${t('oauth_analytics_requests')}: ${formatNumber(totals.requests)}`,
            `${t('oauth_analytics_bandwidth')}: ${formatBytes(totals.bytes || 0)}`,
            `${t('oauth_analytics_threats')}: ${formatNumber(totals.threats)}`,
            `${t('oauth_analytics_pageviews')}: ${formatNumber(totals.pageviews)}`,
            `${t('oauth_analytics_uniques')}: ${formatNumber(totals.uniques)}`,
            `${t('oauth_analytics_cache_hit')}: ${formatPercent(cacheHitRate(totals))}`,
        ].filter(Boolean).join('\n');
    }

    function zoneSettingsDiagnosticsText() {
        const status = state.oauth.status || {};
        const session = state.oauth.zoneSettingsSession || status.current || {};
        return JSON.stringify({
            type: 'cfui_oauth_zone_settings_diagnostics',
            version: 1,
            generated_at: new Date().toISOString(),
            browser_origin: window.location.origin,
            browser_path: window.location.pathname,
            contains_oauth_token: false,
            contains_refresh_token: false,
            sensitive_fields_omitted: [
                'oauth_access_token',
                'oauth_refresh_token',
            ],
            selected: {
                account_id: state.oauth.selectedAccountId || '',
                account_name: selectedAccountName(),
                zone_id: state.oauth.selectedZoneId || '',
                zone_name: selectedZoneName(),
                resource: state.oauth.resource || '',
            },
            identity: {
                label: session.label || '',
                expires_at: session.expires_at || '',
                scopes: Array.isArray(session.scopes) ? session.scopes : [],
            },
            capability: {
                zone_settings_read: canRead('zone_settings'),
                zone_settings_write: canWrite('zone_settings'),
                cache_purge_write: canWrite('cache_purge'),
            },
            state: {
                settings_loaded: state.oauth.zoneSettings.length,
                settings_error: state.oauth.zoneSettingsError || '',
                mutation_error: state.oauth.zoneSettingsMutationError || '',
                cache_purge_error: state.oauth.zoneCachePurgeError || '',
                scope_ready: canRead('zone_settings'),
            },
            settings: state.oauth.zoneSettings.map(zoneSettingDiagnostics),
            capabilities: oauthCapabilityDiagnostics(state.oauth.zoneSettingsCapabilities || status.capabilities || {}),
        }, null, 2);
    }

    function zoneSettingDiagnostics(setting) {
        return {
            id: setting?.id || '',
            value: setting?.value == null ? null : String(setting.value),
            editable: !!setting?.editable,
            writable_in_cfui: !!(setting?.editable && isWritableZoneSetting(setting)),
            modified_on: setting?.modified_on || '',
            time_remaining: setting?.time_remaining == null ? null : Number(setting.time_remaining),
        };
    }

    function metricNode(label, value) {
        const node = document.createElement('div');
        node.className = 'oauth-metric';
        const valueEl = document.createElement('div');
        valueEl.className = 'oauth-metric-value';
        valueEl.textContent = value || '0';
        const labelEl = document.createElement('div');
        labelEl.className = 'oauth-metric-label';
        labelEl.textContent = label || '';
        node.append(valueEl, labelEl);
        return node;
    }

    function renderZoneSettings(body) {
        if (!state.oauth.selectedZoneId) {
            body.appendChild(empty(t('oauth_select_zone')));
            return;
        }
        body.appendChild(resourceActionBar(t('oauth_zone_settings'), {
            text: t('oauth_zone_settings_copy_diagnostics'),
            className: 'btn btn--sm btn--ghost',
            title: t('oauth_zone_settings_copy_diagnostics_title'),
            onClick: () => copyOAuthText(zoneSettingsDiagnosticsText()),
        }));
        if (canWrite('zone_settings') || canWrite('cache_purge')) {
            body.appendChild(zoneSettingActionsNode());
        }
        if (state.oauth.zoneSettingsMutationError) body.appendChild(empty(state.oauth.zoneSettingsMutationError));
        if (state.oauth.zoneCachePurgeError) body.appendChild(empty(state.oauth.zoneCachePurgeError));
        if (state.oauth.zoneSettingsError) {
            body.appendChild(empty(state.oauth.zoneSettingsError));
            return;
        }
        if (!state.oauth.zoneSettings.length) {
            body.appendChild(empty(t('oauth_no_zone_settings')));
            return;
        }
        for (const setting of state.oauth.zoneSettings) {
            const value = setting.value == null ? '' : String(setting.value);
            const editable = setting.editable ? t('oauth_editable') : t('oauth_readonly');
            const writable = canWrite('zone_settings') && isWritableZoneSetting(setting);
            body.appendChild(rowNode(setting.id, `${value} · ${editable}${writable ? ' · ' + t('oauth_write_enabled') : ''}`));
        }
    }

    function renderOAuthError(message) {
        const body = $('oauth-resource-body');
        if (!body) return;
        body.innerHTML = '';
        body.appendChild(empty(message));
    }

    function storageBackHeader(title, metaText, extraActions = []) {
        const bar = document.createElement('div');
        bar.className = 'oauth-action-bar';
        const copy = document.createElement('div');
        const label = document.createElement('div');
        label.className = 'oauth-action-title';
        label.textContent = title || '';
        const meta = document.createElement('div');
        meta.className = 'oauth-action-meta mono';
        meta.textContent = metaText || '';
        copy.append(label, meta);
        const back = smallButton(t('oauth_storage_back'), 'btn btn--sm btn--ghost', () => {
            resetStorageDetail();
            renderOAuthResource();
        });
        const actions = document.createElement('div');
        actions.className = 'oauth-row-actions';
        for (const action of Array.isArray(extraActions) ? extraActions : []) {
            actions.appendChild(actionButton(action));
        }
        actions.appendChild(back);
        bar.append(copy, actions);
        return bar;
    }

    function resourceActionBar(title, action) {
        const bar = document.createElement('div');
        bar.className = 'oauth-action-bar';
        const copy = document.createElement('div');
        const label = document.createElement('div');
        label.className = 'oauth-action-title';
        label.textContent = title || '';
        const meta = document.createElement('div');
        meta.className = 'oauth-action-meta mono';
        meta.textContent = state.oauth.selectedZoneId || state.oauth.selectedAccountId || '';
        copy.append(label, meta);
        bar.appendChild(copy);
        const actions = Array.isArray(action) ? action : (action ? [action] : []);
        if (actions.length) {
            const wrap = document.createElement('div');
            wrap.className = 'oauth-row-actions';
            for (const item of actions) wrap.appendChild(actionButton(item));
            bar.appendChild(wrap);
        }
        return bar;
    }

    function actionButton(action) {
        const button = smallButton(action?.text || '', action?.className || 'btn btn--sm btn--ghost', action?.onClick);
        if (action?.title) {
            button.title = action.title;
            button.setAttribute('aria-label', action.title);
        }
        return button;
    }

    function kvValueFormNode(key, value, creating) {
        const form = document.createElement('form');
        form.className = 'oauth-form';
        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid oauth-form-grid--kv';

        const keyInput = textInput(key || '', 'text');
        keyInput.required = true;
        keyInput.disabled = !creating;
        grid.appendChild(formField(t('oauth_kv_key_name'), keyInput));

        const valueArea = document.createElement('textarea');
        valueArea.className = 'oauth-code-editor';
        valueArea.value = value || '';
        valueArea.spellcheck = false;
        valueArea.readOnly = !canWrite('kv');
        grid.appendChild(formField(t('oauth_kv_value'), valueArea));
        form.appendChild(grid);

        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        if (creating) {
            actions.appendChild(smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
                state.oauth.kvCreateOpen = false;
                renderOAuthResource();
            }));
        }
        if (canWrite('kv')) {
            actions.appendChild(kvUploadControl(() => keyInput.value.trim()));
            const saveButton = smallButton(t('save'), 'btn btn--sm btn--primary');
            saveButton.type = 'submit';
            actions.appendChild(saveButton);
            form.addEventListener('submit', (event) => {
                event.preventDefault();
                saveKVValue(keyInput.value.trim(), valueArea.value, saveButton);
            });
        }
        form.appendChild(actions);
        if (canWrite('kv')) {
            const hint = document.createElement('div');
            hint.className = 'help-text';
            hint.textContent = t('oauth_kv_upload_limit', { max: formatBytes(maxKVValueUploadBytes) });
            form.appendChild(hint);
        }
        return form;
    }

    function kvUploadControl(resolveKey) {
        const wrapper = document.createElement('span');
        wrapper.className = 'oauth-inline-upload';
        const input = document.createElement('input');
        input.type = 'file';
        const button = smallButton(t('oauth_kv_upload_file'), 'btn btn--sm btn--ghost', () => input.click());
        input.addEventListener('change', async () => {
            await uploadKVValueFile(resolveKey(), input.files?.[0], button);
            input.value = '';
        });
        wrapper.append(button, input);
        return wrapper;
    }

    function kvNamespaceFormNode(titleValue, creating, namespaceID = '') {
        const form = document.createElement('form');
        form.className = 'oauth-form';
        const title = document.createElement('h4');
        title.className = 'oauth-section-title';
        title.textContent = creating ? t('oauth_kv_create_namespace') : t('oauth_kv_rename_namespace');
        form.appendChild(title);

        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid oauth-form-grid--kv';
        const titleInput = textInput(titleValue || '', 'text');
        titleInput.required = true;
        titleInput.maxLength = 512;
        titleInput.placeholder = t('oauth_kv_namespace_title_placeholder');
        grid.appendChild(formField(t('oauth_kv_namespace_title'), titleInput));
        form.appendChild(grid);

        const hint = document.createElement('div');
        hint.className = 'oauth-row-meta';
        hint.textContent = creating ? t('oauth_kv_create_namespace_hint') : t('oauth_kv_rename_namespace_hint');
        form.appendChild(hint);

        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        actions.appendChild(smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
            if (creating) state.oauth.kvNamespaceCreateOpen = false;
            else state.oauth.kvNamespaceEditingId = '';
            renderOAuthResource();
        }));
        const submitBtn = smallButton(creating ? t('oauth_kv_create_namespace') : t('save'), 'btn btn--sm btn--primary');
        submitBtn.type = 'submit';
        actions.appendChild(submitBtn);
        form.appendChild(actions);

        form.addEventListener('submit', (event) => {
            event.preventDefault();
            const payload = { title: titleInput.value.trim() };
            if (creating) createKVNamespace(payload, submitBtn);
            else updateKVNamespace(namespaceID, payload, submitBtn);
        });
        requestAnimationFrame(() => titleInput.focus());
        return form;
    }

    function r2BucketFormNode() {
        const form = document.createElement('form');
        form.className = 'oauth-form';
        const title = document.createElement('h4');
        title.className = 'oauth-section-title';
        title.textContent = t('oauth_r2_create_bucket');
        form.appendChild(title);

        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid oauth-form-grid--r2';
        const nameInput = textInput('', 'text');
        nameInput.required = true;
        nameInput.minLength = 3;
        nameInput.maxLength = 63;
        nameInput.pattern = '[a-z0-9][a-z0-9-]{1,61}[a-z0-9]';
        grid.appendChild(formField(t('oauth_r2_bucket_name'), nameInput));

        const locationInput = textInput('', 'text');
        locationInput.maxLength = 64;
        locationInput.placeholder = 'ENAM';
        grid.appendChild(formField(t('oauth_r2_location_hint'), locationInput));
        form.appendChild(grid);

        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        actions.appendChild(smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
            state.oauth.r2CreateOpen = false;
            renderOAuthResource();
        }));
        const submitBtn = smallButton(t('oauth_r2_create_bucket'), 'btn btn--sm btn--primary');
        submitBtn.type = 'submit';
        actions.appendChild(submitBtn);
        form.appendChild(actions);

        form.addEventListener('submit', (event) => {
            event.preventDefault();
            createR2Bucket({
                name: nameInput.value.trim(),
                location_hint: locationInput.value.trim(),
            }, submitBtn);
        });
        return form;
    }

    function d1DatabaseFormNode() {
        const form = document.createElement('form');
        form.className = 'oauth-form';
        const title = document.createElement('h4');
        title.className = 'oauth-section-title';
        title.textContent = t('oauth_d1_create_database');
        form.appendChild(title);

        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid oauth-form-grid--d1-row';
        const nameInput = textInput('', 'text');
        nameInput.required = true;
        nameInput.maxLength = 512;
        nameInput.placeholder = t('oauth_d1_database_name_placeholder');
        grid.appendChild(formField(t('oauth_d1_database_name'), nameInput));
        form.appendChild(grid);

        const hint = document.createElement('div');
        hint.className = 'oauth-row-meta';
        hint.textContent = t('oauth_d1_create_database_hint');
        form.appendChild(hint);

        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        actions.appendChild(smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
            state.oauth.d1CreateOpen = false;
            renderOAuthResource();
        }));
        const submitBtn = smallButton(t('oauth_d1_create_database'), 'btn btn--sm btn--primary');
        submitBtn.type = 'submit';
        actions.appendChild(submitBtn);
        form.appendChild(actions);

        form.addEventListener('submit', (event) => {
            event.preventDefault();
            createD1Database({ name: nameInput.value.trim() }, submitBtn);
        });
        requestAnimationFrame(() => nameInput.focus());
        return form;
    }

    function r2ObjectFormNode(key, value, contentType, creating) {
        const form = document.createElement('form');
        form.className = 'oauth-form';
        const title = document.createElement('h4');
        title.className = 'oauth-section-title';
        title.textContent = creating ? t('oauth_r2_object_create') : t('oauth_r2_object_edit');
        form.appendChild(title);

        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid oauth-form-grid--r2';
        const keyInput = textInput(key || '', 'text');
        keyInput.required = true;
        keyInput.maxLength = 1024;
        keyInput.disabled = !creating;
        grid.appendChild(formField(t('oauth_r2_object_key'), keyInput));

        const contentTypeInput = textInput(contentType || 'text/plain; charset=utf-8', 'text');
        grid.appendChild(formField(t('oauth_r2_object_content_type'), contentTypeInput));
        form.appendChild(grid);

        const valueArea = document.createElement('textarea');
        valueArea.className = 'oauth-code-editor';
        valueArea.value = value || '';
        valueArea.spellcheck = false;
        valueArea.readOnly = !canWrite('r2');
        form.appendChild(formField(t('oauth_r2_object_value'), valueArea));

        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        if (creating) {
            actions.appendChild(smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
                state.oauth.r2ObjectCreateOpen = false;
                renderOAuthResource();
            }));
        }
        if (canWrite('r2')) {
            const saveButton = smallButton(t('save'), 'btn btn--sm btn--primary');
            saveButton.type = 'submit';
            actions.appendChild(saveButton);
            form.addEventListener('submit', (event) => {
                event.preventDefault();
                saveR2Object(keyInput.value.trim(), valueArea.value, contentTypeInput.value.trim(), saveButton);
            });
        }
        form.appendChild(actions);
        return form;
    }

    function r2ObjectUploadFormNode() {
        const form = document.createElement('form');
        form.className = 'oauth-form';
        const title = document.createElement('h4');
        title.className = 'oauth-section-title';
        title.textContent = t('oauth_r2_object_upload');
        form.appendChild(title);

        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid oauth-form-grid--r2';
        const keyInput = textInput('', 'text');
        keyInput.maxLength = 1024;
        const fileInput = document.createElement('input');
        fileInput.type = 'file';
        fileInput.className = 'input';
        fileInput.required = true;
        const contentTypeInput = textInput('application/octet-stream', 'text');
        const fileState = document.createElement('p');
        fileState.className = 'help-text';
        fileState.textContent = t('oauth_r2_object_upload_limit', {
            direct: formatBytes(maxR2ObjectUploadBytes),
            max: formatBytes(maxR2ChunkedUploadBytes),
        });
        fileInput.addEventListener('change', () => {
            const file = fileInput.files?.[0];
            if (!file) {
                fileState.textContent = t('oauth_r2_object_upload_limit', {
                    direct: formatBytes(maxR2ObjectUploadBytes),
                    max: formatBytes(maxR2ChunkedUploadBytes),
                });
                fileState.removeAttribute('data-state');
                uploadButton.disabled = false;
                uploadButton.removeAttribute('title');
                return;
            }
            if (!keyInput.value.trim()) keyInput.value = file.name;
            contentTypeInput.value = file.type || 'application/octet-stream';
            const tooLarge = file.size > maxR2ChunkedUploadBytes;
            const chunked = file.size > maxR2ObjectUploadBytes;
            fileState.textContent = t('oauth_r2_object_upload_selected', {
                size: formatBytes(file.size),
                mode: t(chunked ? 'oauth_r2_object_upload_mode_chunked' : 'oauth_r2_object_upload_mode_direct'),
            });
            fileState.setAttribute('data-state', tooLarge ? 'error' : 'ok');
            uploadButton.disabled = tooLarge;
            if (tooLarge) uploadButton.title = t('oauth_r2_object_upload_too_large', {
                size: formatBytes(file.size),
                max: formatBytes(maxR2ChunkedUploadBytes),
            });
            else uploadButton.removeAttribute('title');
        });
        grid.append(
            formField(t('oauth_r2_object_key'), keyInput),
            formField(t('oauth_r2_object_file'), fileInput),
            formField(t('oauth_r2_object_content_type'), contentTypeInput),
        );
        form.appendChild(grid);
        form.appendChild(fileState);
        form.appendChild(r2UploadProgressNode());

        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        const uploadButton = smallButton(t('upload'), 'btn btn--sm btn--primary');
        uploadButton.type = 'submit';
        actions.appendChild(uploadButton);
        form.appendChild(actions);

        form.addEventListener('submit', (event) => {
            event.preventDefault();
            uploadR2ObjectFile(keyInput.value.trim(), fileInput.files?.[0], contentTypeInput.value.trim(), uploadButton);
        });
        return form;
    }

    function r2UploadProgressNode() {
        const progress = state.oauth.r2UploadProgress;
        const node = document.createElement('div');
        node.id = 'oauth-r2-upload-progress';
        node.className = 's3-upload-progress';
        node.hidden = !progress;
        const head = document.createElement('div');
        head.className = 's3-upload-progress__head';
        const title = document.createElement('span');
        title.dataset.role = 'title';
        const percent = document.createElement('span');
        percent.dataset.role = 'percent';
        head.append(title, percent);
        const barWrap = document.createElement('div');
        barWrap.className = 's3-upload-progress__bar';
        const bar = document.createElement('span');
        bar.dataset.role = 'bar';
        barWrap.appendChild(bar);
        const bytes = document.createElement('div');
        bytes.className = 's3-upload-progress__bytes';
        bytes.dataset.role = 'bytes';
        node.append(head, barWrap, bytes);
        queueMicrotask(updateR2UploadProgressNode);
        return node;
    }

    function r2ObjectPanelNode() {
        const object = state.oauth.r2ObjectValue;
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const header = document.createElement('div');
        header.className = 'oauth-action-bar';
        const copy = document.createElement('div');
        const heading = document.createElement('h4');
        heading.className = 'oauth-action-title mono';
        heading.textContent = state.oauth.selectedR2ObjectKey || t('oauth_r2_object');
        const subtitle = document.createElement('div');
        subtitle.className = 'oauth-row-meta';
        subtitle.textContent = t('oauth_r2_object_detail');
        copy.append(heading, subtitle);
        header.appendChild(copy);
        if (state.oauth.selectedR2ObjectKey) {
            header.appendChild(smallButton(t('oauth_r2_object_copy_key'), 'btn btn--sm btn--ghost', () => copyOAuthText(state.oauth.selectedR2ObjectKey)));
        }
        section.appendChild(header);
        if (!object) {
            section.appendChild(empty(t('oauth_r2_object_loading')));
            return section;
        }
        const meta = document.createElement('div');
        meta.className = 'oauth-row-meta';
        meta.textContent = [
            formatBytes(object.bytes || 0),
            object.content_type || '',
            object.truncated ? t('oauth_r2_object_truncated') : '',
        ].filter(Boolean).join(' · ');
        section.appendChild(meta);
        section.appendChild(r2ObjectMetadataNode(object));
        if (object.encoding === 'binary') {
            const preview = r2ObjectPreviewNode(object);
            if (preview) {
                section.appendChild(preview);
            } else if (object.binary_preview?.hexdump) {
                section.appendChild(r2BinaryPreviewNode(object));
            } else {
                section.appendChild(empty(t('oauth_r2_object_binary', { bytes: formatBytes(object.bytes || 0) })));
            }
            const actions = document.createElement('div');
            actions.className = 'oauth-row-actions';
            actions.appendChild(smallButton(t('oauth_r2_object_copy_key'), 'btn btn--sm btn--ghost', () => copyOAuthText(object.key || state.oauth.selectedR2ObjectKey)));
            actions.appendChild(smallButton(t('download'), 'btn btn--sm btn--ghost', () => downloadR2Object(object.key || state.oauth.selectedR2ObjectKey)));
            if (canWrite('r2')) {
                actions.appendChild(smallButton(t('oauth_r2_object_copy'), 'btn btn--sm btn--ghost', (event) => copyOrMoveR2Object(object.key || state.oauth.selectedR2ObjectKey, false, event.currentTarget)));
                actions.appendChild(smallButton(t('oauth_r2_object_move'), 'btn btn--sm btn--ghost', (event) => copyOrMoveR2Object(object.key || state.oauth.selectedR2ObjectKey, true, event.currentTarget)));
                actions.appendChild(smallButton(t('delete'), 'btn btn--sm btn--danger', () => deleteR2Object(object.key || state.oauth.selectedR2ObjectKey)));
            }
            section.appendChild(actions);
            return section;
        }
        section.appendChild(r2ObjectFormNode(object.key || state.oauth.selectedR2ObjectKey, object.value || '', object.content_type || 'text/plain; charset=utf-8', false));
        const actions = document.createElement('div');
        actions.className = 'oauth-row-actions';
        actions.appendChild(smallButton(t('oauth_r2_object_copy_key'), 'btn btn--sm btn--ghost', () => copyOAuthText(object.key || state.oauth.selectedR2ObjectKey)));
        actions.appendChild(smallButton(t('download'), 'btn btn--sm btn--ghost', () => downloadR2Object(object.key || state.oauth.selectedR2ObjectKey)));
        if (canWrite('r2')) {
            actions.appendChild(smallButton(t('oauth_r2_object_copy'), 'btn btn--sm btn--ghost', (event) => copyOrMoveR2Object(object.key || state.oauth.selectedR2ObjectKey, false, event.currentTarget)));
            actions.appendChild(smallButton(t('oauth_r2_object_move'), 'btn btn--sm btn--ghost', (event) => copyOrMoveR2Object(object.key || state.oauth.selectedR2ObjectKey, true, event.currentTarget)));
            actions.appendChild(smallButton(t('delete'), 'btn btn--sm btn--danger', () => deleteR2Object(object.key || state.oauth.selectedR2ObjectKey)));
        }
        section.appendChild(actions);
        return section;
    }

    function r2ObjectMetadataNode(object) {
        const listObject = selectedR2ListObject(object);
        const httpMetadata = listObject.http_metadata || {};
        const contentType = object.content_type || httpMetadata.contentType || '';
        const grid = document.createElement('div');
        grid.className = 'oauth-metric-grid';
        grid.append(
            metricNode(t('oauth_r2_object_size'), formatBytes(object.bytes ?? listObject.size ?? 0)),
            metricNode(t('oauth_r2_object_content_type'), contentType || t('oauth_unavailable')),
            metricNode(t('oauth_r2_object_etag'), listObject.etag || t('oauth_unavailable')),
            metricNode(t('oauth_r2_object_storage_class'), listObject.storage_class || t('oauth_unavailable')),
            metricNode(t('oauth_r2_object_last_modified'), formatDate(listObject.last_modified) || t('oauth_unavailable')),
            metricNode(t('oauth_r2_object_encoding'), r2ObjectEncodingLabel(object)),
        );
        return grid;
    }

    function selectedR2ListObject(object) {
        const key = object?.key || state.oauth.selectedR2ObjectKey;
        return state.oauth.r2Objects.find((item) => item.key === key) || {};
    }

    function r2ObjectKnownBytes(object) {
        const value = object?.bytes ?? selectedR2ListObject(object).size;
        const bytes = Number(value);
        return Number.isFinite(bytes) && bytes > 0 ? bytes : 0;
    }

    function r2ObjectPreviewTooLarge(object) {
        const bytes = r2ObjectKnownBytes(object);
        return bytes > maxR2InlinePreviewBytes;
    }

    function r2ObjectEncodingLabel(object) {
        if (object.truncated) return t('oauth_r2_object_truncated');
        if (object.encoding === 'binary') return t('oauth_r2_object_encoding_binary');
        if (object.encoding === 'too_large') return t('oauth_r2_object_encoding_too_large');
        if (object.encoding === 'text') return t('oauth_r2_object_encoding_text');
        if (object.encoding === 'utf8' || object.encoding === 'utf-8') return t('oauth_r2_object_encoding_utf8');
        return object.encoding || t('oauth_unavailable');
    }

    function kvValuePanelNode() {
        const value = state.oauth.kvValue;
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = state.oauth.selectedKVKey || t('oauth_kv_value');
        section.appendChild(heading);
        if (!value) {
            section.appendChild(empty(t('oauth_kv_loading_value')));
            return section;
        }
        if (value.encoding === 'binary') {
            section.appendChild(empty(t('oauth_kv_value_binary', { bytes: formatBytes(value.bytes || 0) })));
            const preview = kvBinaryPreviewNode(value);
            if (preview) section.appendChild(preview);
            section.appendChild(kvValueActionButtons(value.key || state.oauth.selectedKVKey));
            return section;
        }
        if (value.encoding === 'too_large') {
            section.appendChild(empty(t('oauth_kv_value_too_large', { bytes: formatBytes(value.bytes || 0) })));
            section.appendChild(kvValueActionButtons(value.key || state.oauth.selectedKVKey));
            return section;
        }
        section.appendChild(kvValueFormNode(value.key || state.oauth.selectedKVKey, value.value || '', false));
        section.appendChild(kvValueActionButtons(value.key || state.oauth.selectedKVKey));
        return section;
    }

    function kvValueActionButtons(key) {
        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions oauth-value-actions';
        const download = smallButton(t('download'), 'btn btn--sm btn--ghost', () => downloadKVValue(key));
        download.disabled = !kvValueDownloadURL(key);
        actions.appendChild(download);
        if (canWrite('kv')) {
            actions.appendChild(kvUploadControl(() => key));
            actions.appendChild(smallButton(t('delete'), 'btn btn--sm btn--danger', () => deleteKVValue(key)));
        }
        return actions;
    }

    function d1ResultNode(result, index, total) {
        const card = document.createElement('div');
        card.className = 'oauth-result-card';
        const meta = document.createElement('div');
        meta.className = 'oauth-row-meta';
        const parts = [];
        if (total > 1) parts.push(t('oauth_d1_statement', { n: index + 1 }));
        if (result.meta?.duration != null) parts.push(`${result.meta.duration}ms`);
        if (result.meta?.rows_read != null) parts.push(`${t('oauth_d1_rows_read')} ${result.meta.rows_read}`);
        if (result.meta?.rows_written != null) parts.push(`${t('oauth_d1_rows_written')} ${result.meta.rows_written}`);
        meta.textContent = parts.join(' · ');
        card.appendChild(meta);

        const rows = Array.isArray(result.results) ? result.results : [];
        if (!rows.length) {
            card.appendChild(empty(result.success === false ? t('oauth_d1_failed') : t('oauth_d1_success_no_rows')));
            return card;
        }
        const columns = Array.from(rows.reduce((set, row) => {
            Object.keys(row || {}).forEach((key) => set.add(key));
            return set;
        }, new Set())).sort();
        const tableWrap = document.createElement('div');
        tableWrap.className = 'oauth-table-wrap';
        const table = document.createElement('table');
        table.className = 'oauth-data-table';
        const thead = document.createElement('thead');
        const headRow = document.createElement('tr');
        for (const column of columns) {
            const th = document.createElement('th');
            th.textContent = column;
            headRow.appendChild(th);
        }
        thead.appendChild(headRow);
        table.appendChild(thead);
        const tbody = document.createElement('tbody');
        rows.slice(0, 100).forEach((row) => {
            const tr = document.createElement('tr');
            for (const column of columns) {
                const td = document.createElement('td');
                td.textContent = displayValue(row?.[column]);
                tr.appendChild(td);
            }
            tbody.appendChild(tr);
        });
        table.appendChild(tbody);
        tableWrap.appendChild(table);
        card.appendChild(tableWrap);
        if (rows.length > 100) card.appendChild(empty(t('oauth_d1_rows_truncated', { n: rows.length })));
        return card;
    }

    function displayValue(value) {
        if (value == null) return 'NULL';
        if (typeof value === 'object') {
            try { return JSON.stringify(value); } catch { return String(value); }
        }
        return String(value);
    }

    function fieldEditValue(value) {
        if (value == null) return '';
        if (typeof value === 'object') {
            try { return JSON.stringify(value); } catch { return String(value); }
        }
        return String(value);
    }

    function dnsFormNode(record) {
        const form = document.createElement('form');
        form.className = 'oauth-form';
        const title = document.createElement('h4');
        title.className = 'oauth-section-title';
        title.textContent = record ? t('oauth_dns_edit') : t('oauth_dns_create');
        form.appendChild(title);

        const grid = document.createElement('div');
        grid.className = 'oauth-form-grid';
        const typeSelect = document.createElement('select');
        typeSelect.className = 'form-select';
        for (const type of dnsTypes) {
            const option = document.createElement('option');
            option.value = type;
            option.textContent = type;
            typeSelect.appendChild(option);
        }
        typeSelect.value = dnsTypes.includes(record?.type) ? record.type : 'A';
        grid.appendChild(formField(t('oauth_dns_type'), typeSelect));

        const nameInput = textInput(record?.name || '', 'text');
        nameInput.required = true;
        grid.appendChild(formField(t('oauth_dns_name'), nameInput));

        const contentInput = textInput(record?.content || '', 'text');
        contentInput.required = true;
        grid.appendChild(formField(t('oauth_dns_content'), contentInput));

        const ttlInput = textInput(String(record?.ttl || 1), 'number');
        ttlInput.min = '1';
        ttlInput.step = '1';
        grid.appendChild(formField(t('oauth_dns_ttl'), ttlInput));

        const commentInput = textInput(record?.comment || '', 'text');
        grid.appendChild(formField(t('oauth_dns_comment'), commentInput));
        form.appendChild(grid);

        const options = document.createElement('div');
        options.className = 'oauth-form-options';
        const proxiedLabel = document.createElement('label');
        proxiedLabel.className = 'toggle';
        const proxiedInput = document.createElement('input');
        proxiedInput.type = 'checkbox';
        proxiedInput.checked = !!record?.proxied;
        const track = document.createElement('span');
        track.className = 'track';
        const label = document.createElement('span');
        label.className = 'label';
        label.textContent = t('proxied');
        proxiedLabel.append(proxiedInput, track, label);
        options.appendChild(proxiedLabel);
        form.appendChild(options);
        const syncProxiedAvailability = () => {
            const canProxy = ['A', 'AAAA', 'CNAME'].includes(typeSelect.value);
            proxiedInput.disabled = !canProxy;
            if (!canProxy) proxiedInput.checked = false;
        };
        typeSelect.addEventListener('change', syncProxiedAvailability);
        syncProxiedAvailability();

        const actions = document.createElement('div');
        actions.className = 'oauth-form-actions';
        const cancelBtn = smallButton(t('cancel'), 'btn btn--sm btn--ghost', () => {
            state.oauth.dnsFormMode = '';
            state.oauth.dnsEditingId = '';
            renderOAuthResource();
        });
        const submitBtn = smallButton(record ? t('oauth_dns_update') : t('oauth_dns_create'), 'btn btn--sm btn--primary');
        submitBtn.type = 'submit';
        actions.append(cancelBtn, submitBtn);
        form.appendChild(actions);

        form.addEventListener('submit', (event) => {
            event.preventDefault();
            const payload = {
                type: typeSelect.value,
                name: nameInput.value.trim(),
                content: contentInput.value.trim(),
                ttl: Number.parseInt(ttlInput.value, 10) || 1,
                comment: commentInput.value.trim(),
            };
            if (['A', 'AAAA', 'CNAME'].includes(typeSelect.value)) {
                payload.proxied = proxiedInput.checked;
            }
            submitDNSRecord(record, payload, submitBtn);
        });
        return form;
    }

    async function toggleDNSProxy(record, button) {
        if (!dnsRecordSupportsProxy(record)) return;
        const nextProxied = !record.proxied;
        const payload = dnsRecordPayload(record, {
            proxied: nextProxied,
            ttl: nextProxied ? 1 : record.ttl,
        });
        await submitDNSRecord(record, payload, button);
    }

    function dnsRecordPayload(record, overrides = {}) {
        const next = { ...record, ...overrides };
        const payload = {
            type: String(next.type || '').toUpperCase(),
            name: next.name || '',
            content: next.content || '',
            ttl: Number.parseInt(next.ttl, 10) || 1,
            comment: next.comment || '',
        };
        if (dnsRecordSupportsProxy(next)) {
            payload.proxied = !!next.proxied;
        }
        return payload;
    }

    function dnsFilterNode() {
        const wrap = document.createElement('div');
        wrap.className = 'oauth-filter-bar';
        const input = textInput(state.oauth.dnsFilter || '', 'search');
        input.dataset.oauthDnsFilter = 'true';
        input.placeholder = t('oauth_dns_search_placeholder');
        input.setAttribute('aria-label', t('oauth_dns_search_label'));
        input.autocomplete = 'off';
        input.spellcheck = false;
        input.addEventListener('input', () => {
            state.oauth.dnsFilter = input.value;
            renderOAuthResource();
            requestAnimationFrame(() => {
                const next = document.querySelector('[data-oauth-dns-filter="true"]');
                if (!next) return;
                next.focus();
                const end = next.value.length;
                next.setSelectionRange(end, end);
            });
        });
        const meta = document.createElement('div');
        meta.className = 'oauth-filter-meta';
        meta.textContent = t('oauth_dns_search_count', {
            n: filteredDNSRecords().length,
            m: state.oauth.dnsRecords.length,
        });
        wrap.append(input, meta);
        return wrap;
    }

    function zoneSettingActionsNode() {
        const section = document.createElement('section');
        section.className = 'oauth-settings-actions';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = t('oauth_zone_actions');
        section.appendChild(heading);

        if (canWrite('zone_settings')) {
            for (const [settingID, labelKey] of zoneSettingToggles) {
                const node = zoneSettingToggleNode(settingID, labelKey);
                if (node) section.appendChild(node);
            }
            appendZoneSettingSelect(section, 'ssl', 'oauth_ssl_mode', sslModes, 'full');
            appendZoneSettingSelect(section, 'security_level', 'oauth_security_level', securityLevels, 'medium');
            appendZoneSettingSelect(section, 'cache_level', 'oauth_cache_level', cacheLevels, 'aggressive');
            appendZoneSettingSelect(
                section,
                'browser_cache_ttl',
                'oauth_browser_cache_ttl',
                browserCacheTTLs,
                14400,
                (value) => Number.parseInt(value, 10)
            );
        }

        if (canWrite('cache_purge')) {
            const purgeButton = smallButton(t('oauth_cache_purge'), 'btn btn--sm btn--danger', () => purgeZoneCache(purgeButton));
            section.appendChild(purgeButton);
        }
        return section;
    }

    function zoneSettingToggleNode(settingID, labelKey) {
        const setting = findZoneSetting(settingID);
        if (!setting) return null;
        const row = document.createElement('div');
        row.className = 'oauth-setting-control';
        const toggle = document.createElement('label');
        toggle.className = 'toggle';
        const input = document.createElement('input');
        input.type = 'checkbox';
        input.checked = String(setting.value) === 'on';
        input.disabled = !setting.editable;
        const track = document.createElement('span');
        track.className = 'track';
        const label = document.createElement('span');
        label.className = 'label';
        label.textContent = t(labelKey);
        toggle.append(input, track, label);
        input.addEventListener('change', () => updateZoneSetting(settingID, input.checked ? 'on' : 'off', input));
        row.appendChild(toggle);
        return row;
    }

    function appendZoneSettingSelect(section, settingID, labelKey, options, fallback, coerceValue) {
        const setting = findZoneSetting(settingID);
        if (!setting) return;
        const select = document.createElement('select');
        select.className = 'form-select';
        const currentValue = String(setting.value ?? fallback);
        let hasCurrent = false;
        for (const [value, optionLabelKey] of options) {
            const option = document.createElement('option');
            option.value = String(value);
            option.textContent = t(optionLabelKey);
            if (option.value === currentValue) hasCurrent = true;
            select.appendChild(option);
        }
        if (!hasCurrent) {
            const option = document.createElement('option');
            option.value = currentValue;
            option.textContent = t('oauth_current_setting_value', { value: currentValue });
            select.prepend(option);
        }
        select.value = currentValue;
        select.disabled = !setting.editable;
        select.addEventListener('change', () => {
            const nextValue = coerceValue ? coerceValue(select.value) : select.value;
            updateZoneSetting(settingID, nextValue, select);
        });
        section.appendChild(formField(t(labelKey), select, 'oauth-setting-control'));
    }

    function formField(labelText, control, className = '') {
        const label = document.createElement('label');
        label.className = ['oauth-form-field', className].filter(Boolean).join(' ');
        const span = document.createElement('span');
        span.textContent = labelText;
        label.append(span, control);
        return label;
    }

    function toggleOption(labelText, checked = false) {
        const node = document.createElement('label');
        node.className = 'toggle';
        const input = document.createElement('input');
        input.type = 'checkbox';
        input.checked = !!checked;
        const track = document.createElement('span');
        track.className = 'track';
        const label = document.createElement('span');
        label.className = 'label';
        label.textContent = labelText;
        node.append(input, track, label);
        return { node, input };
    }

    function textInput(value, type) {
        const input = document.createElement('input');
        input.type = type || 'text';
        input.value = value;
        return input;
    }

    function r2ObjectFilterNode() {
        const wrap = document.createElement('div');
        wrap.className = 'oauth-filter-bar';
        const input = textInput(state.oauth.r2ObjectFilter || '', 'search');
        input.dataset.oauthR2ObjectFilter = 'true';
        input.placeholder = t('oauth_r2_object_search_placeholder');
        input.setAttribute('aria-label', t('oauth_r2_object_search_label'));
        input.autocomplete = 'off';
        input.spellcheck = false;
        input.addEventListener('input', () => {
            state.oauth.r2ObjectFilter = input.value;
            renderOAuthResource();
            requestAnimationFrame(() => {
                const next = document.querySelector('[data-oauth-r2-object-filter="true"]');
                if (!next) return;
                next.focus();
                const end = next.value.length;
                next.setSelectionRange(end, end);
            });
        });
        const meta = document.createElement('div');
        meta.className = 'oauth-filter-meta';
        meta.textContent = t('oauth_r2_object_search_count', {
            n: filteredR2Objects().length,
            m: state.oauth.r2Objects.length,
        });
        wrap.append(input, meta);
        return wrap;
    }

    function selectedZoneName() {
        const zone = state.oauth.zones.find((item) => item.id === state.oauth.selectedZoneId);
        return zone?.name || state.oauth.selectedZoneId || '';
    }

    function dnsRecordSupportsProxy(record) {
        return ['A', 'AAAA', 'CNAME'].includes(String(record?.type || '').toUpperCase());
    }

    function filteredDNSRecords() {
        const query = normalizeFilterText(state.oauth.dnsFilter);
        if (!query) return state.oauth.dnsRecords;
        return state.oauth.dnsRecords.filter((record) => {
            if (record.id && record.id === state.oauth.dnsEditingId) return true;
            return dnsRecordSearchText(record).includes(query);
        });
    }

    function dnsRecordSearchText(record) {
        const proxied = record?.proxied == null ? '' : (record.proxied ? t('proxied') : t('dns_only'));
        return normalizeFilterText([
            record?.type,
            record?.name,
            record?.content,
            record?.comment,
            record?.ttl,
            proxied,
        ].filter((item) => item != null && item !== '').join(' '));
    }

    function filteredR2Objects() {
        const query = normalizeFilterText(state.oauth.r2ObjectFilter);
        if (!query) return state.oauth.r2Objects;
        return state.oauth.r2Objects.filter((object) => r2ObjectSearchText(object).includes(query));
    }

    function r2ObjectSearchText(object) {
        return normalizeFilterText([
            object?.key,
            object?.etag,
            object?.storage_class,
            object?.http_metadata?.contentType,
            object?.last_modified,
            object?.size,
            object?.size == null ? '' : formatBytes(object.size),
        ].filter((item) => item != null && item !== '').join(' '));
    }

    function normalizeFilterText(value) {
        return String(value || '').trim().toLowerCase();
    }

    function selectedZoneDetail() {
        if (state.oauth.zoneDetail?.zone?.id === state.oauth.selectedZoneId) {
            return state.oauth.zoneDetail.zone;
        }
        return state.oauth.zones.find((item) => item.id === state.oauth.selectedZoneId);
    }

    function selectedAccountName() {
        const account = state.oauth.accounts.find((item) => item.id === state.oauth.selectedAccountId);
        return account?.name || state.oauth.selectedAccountId || '';
    }

    function selectedR2Bucket() {
        return state.oauth.r2Buckets.find((item) => item.name === state.oauth.selectedR2BucketName);
    }

    function selectedKVNamespace() {
        return state.oauth.kvNamespaces.find((item) => item.id === state.oauth.selectedKVNamespaceId);
    }

    function selectedKVListKey(keyName = state.oauth.selectedKVKey) {
        return state.oauth.kvKeys.find((item) => item.name === keyName) || {};
    }

    function kvSelectedKeys() {
        return Array.isArray(state.oauth.kvSelectedKeys) ? state.oauth.kvSelectedKeys.filter(Boolean) : [];
    }

    function kvSelectedKeySet() {
        return new Set(kvSelectedKeys());
    }

    function setKVKeySelected(keyName, selected) {
        keyName = String(keyName || '').trim();
        if (!keyName) return;
        const keys = kvSelectedKeySet();
        if (selected) keys.add(keyName);
        else keys.delete(keyName);
        state.oauth.kvSelectedKeys = Array.from(keys);
    }

    function selectLoadedKVKeys() {
        const keys = kvSelectedKeySet();
        for (const item of state.oauth.kvKeys) {
            if (item?.name) keys.add(item.name);
        }
        state.oauth.kvSelectedKeys = Array.from(keys);
        renderOAuthResource();
    }

    function pruneKVSelectedKeys() {
        const loaded = new Set(state.oauth.kvKeys.map((item) => item.name).filter(Boolean));
        state.oauth.kvSelectedKeys = kvSelectedKeys().filter((key) => loaded.has(key));
    }

    function selectedD1Database() {
        return state.oauth.d1Databases.find((item) => item.uuid === state.oauth.selectedD1DatabaseId);
    }

    function snippetTemplate() {
        return [
            'export default {',
            '  async fetch(request) {',
            '    return fetch(request);',
            '  }',
            '};',
        ].join('\n');
    }

    function workerTailStatusText() {
        if (state.oauth.workerTailConnecting) return t('oauth_worker_tail_connecting');
        if (state.oauth.workerTailConnected) return state.oauth.workerTailPaused ? t('oauth_worker_tail_paused') : t('oauth_worker_tail_connected_short');
        return t('oauth_worker_tail_idle');
    }

    function closeWorkerTailStream() {
        if (state.oauth.workerTailSource) state.oauth.workerTailSource.close();
        state.oauth.workerTailSource = null;
        state.oauth.workerTailConnecting = false;
        state.oauth.workerTailConnected = false;
        state.oauth.workerTailPaused = false;
    }

    function closeOAuthLiveStreams() {
        closeWorkerTailStream();
    }

    function appendWorkerTailLine(level, text, ts = Date.now()) {
        state.oauth.workerTailLines.push({ level: level || 'info', text: text || '', ts });
        if (state.oauth.workerTailLines.length > 500) {
            state.oauth.workerTailLines.splice(0, state.oauth.workerTailLines.length - 500);
        }
    }

    function appendWorkerTailData(raw) {
        if (!raw) return;
        let item;
        try {
            item = JSON.parse(raw);
        } catch {
            appendWorkerTailLine('event', raw);
            return;
        }
        if (Array.isArray(item)) {
            item.forEach((entry) => appendWorkerTailItem(entry, raw));
            return;
        }
        appendWorkerTailItem(item, raw);
    }

    function appendWorkerTailItem(item, fallbackText) {
        if (!item || typeof item !== 'object') {
            appendWorkerTailLine('event', displayTailValue(item));
            return;
        }
        const eventTS = Number(item.eventTimestamp || Date.now());
        const ts = Number.isFinite(eventTS) ? eventTS : Date.now();
        const request = item.event?.request;
        if (request) {
            const status = item.event?.response?.status ? ` ${item.event.response.status}` : '';
            appendWorkerTailLine('event', `${request.method || 'GET'} ${request.url || ''}${status} -> ${item.outcome || '?'}`, ts);
        } else if (item.event?.cron) {
            appendWorkerTailLine('event', `cron ${item.event.cron} -> ${item.outcome || '?'}`, ts);
        }
        if (Array.isArray(item.logs)) {
            for (const log of item.logs) {
                const logTS = Number(log.timestamp || ts);
                const text = Array.isArray(log.message) ? log.message.map(displayTailValue).join(' ') : displayTailValue(log.message);
                appendWorkerTailLine(log.level || 'log', text, Number.isFinite(logTS) ? logTS : ts);
            }
        }
        if (Array.isArray(item.exceptions)) {
            for (const exception of item.exceptions) {
                const exTS = Number(exception.timestamp || ts);
                appendWorkerTailLine('exception', [exception.name, exception.message].filter(Boolean).join(': '), Number.isFinite(exTS) ? exTS : ts);
            }
        }
        if (!request && !item.event?.cron && !Array.isArray(item.logs) && !Array.isArray(item.exceptions)) {
            appendWorkerTailLine('event', fallbackText || JSON.stringify(item), ts);
        }
    }

    function displayTailValue(value) {
        if (value == null) return 'null';
        if (typeof value === 'string') return value;
        if (typeof value === 'number' || typeof value === 'boolean') return String(value);
        if (Array.isArray(value)) return `[${value.map(displayTailValue).join(', ')}]`;
        if (typeof value === 'object') {
            try { return JSON.stringify(value); } catch { return String(value); }
        }
        return String(value);
    }

    function parseSSEPayload(data) {
        try { return JSON.parse(data || '{}'); } catch { return {}; }
    }

    function formatTimeOnly(value) {
        const date = new Date(value || Date.now());
        if (Number.isNaN(date.getTime())) return '';
        return date.toTimeString().slice(0, 8);
    }

    function resetOAuthResourceState({ keepStatus = false } = {}) {
        const status = state.oauth.status;
        state.oauth.accounts = [];
        state.oauth.zones = [];
        resetOverview();
        state.oauth.dnsRecords = [];
        state.oauth.dnsSession = null;
        state.oauth.dnsCapabilities = null;
        state.oauth.dnsRecordsError = '';
        state.oauth.dnsMutationError = '';
        state.oauth.tunnels = [];
        state.oauth.localTunnelProfiles = [];
        resetTunnelDetail();
        state.oauth.workers = [];
        state.oauth.r2Buckets = [];
        state.oauth.r2Metrics = null;
        state.oauth.r2MetricsError = '';
        state.oauth.r2MetricsLoading = false;
        state.oauth.r2Session = null;
        state.oauth.r2Capabilities = null;
        state.oauth.d1Databases = [];
        state.oauth.d1Session = null;
        state.oauth.d1Capabilities = null;
        state.oauth.d1DatabasesError = '';
        state.oauth.d1DetailsError = '';
        state.oauth.kvNamespaces = [];
        state.oauth.kvSession = null;
        state.oauth.kvCapabilities = null;
        state.oauth.snippets = [];
        state.oauth.snippetSession = null;
        state.oauth.snippetCapabilities = null;
        state.oauth.snippetsError = '';
        resetZoneSettingsDetail();
        state.oauth.selectedAccountId = '';
        state.oauth.selectedZoneId = '';
        resetZoneDetail();
        state.oauth.dnsFormMode = '';
        state.oauth.dnsEditingId = '';
        state.oauth.dnsFilter = '';
        resetWorkerDetail();
        resetStorageDetail();
        resetSnippetDetail();
        resetWAFDetail();
        resetAnalyticsDetail();
        resetUsageDetail();
        if (!keepStatus) state.oauth.status = null;
        else state.oauth.status = status;
    }

    function resetStorageDetail() {
        state.oauth.storageView = '';
        state.oauth.r2CreateOpen = false;
        state.oauth.r2ObjectCreateOpen = false;
        state.oauth.d1CreateOpen = false;
        state.oauth.selectedR2BucketName = '';
        state.oauth.selectedR2ObjectKey = '';
        state.oauth.r2Objects = [];
        state.oauth.r2ObjectsError = '';
        state.oauth.r2Cursor = '';
        state.oauth.r2ObjectValue = null;
        state.oauth.r2ObjectValueError = '';
        state.oauth.r2ObjectFilter = '';
        state.oauth.r2MetricsError = '';
        state.oauth.r2UploadProgress = null;
        state.oauth.kvNamespaceCreateOpen = false;
        state.oauth.kvNamespaceEditingId = '';
        state.oauth.selectedKVNamespaceId = '';
        state.oauth.selectedKVKey = '';
        state.oauth.kvSelectedKeys = [];
        state.oauth.kvKeys = [];
        state.oauth.kvKeysError = '';
        state.oauth.kvCursor = '';
        state.oauth.kvValue = null;
        state.oauth.kvValueError = '';
        state.oauth.kvCreateOpen = false;
        state.oauth.selectedD1DatabaseId = '';
        state.oauth.selectedD1TableName = '';
        state.oauth.d1Tables = [];
        state.oauth.d1TablesDatabaseId = '';
        state.oauth.d1TablesError = '';
        state.oauth.d1TableColumns = [];
        state.oauth.d1TableRows = [];
        state.oauth.d1TableRowsError = '';
        state.oauth.d1TableOffset = 0;
        state.oauth.d1TableHasMore = false;
        state.oauth.d1EditingRow = null;
        state.oauth.d1Results = [];
        state.oauth.d1QueryError = '';
    }

    function resetKVDetailSelection() {
        state.oauth.storageView = '';
        state.oauth.selectedKVNamespaceId = '';
        state.oauth.selectedKVKey = '';
        state.oauth.kvSelectedKeys = [];
        state.oauth.kvKeys = [];
        state.oauth.kvKeysError = '';
        state.oauth.kvCursor = '';
        state.oauth.kvValue = null;
        state.oauth.kvValueError = '';
        state.oauth.kvCreateOpen = false;
        state.oauth.kvNamespaceEditingId = '';
    }

    function resetTunnelDetail() {
        state.oauth.tunnelCreateOpen = false;
        state.oauth.tunnelConfigs = {};
        state.oauth.tunnelConfigLoading = {};
        state.oauth.tunnelConfigErrors = {};
        state.oauth.tunnelIngressCreateTunnelId = '';
        state.oauth.tunnelIngressEditing = null;
    }

    function resetOverview() {
        state.oauth.overview = null;
        state.oauth.overviewLoading = false;
        state.oauth.overviewError = '';
    }

    function resetZoneDetail() {
        state.oauth.zoneDetail = null;
        state.oauth.zoneDetailError = '';
        state.oauth.zoneDetailLoading = false;
        resetZoneDNSCount();
    }

    function resetZoneDNSCount() {
        state.oauth.zoneDNSCount = null;
        state.oauth.zoneDNSCountError = '';
        state.oauth.zoneDNSCountLoading = false;
        state.oauth.zoneDNSCountZoneId = '';
    }

    function resetSelectedZoneResources() {
        state.oauth.dnsRecords = [];
        resetZoneSettingsDetail();
        state.oauth.dnsFormMode = '';
        state.oauth.dnsEditingId = '';
        state.oauth.dnsFilter = '';
        state.oauth.snippets = [];
        state.oauth.snippetSession = null;
        state.oauth.snippetCapabilities = null;
        state.oauth.snippetsError = '';
        resetSnippetDetail();
        resetWAFDetail();
        resetAnalyticsDetail();
    }

    function resetZoneSettingsDetail() {
        state.oauth.zoneSettings = [];
        state.oauth.zoneSettingsSession = null;
        state.oauth.zoneSettingsCapabilities = null;
        state.oauth.zoneSettingsError = '';
        state.oauth.zoneSettingsMutationError = '';
        state.oauth.zoneCachePurgeError = '';
    }

    function resetWorkerDetail() {
        closeWorkerTailStream();
        state.oauth.selectedWorkerId = '';
        state.oauth.workerDetail = null;
        state.oauth.workerMetrics = null;
        state.oauth.workerMetricsError = '';
        state.oauth.workerMetricsLoading = false;
        state.oauth.workerTailLines = [];
        state.oauth.workerTailPaused = false;
    }

    function resetSnippetDetail() {
        state.oauth.selectedSnippetName = '';
        state.oauth.snippetRules = [];
        state.oauth.snippetRulesError = '';
        state.oauth.snippetMutationError = '';
        state.oauth.snippetCreateOpen = false;
        state.oauth.snippetRuleCreateOpen = false;
        resetSnippetContent();
    }

    function resetSnippetContent() {
        state.oauth.snippetContent = null;
        state.oauth.snippetContentLoading = false;
        state.oauth.snippetContentError = '';
        state.oauth.snippetContentDraft = '';
        state.oauth.snippetContentMainFile = 'snippet.js';
    }

	function resetWAFDetail() {
		state.oauth.wafRuleset = null;
		state.oauth.wafManagedRuleset = null;
		state.oauth.wafManagedOverrideRuleset = null;
		state.oauth.wafSession = null;
		state.oauth.wafCapabilities = null;
		state.oauth.wafError = '';
		state.oauth.wafMutationError = '';
		state.oauth.wafCreateOpen = false;
		state.oauth.wafEditingId = '';
		state.oauth.wafManagedExceptionCreateOpen = false;
		state.oauth.wafManagedExceptionEditingId = '';
		state.oauth.wafManagedOverrideCreateOpen = false;
		state.oauth.wafManagedOverrideEditingId = '';
	}

    function resetAnalyticsDetail() {
        state.oauth.zoneAnalytics = null;
        state.oauth.zoneAnalyticsError = '';
        state.oauth.zoneAnalyticsLoading = false;
    }

    function resetUsageDetail() {
        state.oauth.accountUsage = null;
        state.oauth.accountUsageError = '';
        state.oauth.accountUsageLoading = false;
    }

    function wafActionLabel(action) {
        const key = `oauth_waf_action_${String(action || '').replaceAll('-', '_')}`;
        const label = t(key);
        return label === key ? (action || '') : label;
    }

    function isEditableWAFRule(rule) {
        if (!rule?.id) return false;
        const action = String(rule.action || '');
        if (!wafActions.includes(action)) return false;
        return isEditableWAFActionParameters(action, rule.action_parameters);
    }

	function isEditableWAFActionParameters(action, params) {
		if (!params) return true;
        const raw = params.raw && typeof params.raw === 'object' ? params.raw : {};
        if (action !== 'skip') return Object.keys(raw).length === 0;
        if (params.id || params.version) return false;
        if (Array.isArray(params.rulesets) && params.rulesets.length) return false;
        if (params.rules && typeof params.rules === 'object' && Object.keys(params.rules).length) return false;
        if (params.ruleset && params.ruleset !== 'current') return false;
        const supportedProducts = new Set(wafSkipProducts.map(([value]) => value));
        const supportedPhases = new Set(wafSkipPhases.map(([value]) => value));
        if (Array.isArray(params.products) && params.products.some((value) => !supportedProducts.has(value))) return false;
        if (Array.isArray(params.phases) && params.phases.some((value) => !supportedPhases.has(value))) return false;
		const allowedRaw = new Set(['ruleset', 'products', 'phases']);
		return Object.keys(raw).every((key) => allowedRaw.has(key));
	}

	function isEditableWAFManagedException(rule) {
		if (!rule?.id) return false;
		if (String(rule.action || '') !== 'skip') return false;
		return isEditableWAFManagedActionParameters(rule.action_parameters);
	}

	function isEditableWAFManagedOverride(rule) {
		if (!rule?.id) return false;
		if (String(rule.action || '') !== 'execute') return false;
		const params = rule.action_parameters;
		if (!params || !params.id) return false;
		const raw = params.raw && typeof params.raw === 'object' ? params.raw : {};
		const allowedRaw = new Set(['id', 'overrides', 'version']);
		return Object.keys(raw).every((key) => allowedRaw.has(key));
	}

	function isEditableWAFManagedActionParameters(params) {
		if (!params) return true;
		const raw = params.raw && typeof params.raw === 'object' ? params.raw : {};
		if (params.id || params.version) return false;
		if (Array.isArray(params.products) && params.products.length) return false;
		if (Array.isArray(params.phases) && params.phases.length) return false;
		if (params.ruleset && params.ruleset !== 'current') return false;
		const allowedRaw = new Set(['ruleset', 'rulesets', 'rules']);
		return Object.keys(raw).every((key) => allowedRaw.has(key));
	}

    function wafActionParametersSummary(rule) {
        const params = rule?.action_parameters || {};
        const parts = [];
        if (params.id) parts.push(params.id);
        if (params.ruleset === 'current') parts.push(t('oauth_waf_skip_current_ruleset_short'));
        if (Array.isArray(params.rulesets) && params.rulesets.length) {
            parts.push(t('oauth_waf_rulesets_summary', { n: params.rulesets.length }));
        }
        const ruleCount = countWAFActionParameterRules(params.rules);
        if (ruleCount > 0) parts.push(t('oauth_waf_rules_summary', { n: ruleCount }));
        if (Array.isArray(params.products) && params.products.length) {
            parts.push(t('oauth_waf_skip_products_summary', { n: params.products.length }));
        }
        if (Array.isArray(params.phases) && params.phases.length) {
            parts.push(t('oauth_waf_skip_phases_summary', { n: params.phases.length }));
        }
        if (params.overrides || hasWAFRawKey(params, 'overrides')) parts.push(t('oauth_waf_overrides_summary'));
        if (hasWAFRawKey(params, 'matched_data')) parts.push(t('oauth_waf_matched_data_summary'));
        if (hasWAFRawKey(params, 'response')) parts.push(t('oauth_waf_response_summary'));
        if (rule?.ratelimit) parts.push(t('oauth_waf_rate_limit_summary'));
        if (rule?.logging) parts.push(t('oauth_waf_logging_summary'));
        if (rule?.exposed_credential_check) parts.push(t('oauth_waf_credential_check_summary'));
        return parts.join(' · ');
    }

    function wafActionParametersDetail(params) {
        if (!params) return '';
        if (params.raw && typeof params.raw === 'object') return formatJSONPreview(params.raw);
        const compact = {};
        for (const key of ['id', 'ruleset', 'rulesets', 'rules', 'products', 'phases', 'overrides', 'version']) {
            const value = params[key];
            if (value == null) continue;
            if (Array.isArray(value) && !value.length) continue;
            if (typeof value === 'object' && !Array.isArray(value) && !Object.keys(value).length) continue;
            compact[key] = value;
        }
        return Object.keys(compact).length ? formatJSONPreview(compact) : '';
    }

    function wafRuleAuditJSON(rule) {
        if (!rule || typeof rule !== 'object') return '';
        const payload = {};
        const fields = [
            'id',
            'ref',
            'version',
            'action',
            'expression',
            'description',
            'enabled',
            'score_threshold',
            'last_updated',
            'action_parameters',
            'ratelimit',
            'logging',
            'exposed_credential_check',
        ];
        for (const field of fields) {
            const value = rule[field];
            if (value == null || value === '') continue;
            if (Array.isArray(value) && !value.length) continue;
            if (typeof value === 'object' && !Array.isArray(value) && !Object.keys(value).length) continue;
            payload[field] = value;
        }
        if (!payload.action_parameters && !payload.ratelimit && !payload.logging && !payload.exposed_credential_check && payload.score_threshold == null) {
            return '';
        }
        try { return JSON.stringify(payload, null, 2); } catch { return ''; }
    }

    function countWAFActionParameterRules(rules) {
        if (!rules || typeof rules !== 'object') return 0;
        return Object.values(rules).reduce((total, values) => total + (Array.isArray(values) ? values.length : 0), 0);
    }

    function hasWAFRawKey(params, key) {
        return !!params?.raw && typeof params.raw === 'object' && Object.prototype.hasOwnProperty.call(params.raw, key);
    }

    function formatJSONPreview(value, maxLength = 1600) {
        if (value == null || value === '') return '';
        let text = '';
        if (typeof value === 'string') {
            text = value;
        } else {
            try { text = JSON.stringify(value, null, 2); } catch { text = String(value); }
        }
        if (text.length <= maxLength) return text;
        return `${text.slice(0, maxLength)}\n${t('oauth_waf_json_truncated')}`;
    }

    function overviewMetricValue(metric) {
        if (!metric || !metric.available) return '—';
        return formatNumber(metric.value || 0);
    }

    function overviewMetricErrorLabel(error) {
        const key = `oauth_overview_error_${String(error || 'unavailable').replaceAll('-', '_')}`;
        const label = t(key);
        return label === key ? (error || t('oauth_overview_error_unavailable')) : label;
    }

    function analyticsRangeLabel(value) {
        switch (value) {
        case '7d':
            return t('oauth_analytics_range_7d');
        case '30d':
            return t('oauth_analytics_range_30d');
        case '24h':
        default:
            return t('oauth_analytics_range_24h');
        }
    }

    function findZoneSetting(id) {
        return state.oauth.zoneSettings.find((setting) => setting.id === id);
    }

    function isWritableZoneSetting(setting) {
        return !!setting?.editable && writableZoneSettings.has(setting.id);
    }

    function statusIndicatorState(indicator) {
        switch (indicator) {
        case 'none':
            return 'ok';
        case 'minor':
        case 'maintenance':
            return 'warn';
        case 'major':
        case 'critical':
            return 'error';
        default:
            return 'info';
        }
    }

    function statusIndicatorLabel(indicator, fallback) {
        const key = `oauth_cf_status_indicator_${String(indicator || '').replaceAll('-', '_')}`;
        const label = t(key);
        return label === key ? (fallback || indicator || '') : label;
    }

    function componentStatusLabel(status) {
        const key = `oauth_cf_status_component_${String(status || '').replaceAll('-', '_')}`;
        const label = t(key);
        return label === key ? (status || '') : label;
    }

    function incidentStatusLabel(status) {
        const key = `oauth_cf_status_incident_${String(status || '').replaceAll('-', '_')}`;
        const label = t(key);
        return label === key ? (status || '') : label;
    }

    function impactLabel(impact) {
        const key = `oauth_cf_status_impact_${String(impact || '').replaceAll('-', '_')}`;
        const label = t(key);
        return label === key ? (impact || '') : label;
    }

    function regionNameLabel(name) {
        switch (name) {
        case 'Africa':
            return t('oauth_cf_status_region_africa');
        case 'Asia':
            return t('oauth_cf_status_region_asia');
        case 'Europe':
            return t('oauth_cf_status_region_europe');
        case 'Latin America & the Caribbean':
            return t('oauth_cf_status_region_latin_america');
        case 'Middle East':
            return t('oauth_cf_status_region_middle_east');
        case 'North America':
            return t('oauth_cf_status_region_north_america');
        case 'Oceania':
            return t('oauth_cf_status_region_oceania');
        default:
            return name || '';
        }
    }

    function smallButton(text, className, onClick) {
        const button = document.createElement('button');
        button.type = 'button';
        button.className = className || 'btn btn--sm';
        const span = document.createElement('span');
        span.className = 'text';
        span.textContent = text;
        button.appendChild(span);
        if (onClick) {
            button.addEventListener('click', (event) => {
                event.stopPropagation();
                onClick(event);
            });
        }
        return button;
    }

    function iconButton(label, svg, onClick, extraClass = '') {
        const button = document.createElement('button');
        button.type = 'button';
        button.className = `icon-btn icon-btn--sm oauth-icon-action ${extraClass}`.trim();
        button.setAttribute('aria-label', label);
        button.title = label;
        button.innerHTML = svg;
        if (onClick) {
            button.addEventListener('click', (event) => {
                event.stopPropagation();
                onClick(event);
            });
        }
        return button;
    }

    function iconEditSVG() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20h9"></path><path d="m16.5 3.5 4 4L7 21H3v-4L16.5 3.5z"></path></svg>';
    }

    function iconTrashSVG() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 6h18"></path><path d="M8 6V4h8v2"></path><path d="M19 6l-1 14H6L5 6"></path><path d="M10 11v5"></path><path d="M14 11v5"></path></svg>';
    }

    function rowNode(title, meta, actions) {
        const row = document.createElement('div');
        row.className = 'oauth-row';
        const copy = document.createElement('div');
        const titleEl = document.createElement('div');
        titleEl.className = 'oauth-row-title';
        titleEl.textContent = title || '';
        const metaEl = document.createElement('div');
        metaEl.className = 'oauth-row-meta';
        metaEl.textContent = meta || '';
        copy.append(titleEl, metaEl);
        row.appendChild(copy);
        if (Array.isArray(actions) && actions.length) {
            const actionWrap = document.createElement('div');
            actionWrap.className = 'oauth-row-actions';
            for (const action of actions) actionWrap.appendChild(action);
            row.appendChild(actionWrap);
        }
        return row;
    }

    function renderSection(body, title, items, renderItem, emptyText) {
        const section = document.createElement('section');
        section.className = 'oauth-section';
        const heading = document.createElement('h4');
        heading.className = 'oauth-section-title';
        heading.textContent = title;
        section.appendChild(heading);
        if (!items.length) {
            section.appendChild(empty(emptyText));
        } else {
            for (const item of items) section.appendChild(renderItem(item));
        }
        body.appendChild(section);
        return 1;
    }

    function empty(text) {
        const node = document.createElement('div');
        node.className = 'oauth-empty';
        node.textContent = text;
        return node;
    }

    function canRead(feature) {
        if (!state.oauth.status?.logged_in) return true;
        return !!state.oauth.status?.capabilities?.[feature]?.read;
    }

    function canWrite(feature) {
        if (!state.oauth.status?.logged_in) return false;
        return !!state.oauth.status?.capabilities?.[feature]?.write;
    }

    function canSeeResource(definition) {
        if (definition.public) return true;
        if (!state.oauth.status?.logged_in) return true;
        if (definition.needsZone && !canRead('zones')) return false;
        if (definition.anyFeature) return definition.anyFeature.some(canRead);
        return canRead(definition.feature);
    }

    function ensureVisibleResource() {
        const current = resourceDefinitions.find((definition) => definition.id === state.oauth.resource);
        if (current && canSeeResource(current)) return;
        const first = resourceDefinitions.find(canSeeResource);
        state.oauth.resource = first?.id || 'zones';
    }

    function updateOAuthResourceTabs() {
        $$('.oauth-resource-tab').forEach((btn) => {
            const definition = resourceDefinitions.find((item) => item.id === btn.dataset.oauthResource);
            const visible = !definition || canSeeResource(definition);
            btn.hidden = !visible;
            btn.setAttribute('aria-pressed', String(btn.dataset.oauthResource === state.oauth.resource));
        });
    }

    function formatDate(value) {
        if (!value) return '';
        const date = new Date(value);
        if (Number.isNaN(date.getTime())) return String(value);
        return date.toLocaleString();
    }

    function formatCPUTime(value) {
        const us = Number(value || 0);
        if (!Number.isFinite(us) || us <= 0) return '0 ms';
        if (us < 1000) return `${Math.round(us)} us`;
        const ms = us / 1000;
        if (ms < 1000) return `${Math.round(ms * 10) / 10} ms`;
        const seconds = ms / 1000;
        return `${Math.round(seconds * 100) / 100} s`;
    }

    function workerStatusLabel(status) {
        switch (status) {
        case 'success':
            return t('oauth_worker_status_success');
        case 'scriptThrewException':
            return t('oauth_worker_status_script_exception');
        case 'exceededCpu':
            return t('oauth_worker_status_exceeded_cpu');
        case 'exceededMemory':
            return t('oauth_worker_status_exceeded_memory');
        case 'clientDisconnected':
            return t('oauth_worker_status_client_disconnected');
        case 'canceled':
            return t('oauth_worker_status_canceled');
        case 'responseStreamDisconnected':
            return t('oauth_worker_status_response_stream_disconnected');
        default:
            return status || '';
        }
    }

    function formatDateRange(since, until) {
        const left = formatDate(since);
        const right = formatDate(until);
        return left && right ? `${left} - ${right}` : (left || right || '');
    }

    function formatNumber(value) {
        const n = Number(value || 0);
        if (!Number.isFinite(n)) return '0';
        return new Intl.NumberFormat().format(n);
    }

    function formatPercent(value) {
        const n = Number(value || 0);
        if (!Number.isFinite(n)) return '0%';
        return `${Math.round(n * 10) / 10}%`;
    }

    function formatBytes(value) {
        const bytes = Number(value || 0);
        if (!bytes) return '0 B';
        const units = ['B', 'KB', 'MB', 'GB', 'TB'];
        let size = bytes;
        let unit = 0;
        while (size >= 1024 && unit < units.length - 1) {
            size /= 1024;
            unit += 1;
        }
        return `${size.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
    }

    function cacheHitRate(point) {
        const requests = Number(point?.requests || 0);
        if (requests <= 0) return 0;
        return (Number(point?.cached_requests || 0) / requests) * 100;
    }

    function wireOAuth() {
        $('oauth-login')?.addEventListener('click', startOAuthLogin);
        $('oauth-logout')?.addEventListener('click', logoutOAuth);
        $('oauth-worker-script-copy')?.addEventListener('click', async () => {
            if (!state.oauth.workerScriptContent && !state.oauth.workerScriptLoading) await loadOAuthWorkerScript();
            copyOAuthText(state.oauth.workerScriptContent || '');
        });
        $('oauth-worker-script-open')?.addEventListener('click', openOAuthWorkerScript);
        $('oauth-refresh')?.addEventListener('click', async () => {
            await fetchOAuthStatus();
            if (state.oauth.resource === 'status') {
                await loadOAuthCurrentResource();
                renderOAuthResource();
            } else {
                await loadOAuthOverview();
            }
        });
        $$('.oauth-resource-tab').forEach((btn) => {
            btn.addEventListener('click', async () => {
                await switchOAuthResource(btn.dataset.oauthResource || 'zones');
            });
        });

        const params = new URLSearchParams(window.location.search);
        if (params.get('oauth') === 'success') {
            toast.ok(t('oauth_login_success'));
            history.replaceState(null, '', window.location.pathname);
        } else if (params.get('oauth') === 'error') {
            toast.err(params.get('message') || t('oauth_login_failed'));
            history.replaceState(null, '', window.location.pathname);
        }
    }

    const ns = window.cfui;
    ns.fetchOAuthStatus = fetchOAuthStatus;
    ns.loadOAuthOverview = loadOAuthOverview;
    ns.closeOAuthLiveStreams = closeOAuthLiveStreams;
    ns.wireOAuth = wireOAuth;
})();
