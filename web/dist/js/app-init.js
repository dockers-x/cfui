/* =========================================================================
   CloudFlared UI — Bootstrap
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, loadLanguage, initTheme, updateMetricsVisibility, addLog,
            fetchVersion, fetchConfig, fetchFeatures, fetchStatus, restoreLastTab,
            fetchTunnelManagerSettings, maybeLoadTunnelManagerZones,
            canLoadTunnelManagerZones, loadTunnelManagerConfig,
            fetchMCPStatus, refreshDDNS,
            wireUI, wireTunnel, wireLogs, wireServices,
            disconnectLogStream, toast } = window.cfui;

    async function init() {
        initTheme();
        wireUI();
        wireTunnel();
        wireLogs();
        wireServices();
        await loadLanguage(state.currentLang);
        updateMetricsVisibility();
        addLog({ key: 'system_ready' }, 'system');
        await fetchVersion();
        await fetchConfig();
        await fetchFeatures();
        restoreLastTab();

        if (state.features.tunnel_manager) {
            await fetchTunnelManagerSettings();
            await maybeLoadTunnelManagerZones(true);
            if (canLoadTunnelManagerZones()) await loadTunnelManagerConfig(true);
        }
        if (state.features.mcp) await fetchMCPStatus();
        if (state.features.ddns) await refreshDDNS();
        await fetchStatus();

        setInterval(fetchStatus, 2000);
        setInterval(() => { if (!$('panel-ddns').hidden) fetchDDNSStatus(); }, 10000);
    }

    function fetchDDNSStatus() { return window.cfui.fetchDDNSStatus(); }

    window.addEventListener('beforeunload', () => disconnectLogStream(true));

    init().catch((err) => {
        console.error('init failed', err);
        toast.err('Initialization failed: ' + err.message);
    });
})();
