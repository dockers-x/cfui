const API_BASE = '/api';

const state = {
    isRunning: false,
    config: {},
    status: 'unknown',
    currentLang: localStorage.getItem('lang') || 'zh',
    currentTheme: localStorage.getItem('theme') || 'light',
    translations: {},
    logs: [] // Store log entries for re-rendering when language changes
};

const elements = {
    statusBadge: document.getElementById('status-badge'),
    statusDot: document.querySelector('.status-dot'),
    statusText: document.querySelector('.status-text'),
    tokenInput: document.getElementById('token-input'),
    customTagInput: document.getElementById('custom-version-input'),
    toggleVisibilityBtn: document.getElementById('toggle-visibility'),
    autoStartToggle: document.getElementById('autostart-toggle'),
    autoRestartToggle: document.getElementById('autorestart-toggle'),
    actionBtn: document.getElementById('action-btn'),
    logsContainer: document.getElementById('logs-container'),
    clearLogsBtn: document.getElementById('clear-logs'),
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
    noTLSVerifyToggle: document.getElementById('no-tls-verify-toggle')
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

    await fetchConfig();
    await fetchStatus();
    setInterval(fetchStatus, 2000);
}

// Event Listeners
elements.themeToggle.addEventListener('click', toggleTheme);

elements.toggleVisibilityBtn.addEventListener('click', () => {
    const type = elements.tokenInput.type === 'password' ? 'text' : 'password';
    elements.tokenInput.type = type;
});

elements.tokenInput.addEventListener('change', saveAllConfig);
elements.customVersionInput?.addEventListener('change', saveAllConfig);
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

elements.langSelect.addEventListener('change', async (e) => {
    const newLang = e.target.value;
    await loadLanguage(newLang);
    state.currentLang = newLang;
    localStorage.setItem('lang', newLang);
    updateUIText();
});

// API Calls
async function fetchConfig() {
    try {
        const res = await fetch(`${API_BASE}/config`);
        const data = await res.json();
        state.config = data;

        elements.tokenInput.value = data.token || '';
        elements.customTagInput.value = data.custom_tag || '';
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
        auto_start: elements.autoStartToggle.checked,
        auto_restart: elements.autoRestartToggle.checked,
        protocol: elements.protocolSelect?.value || 'auto',
        grace_period: elements.gracePeriodInput?.value || '30s',
        region: elements.regionSelect?.value || '',
        retries: parseInt(elements.retriesInput?.value || '5'),
        metrics_enable: elements.metricsEnableToggle?.checked || false,
        metrics_port: parseInt(elements.metricsPortInput?.value || '60123'),
        edge_bind_address: elements.edgeBindAddressInput?.value || '',
        no_tls_verify: elements.noTLSVerifyToggle?.checked || false
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
    document.querySelector('.card-header h2').textContent = t('tunnel_config');
    document.querySelector('.card-header .subtitle').textContent = t('tunnel_config_subtitle');

    // Token section
    document.querySelector('label[for="token-input"]').textContent = t('tunnel_token');
    document.querySelectorAll('.help-text')[0].textContent = t('token_help');

    // Advanced configuration
    document.querySelector('.advanced-toggle').textContent = t('advanced_config');

    const advancedLabels = document.querySelectorAll('.advanced-content label:not(.switch)');

    // Tunnel identifier
    advancedLabels[0].textContent = t('tunnel_identifier');
    document.querySelectorAll('.advanced-content .help-text')[0].textContent = t('tunnel_identifier_help');

    // Protocol
    advancedLabels[1].textContent = t('protocol');
    document.querySelector('#protocol-select option[value="auto"]').textContent = t('protocol_auto');
    document.querySelectorAll('.advanced-content .help-text')[1].textContent = t('protocol_help');

    // Grace period
    advancedLabels[2].textContent = t('grace_period');
    document.querySelectorAll('.advanced-content .help-text')[2].textContent = t('grace_period_help');

    // Region
    advancedLabels[3].textContent = t('region');
    document.querySelector('#region-select option[value=""]').textContent = t('region_global');
    document.querySelector('#region-select option[value="us"]').textContent = t('region_us');
    document.querySelectorAll('.advanced-content .help-text')[3].textContent = t('region_help');

    // Max retries
    advancedLabels[4].textContent = t('max_retries');
    document.querySelectorAll('.advanced-content .help-text')[4].textContent = t('max_retries_help');

    // Metrics Server Title
    advancedLabels[5].textContent = t('metrics_server_title');

    // Metrics enable
    document.querySelectorAll('.label-text')[0].textContent = t('metrics_enable');

    // Metrics port
    advancedLabels[6].textContent = t('metrics_port');
    document.querySelectorAll('.advanced-content .help-text')[5].textContent = t('metrics_port_help');

    // Edge Bind IP Address
    advancedLabels[7].textContent = t('edge_bind_address');
    document.querySelectorAll('.advanced-content .help-text')[6].textContent = t('edge_bind_address_help');

    // Backend TLS Verification Title
    advancedLabels[8].textContent = t('backend_tls_title');

    // No TLS Verify
    document.querySelectorAll('.label-text')[1].textContent = t('no_tls_verify');
    document.querySelectorAll('.advanced-content .help-text')[7].textContent = t('no_tls_verify_help');

    // Autostart
    document.querySelectorAll('.label-text')[2].textContent = t('autostart');

    // Autorestart
    document.querySelectorAll('.label-text')[3].textContent = t('autorestart');

    // Logs section
    document.querySelector('.logs-card .card-header h2').textContent = t('system_logs');
    elements.clearLogsBtn.textContent = t('clear');

    // Re-render all logs with new language
    rerenderAllLogs();
}

init();
