const API_BASE = '/api';

const state = {
    isRunning: false,
    config: {},
    status: 'unknown',
    currentLang: localStorage.getItem('lang') || 'zh',
    currentTheme: localStorage.getItem('theme') || 'light',
    translations: {},
    logs: [], // Store log entries for re-rendering when language changes
    logStream: null, // EventSource for log streaming
    isStreamConnected: false,
    features: { tunnel_manager: false, ddns: false, mcp: false },
    tunnelManager: {
        settings: {},
        config: null,
        zones: [],
        zonesLoaded: false
    },
    mcp: {
        status: null,
        tokens: []
    },
    ddns: {
        config: null,
        status: null,
        zones: [],
        zonesLoaded: false
    }
};

const elements = {
    statusBadge: document.getElementById('status-badge'),
    statusDot: document.querySelector('.status-dot'),
    statusText: document.querySelector('.status-text'),
    versionInfo: document.getElementById('version-info'),
    tokenInput: document.getElementById('token-input'),
    customTagInput: document.getElementById('custom-version-input'),
    softwareNameInput: document.getElementById('software-name-input'),
    toggleVisibilityBtn: document.getElementById('toggle-visibility'),
    autoStartToggle: document.getElementById('autostart-toggle'),
    autoRestartToggle: document.getElementById('autorestart-toggle'),
    actionBtn: document.getElementById('action-btn'),
    logsContainer: document.getElementById('logs-container'),
    clearLogsBtn: document.getElementById('clear-logs'),
    toggleStreamBtn: document.getElementById('toggle-stream'),
    langSelect: document.getElementById('lang-select'),
    themeToggle: document.getElementById('theme-toggle'),
    // Advanced config elements
    protocolSelect: document.getElementById('protocol-select'),
    gracePeriodInput: document.getElementById('grace-period-input'),
    regionSelect: document.getElementById('region-select'),
    retriesInput: document.getElementById('retries-input'),
    metricsEnableToggle: document.getElementById('metrics-enable-toggle'),
    metricsPortField: document.getElementById('metrics-port-field'),
    metricsPortInput: document.getElementById('metrics-port-input'),
    edgeBindAddressInput: document.getElementById('edge-bind-address-input'),
    noTLSVerifyToggle: document.getElementById('no-tls-verify-toggle'),
    tokenHelpText: document.getElementById('token-help-text'),
    tokenHelpLink: document.getElementById('token-help-link'),
    managerStatus: document.getElementById('manager-status'),
    managerSettingsSection: document.getElementById('manager-settings-section'),
    managerSettingsToggle: document.getElementById('manager-settings-toggle'),
    managerEnableToggle: document.getElementById('manager-enable-toggle'),
    managerAccountId: document.getElementById('manager-account-id'),
    managerTunnelId: document.getElementById('manager-tunnel-id'),
    managerAuthMode: document.getElementById('manager-auth-mode'),
    managerTokenField: document.querySelector('.manager-token-field'),
    managerKeyFields: document.querySelector('.manager-key-fields'),
    managerAPIToken: document.getElementById('manager-api-token'),
    managerAPIEmail: document.getElementById('manager-api-email'),
    managerAPIKey: document.getElementById('manager-api-key'),
    managerAPIHelp: document.getElementById('manager-api-help'),
    managerAPIHelpDns: document.getElementById('manager-api-help-dns'),
    managerAPIHelpPanel: document.getElementById('manager-api-help-panel'),
    managerTokenState: document.getElementById('manager-token-state'),
    managerKeyState: document.getElementById('manager-key-state'),
    managerAPITokenHelp: document.getElementById('manager-api-token-help'),
    managerAPIEmailHelp: document.getElementById('manager-api-email-help'),
    managerAPIKeyHelp: document.getElementById('manager-api-key-help'),
    managerVerifyPermissions: document.getElementById('manager-verify-permissions'),
    managerVerifyResult: document.getElementById('manager-verify-result'),
    managerSaveSettings: document.getElementById('manager-save-settings'),
    managerLoadConfig: document.getElementById('manager-load-config'),
    managerConfigPanel: document.getElementById('manager-config-panel'),
    managerConfigMeta: document.getElementById('manager-config-meta'),
    managerRulesList: document.getElementById('manager-rules-list'),
    managerEntryForm: document.getElementById('manager-entry-form'),
    managerEntryIndex: document.getElementById('manager-entry-index'),
    managerEntrySubdomain: document.getElementById('manager-entry-subdomain'),
    managerEntryDomain: document.getElementById('manager-entry-domain'),
    managerEntryDomainSelect: document.getElementById('manager-entry-domain-select'),
    managerRefreshZones: document.getElementById('manager-refresh-zones'),
    managerEntryPath: document.getElementById('manager-entry-path'),
    managerEntryServiceType: document.getElementById('manager-entry-service-type'),
    managerEntryService: document.getElementById('manager-entry-service'),
    managerEntryHTTPHostHeader: document.getElementById('manager-entry-http-host-header'),
    managerEntryOriginServerName: document.getElementById('manager-entry-origin-server-name'),
    managerEntryNoTLS: document.getElementById('manager-entry-no-tls'),
    managerEntrySubmit: document.getElementById('manager-entry-submit'),
    managerEntryCancel: document.getElementById('manager-entry-cancel'),
    localTab: document.getElementById('local-tab'),
    managerTab: document.getElementById('manager-tab'),
    mcpTab: document.getElementById('mcp-tab'),
    localPanel: document.getElementById('local-panel'),
    managerPanel: document.getElementById('manager-panel'),
    mcpPanel: document.getElementById('mcp-panel'),
    mcpStatus: document.getElementById('mcp-status'),
    mcpHelpToggle: document.getElementById('mcp-help-toggle'),
    mcpHelpPanel: document.getElementById('mcp-help-panel'),
    mcpEndpoint: document.getElementById('mcp-endpoint'),
    mcpTokenForm: document.getElementById('mcp-token-form'),
    mcpTokenName: document.getElementById('mcp-token-name'),
    mcpTokenInput: document.getElementById('mcp-token-input'),
    mcpTokenCreate: document.getElementById('mcp-token-create'),
    mcpCreatedToken: document.getElementById('mcp-created-token'),
    mcpCreatedValue: document.getElementById('mcp-created-value'),
    mcpTokenList: document.getElementById('mcp-token-list'),
    ddnsTab: document.getElementById('ddns-tab'),
    featuresTab: document.getElementById('features-tab'),
    featuresPanel: document.getElementById('features-panel'),
    featuresTitle: document.getElementById('features-title'),
    featuresSubtitle: document.getElementById('features-subtitle'),
    featureManagerToggle: document.getElementById('feature-manager-toggle'),
    featureManagerName: document.getElementById('feature-manager-name'),
    featureManagerDesc: document.getElementById('feature-manager-desc'),
    featureDdnsToggle: document.getElementById('feature-ddns-toggle'),
    featureDdnsName: document.getElementById('feature-ddns-name'),
    featureDdnsDesc: document.getElementById('feature-ddns-desc'),
    featureMcpToggle: document.getElementById('feature-mcp-toggle'),
    featureMcpName: document.getElementById('feature-mcp-name'),
    featureMcpDesc: document.getElementById('feature-mcp-desc'),
    ddnsPanel: document.getElementById('ddns-panel'),
    ddnsStatus: document.getElementById('ddns-status'),
    ddnsNoCreds: document.getElementById('ddns-no-creds'),
    ddnsMain: document.getElementById('ddns-main'),
    ddnsIPBanner: document.getElementById('ddns-ip-banner'),
    ddnsIPv4Value: document.getElementById('ddns-ipv4-value'),
    ddnsIPv6Value: document.getElementById('ddns-ipv6-value'),
    ddnsLastCheck: document.getElementById('ddns-last-check'),
    ddnsSyncNow: document.getElementById('ddns-sync-now'),
    ddnsIPv4Textarea: document.getElementById('ddns-ipv4-textarea'),
    ddnsIPv6Textarea: document.getElementById('ddns-ipv6-textarea'),
    ddnsInterval: document.getElementById('ddns-interval'),
    ddnsMaxRetries: document.getElementById('ddns-max-retries'),
    ddnsOnlyOnChange: document.getElementById('ddns-only-on-change'),
    ddnsSaveSettings: document.getElementById('ddns-save-settings'),
    ddnsRecordsList: document.getElementById('ddns-records-list'),
    ddnsAddRecordBtn: document.getElementById('ddns-add-record-btn'),
    ddnsRecordForm: document.getElementById('ddns-record-form'),
    ddnsRecordSubdomain: document.getElementById('ddns-record-subdomain'),
    ddnsRecordZoneSelect: document.getElementById('ddns-record-zone-select'),
    ddnsRecordIPv4: document.getElementById('ddns-record-ipv4'),
    ddnsRecordIPv6: document.getElementById('ddns-record-ipv6'),
    ddnsRecordIPv4ValueGroup: document.getElementById('ddns-record-ipv4-value-group'),
    ddnsRecordIPv6ValueGroup: document.getElementById('ddns-record-ipv6-value-group'),
    ddnsRecordIPv4Value: document.getElementById('ddns-record-ipv4-value'),
    ddnsRecordIPv6Value: document.getElementById('ddns-record-ipv6-value'),
    ddnsRecordTTLSelect: document.getElementById('ddns-record-ttl-select'),
    ddnsRecordProxied: document.getElementById('ddns-record-proxied'),
    ddnsRecordSubmit: document.getElementById('ddns-record-submit'),
    ddnsRecordCancel: document.getElementById('ddns-record-cancel'),
    ddnsSyncLogList: document.getElementById('ddns-sync-log-list')
};

// Theme Management
function initTheme() {
    const savedTheme = localStorage.getItem('theme') || 'light';
    state.currentTheme = savedTheme;
    document.documentElement.setAttribute('data-theme', savedTheme);
}

function toggleTheme() {
    const newTheme = state.currentTheme === 'light' ? 'dark' : 'light';
    state.currentTheme = newTheme;
    document.documentElement.setAttribute('data-theme', newTheme);
    localStorage.setItem('theme', newTheme);
}

// Init
async function init() {
    initTheme();
    await loadLanguage(state.currentLang);
    elements.langSelect.value = state.currentLang;
    updateUIText();

    // Add initial system ready log
    elements.logsContainer.innerHTML = '';
    addLog(t('system_ready'), 'system');

    await fetchVersion();
    await fetchConfig();
    await refreshFeatures();
    await fetchTunnelManagerSettings();
    await fetchMCPStatus();
    await maybeLoadTunnelManagerZones(true);
    if (state.tunnelManager.settings?.enabled && state.tunnelManager.settings?.account_id && state.tunnelManager.settings?.tunnel_id) {
        await loadTunnelManagerConfig();
    }
    await fetchStatus();
    syncReleasedRows();
    setInterval(fetchStatus, 2000);

    // DO NOT auto-connect log stream - user must manually enable it
    // connectLogStream();
}

