/* =========================================================================
   CloudFlared UI — Bootstrap
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, loadLanguage, initTheme, updateMetricsVisibility, addLog,
            fetchVersion, fetchConfig, fetchFeatures, fetchStatus, syncWorkspaceFromRoute,
            currentWorkspace,
            fetchTunnelManagerSettings, maybeLoadTunnelManagerZones,
            canLoadTunnelManagerZones, loadTunnelManagerConfig,
            fetchMCPStatus, refreshDDNS, fetchS3Settings,
            fetchOAuthStatus, loadOAuthOverview,
            wireUI, wireTunnel, wireLogs, wireServices, wireS3, wireOAuth,
            disconnectLogStream, closeOAuthLiveStreams, toast } = window.cfui;

    let statusTimer = null;

    function startStatusPolling() {
        if (statusTimer) return;
        statusTimer = setInterval(fetchStatus, 2000);
    }

    function stopStatusPolling() {
        if (!statusTimer) return;
        clearInterval(statusTimer);
        statusTimer = null;
    }

    async function loadLocalWorkspaceData() {
        closeOAuthLiveStreams?.();
        await fetchConfig();
        await fetchStatus();
        startStatusPolling();
        if (state.features.tunnel_manager) {
            await fetchTunnelManagerSettings();
            await maybeLoadTunnelManagerZones(true);
            if (canLoadTunnelManagerZones()) await loadTunnelManagerConfig(true);
        }
        if (state.features.mcp) await fetchMCPStatus();
        if (state.features.ddns) await refreshDDNS();
        if (state.features.s3_webdav) await fetchS3Settings();
    }

    async function loadCloudflareWorkspaceData() {
        stopStatusPolling();
        disconnectLogStream(true);
        if (!state.features.oauth_enabled) return;
        await fetchOAuthStatus?.();
        await loadOAuthOverview?.();
    }

    async function loadWorkspaceData(workspace) {
        if (workspace === 'cloudflare') {
            await loadCloudflareWorkspaceData();
            return;
        }
        await loadLocalWorkspaceData();
    }

    async function init() {
        initTheme();
        wireUI();
        wireTunnel();
        wireLogs();
        wireServices();
        wireS3();
        wireOAuth?.();
        await loadLanguage(state.currentLang);
        updateMetricsVisibility();
        addLog({ key: 'system_ready' }, 'system');
        await fetchVersion();
        await fetchFeatures();
        syncWorkspaceFromRoute?.({ replaceDefault: true });
        await loadWorkspaceData(currentWorkspace?.() || 'local');
        document.addEventListener('workspacechange', (e) => {
            loadWorkspaceData(e.detail?.workspace || 'local').catch((err) => {
                console.error('workspace load failed', err);
                toast.err(err.message);
            });
        });
        setInterval(() => { if (!$('panel-ddns').hidden) fetchDDNSStatus(); }, 10000);
    }

    function fetchDDNSStatus() { return window.cfui.fetchDDNSStatus(); }

    window.addEventListener('beforeunload', () => disconnectLogStream(true));

    init().catch((err) => {
        console.error('init failed', err);
        toast.err('Initialization failed: ' + err.message);
    });
})();
