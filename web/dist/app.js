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
    tunnelManager: {
        settings: {},
        config: null
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
    metricsPortInput: document.getElementById('metrics-port-input'),
    edgeBindAddressInput: document.getElementById('edge-bind-address-input'),
    noTLSVerifyToggle: document.getElementById('no-tls-verify-toggle'),
    managerStatus: document.getElementById('manager-status'),
    managerEnableToggle: document.getElementById('manager-enable-toggle'),
    managerAccountId: document.getElementById('manager-account-id'),
    managerTunnelId: document.getElementById('manager-tunnel-id'),
    managerAuthMode: document.getElementById('manager-auth-mode'),
    managerTokenField: document.querySelector('.manager-token-field'),
    managerKeyFields: document.querySelector('.manager-key-fields'),
    managerAPIToken: document.getElementById('manager-api-token'),
    managerAPIEmail: document.getElementById('manager-api-email'),
    managerAPIKey: document.getElementById('manager-api-key'),
    managerTokenState: document.getElementById('manager-token-state'),
    managerKeyState: document.getElementById('manager-key-state'),
    managerSaveSettings: document.getElementById('manager-save-settings'),
    managerLoadConfig: document.getElementById('manager-load-config'),
    managerConfigPanel: document.getElementById('manager-config-panel'),
    managerConfigMeta: document.getElementById('manager-config-meta'),
    managerRulesList: document.getElementById('manager-rules-list'),
    managerEntryForm: document.getElementById('manager-entry-form'),
    managerEntryIndex: document.getElementById('manager-entry-index'),
    managerEntrySubdomain: document.getElementById('manager-entry-subdomain'),
    managerEntryDomain: document.getElementById('manager-entry-domain'),
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
    localPanel: document.getElementById('local-panel'),
    managerPanel: document.getElementById('manager-panel')
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
    await fetchTunnelManagerSettings();
    if (state.tunnelManager.settings?.enabled && state.tunnelManager.settings?.account_id && state.tunnelManager.settings?.tunnel_id) {
        await loadTunnelManagerConfig();
    }
    await fetchStatus();
    setInterval(fetchStatus, 2000);

    // DO NOT auto-connect log stream - user must manually enable it
    // connectLogStream();
}

// Event Listeners
elements.themeToggle.addEventListener('click', toggleTheme);

elements.toggleVisibilityBtn.addEventListener('click', () => {
    const type = elements.tokenInput.type === 'password' ? 'text' : 'password';
    elements.tokenInput.type = type;
});

elements.tokenInput.addEventListener('change', saveAllConfig);
elements.customTagInput?.addEventListener('change', saveAllConfig);
elements.softwareNameInput?.addEventListener('change', saveAllConfig);
elements.autoStartToggle.addEventListener('change', saveAllConfig);
elements.autoRestartToggle.addEventListener('change', saveAllConfig);
elements.protocolSelect?.addEventListener('change', saveAllConfig);
elements.gracePeriodInput?.addEventListener('change', saveAllConfig);
elements.regionSelect?.addEventListener('change', saveAllConfig);
elements.retriesInput?.addEventListener('change', saveAllConfig);
elements.metricsEnableToggle?.addEventListener('change', saveAllConfig);
elements.metricsPortInput?.addEventListener('change', saveAllConfig);
elements.edgeBindAddressInput?.addEventListener('change', saveAllConfig);
elements.noTLSVerifyToggle?.addEventListener('change', saveAllConfig);

elements.localTab?.addEventListener('click', () => activateTab('local'));
elements.managerTab?.addEventListener('click', () => activateTab('manager'));
elements.managerAuthMode?.addEventListener('change', updateManagerAuthMode);
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
    elements.localTab?.classList.toggle('active', !managerActive);
    elements.managerTab?.classList.toggle('active', managerActive);
    elements.localTab?.setAttribute('aria-selected', String(!managerActive));
    elements.managerTab?.setAttribute('aria-selected', String(managerActive));

    if (elements.localPanel) {
        elements.localPanel.hidden = managerActive;
        elements.localPanel.classList.toggle('active', !managerActive);
    }
    if (elements.managerPanel) {
        elements.managerPanel.hidden = !managerActive;
        elements.managerPanel.classList.toggle('active', managerActive);
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
    elements.managerAPIToken.value = '';
    elements.managerAPIKey.value = '';
    elements.managerTokenState.textContent = settings.api_token_set ? t('api_token_configured') : t('api_token_not_saved');
    elements.managerKeyState.textContent = settings.api_key_set ? t('api_key_configured') : t('api_key_not_saved');
    updateManagerAuthMode();
    setManagerStatus(settings.enabled ? t('manager_status_ready') : t('manager_status_disabled'), settings.enabled ? 'ready' : 'disabled');
    updateTunnelManagerText();
}