// Event Listeners
elements.themeToggle.addEventListener('click', toggleTheme);

function toggleVisibility(input, btn) {
    const type = input.type === 'password' ? 'text' : 'password';
    input.type = type;
    const visible = type === 'text';
    btn.setAttribute('aria-pressed', String(visible));
    btn.setAttribute('aria-label', visible ? 'Hide' : 'Show');
}
elements.toggleVisibilityBtn.addEventListener('click', () => toggleVisibility(elements.tokenInput, elements.toggleVisibilityBtn));

elements.tokenInput.addEventListener('change', saveAllConfig);
elements.customTagInput?.addEventListener('change', saveAllConfig);
elements.softwareNameInput?.addEventListener('change', saveAllConfig);
elements.autoStartToggle.addEventListener('change', saveAllConfig);
elements.autoRestartToggle.addEventListener('change', saveAllConfig);
elements.protocolSelect?.addEventListener('change', saveAllConfig);
elements.gracePeriodInput?.addEventListener('change', saveAllConfig);
elements.regionSelect?.addEventListener('change', saveAllConfig);
elements.retriesInput?.addEventListener('change', saveAllConfig);
elements.metricsEnableToggle?.addEventListener('change', () => {
    updateMetricsVisibility();
    saveAllConfig();
});
elements.metricsPortInput?.addEventListener('change', saveAllConfig);
elements.edgeBindAddressInput?.addEventListener('change', saveAllConfig);
elements.noTLSVerifyToggle?.addEventListener('change', saveAllConfig);

elements.localTab?.addEventListener('click', () => activateTab('local'));
elements.managerTab?.addEventListener('click', async () => {
    activateTab('manager');
    await maybeLoadTunnelManagerZones(true);
});
elements.ddnsTab?.addEventListener('click', async () => {
    activateTab('ddns');
    await refreshDDNS();
});
elements.mcpTab?.addEventListener('click', async () => {
    activateTab('mcp');
    await fetchMCPStatus();
});
elements.featuresTab?.addEventListener('click', async () => {
    activateTab('features');
    await refreshFeatures();
});
elements.featureManagerToggle?.addEventListener('change', (e) => saveFeature('tunnel_manager', e.target.checked));
elements.featureDdnsToggle?.addEventListener('change', (e) => saveFeature('ddns', e.target.checked));
elements.featureMcpToggle?.addEventListener('change', (e) => saveFeature('mcp', e.target.checked));
elements.mcpHelpToggle?.addEventListener('click', () => toggleMCPHelp());
elements.mcpTokenForm?.addEventListener('submit', createMCPToken);
elements.managerAuthMode?.addEventListener('change', updateManagerAuthMode);
elements.managerAPIHelp?.addEventListener('click', (event) => {
    event.stopPropagation();
    toggleAPIHelp();
});
document.addEventListener('click', (event) => {
    if (!elements.managerAPIHelpPanel || elements.managerAPIHelpPanel.hidden) return;
    if (event.target === elements.managerAPIHelp || elements.managerAPIHelpPanel.contains(event.target)) return;
    toggleAPIHelp(false);
});
elements.managerVerifyPermissions?.addEventListener('click', verifyTokenPermissions);
elements.managerRefreshZones?.addEventListener('click', () => loadTunnelManagerZones(false));
elements.managerEntryDomainSelect?.addEventListener('change', () => {
    updateDomainInputMode({ clearSelectedZone: true });
});
elements.managerEntryServiceType?.addEventListener('change', updateServicePlaceholder);
elements.managerSaveSettings?.addEventListener('click', () => saveTunnelManagerSettings(false));
elements.managerLoadConfig?.addEventListener('click', async () => {
    await saveTunnelManagerSettings(true);
    await loadTunnelManagerConfig();
});
elements.managerTunnelId?.addEventListener('change', async () => {
    if (!elements.managerEnableToggle?.checked) return;
    await saveTunnelManagerSettings(true);
    await loadTunnelManagerConfig();
});
elements.managerEntryForm?.addEventListener('submit', submitTunnelManagerEntry);
elements.managerEntryCancel?.addEventListener('click', resetTunnelEntryForm);

// API token/key visibility toggles
const managerAPITokenToggle = document.getElementById('manager-api-token-toggle');
const managerAPIKeyToggle = document.getElementById('manager-api-key-toggle');
managerAPITokenToggle?.addEventListener('click', () => toggleVisibility(elements.managerAPIToken, managerAPITokenToggle));
managerAPIKeyToggle?.addEventListener('click', () => toggleVisibility(elements.managerAPIKey, managerAPIKeyToggle));

elements.ddnsSyncNow?.addEventListener('click', ddnsSyncNow);
elements.ddnsSaveSettings?.addEventListener('click', ddnsSaveSettings);
elements.ddnsAddRecordBtn?.addEventListener('click', () => {
    resetDDNSRecordForm();
    elements.ddnsRecordForm.hidden = false;
    loadDDNSZones();
});
elements.ddnsRecordForm?.addEventListener('submit', ddnsSubmitRecord);
elements.ddnsRecordCancel?.addEventListener('click', resetDDNSRecordForm);
elements.ddnsRecordIPv4?.addEventListener('change', syncDDNSRecordValueFields);
elements.ddnsRecordIPv6?.addEventListener('change', syncDDNSRecordValueFields);
document.getElementById('ddns-tab-sources')?.addEventListener('click', () => switchDDNSSubTab('sources'));
document.getElementById('ddns-tab-auto')?.addEventListener('click', () => switchDDNSSubTab('auto'));
syncDDNSRecordValueFields();

elements.actionBtn.addEventListener('click', async () => {
    const action = state.isRunning ? 'stop' : 'start';
    if (action === 'start' && !elements.tokenInput.value) {
        addLog(t('error_token_required'), 'error');
        return;
    }

    elements.actionBtn.disabled = true;
    try {
        const res = await fetch(`${API_BASE}/control`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ action })
        });

        if (!res.ok) {
            const data = await res.json();
            throw new Error(data.error || 'Failed to perform action');
        }

        addLog(`${t('command_sent')}: ${action}`, 'system');
        setTimeout(fetchStatus, 500);
    } catch (err) {
        // For stop action, the tunnel might shut down before responding
        // Check status anyway to see if the operation succeeded
        if (action === 'stop') {
            addLog(`${t('command_sent')}: ${action}`, 'system');
            setTimeout(fetchStatus, 500);
        } else {
            addLog(err.message, 'error');
        }
    } finally {
        elements.actionBtn.disabled = false;
    }
});

elements.clearLogsBtn.addEventListener('click', () => {
    elements.logsContainer.innerHTML = '';
    state.logs = []; // Clear the stored logs array
});

elements.toggleStreamBtn.addEventListener('click', () => {
    if (state.isStreamConnected) {
        disconnectLogStream();
    } else {
        connectLogStream();
    }
});

elements.langSelect.addEventListener('change', async (e) => {
    const newLang = e.target.value;
    await loadLanguage(newLang);
    state.currentLang = newLang;
    localStorage.setItem('lang', newLang);
    updateUIText();
});

function activateTab(tab) {
    const managerActive = tab === 'manager';
    const mcpActive = tab === 'mcp';
    const ddnsActive = tab === 'ddns';
    const featuresActive = tab === 'features';
    const localActive = !managerActive && !mcpActive && !ddnsActive && !featuresActive;
    elements.localTab?.classList.toggle('active', localActive);
    elements.managerTab?.classList.toggle('active', managerActive);
    elements.ddnsTab?.classList.toggle('active', ddnsActive);
    elements.mcpTab?.classList.toggle('active', mcpActive);
    elements.featuresTab?.classList.toggle('active', featuresActive);
    elements.localTab?.setAttribute('aria-selected', String(localActive));
    elements.managerTab?.setAttribute('aria-selected', String(managerActive));
    elements.ddnsTab?.setAttribute('aria-selected', String(ddnsActive));
    elements.mcpTab?.setAttribute('aria-selected', String(mcpActive));
    elements.featuresTab?.setAttribute('aria-selected', String(featuresActive));

    if (elements.localPanel) {
        elements.localPanel.hidden = !localActive;
        elements.localPanel.classList.toggle('active', localActive);
    }
    if (elements.managerPanel) {
        elements.managerPanel.hidden = !managerActive;
        elements.managerPanel.classList.toggle('active', managerActive);
    }
    if (elements.ddnsPanel) {
        elements.ddnsPanel.hidden = !ddnsActive;
        elements.ddnsPanel.classList.toggle('active', ddnsActive);
    }
    if (elements.mcpPanel) {
        elements.mcpPanel.hidden = !mcpActive;
        elements.mcpPanel.classList.toggle('active', mcpActive);
    }
    if (elements.featuresPanel) {
        elements.featuresPanel.hidden = !featuresActive;
        elements.featuresPanel.classList.toggle('active', featuresActive);
    }
}

// API Calls
async function fetchVersion() {
    try {
        const res = await fetch(`${API_BASE}/version`);
        const data = await res.json();

        // Display version in the header
        if (elements.versionInfo) {
            let version = data.version;

            // Extract main version (e.g., v0.2.2 from v0.2.2-1-g6e29258-dirty)
            // Match pattern: v0.2.2 or 0.2.2 (before any dash)
            const match = version.match(/^(v?\d+\.\d+\.\d+)/);
            const displayVersion = match ? match[1] : version;

            // Ensure it has 'v' prefix
            const versionText = displayVersion.startsWith('v') ? displayVersion : `v${displayVersion}`;

            elements.versionInfo.textContent = versionText;
            elements.versionInfo.title = `Version: ${data.version}\nBuild Time: ${data.build_time}\nGit Commit: ${data.git_commit}`;
        }
    } catch (err) {
        // Silently fail - version info is not critical
        if (elements.versionInfo) {
            elements.versionInfo.textContent = '';
        }
    }
}

async function fetchConfig() {
    try {
        const res = await fetch(`${API_BASE}/config`);
        const data = await res.json();
        state.config = data;

        elements.tokenInput.value = data.token || '';
        elements.customTagInput.value = data.custom_tag || '';
        elements.softwareNameInput.value = data.software_name || 'cfui';
        elements.autoStartToggle.checked = data.auto_start || false;
        elements.autoRestartToggle.checked = data.auto_restart !== undefined ? data.auto_restart : true;

        if (elements.protocolSelect) elements.protocolSelect.value = data.protocol || 'auto';
        if (elements.gracePeriodInput) elements.gracePeriodInput.value = data.grace_period || '30s';
        if (elements.regionSelect) elements.regionSelect.value = data.region || '';
        if (elements.retriesInput) elements.retriesInput.value = data.retries || 5;
        if (elements.metricsEnableToggle) elements.metricsEnableToggle.checked = data.metrics_enable || false;
        if (elements.metricsPortInput) elements.metricsPortInput.value = data.metrics_port || 60123;
        if (elements.edgeBindAddressInput) elements.edgeBindAddressInput.value = data.edge_bind_address || '';
        if (elements.noTLSVerifyToggle) elements.noTLSVerifyToggle.checked = data.no_tls_verify || false;
        updateMetricsVisibility();
    } catch (err) {
        addLog('Failed to load config', 'error');
    }
}

