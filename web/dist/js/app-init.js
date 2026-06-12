/* =========================================================================
   CloudFlared UI — Bootstrap
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, loadLanguage, initTheme, updateMetricsVisibility, addLog,
            fetchVersion, fetchConfig, fetchFeatures, fetchStatus, restoreLastTab,
            fetchTunnelManagerSettings, maybeLoadTunnelManagerZones,
            canLoadTunnelManagerZones, loadTunnelManagerConfig,
            fetchMCPStatus, refreshDDNS, fetchS3Settings,
            wireUI, wireTunnel, wireLogs, wireServices, wireS3,
            disconnectLogStream, toast } = window.cfui;

    async function init() {
        initTheme();
        wireUI();
        wireTunnel();
        wireLogs();
        wireServices();
        wireS3();
        await loadLanguage(state.currentLang);
        updateMetricsVisibility();
        addLog({ key: 'system_ready' }, 'system');
        await fetchVersion();
        await fetchConfig();
        await fetchFeatures();
        await fetchStatus();
        setInterval(fetchStatus, 2000);

        restoreLastTab();

        if (state.features.tunnel_manager) {
            await fetchTunnelManagerSettings();
            await maybeLoadTunnelManagerZones(true);
            if (canLoadTunnelManagerZones()) await loadTunnelManagerConfig(true);
        }
        if (state.features.mcp) await fetchMCPStatus();
        if (state.features.ddns) await refreshDDNS();
        if (state.features.s3_webdav) await fetchS3Settings();
        setInterval(() => { if (!$('panel-ddns').hidden) fetchDDNSStatus(); }, 10000);
    }

    function fetchDDNSStatus() { return window.cfui.fetchDDNSStatus(); }

    window.addEventListener('beforeunload', () => disconnectLogStream(true));

    init().catch((err) => {
        console.error('init failed', err);
        toast.err('Initialization failed: ' + err.message);
    });
})();