function updateManagerAuthMode() {
    const keyMode = elements.managerAuthMode?.value === 'key';
    if (elements.managerTokenField) elements.managerTokenField.hidden = keyMode;
    if (elements.managerKeyFields) elements.managerKeyFields.hidden = !keyMode;
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
    } catch (err) {
        setManagerStatus(err.message, 'error');
        addLog(`Tunnel manager settings failed: ${err.message}`, 'error');
    }
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
    document.querySelectorAll('.help-text')[0].textContent = t('token_help');

    // Advanced configuration
    document.querySelector('.advanced-toggle').textContent = t('advanced_config');

    const advancedLabels = document.querySelectorAll('.advanced-content label:not(.switch)');

    // Tunnel identifier
    advancedLabels[0].textContent = t('tunnel_identifier');
    document.querySelectorAll('.advanced-content .help-text')[0].textContent = t('tunnel_identifier_help');

    // Software name
    advancedLabels[1].textContent = t('software_name');
    document.querySelectorAll('.advanced-content .help-text')[1].textContent = t('software_name_help');

    // Protocol
    advancedLabels[2].textContent = t('protocol');
    document.querySelector('#protocol-select option[value="auto"]').textContent = t('protocol_auto');
    document.querySelectorAll('.advanced-content .help-text')[2].textContent = t('protocol_help');

    // Grace period
    advancedLabels[3].textContent = t('grace_period');
    document.querySelectorAll('.advanced-content .help-text')[3].textContent = t('grace_period_help');

    // Region
    advancedLabels[4].textContent = t('region');
    document.querySelector('#region-select option[value=""]').textContent = t('region_global');
    document.querySelector('#region-select option[value="us"]').textContent = t('region_us');
    document.querySelectorAll('.advanced-content .help-text')[4].textContent = t('region_help');

    // Max retries
    advancedLabels[5].textContent = t('max_retries');
    document.querySelectorAll('.advanced-content .help-text')[5].textContent = t('max_retries_help');

    // Metrics Server Title
    advancedLabels[6].textContent = t('metrics_server_title');

    // Metrics enable
    document.querySelectorAll('.label-text')[0].textContent = t('metrics_enable');

    // Metrics port
    advancedLabels[7].textContent = t('metrics_port');
    document.querySelectorAll('.advanced-content .help-text')[6].textContent = t('metrics_port_help');

    // Edge Bind IP Address
    advancedLabels[8].textContent = t('edge_bind_address');
    document.querySelectorAll('.advanced-content .help-text')[7].textContent = t('edge_bind_address_help');

    // Backend TLS Verification Title
    advancedLabels[9].textContent = t('backend_tls_title');

    // No TLS Verify
    document.querySelectorAll('.label-text')[1].textContent = t('no_tls_verify');
    document.querySelectorAll('.advanced-content .help-text')[8].textContent = t('no_tls_verify_help');

    // Autostart
    document.querySelectorAll('.label-text')[2].textContent = t('autostart');

    // Autorestart
    document.querySelectorAll('.label-text')[3].textContent = t('autorestart');

    // Logs section
    document.querySelector('.logs-card .card-header h2').textContent = t('system_logs');
    elements.clearLogsBtn.textContent = t('clear');

    // Update stream button text
    updateStreamButtonState();
    updateTunnelManagerText();

    // Re-render all logs with new language
    rerenderAllLogs();
}

function updateTunnelManagerText() {
    const managerTitle = document.getElementById('manager-title');
    if (!managerTitle) return;

    managerTitle.textContent = t('remote_tunnel_manager');
    document.getElementById('manager-subtitle').textContent = t('remote_tunnel_manager_subtitle');
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
    document.querySelector('label[for="manager-api-email"]').textContent = t('api_email');
    elements.managerAPIEmail.placeholder = t('api_email_placeholder');
    document.querySelector('label[for="manager-api-key"]').textContent = t('api_key');
    elements.managerAPIKey.placeholder = t('api_key_placeholder');
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
    document.querySelector('.public-hostname-advanced .label-text').textContent = t('disable_origin_tls_verify');
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

init();