async function saveAllConfig() {
    const config = {
        token: elements.tokenInput.value,
        custom_tag: elements.customTagInput?.value || '',
        software_name: elements.softwareNameInput?.value || 'cfui',
        auto_start: elements.autoStartToggle.checked,
        auto_restart: elements.autoRestartToggle.checked,
        protocol: elements.protocolSelect?.value || 'auto',
        grace_period: elements.gracePeriodInput?.value || '30s',
        region: elements.regionSelect?.value || '',
        retries: parseInt(elements.retriesInput?.value || '5'),
        metrics_enable: elements.metricsEnableToggle?.checked || false,
        metrics_port: parseInt(elements.metricsPortInput?.value || '60123'),
        edge_bind_address: elements.edgeBindAddressInput?.value || '',
        no_tls_verify: elements.noTLSVerifyToggle?.checked || false,
        tunnel_management: state.config.tunnel_management || {}
    };

    try {
        await fetch(`${API_BASE}/config`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(config)
        });
        addLog(t('config_saved'), 'system');
    } catch (err) {
        addLog('Failed to save config', 'error');
    }
}

async function refreshFeatures() {
    try {
        const res = await fetch(`${API_BASE}/features`);
        if (!res.ok) return;
        const data = await res.json();
        state.features = data;
        applyFeatureVisibility(data);
        if (elements.featureManagerToggle) elements.featureManagerToggle.checked = !!data.tunnel_manager;
        if (elements.featureDdnsToggle) {
            elements.featureDdnsToggle.checked = !!data.ddns;
            elements.featureDdnsToggle.disabled = !data.tunnel_manager;
        }
        if (elements.featureMcpToggle) elements.featureMcpToggle.checked = !!data.mcp;
    } catch (err) {
        console.error('fetch features failed', err);
    }
}

function applyFeatureVisibility(data) {
    if (elements.managerTab) elements.managerTab.hidden = !data.tunnel_manager;
    if (elements.ddnsTab) elements.ddnsTab.hidden = !data.ddns;
    if (elements.mcpTab) elements.mcpTab.hidden = !data.mcp;
}

async function saveFeature(key, value) {
    if (key === 'ddns' && value && !state.features?.tunnel_manager) {
        if (elements.featureDdnsToggle) elements.featureDdnsToggle.checked = false;
        addLog(t('feature_ddns_requires_manager'), 'error');
        return;
    }
    const body = {};
    body[key] = value;
    try {
        const res = await fetch(`${API_BASE}/features`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
        if (!res.ok) {
            const err = await res.json().catch(() => ({}));
            addLog(err.error || 'feature update failed', 'error');
            await refreshFeatures();
            return;
        }
        const data = await res.json();
        state.features = data;
        applyFeatureVisibility(data);
        if (elements.featureManagerToggle) elements.featureManagerToggle.checked = !!data.tunnel_manager;
        if (elements.featureDdnsToggle) {
            elements.featureDdnsToggle.checked = !!data.ddns;
            elements.featureDdnsToggle.disabled = !data.tunnel_manager;
        }
        if (elements.featureMcpToggle) elements.featureMcpToggle.checked = !!data.mcp;
    } catch (err) {
        console.error('save feature failed', err);
    }
}

async function fetchMCPStatus() {
    if (!elements.mcpStatus) return;
    try {
        const res = await fetch(`${API_BASE}/mcp/status`);
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.mcp.status = data;
        state.mcp.tokens = data.tokens || [];
        renderMCPStatus(data);
        renderMCPTokens();
    } catch (err) {
        setMCPStatus(err.message, 'error');
        addLog(`MCP status failed: ${err.message}`, 'error');
    }
}

async function createMCPToken(event) {
    event.preventDefault();
    const payload = {
        name: elements.mcpTokenName.value.trim(),
        token: elements.mcpTokenInput.value.trim()
    };
    try {
        elements.mcpTokenCreate.disabled = true;
        const res = await fetch(`${API_BASE}/mcp/tokens`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        elements.mcpTokenName.value = '';
        elements.mcpTokenInput.value = '';
        showCreatedMCPToken(data.token);
        await fetchMCPStatus();
        addLog(t('mcp_token_created'), 'system');
    } catch (err) {
        setMCPStatus(err.message, 'error');
        addLog(`MCP token create failed: ${err.message}`, 'error');
    } finally {
        elements.mcpTokenCreate.disabled = false;
    }
}

async function deleteMCPToken(id) {
    try {
        const res = await fetch(`${API_BASE}/mcp/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' });
        if (!res.ok) throw new Error(await responseError(res));
        await fetchMCPStatus();
        addLog(t('mcp_token_deleted'), 'system');
    } catch (err) {
        setMCPStatus(err.message, 'error');
        addLog(`MCP token delete failed: ${err.message}`, 'error');
    }
}

function renderMCPStatus(status) {
    const endpoint = status.endpoint || '/mcp';
    const absolute = `${window.location.origin}${endpoint}`;
    elements.mcpEndpoint.value = absolute;
    updateMCPConfigExample(absolute);
    setMCPStatus(status.enabled ? t('mcp_status_enabled') : t('mcp_status_disabled'), status.enabled ? 'ready' : 'disabled');
}

function renderMCPTokens() {
    if (!elements.mcpTokenList) return;
    elements.mcpTokenList.innerHTML = '';
    const tokens = state.mcp.tokens || [];
    if (tokens.length === 0) {
        const empty = document.createElement('div');
        empty.className = 'empty-state';
        empty.textContent = t('mcp_no_tokens');
        elements.mcpTokenList.appendChild(empty);
        return;
    }

    tokens.forEach(token => {
        const row = document.createElement('div');
        row.className = 'rule-row';
        const content = document.createElement('div');
        content.className = 'rule-content';
        const title = document.createElement('div');
        title.className = 'rule-title';
        title.textContent = token.name || t('mcp_token');
        const detail = document.createElement('div');
        detail.className = 'rule-detail';
        const createdAt = token.created_at ? new Date(token.created_at).toLocaleString() : '';
        detail.textContent = createdAt ? `${token.masked} · ${createdAt}` : token.masked;
        content.append(title, detail);

        const actions = document.createElement('div');
        actions.className = 'rule-actions';
        const del = document.createElement('button');
        del.className = 'btn btn-sm btn-ghost';
        del.type = 'button';
        del.textContent = t('delete');
        del.addEventListener('click', () => deleteMCPToken(token.id));
        actions.append(del);
        row.append(content, actions);
        elements.mcpTokenList.appendChild(row);
    });
}

function showCreatedMCPToken(token) {
    if (!elements.mcpCreatedToken || !elements.mcpCreatedValue) return;
    elements.mcpCreatedValue.textContent = token || '';
    elements.mcpCreatedToken.hidden = !token;
}

function setMCPStatus(message, type = 'disabled') {
    if (!elements.mcpStatus) return;
    elements.mcpStatus.textContent = message;
    elements.mcpStatus.className = `manager-status ${type}`;
}

function toggleMCPHelp(force) {
    if (!elements.mcpHelpToggle || !elements.mcpHelpPanel) return;
    const show = force !== undefined ? force : elements.mcpHelpPanel.hidden;
    elements.mcpHelpPanel.hidden = !show;
    elements.mcpHelpToggle.setAttribute('aria-expanded', String(show));
}

function updateMCPConfigExample(endpoint) {
    const example = document.getElementById('mcp-config-example');
    if (!example) return;
    example.textContent = `{
  "mcpServers": {
    "cfui": {
      "url": "${endpoint}",
      "headers": {
        "Authorization": "Bearer YOUR_TOKEN"
      }
    }
  }
}`;
}

async function fetchTunnelManagerSettings() {
    if (!elements.managerEnableToggle) return;
    try {
        const res = await fetch(`${API_BASE}/tunnel-manager/settings`);
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.tunnelManager.settings = data;
        renderTunnelManagerSettings(data);
    } catch (err) {
        setManagerStatus(`Settings error: ${err.message}`, 'error');
        addLog(`Tunnel manager settings failed: ${err.message}`, 'error');
    }
}

function renderTunnelManagerSettings(settings) {
    elements.managerEnableToggle.checked = !!settings.enabled;
    elements.managerAccountId.value = settings.account_id || '';
    elements.managerTunnelId.value = settings.tunnel_id || '';
    elements.managerAuthMode.value = settings.auth_mode === 'key' ? 'key' : 'token';
    elements.managerAPIEmail.value = settings.api_email || '';
    elements.managerAPIToken.value = settings.api_token || '';
    elements.managerAPIKey.value = settings.api_key || '';
    elements.managerTokenState.textContent = settings.api_token_set ? t('api_token_configured') : t('api_token_not_saved');
    elements.managerKeyState.textContent = settings.api_key_set ? t('api_key_configured') : t('api_key_not_saved');
    updateManagerAuthMode();
    setManagerStatus(settings.enabled ? t('manager_status_ready') : t('manager_status_disabled'), settings.enabled ? 'ready' : 'disabled');
    updateManagerSettingsDisclosure(settings);
    updateTunnelManagerText();
}

function updateManagerSettingsDisclosure(settings) {
    if (!elements.managerSettingsSection) return;
    const hasIdentity = !!(settings.account_id && settings.tunnel_id);
    const hasAuth = !!(settings.api_token_set || settings.api_key_set);
    elements.managerSettingsSection.open = !(settings.enabled && hasIdentity && hasAuth);
}

function syncReleasedRows() {
    document.querySelectorAll('.form-row').forEach(row => {
        const visibleChildren = Array.from(row.children).filter(child => !child.hidden);
        row.classList.toggle('single-column', visibleChildren.length === 1);
    });
}

function updateMetricsVisibility() {
    if (!elements.metricsPortField || !elements.metricsPortInput || !elements.metricsEnableToggle) return;
    const visible = elements.metricsEnableToggle.checked;
    elements.metricsPortField.hidden = !visible;
    elements.metricsPortInput.disabled = !visible;
    elements.metricsPortInput.setAttribute('aria-hidden', String(!visible));
    syncReleasedRows();
}

function updateManagerAuthMode() {
    const keyMode = elements.managerAuthMode?.value === 'key';
    if (elements.managerTokenField) elements.managerTokenField.hidden = keyMode;
    if (elements.managerKeyFields) elements.managerKeyFields.hidden = !keyMode;
    syncReleasedRows();
}

function toggleAPIHelp(force) {
    if (!elements.managerAPIHelp || !elements.managerAPIHelpPanel) return;
    const show = force !== undefined ? force : elements.managerAPIHelpPanel.hidden;
    elements.managerAPIHelpPanel.hidden = !show;
    elements.managerAPIHelp.setAttribute('aria-expanded', String(show));
}

function updateServicePlaceholder() {
    if (!elements.managerEntryService || !elements.managerEntryServiceType) return;
    const placeholders = {
        http: 'localhost:8080',
        https: 'localhost:8443',
        ssh: 'localhost:22',
        rdp: 'localhost:3389',
        tcp: 'localhost:5432',
        unix: '/var/run/app.sock',
        http_status: '404',
        raw: 'http://localhost:8080'
    };
    elements.managerEntryService.placeholder = placeholders[elements.managerEntryServiceType.value] || placeholders.http;
}

function canLoadTunnelManagerZones(settings = state.tunnelManager.settings) {
    return !!(settings?.enabled && settings?.account_id && (settings?.api_token_set || settings?.api_key_set));
}

async function maybeLoadTunnelManagerZones(quiet = true) {
    if (!elements.managerEntryDomainSelect) return;
    if (!canLoadTunnelManagerZones()) {
        state.tunnelManager.zones = [];
        state.tunnelManager.zonesLoaded = false;
        renderTunnelManagerZones();
        return;
    }
    if (state.tunnelManager.zonesLoaded) {
        renderTunnelManagerZones();
        return;
    }
    await loadTunnelManagerZones(quiet);
}

async function saveTunnelManagerSettings(quiet = false) {
    if (!elements.managerEnableToggle) return;
    const payload = {
        enabled: elements.managerEnableToggle.checked,
        account_id: elements.managerAccountId.value.trim(),
        tunnel_id: elements.managerTunnelId.value.trim(),
        api_token: elements.managerAuthMode.value === 'token' ? elements.managerAPIToken.value.trim() : '',
        api_email: elements.managerAuthMode.value === 'key' ? elements.managerAPIEmail.value.trim() : '',
        api_key: elements.managerAuthMode.value === 'key' ? elements.managerAPIKey.value.trim() : ''
    };

    try {
        const res = await fetch(`${API_BASE}/tunnel-manager/settings`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.tunnelManager.settings = data;
        state.tunnelManager.zonesLoaded = false;
        renderTunnelManagerSettings(data);
        state.config.tunnel_management = state.config.tunnel_management || {};
        state.config.tunnel_management.enabled = payload.enabled;
        state.config.tunnel_management.account_id = payload.account_id;
        state.config.tunnel_management.tunnel_id = payload.tunnel_id;
        state.config.tunnel_management.api_email = payload.api_email;
        if (payload.api_token) state.config.tunnel_management.api_token = payload.api_token;
        if (payload.api_key) state.config.tunnel_management.api_key = payload.api_key;
        if (!quiet) {
            addLog(t('manager_settings_saved'), 'system');
        }
        if (canLoadTunnelManagerZones(data)) {
            await loadTunnelManagerZones(true);
        } else {
            state.tunnelManager.zones = [];
            renderTunnelManagerZones();
        }
    } catch (err) {
        setManagerStatus(err.message, 'error');
        addLog(`Tunnel manager settings failed: ${err.message}`, 'error');
    }
}

async function loadTunnelManagerZones(quiet = false) {
    if (!elements.managerEntryDomainSelect) return;
    try {
        if (!quiet) setManagerStatus(t('manager_status_loading_zones'), 'loading');
        const res = await fetch(`${API_BASE}/tunnel-manager/zones`);
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.tunnelManager.zones = data.zones || [];
        state.tunnelManager.zonesLoaded = true;
        renderTunnelManagerZones();
        if (!quiet) {
            setManagerStatus(t('manager_status_zones_loaded'), 'ready');
        }
    } catch (err) {
        state.tunnelManager.zones = [];
        state.tunnelManager.zonesLoaded = false;
        renderTunnelManagerZones();
        if (!quiet) {
            setManagerStatus(err.message, 'error');
            addLog(`${t('zone_load_failed')}: ${err.message}`, 'error');
        }
    }
}

function updateDomainInputMode(options = {}) {
    const select = elements.managerEntryDomainSelect;
    const input = elements.managerEntryDomain;
    if (!select || !input) return;

    const manual = !select.value;
    input.hidden = !manual;
    input.disabled = !manual;
    input.setAttribute('aria-hidden', String(!manual));
    syncReleasedRows();

    if (!manual) {
        input.value = select.value;
        return;
    }

    if (options.clearSelectedZone) {
        const zoneNames = new Set((state.tunnelManager.zones || []).map(zone => zone.name));
        if (zoneNames.has(input.value.trim())) {
            input.value = '';
        }
    }
}

function renderTunnelManagerZones() {
    const select = elements.managerEntryDomainSelect;
    if (!select) return;
    const current = elements.managerEntryDomain.value.trim() || select.value;
    const zones = state.tunnelManager.zones || [];
    const zoneNames = new Set(zones.map(zone => zone.name));
    select.innerHTML = '';

    const manual = document.createElement('option');
    manual.value = '';
    manual.textContent = t('manual_domain_option');
    select.appendChild(manual);

    for (const zone of zones) {
        const option = document.createElement('option');
        option.value = zone.name;
        option.textContent = zone.status ? `${zone.name} (${zone.status})` : zone.name;
        select.appendChild(option);
    }

    if (current && zoneNames.has(current)) {
        select.value = current;
    } else if (!current && zones.length > 0) {
        select.value = zones[0].name;
    } else {
        select.value = '';
    }
    updateDomainInputMode();
}

async function loadTunnelManagerConfig() {
    try {
        setManagerStatus(t('manager_status_loading'), 'loading');
        const res = await fetch(`${API_BASE}/tunnel-manager/config`);
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.tunnelManager.config = data;
        renderTunnelManagerConfig(data);
        setManagerStatus(t('manager_status_loaded'), 'ready');
        addLog(`Loaded tunnel config ${data.tunnel_id || ''} v${data.version}`, 'system');
    } catch (err) {
        setManagerStatus(err.message, 'error');
        addLog(`Tunnel manager load failed: ${err.message}`, 'error');
    }
}

function renderTunnelManagerConfig(config) {
    elements.managerConfigPanel.hidden = false;
    elements.managerConfigMeta.textContent = `${t('tunnel_label')} ${config.tunnel_id || elements.managerTunnelId.value} · ${t('version_label')} ${config.version || 0} · ${config.entries?.length || 0} ${t('rules_label')}`;
    elements.managerRulesList.innerHTML = '';

    if (!config.entries || config.entries.length === 0) {
        const empty = document.createElement('div');
        empty.className = 'empty-state';
        empty.textContent = t('no_ingress_rules');
        elements.managerRulesList.appendChild(empty);
        return;
    }

    config.entries.forEach((entry) => {
        const row = document.createElement('div');
        row.className = 'rule-row';

        const content = document.createElement('div');
        content.className = 'rule-content';
        const title = document.createElement('div');
        title.className = 'rule-title';
        title.textContent = entry.hostname || t('catch_all_rule');
        const detail = document.createElement('div');
        detail.className = 'rule-detail';
        detail.textContent = `${entry.path || '/'} → ${entry.service}${entry.no_tls_verify ? ` · ${t('no_tls_verify_detail')}` : ''}`;
        content.append(title, detail);

        const actions = document.createElement('div');
        actions.className = 'rule-actions';
        const edit = document.createElement('button');
        edit.className = 'btn btn-sm btn-secondary';
        edit.type = 'button';
        edit.textContent = t('edit');
        edit.addEventListener('click', () => editTunnelManagerEntry(entry));
        const del = document.createElement('button');
        del.className = 'btn btn-sm btn-ghost';
        del.type = 'button';
        del.textContent = t('delete');
        del.addEventListener('click', () => deleteTunnelManagerEntry(entry.index));
        actions.append(edit, del);

        row.append(content, actions);
        elements.managerRulesList.appendChild(row);
    });
}

function editTunnelManagerEntry(entry) {
    const hostname = splitHostname(entry.hostname || '');
    const service = splitService(entry.service || '');
    elements.managerEntryIndex.value = String(entry.index);
    elements.managerEntrySubdomain.value = hostname.subdomain;
    elements.managerEntryDomain.value = hostname.domain;
    renderTunnelManagerZones();
    elements.managerEntryPath.value = entry.path || '';
    elements.managerEntryServiceType.value = service.type;
    elements.managerEntryService.value = service.value;
    elements.managerEntryHTTPHostHeader.value = entry.http_host_header || '';
    elements.managerEntryOriginServerName.value = entry.origin_server_name || '';
    elements.managerEntryNoTLS.checked = !!entry.no_tls_verify;
    elements.managerEntrySubmit.textContent = t('update_rule');
    updateServicePlaceholder();
    elements.managerEntryService.focus();
}

function resetTunnelEntryForm() {
    elements.managerEntryIndex.value = '';
    elements.managerEntrySubdomain.value = '';
    elements.managerEntryDomain.value = '';
    renderTunnelManagerZones();
    elements.managerEntryPath.value = '';
    elements.managerEntryServiceType.value = 'http';
    elements.managerEntryService.value = '';
    elements.managerEntryHTTPHostHeader.value = '';
    elements.managerEntryOriginServerName.value = '';
    elements.managerEntryNoTLS.checked = false;
    elements.managerEntrySubmit.textContent = t('add_rule');
    updateServicePlaceholder();
}

function buildHostname(subdomain, domain) {
    subdomain = (subdomain || '').trim().replace(/^\.+|\.+$/g, '');
    domain = (domain || '').trim().replace(/^\.+|\.+$/g, '');
    if (!subdomain) return domain;
    if (!domain) return subdomain;
    return `${subdomain}.${domain}`;
}

function splitHostname(hostname) {
    hostname = (hostname || '').trim();
    if (!hostname || !hostname.includes('.')) {
        return { subdomain: hostname, domain: '' };
    }
    const parts = hostname.split('.');
    return {
        subdomain: parts.shift(),
        domain: parts.join('.')
    };
}

function buildService(type, value) {
    value = (value || '').trim();
    if (type === 'raw') return value;
    if (type === 'http_status') return value.startsWith('http_status:') ? value : `http_status:${value || '404'}`;
    if (value.startsWith(`${type}:`)) return value;
    return `${type}://${value}`;
}

function splitService(service) {
    service = (service || '').trim();
    const statusPrefix = 'http_status:';
    if (service.startsWith(statusPrefix)) {
        return { type: 'http_status', value: service.slice(statusPrefix.length) };
    }
    const match = service.match(/^([a-z_]+):\/\/(.+)$/i);
    if (match) {
        const supported = ['http', 'https', 'ssh', 'rdp', 'tcp', 'unix'];
        return {
            type: supported.includes(match[1]) ? match[1] : 'raw',
            value: match[2]
        };
    }
    return { type: 'raw', value: service };
}

async function submitTunnelManagerEntry(event) {
    event.preventDefault();
    const index = elements.managerEntryIndex.value;
    const hostname = buildHostname(elements.managerEntrySubdomain.value, elements.managerEntryDomain.value);
    const entry = {
        hostname,
        path: elements.managerEntryPath.value.trim(),
        service: buildService(elements.managerEntryServiceType.value, elements.managerEntryService.value),
        no_tls_verify: elements.managerEntryNoTLS.checked,
        http_host_header: elements.managerEntryHTTPHostHeader.value.trim(),
        origin_server_name: elements.managerEntryOriginServerName.value.trim()
    };
    if (!entry.service) {
        setManagerStatus(t('service_required'), 'error');
        return;
    }

    const url = index === '' ? `${API_BASE}/tunnel-manager/entries` : `${API_BASE}/tunnel-manager/entries/${index}`;
    const method = index === '' ? 'POST' : 'PUT';
    try {
        setManagerStatus(index === '' ? t('manager_status_adding_rule') : t('manager_status_updating_rule'), 'loading');
        const res = await fetch(url, {
            method,
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(entry)
        });
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.tunnelManager.config = data;
        renderTunnelManagerConfig(data);
        resetTunnelEntryForm();
        setManagerStatus(t('manager_status_saved'), 'ready');
        addLog(index === '' ? t('tunnel_rule_added') : t('tunnel_rule_updated'), 'system');
    } catch (err) {
        setManagerStatus(err.message, 'error');
        addLog(`Tunnel rule save failed: ${err.message}`, 'error');
    }
}

async function deleteTunnelManagerEntry(index) {
    try {
        setManagerStatus(t('manager_status_deleting_rule'), 'loading');
        const res = await fetch(`${API_BASE}/tunnel-manager/entries/${index}`, { method: 'DELETE' });
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.tunnelManager.config = data;
        renderTunnelManagerConfig(data);
        setManagerStatus(t('manager_status_deleted'), 'ready');
        addLog(t('tunnel_rule_deleted'), 'system');
    } catch (err) {
        setManagerStatus(err.message, 'error');
        addLog(`Tunnel rule delete failed: ${err.message}`, 'error');
    }
}

function setManagerStatus(message, type = 'disabled') {
    if (!elements.managerStatus) return;
    elements.managerStatus.textContent = message;
    elements.managerStatus.className = `manager-status ${type}`;
}

async function responseError(res) {
    try {
        const data = await res.json();
        return data.error || res.statusText;
    } catch {
        return res.statusText;
    }
}

async function fetchStatus() {
    try {
        const res = await fetch(`${API_BASE}/status`);
        const data = await res.json();

        const prevStatus = state.status;
        state.status = data.status;
        state.isRunning = data.running;

        updateUI();

        if (prevStatus !== state.status) {
            addLog(`${t('status_changed')}: ${state.status}`, 'system');
        }
    } catch (err) {
        console.error('Status fetch failed', err);
    }
}

function updateUI() {
    elements.statusBadge.className = 'status-badge';
    if (state.isRunning) {
        elements.statusBadge.classList.add('running');
        elements.statusText.textContent = t('status_running');
    } else if (state.status === 'error') {
        elements.statusBadge.classList.add('error');
        elements.statusText.textContent = t('status_error');
    } else {
        elements.statusBadge.classList.add('stopped');
        elements.statusText.textContent = t('status_stopped');
    }

    if (state.isRunning) {
        elements.actionBtn.textContent = t('stop_tunnel');
        elements.actionBtn.classList.remove('btn-primary');
        elements.actionBtn.classList.add('btn-danger');
    } else {
        elements.actionBtn.textContent = t('start_tunnel');
        elements.actionBtn.classList.remove('btn-danger');
        elements.actionBtn.classList.add('btn-primary');
    }
}

function addLog(message, type = 'info') {
    // Store log entry with translation key if available
    const logEntry = {
        message,
        type,
        timestamp: new Date()
    };
    state.logs.push(logEntry);

    // Render the log entry
    renderLog(logEntry);
}

function renderLog(logEntry) {
    const entry = document.createElement('div');
    entry.className = `log-entry ${logEntry.type}`;
    const timestamp = logEntry.timestamp.toLocaleTimeString();
    entry.textContent = `[${timestamp}] ${logEntry.message}`;
    elements.logsContainer.appendChild(entry);
    elements.logsContainer.scrollTop = elements.logsContainer.scrollHeight;
}

function rerenderAllLogs() {
    // Clear the logs container
    elements.logsContainer.innerHTML = '';

    // Re-render all logs with updated translations
    state.logs.forEach(logEntry => {
        // Try to translate the message if it's a translation key
        let message = logEntry.message;

        // Check if message contains translation patterns and update them
        if (message.includes('System ready') || message.includes('系统就绪') || message.includes('システム準備完了')) {
            message = t('system_ready');
        } else if (message.includes('Status changed') || message.includes('状态已改变') || message.includes('ステータスが変更されました')) {
            const statusMatch = message.match(/: (.+)$/);
            if (statusMatch) {
                message = `${t('status_changed')}: ${statusMatch[1]}`;
            }
        } else if (message.includes('Command sent') || message.includes('命令已发送') || message.includes('コマンド送信')) {
            const actionMatch = message.match(/: (.+)$/);
            if (actionMatch) {
                message = `${t('command_sent')}: ${actionMatch[1]}`;
            }
        } else if (message.includes('Config saved') || message.includes('配置已保存') || message.includes('設定が保存されました')) {
            message = t('config_saved');
        }

        renderLog({ ...logEntry, message });
    });
}

// I18n Functions
async function loadLanguage(lang) {
    try {
        const res = await fetch(`${API_BASE}/i18n/${lang}`);
        if (!res.ok) throw new Error('Failed to load language');
        state.translations = await res.json();
    } catch (err) {
        console.error('Failed to load translations:', err);
        // Fallback to English if loading fails
        if (lang !== 'en') {
            await loadLanguage('en');
        }
    }
}

function t(key) {
    return state.translations[key] || key;
}

function updateUIText() {
    // Update title
    document.querySelector('h1').textContent = t('app_title');
    if (elements.localTab) elements.localTab.textContent = t('local_tunnel_tab');
    if (elements.managerTab) elements.managerTab.textContent = t('remote_tunnel_tab');
    if (elements.mcpTab) elements.mcpTab.textContent = t('mcp_tab');

    // Update status text
    if (state.isRunning) {
        elements.statusText.textContent = t('status_running');
    } else if (state.status === 'error') {
        elements.statusText.textContent = t('status_error');
    } else {
        elements.statusText.textContent = t('status_stopped');
    }

    // Update button text
    if (state.isRunning) {
        elements.actionBtn.textContent = t('stop_tunnel');
    } else {
        elements.actionBtn.textContent = t('start_tunnel');
    }

    // Update main configuration section
    document.querySelector('.tunnel-config-card .card-header h2').textContent = t('tunnel_config');
    document.querySelector('.tunnel-config-card .card-header .subtitle').textContent = t('tunnel_config_subtitle');

    // Token section
    document.querySelector('label[for="token-input"]').textContent = t('tunnel_token');
    if (elements.tokenHelpText) elements.tokenHelpText.textContent = t('token_help');
    if (elements.tokenHelpLink) elements.tokenHelpLink.textContent = t('token_help_link');

    // Advanced configuration
    document.querySelector('.tunnel-config-card .advanced-toggle').textContent = t('advanced_config');

    const tunnelAdvancedContent = document.querySelector('.tunnel-config-card .advanced-content');
    const advancedLabels = tunnelAdvancedContent.querySelectorAll('.form-group > label:not(.switch):not(.label-text)');
    const advancedHelpTexts = tunnelAdvancedContent.querySelectorAll('.help-text');

    // Tunnel identifier
    advancedLabels[0].textContent = t('tunnel_identifier');
    advancedHelpTexts[0].textContent = t('tunnel_identifier_help');

    // Software name
    advancedLabels[1].textContent = t('software_name');
    advancedHelpTexts[1].textContent = t('software_name_help');

    // Protocol
    advancedLabels[2].textContent = t('protocol');
    document.querySelector('#protocol-select option[value="auto"]').textContent = t('protocol_auto');
    advancedHelpTexts[2].textContent = t('protocol_help');

    // Grace period
    advancedLabels[3].textContent = t('grace_period');
    advancedHelpTexts[3].textContent = t('grace_period_help');

    // Region
    advancedLabels[4].textContent = t('region');
    document.querySelector('#region-select option[value=""]').textContent = t('region_global');
    document.querySelector('#region-select option[value="us"]').textContent = t('region_us');
    advancedHelpTexts[4].textContent = t('region_help');

    // Max retries
    advancedLabels[5].textContent = t('max_retries');
    advancedHelpTexts[5].textContent = t('max_retries_help');

    // Metrics Server Title
    advancedLabels[6].textContent = t('metrics_server_title');

    // Metrics enable
    document.querySelector('label[for="metrics-enable-toggle"]').textContent = t('metrics_enable');

    // Metrics port
    advancedLabels[7].textContent = t('metrics_port');
    advancedHelpTexts[6].textContent = t('metrics_port_help');

    // Edge Bind IP Address
    advancedLabels[8].textContent = t('edge_bind_address');
    advancedHelpTexts[7].textContent = t('edge_bind_address_help');

    // Backend TLS Verification Title
    advancedLabels[9].textContent = t('backend_tls_title');

    // No TLS Verify
    document.querySelector('label[for="no-tls-verify-toggle"]').textContent = t('no_tls_verify');
    advancedHelpTexts[8].textContent = t('no_tls_verify_help');

    // Autostart
    document.querySelector('label[for="autostart-toggle"]').textContent = t('autostart');

    // Autorestart
    document.querySelector('label[for="autorestart-toggle"]').textContent = t('autorestart');

    // Logs section
    document.querySelector('.logs-card .card-header h2').textContent = t('system_logs');
    elements.clearLogsBtn.textContent = t('clear');

    // Update stream button text
    updateStreamButtonState();
    updateTunnelManagerText();
    updateMCPText();
    updateDDNSText();
    updateFeaturesText();

    // Re-render all logs with new language
    rerenderAllLogs();
}

function updateMCPText() {
    if (!elements.mcpPanel) return;
    document.getElementById('mcp-title').textContent = t('mcp_access');
    document.getElementById('mcp-subtitle').textContent = t('mcp_subtitle');
    document.getElementById('mcp-token-title').textContent = t('mcp_token_title');
    document.getElementById('mcp-token-help').textContent = t('mcp_token_help');
    elements.mcpHelpToggle.textContent = t('help');
    document.getElementById('mcp-help-title').textContent = t('mcp_help_title');
    document.getElementById('mcp-help-summary').textContent = t('mcp_help_summary');
    document.getElementById('mcp-help-tools').textContent = t('mcp_help_tools');
    document.querySelector('label[for="mcp-endpoint"]').textContent = t('mcp_endpoint');
    document.querySelector('label[for="mcp-token-name"]').textContent = t('mcp_token_name');
    elements.mcpTokenName.placeholder = t('mcp_token_name_placeholder');
    document.querySelector('label[for="mcp-token-input"]').textContent = t('mcp_token_value');
    elements.mcpTokenInput.placeholder = t('mcp_token_value_placeholder');
    document.getElementById('mcp-token-input-help').textContent = t('mcp_token_input_help');
    elements.mcpTokenCreate.textContent = t('mcp_create_token');
    document.getElementById('mcp-created-title').textContent = t('mcp_created_title');
    document.getElementById('mcp-created-warning').textContent = t('mcp_created_warning');
    if (state.mcp.status) {
        renderMCPStatus(state.mcp.status);
    }
    renderMCPTokens();
}

async function verifyTokenPermissions() {
    const btn = elements.managerVerifyPermissions;
    const result = elements.managerVerifyResult;
    if (!btn || !result) return;

    const authMode = elements.managerAuthMode?.value || 'token';
    const payload = {
        auth_mode: authMode,
        api_token: authMode === 'token' ? elements.managerAPIToken?.value.trim() || '' : '',
        api_email: authMode === 'key' ? elements.managerAPIEmail?.value.trim() || '' : '',
        api_key: authMode === 'key' ? elements.managerAPIKey?.value.trim() || '' : ''
    };

    if (authMode === 'token' && !payload.api_token && !state.tunnelManager.settings?.api_token_set) {
        result.hidden = false;
        result.innerHTML = '<span class="perm-error">' + t('verify_enter_token') + '</span>';
        return;
    }
    if (authMode === 'key' && !payload.api_email && !payload.api_key) {
        result.hidden = false;
        result.innerHTML = '<span class="perm-error">' + t('verify_enter_credentials') + '</span>';
        return;
    }

    btn.disabled = true;
    result.hidden = false;
    result.innerHTML = '<span class="perm-loading">' + t('verify_checking') + '</span>';

    try {
        const res = await fetch(API_BASE + '/tunnel-manager/verify-token', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        const data = await res.json();

        if (!data.valid && data.error && !data.permissions) {
            result.innerHTML = '<span class="perm-error">' + (data.error || t('verify_failed')) + '</span>';
            return;
        }

        renderPermissionResult(data);
    } catch (err) {
        result.innerHTML = '<span class="perm-error">' + err.message + '</span>';
    } finally {
        btn.disabled = false;
    }
}

function renderPermissionResult(data) {
    const result = elements.managerVerifyResult;
    if (!result) return;

    if (data.token_status === 'inactive' || data.token_status === 'revoked') {
        result.innerHTML = '<span class="perm-error">Token status: ' + data.token_status + '</span>';
        return;
    }

    const perms = data.permissions || [];
    let html = '';
    perms.forEach(p => {
        const icon = p.granted ? '<span class="perm-granted">✔</span>' : (p.required ? '<span class="perm-denied">✘</span>' : '<span class="perm-granted">✔</span>');
        html += '<span class="perm-item">' + icon + ' ' + escapeHtml(p.description) + '</span>';
    });
    result.innerHTML = html;
}

function escapeHtml(str) {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

function updateTunnelManagerText() {
    const managerTitle = document.getElementById('manager-title');
    if (!managerTitle) return;

    managerTitle.textContent = t('remote_tunnel_manager');
    document.getElementById('manager-subtitle').textContent = t('remote_tunnel_manager_subtitle');
    if (elements.managerSettingsToggle) elements.managerSettingsToggle.textContent = t('remote_manager_config');
    document.getElementById('manager-enable-label').textContent = t('manager_enable');
    document.querySelector('label[for="manager-account-id"]').textContent = t('account_id');
    elements.managerAccountId.placeholder = t('account_id_placeholder');
    document.getElementById('manager-account-help').textContent = t('account_id_help');
    document.querySelector('label[for="manager-tunnel-id"]').textContent = t('managed_tunnel_id');
    elements.managerTunnelId.placeholder = t('tunnel_id_placeholder');
    document.getElementById('manager-tunnel-help').textContent = t('tunnel_id_help');
    document.querySelector('label[for="manager-auth-mode"]').textContent = t('authentication');
    document.querySelector('#manager-auth-mode option[value="token"]').textContent = t('api_token');
    document.querySelector('#manager-auth-mode option[value="key"]').textContent = t('email_api_key');
    document.getElementById('manager-auth-help').textContent = t('auth_help');
    document.querySelector('label[for="manager-api-token"]').textContent = t('api_token');
    elements.managerAPIToken.placeholder = t('api_token_placeholder');
    document.getElementById('manager-api-help-title').textContent = t('api_permissions_title');
    document.getElementById('manager-api-help-tunnel').textContent = t('api_permission_tunnel');
    document.getElementById('manager-api-help-zone').textContent = t('api_permission_zone');
    if (elements.managerAPIHelpDns) elements.managerAPIHelpDns.textContent = t('api_permission_dns');
    document.getElementById('manager-api-help-note').textContent = t('api_permission_note');
    if (elements.managerVerifyPermissions) elements.managerVerifyPermissions.textContent = t('verify_permissions');
    document.querySelector('label[for="manager-api-email"]').textContent = t('api_email');
    elements.managerAPIEmail.placeholder = t('api_email_placeholder');
    document.querySelector('label[for="manager-api-key"]').textContent = t('api_key');
    elements.managerAPIKey.placeholder = t('api_key_placeholder');
    if (elements.managerAPITokenHelp) elements.managerAPITokenHelp.textContent = t('api_token_help');
    if (elements.managerAPIEmailHelp) elements.managerAPIEmailHelp.textContent = t('api_email_help');
    if (elements.managerAPIKeyHelp) elements.managerAPIKeyHelp.textContent = t('api_key_help');
    elements.managerSaveSettings.textContent = t('save_manager_settings');
    elements.managerLoadConfig.textContent = t('load_tunnel_config');
    document.querySelector('.manager-config-panel .section-heading h3').textContent = t('ingress_rules');
    if (!state.tunnelManager.config) {
        elements.managerConfigMeta.textContent = t('no_remote_config_loaded');
    }
    document.getElementById('published-app-title').textContent = t('published_app_title');
    document.getElementById('published-app-help').textContent = t('published_app_help');
    document.querySelector('label[for="manager-entry-subdomain"]').textContent = t('subdomain');
    elements.managerEntrySubdomain.placeholder = t('subdomain_placeholder');
    document.querySelector('label[for="manager-entry-domain"]').textContent = t('domain');
    elements.managerEntryDomain.placeholder = t('domain_placeholder');
    elements.managerRefreshZones.textContent = t('refresh_domains');
    document.getElementById('manager-entry-domain-help').textContent = t('domain_select_help');
    renderTunnelManagerZones();
    document.querySelector('label[for="manager-entry-path"]').textContent = t('path');
    elements.managerEntryPath.placeholder = t('path_placeholder');
    document.getElementById('manager-entry-path-help').textContent = t('path_help');
    document.querySelector('label[for="manager-entry-service-type"]').textContent = t('service_type');
    document.querySelector('#manager-entry-service-type option[value="unix"]').textContent = t('service_type_unix');
    document.querySelector('#manager-entry-service-type option[value="http_status"]').textContent = t('service_type_http_status');
    document.querySelector('#manager-entry-service-type option[value="raw"]').textContent = t('service_type_raw');
    document.querySelector('label[for="manager-entry-service"]').textContent = t('service');
    document.getElementById('manager-entry-service-help').textContent = t('service_help');
    document.querySelector('label[for="manager-entry-http-host-header"]').textContent = t('http_host_header');
    elements.managerEntryHTTPHostHeader.placeholder = t('origin_hostname_placeholder');
    document.querySelector('label[for="manager-entry-origin-server-name"]').textContent = t('origin_server_name');
    elements.managerEntryOriginServerName.placeholder = t('origin_hostname_placeholder');
    const formSectionLabels = document.querySelectorAll('.public-hostname-form .form-section-label');
    formSectionLabels[0].textContent = t('public_hostname_section');
    formSectionLabels[1].textContent = t('service_section');
    document.querySelector('.public-hostname-advanced .advanced-toggle').textContent = t('additional_app_settings');
    document.getElementById('manager-entry-origin-tls-label').textContent = t('origin_tls');
    document.getElementById('manager-entry-no-tls-label').textContent = t('disable_origin_tls_verify');
    elements.managerEntryCancel.textContent = t('cancel_edit');
    elements.managerEntrySubmit.textContent = elements.managerEntryIndex.value === '' ? t('add_rule') : t('update_rule');

    const settings = state.tunnelManager.settings || {};
    if (elements.managerTokenState) elements.managerTokenState.textContent = settings.api_token_set ? t('api_token_configured') : t('api_token_not_saved');
    if (elements.managerKeyState) elements.managerKeyState.textContent = settings.api_key_set ? t('api_key_configured') : t('api_key_not_saved');
    if (elements.managerStatus?.classList.contains('ready') || elements.managerStatus?.classList.contains('disabled')) {
        if (state.tunnelManager.config) {
            setManagerStatus(t('manager_status_loaded'), 'ready');
        } else {
            setManagerStatus(settings.enabled ? t('manager_status_ready') : t('manager_status_disabled'), settings.enabled ? 'ready' : 'disabled');
        }
    }

    if (state.tunnelManager.config) {
        renderTunnelManagerConfig(state.tunnelManager.config);
    }

    updateManagerDerivedIdentityText(settings);
}

function updateManagerDerivedIdentityText(settings) {
    if (settings.derived_from_token) {
        document.getElementById('manager-account-help').textContent = t('account_id_derived_from_token');
        document.getElementById('manager-tunnel-help').textContent = t('tunnel_id_derived_from_token');
    } else if (settings.derive_token_failed) {
        document.getElementById('manager-account-help').textContent = t('token_identity_parse_failed');
        document.getElementById('manager-tunnel-help').textContent = t('token_identity_parse_failed');
    }
}

// Log Streaming Functions
function connectLogStream() {
    if (state.logStream) {
        return; // Already connected
    }

    console.log('Connecting to log stream...');
    state.logStream = new EventSource(`${API_BASE}/logs/stream`);

    state.logStream.onopen = () => {
        console.log('Log stream connected');
        state.isStreamConnected = true;
        updateStreamButtonState();
    };

    state.logStream.onmessage = (event) => {
        // Add streamed log to container (without storing in state.logs)
        addStreamLog(event.data);
    };

    state.logStream.onerror = (error) => {
        console.error('Log stream error:', error);
        disconnectLogStream();
    };
}

function disconnectLogStream() {
    if (state.logStream) {
        state.logStream.close();
        state.logStream = null;
        state.isStreamConnected = false;
        updateStreamButtonState();
        console.log('Log stream disconnected');
    }
}

function updateStreamButtonState() {
    if (state.isStreamConnected) {
        elements.toggleStreamBtn.textContent = t('log_stream_disable');
        elements.toggleStreamBtn.classList.remove('btn-secondary');
        elements.toggleStreamBtn.classList.add('btn-success');
    } else {
        elements.toggleStreamBtn.textContent = t('log_stream_enable');
        elements.toggleStreamBtn.classList.remove('btn-success');
        elements.toggleStreamBtn.classList.add('btn-secondary');
    }
}

function addStreamLog(line) {
    if (!line || line.trim() === '') return;

    const entry = document.createElement('div');
    entry.className = 'log-entry info';
    entry.textContent = line;
    elements.logsContainer.appendChild(entry);

    // Auto-scroll to bottom
    elements.logsContainer.scrollTop = elements.logsContainer.scrollHeight;

    // Limit to 500 lines for performance
    while (elements.logsContainer.children.length > 500) {
        elements.logsContainer.removeChild(elements.logsContainer.firstChild);
    }
}

// Cleanup on page unload
window.addEventListener('beforeunload', () => {
    disconnectLogStream();
});

// DDNS Functions

async function refreshDDNS() {
    await fetchDDNSConfig();
    await fetchDDNSStatus();
}

async function fetchDDNSConfig() {
    try {
        const res = await fetch(`${API_BASE}/ddns/config`);
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.ddns.config = data;
        renderDDNSConfig(data);
    } catch (err) {
        setDDNSStatus(err.message, 'error');
    }
}

function renderDDNSConfig(cfg) {
    if (!elements.ddnsMain) return;

    if (!cfg.has_credentials) {
        elements.ddnsNoCreds.hidden = false;
        elements.ddnsMain.hidden = true;
        elements.ddnsIPBanner.hidden = true;
        setDDNSStatus(t('ddns_status_disabled'), 'disabled');
        return;
    }

    elements.ddnsNoCreds.hidden = true;
    // Always show IP banner and settings so the user can review DDNS configuration
    elements.ddnsIPBanner.hidden = false;
    elements.ddnsMain.hidden = false;

    const v4Sources = (cfg.ip_sources || []).filter(s => s.ip_type === 'ipv4').map(s => s.url).join('\n');
    const v6Sources = (cfg.ip_sources || []).filter(s => s.ip_type === 'ipv6').map(s => s.url).join('\n');
    elements.ddnsIPv4Textarea.value = v4Sources;
    elements.ddnsIPv6Textarea.value = v6Sources;

    elements.ddnsInterval.value = String(cfg.interval_mins || 5);
    elements.ddnsMaxRetries.value = String(cfg.max_retries || 3);
    elements.ddnsOnlyOnChange.checked = cfg.only_on_change !== false;

    setDDNSStatus(cfg.enabled ? t('ddns_status_running') : t('ddns_status_disabled'), cfg.enabled ? 'ready' : 'disabled');

    renderDDNSRecords(cfg.records || []);
}

async function fetchDDNSStatus() {
    try {
        const res = await fetch(`${API_BASE}/ddns/status`);
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.ddns.status = data;
        renderDDNSStatus(data);
    } catch (err) { /* silently fail for periodic poll */ }
}

function renderDDNSStatus(data) {
    if (elements.ddnsIPv4Value) elements.ddnsIPv4Value.textContent = data.current_v4 || t('ddns_unknown');
    if (elements.ddnsIPv6Value) elements.ddnsIPv6Value.textContent = data.current_v6 || t('ddns_unknown');
    if (elements.ddnsLastCheck) {
        elements.ddnsLastCheck.textContent = data.last_check ?
            `${t('ddns_last_check')}: ${new Date(data.last_check).toLocaleString()}` :
            '—';
    }

    // Render sync log
    if (elements.ddnsSyncLogList && data.results) {
        elements.ddnsSyncLogList.innerHTML = '';
        const results = data.results.slice().reverse();
        if (results.length === 0) {
            const empty = document.createElement('div');
            empty.className = 'empty-state';
            empty.textContent = t('ddns_no_sync_history');
            elements.ddnsSyncLogList.appendChild(empty);
        }
        results.forEach(r => {
            const div = document.createElement('div');
            div.className = 'ddns-sync-item';
            const time = new Date(r.time).toLocaleTimeString();
            div.innerHTML = `<span class="ddns-sync-time">${time}</span>` +
                (r.success ? '<span class="ddns-sync-ok">✓</span>' : '<span class="ddns-sync-err">✗</span>') +
                `<span class="ddns-sync-host">${escapeHtml(r.hostname)}</span>` +
                `<span class="ddns-sync-ip">${escapeHtml(r.ip || '')}</span>` +
                `<span class="ddns-sync-msg">${escapeHtml(r.message)}</span>`;
            elements.ddnsSyncLogList.appendChild(div);
        });
    }
}

function defaultDDNSRecordValue(recordType) {
    return recordType === 'AAAA' ? '{IPV6}' : '{IPV4}';
}

function normalizeDDNSRecordValue(recordType, value) {
    const trimmed = (value || '').trim();
    return trimmed || defaultDDNSRecordValue(recordType);
}

function formatDDNSRecordValue(rec) {
    const normalized = normalizeDDNSRecordValue(rec.type, rec.value);
    if (normalized === '{IPV4}') return `{IPV4} · ${t('ddns_record_value_auto_ipv4')}`;
    if (normalized === '{IPV6}') return `{IPV6} · ${t('ddns_record_value_auto_ipv6')}`;
    return normalized;
}

function syncDDNSRecordValueFields() {
    if (!elements.ddnsRecordIPv4ValueGroup || !elements.ddnsRecordIPv6ValueGroup) return;

    const editing = elements.ddnsRecordForm?.dataset.editIndex !== undefined;
    const showIPv4 = !!elements.ddnsRecordIPv4?.checked;
    const showIPv6 = !!elements.ddnsRecordIPv6?.checked;

    elements.ddnsRecordIPv4ValueGroup.hidden = !showIPv4;
    elements.ddnsRecordIPv6ValueGroup.hidden = !showIPv6;

    if (elements.ddnsRecordIPv4Value) {
        if (!elements.ddnsRecordIPv4Value.value.trim()) {
            elements.ddnsRecordIPv4Value.value = defaultDDNSRecordValue('A');
        }
        elements.ddnsRecordIPv4Value.disabled = !showIPv4;
    }
    if (elements.ddnsRecordIPv6Value) {
        if (!elements.ddnsRecordIPv6Value.value.trim()) {
            elements.ddnsRecordIPv6Value.value = defaultDDNSRecordValue('AAAA');
        }
        elements.ddnsRecordIPv6Value.disabled = !showIPv6;
    }

    if (elements.ddnsRecordIPv4) {
        elements.ddnsRecordIPv4.disabled = editing;
    }
    if (elements.ddnsRecordIPv6) {
        elements.ddnsRecordIPv6.disabled = editing;
    }
}

function renderDDNSRecords(records) {
    if (!elements.ddnsRecordsList) return;
    elements.ddnsRecordsList.innerHTML = '';
    if (!records || records.length === 0) {
        const empty = document.createElement('div');
        empty.className = 'empty-state';
        empty.textContent = t('ddns_no_records');
        elements.ddnsRecordsList.appendChild(empty);
        return;
    }
    records.forEach((rec, i) => {
        const row = document.createElement('div');
        row.className = 'rule-row';
        const content = document.createElement('div');
        content.className = 'rule-content';
        const title = document.createElement('div');
        title.className = 'rule-title';
        title.textContent = rec.name || '—';
        const detail = document.createElement('div');
        detail.className = 'rule-detail';
        const ttlText = rec.ttl === 1 ? t('ddns_ttl_auto') : rec.ttl + 's';
        const proxied = rec.proxied ? ` · ${t('ddns_record_proxied')}` : '';
        detail.textContent = `${rec.type} · ${t('ddns_record_value')}: ${formatDDNSRecordValue(rec)} · ${t('ddns_record_ttl')}: ${ttlText}${proxied}`;
        content.append(title, detail);

        const actions = document.createElement('div');
        actions.className = 'rule-actions';
        const editBtn = document.createElement('button');
        editBtn.className = 'btn btn-sm btn-secondary';
        editBtn.textContent = t('edit');
        editBtn.addEventListener('click', () => editDDNSRecord(i, rec));
        const delBtn = document.createElement('button');
        delBtn.className = 'btn btn-sm btn-ghost';
        delBtn.textContent = t('delete');
        delBtn.addEventListener('click', () => deleteDDNSRecord(i));
        actions.append(editBtn, delBtn);
        row.append(content, actions);
        elements.ddnsRecordsList.appendChild(row);
    });
}

async function ddnsSaveSettings() {
    const v4Lines = elements.ddnsIPv4Textarea.value.split('\n').map(l => l.trim()).filter(l => l);
    const v6Lines = elements.ddnsIPv6Textarea.value.split('\n').map(l => l.trim()).filter(l => l);
    const sources = [
        ...v4Lines.map(url => ({ url, ip_type: 'ipv4' })),
        ...v6Lines.map(url => ({ url, ip_type: 'ipv6' }))
    ];
    const enabled = state.ddns.config?.enabled ?? !!elements.featureDdnsToggle?.checked;

    const payload = {
        enabled,
        ip_sources: sources,
        interval_mins: parseInt(elements.ddnsInterval.value) || 5,
        max_retries: parseInt(elements.ddnsMaxRetries.value) || 3,
        only_on_change: elements.ddnsOnlyOnChange.checked
    };

    try {
        const res = await fetch(`${API_BASE}/ddns/config`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload)
        });
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.ddns.config = data;
        renderDDNSConfig(data);
        addLog(t('ddns_settings_saved'), 'system');
        await fetchDDNSStatus();
    } catch (err) {
        setDDNSStatus(err.message, 'error');
        addLog(`DDNS save failed: ${err.message}`, 'error');
    }
}

async function ddnsSyncNow() {
    try {
        setDDNSStatus('Syncing...', 'loading');
        const res = await fetch(`${API_BASE}/ddns/sync-now`, { method: 'POST' });
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.ddns.status = data;
        renderDDNSStatus(data);
        setDDNSStatus(t('ddns_status_running'), 'ready');
        addLog(t('ddns_sync_triggered'), 'system');
    } catch (err) {
        setDDNSStatus(err.message, 'error');
        addLog(`DDNS sync failed: ${err.message}`, 'error');
    }
}

async function loadDDNSZones() {
    if (state.ddns.zonesLoaded) {
        renderDDNSZones();
        return;
    }
    try {
        const res = await fetch(`${API_BASE}/ddns/zones`);
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.ddns.zones = data.zones || [];
        state.ddns.zonesLoaded = true;
        renderDDNSZones();
    } catch (err) {
        console.error('Failed to load DDNS zones:', err);
    }
}

function renderDDNSZones() {
    const select = elements.ddnsRecordZoneSelect;
    if (!select) return;
    const zones = state.ddns.zones || [];
    select.innerHTML = '';
    zones.forEach(z => {
        const opt = document.createElement('option');
        opt.value = z.id;
        opt.textContent = z.name + (z.status ? ` (${z.status})` : '');
        select.appendChild(opt);
    });
    if (!select.value && zones.length > 0) {
        select.value = zones[0].id;
    }
}

async function ddnsSubmitRecord(event) {
    event.preventDefault();
    const zoneName = elements.ddnsRecordZoneSelect.selectedOptions[0]?.textContent?.replace(/ \(.*\)/, '') || '';
    const index = elements.ddnsRecordForm.dataset.editIndex;
    const editing = index !== undefined && index !== '';
    const entry = {
        subdomain: elements.ddnsRecordSubdomain.value.trim(),
        zone_id: elements.ddnsRecordZoneSelect.value,
        zone_name: zoneName,
        ipv4: elements.ddnsRecordIPv4.checked,
        ipv6: elements.ddnsRecordIPv6.checked,
        ipv4_value: normalizeDDNSRecordValue('A', elements.ddnsRecordIPv4Value?.value),
        ipv6_value: normalizeDDNSRecordValue('AAAA', elements.ddnsRecordIPv6Value?.value),
        proxied: elements.ddnsRecordProxied.checked,
        ttl: parseInt(elements.ddnsRecordTTLSelect.value) || 1
    };
    if (editing) {
        entry.value = elements.ddnsRecordIPv4.checked ?
            normalizeDDNSRecordValue('A', elements.ddnsRecordIPv4Value?.value) :
            normalizeDDNSRecordValue('AAAA', elements.ddnsRecordIPv6Value?.value);
    }

    const url = editing ?
        `${API_BASE}/ddns/records/${index}` :
        `${API_BASE}/ddns/records`;
    const method = editing ? 'PUT' : 'POST';

    try {
        const res = await fetch(url, {
            method,
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(entry)
        });
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.ddns.config = data;
        renderDDNSConfig(data);
        resetDDNSRecordForm();
        addLog(editing ? t('ddns_record_updated') : t('ddns_record_added'), 'system');
    } catch (err) {
        setDDNSStatus(err.message, 'error');
    }
}

function editDDNSRecord(index, rec) {
    elements.ddnsRecordForm.hidden = false;
    elements.ddnsRecordForm.dataset.editIndex = String(index);
    const zoneName = rec.zone_name || '';
    const suffix = zoneName ? `.${zoneName}` : '';
    elements.ddnsRecordSubdomain.value = suffix && (rec.name || '').endsWith(suffix) ?
        (rec.name || '').slice(0, -suffix.length) :
        (rec.name || '');
    elements.ddnsRecordIPv4.checked = rec.type === 'A';
    elements.ddnsRecordIPv6.checked = rec.type === 'AAAA';
    if (elements.ddnsRecordIPv4Value) {
        elements.ddnsRecordIPv4Value.value = normalizeDDNSRecordValue('A', rec.type === 'A' ? rec.value : '');
    }
    if (elements.ddnsRecordIPv6Value) {
        elements.ddnsRecordIPv6Value.value = normalizeDDNSRecordValue('AAAA', rec.type === 'AAAA' ? rec.value : '');
    }
    elements.ddnsRecordTTLSelect.value = String(rec.ttl || 1);
    elements.ddnsRecordProxied.checked = rec.proxied !== false;
    elements.ddnsRecordSubmit.textContent = t('update_rule');
    syncDDNSRecordValueFields();
    loadDDNSZones().then(() => {
        if (rec.zone_id && elements.ddnsRecordZoneSelect) {
            elements.ddnsRecordZoneSelect.value = rec.zone_id;
        }
    });
}

function resetDDNSRecordForm() {
    elements.ddnsRecordForm.hidden = true;
    delete elements.ddnsRecordForm.dataset.editIndex;
    elements.ddnsRecordSubdomain.value = '';
    elements.ddnsRecordIPv4.checked = true;
    elements.ddnsRecordIPv6.checked = true;
    if (elements.ddnsRecordIPv4Value) {
        elements.ddnsRecordIPv4Value.value = defaultDDNSRecordValue('A');
    }
    if (elements.ddnsRecordIPv6Value) {
        elements.ddnsRecordIPv6Value.value = defaultDDNSRecordValue('AAAA');
    }
    elements.ddnsRecordTTLSelect.value = '1';
    elements.ddnsRecordProxied.checked = true;
    elements.ddnsRecordSubmit.textContent = t('ddns_add_record');
    syncDDNSRecordValueFields();
}

async function deleteDDNSRecord(index) {
    try {
        const res = await fetch(`${API_BASE}/ddns/records/${index}`, { method: 'DELETE' });
        if (!res.ok) throw new Error(await responseError(res));
        const data = await res.json();
        state.ddns.config = data;
        renderDDNSConfig(data);
        addLog(t('ddns_record_deleted'), 'system');
    } catch (err) {
        setDDNSStatus(err.message, 'error');
    }
}

function switchDDNSSubTab(name) {
    document.getElementById('ddns-tab-sources')?.classList.toggle('active', name === 'sources');
    document.getElementById('ddns-tab-auto')?.classList.toggle('active', name === 'auto');
    document.getElementById('ddns-tab-sources')?.setAttribute('aria-selected', String(name === 'sources'));
    document.getElementById('ddns-tab-auto')?.setAttribute('aria-selected', String(name === 'auto'));
    document.getElementById('ddns-panel-sources').hidden = name !== 'sources';
    document.getElementById('ddns-panel-auto').hidden = name !== 'auto';
}

function setDDNSStatus(message, type) {
    if (!elements.ddnsStatus) return;
    elements.ddnsStatus.textContent = message;
    elements.ddnsStatus.className = `manager-status ${type}`;
}

function updateFeaturesText() {
    if (elements.featuresTab) elements.featuresTab.textContent = t('features_tab');
    if (elements.featuresTitle) elements.featuresTitle.textContent = t('features_title');
    if (elements.featuresSubtitle) elements.featuresSubtitle.textContent = t('features_subtitle');
    if (elements.featureManagerName) elements.featureManagerName.textContent = t('feature_manager_name');
    if (elements.featureManagerDesc) elements.featureManagerDesc.textContent = t('feature_manager_desc');
    if (elements.featureDdnsName) elements.featureDdnsName.textContent = t('feature_ddns_name');
    if (elements.featureDdnsDesc) elements.featureDdnsDesc.textContent = t('feature_ddns_desc');
    if (elements.featureMcpName) elements.featureMcpName.textContent = t('feature_mcp_name');
    if (elements.featureMcpDesc) elements.featureMcpDesc.textContent = t('feature_mcp_desc');
}

function updateDDNSText() {
    if (!elements.ddnsPanel) return;
    document.getElementById('ddns-title').textContent = t('ddns_title');
    document.getElementById('ddns-subtitle').textContent = t('ddns_subtitle');
    document.getElementById('ddns-no-credentials').textContent = t('ddns_no_credentials');
    document.getElementById('ddns-current-ip-label').textContent = t('ddns_current_ip');
    document.getElementById('ddns-ipv4-label').textContent = t('ddns_ipv4');
    document.getElementById('ddns-ipv6-label').textContent = t('ddns_ipv6');
    elements.ddnsSyncNow.textContent = t('ddns_sync_now');
    document.getElementById('ddns-settings-toggle').textContent = t('ddns_settings');
    document.getElementById('ddns-tab-sources').textContent = t('ddns_ip_sources');
    document.getElementById('ddns-tab-auto').textContent = t('ddns_auto_update');
    document.getElementById('ddns-ipv4-sources-label').textContent = t('ddns_ipv4_sources');
    document.getElementById('ddns-ipv6-sources-label').textContent = t('ddns_ipv6_sources');
    document.getElementById('ddns-batch-add-help').textContent = t('ddns_batch_add_help');
    document.getElementById('ddns-interval-label').textContent = t('ddns_interval');
    document.getElementById('ddns-max-retries-label').textContent = t('ddns_max_retries');
    document.getElementById('ddns-only-on-change-label').textContent = t('ddns_only_on_change');
    elements.ddnsSaveSettings.textContent = t('ddns_save_settings');
    document.getElementById('ddns-records-label').textContent = t('ddns_records');
    elements.ddnsAddRecordBtn.textContent = t('ddns_add_record');
    document.getElementById('ddns-record-subdomain-label').textContent = t('subdomain');
    elements.ddnsRecordSubdomain.placeholder = t('subdomain_placeholder');
    document.getElementById('ddns-record-zone-label').textContent = t('domain');
    document.getElementById('ddns-record-ip-version-label').textContent = t('ddns_record_ip_version');
    document.getElementById('ddns-record-ttl-label').textContent = t('ddns_record_ttl');
    document.getElementById('ddns-record-ipv4-value-label').textContent = t('ddns_record_ipv4_value');
    document.getElementById('ddns-record-ipv6-value-label').textContent = t('ddns_record_ipv6_value');
    document.getElementById('ddns-record-value-help').textContent = t('ddns_record_value_help');
    document.getElementById('ddns-record-proxied-label').textContent = t('ddns_record_proxied');
    const ipv4Label = document.getElementById('ddns-record-ipv4-label');
    if (ipv4Label) ipv4Label.textContent = t('ddns_record_a');
    const ipv6Label = document.getElementById('ddns-record-ipv6-label');
    if (ipv6Label) ipv6Label.textContent = t('ddns_record_aaaa');
    if (elements.ddnsRecordIPv4Value) elements.ddnsRecordIPv4Value.placeholder = '{IPV4}';
    if (elements.ddnsRecordIPv6Value) elements.ddnsRecordIPv6Value.placeholder = '{IPV6}';
    elements.ddnsRecordCancel.textContent = t('cancel_edit');
    elements.ddnsRecordSubmit.textContent = elements.ddnsRecordForm?.dataset.editIndex ? t('update_rule') : t('ddns_add_record');
    document.getElementById('ddns-sync-log-label').textContent = t('ddns_sync_log');
    if (elements.ddnsTab) elements.ddnsTab.textContent = t('ddns_tab');

    // Update interval select options with translated "min" suffix
    const minUnit = t('minutes');
    Array.from(elements.ddnsInterval.options).forEach(opt => {
        opt.textContent = opt.value + ' ' + minUnit;
    });

    // Update TTL select options
    const ttlSelect = document.getElementById('ddns-record-ttl-select');
    if (ttlSelect && ttlSelect.options[0]) {
        ttlSelect.options[0].textContent = t('ddns_ttl_auto');
    }

    if (state.ddns.config) renderDDNSConfig(state.ddns.config);
    if (state.ddns.status) renderDDNSStatus(state.ddns.status);
    syncDDNSRecordValueFields();
}

// Poll DDNS status periodically when tab is active
setInterval(() => {
    if (elements.ddnsPanel && !elements.ddnsPanel.hidden) {
        fetchDDNSStatus();
    }
}, 10000);

init();
