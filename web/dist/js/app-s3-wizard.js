/* =========================================================================
   CloudFlared UI - S3 WebDAV setup wizard
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, t, apiGet, apiSend, toast, setBusy, setTokenVisible } = window.cfui;

    const PROVIDER_R2 = 'cloudflare_r2';
    const DEFAULT_MOUNT = '/webdav/s3/';
    const steps = ['type', 'storage', 's3', 'webdav', 'review'];
    const wizard = { mode: 'create', step: 0, mount: null, provider: 'generic_s3', buckets: [] };

    function openS3Wizard({ mode = 'create', mount = null } = {}) {
        wizard.mode = mode;
        wizard.step = 0;
        wizard.mount = mount;
        wizard.provider = mount?.provider || 'generic_s3';
        wizard.buckets = [];
        fillWizard(mount);
        renderWizard();
        window.cfui.openDialog($('s3-wizard-dialog'));
    }

    function fillWizard(mount) {
        const isEdit = !!mount;
        setValue('s3-wizard-name', mount?.name || t('s3_new_mount_default'));
        $('s3-wizard-enabled').checked = mount?.enabled !== false;
        setProvider(mount?.provider || 'generic_s3');
        setValue('s3-wizard-account-id', mount?.account_id || '');
        setValue('s3-wizard-jurisdiction', mount?.jurisdiction || 'default');
        setValue('s3-wizard-endpoint-url', mount?.endpoint_url || '');
        setValue('s3-wizard-region', mount?.region || 'auto');
        $('s3-wizard-path-style').checked = mount?.path_style !== false;
        setValue('s3-wizard-bucket-name', mount?.bucket_name || '');
        setValue('s3-wizard-root-prefix', mount?.root_prefix || '');
        setValue('s3-wizard-access-key-id', mount?.access_key_id || '');
        setValue('s3-wizard-secret-access-key', '');
        setValue('s3-wizard-webdav-username', mount?.webdav_username || '');
        setValue('s3-wizard-webdav-password', '');
        setValue('s3-wizard-mount-path', mount?.mount_path || window.cfui.s3NextMountPath?.() || DEFAULT_MOUNT);
        $('s3-wizard-webdav-enabled').checked = mount?.webdav_enabled !== false;
        $('s3-wizard-webdav-auth-enabled').checked = mount?.webdav_auth_enabled !== false;
        $('s3-wizard-skip-bucket').checked = false;
        $('s3-wizard-secret-state').textContent = t(isEdit && mount?.secret_access_key_set ? 's3_secret_set' : 's3_secret_not_set');
        $('s3-wizard-password-state').textContent = t(isEdit && mount?.password_set ? 's3_password_set' : 's3_password_not_set');
        setCreateBucketPanel(false);
        renderBucketSelect([], mount?.bucket_name || '');
        applyProviderDefaults();
        updateEndpointPreview();
        clearAlert();
    }

    function setProvider(provider) {
        wizard.provider = provider === PROVIDER_R2 ? PROVIDER_R2 : 'generic_s3';
        document.querySelectorAll('.s3-provider-card').forEach((card) => {
            const active = card.dataset.provider === wizard.provider;
            card.setAttribute('aria-checked', String(active));
        });
    }

    function applyProviderDefaults() {
        const isR2 = wizard.provider === PROVIDER_R2;
        $('s3-wizard-r2-guide').hidden = !isR2;
        $('s3-wizard-r2-account-row').hidden = !isR2;
        $('s3-wizard-r2-buckets').hidden = !isR2;
        if (isR2) {
            if (!value('s3-wizard-region')) setValue('s3-wizard-region', 'auto');
            applyR2EndpointPreset(false);
        }
        renderR2TokenPath();
    }

    function renderWizard() {
        $('s3-wizard-title').textContent = wizard.mode === 'create' ? t('s3_wizard_create_title') : t('s3_wizard_edit_title');
        $('s3-wizard-subtitle').textContent = wizard.mode === 'create' ? t('s3_wizard_create_subtitle') : t('s3_wizard_edit_subtitle');
        document.querySelectorAll('.s3-step').forEach((step) => {
            const idx = Number(step.dataset.step);
            step.dataset.state = idx === wizard.step ? 'active' : idx < wizard.step ? 'done' : 'todo';
            step.disabled = idx > wizard.step;
        });
        document.querySelectorAll('.s3-wizard-panel').forEach((panel) => {
            panel.hidden = Number(panel.dataset.panel) !== wizard.step;
        });
        $('s3-wizard-prev').hidden = wizard.step === 0;
        $('s3-wizard-next').hidden = wizard.step === steps.length - 1;
        $('s3-wizard-save').hidden = wizard.step !== steps.length - 1;
        $('s3-wizard-test').hidden = wizard.step < 2;
        renderTestButton();
        if (wizard.step === steps.length - 1) renderReview();
        applyProviderDefaults();
        applyWebDAVControls();
        updateEndpointPreview();
    }

    function nextStep() {
        if (!validateCurrentStep()) return;
        wizard.step = Math.min(wizard.step + 1, steps.length - 1);
        clearAlert();
        renderWizard();
    }

    function prevStep() {
        wizard.step = Math.max(wizard.step - 1, 0);
        clearAlert();
        renderWizard();
    }

    function validateCurrentStep() {
        if (wizard.step === 0 && !value('s3-wizard-name')) {
            showAlert(t('s3_mount_name_required'), 'error');
            $('s3-wizard-name').focus();
            return false;
        }
        if (wizard.step === 3 && !validMountPath(value('s3-wizard-mount-path'))) {
            showAlert(t('s3_mount_path_invalid'), 'error');
            $('s3-wizard-mount-path').focus();
            return false;
        }
        return true;
    }

    function payload() {
        const skipBucket = wizard.provider === PROVIDER_R2 && $('s3-wizard-skip-bucket').checked;
        return {
            key: wizard.mount?.key || '',
            name: value('s3-wizard-name'),
            enabled: $('s3-wizard-enabled').checked,
            webdav_enabled: $('s3-wizard-webdav-enabled').checked,
            webdav_auth_enabled: $('s3-wizard-webdav-auth-enabled').checked,
            provider: wizard.provider,
            endpoint_url: value('s3-wizard-endpoint-url'),
            region: value('s3-wizard-region') || 'auto',
            path_style: $('s3-wizard-path-style').checked,
            account_id: value('s3-wizard-account-id'),
            bucket_name: skipBucket ? '' : value('s3-wizard-bucket-name'),
            root_prefix: value('s3-wizard-root-prefix'),
            mount_path: value('s3-wizard-mount-path') || DEFAULT_MOUNT,
            jurisdiction: value('s3-wizard-jurisdiction') || 'default',
            access_key_id: value('s3-wizard-access-key-id'),
            secret_access_key: $('s3-wizard-secret-access-key').value,
            webdav_username: value('s3-wizard-webdav-username'),
            webdav_password: $('s3-wizard-webdav-password').value,
        };
    }

    async function saveWizard() {
        if (!validateCurrentStep()) return;
        const btn = $('s3-wizard-save');
        setBusy(btn, true, t('saving'));
        try {
            const body = payload();
            const data = wizard.mode === 'create'
                ? await apiSend('/s3/mounts', 'POST', body)
                : await apiSend(`/s3/mounts/${encodeURIComponent(wizard.mount.key)}`, 'PUT', body);
            state.s3.settings = data;
            state.s3.activeKey = data.active_key || body.key;
            state.s3.path = '/';
            window.cfui.renderS3Settings?.(data);
            await window.cfui.fetchFeatures?.();
            if (data.enabled && window.cfui.s3ActiveMount?.()?.availability?.can_enable) await window.cfui.loadS3Files?.('/');
            window.cfui.closeDialog($('s3-wizard-dialog'));
            toast.ok(wizard.mode === 'create' ? t('s3_mount_created') : t('s3_settings_saved'));
        } catch (err) {
            showAlert(err.message, 'error');
            toast.err(t('s3_settings_save_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    async function testConnection() {
        const btn = $('s3-wizard-test');
        const webDAVTest = wizard.step >= 3;
        const successKey = webDAVTest ? 's3_webdav_test_success' : 's3_test_success';
        const failedKey = webDAVTest ? 's3_webdav_test_failed' : 's3_test_failed';
        const path = webDAVTest ? '/s3/webdav-test' : '/s3/test';
        setBusy(btn, true, t('testing'));
        try {
            const key = wizard.mode === 'edit' && wizard.mount?.key ? '?mount_key=' + encodeURIComponent(wizard.mount.key) : '';
            const data = await apiSend(path + key, 'POST', payload());
            const message = data.success ? t(successKey) : (data.message || t(failedKey));
            showAlert(message, data.success ? 'ok' : 'error');
            toast[data.success ? 'ok' : 'err'](message);
        } catch (err) {
            showAlert(err.message, 'error');
            toast.err(t(failedKey) + ': ' + err.message);
        } finally {
            setBusy(btn, false);
            renderTestButton();
        }
    }

    async function loadBuckets() {
        if (wizard.provider !== PROVIDER_R2) return;
        const btn = $('s3-wizard-refresh-buckets');
        setBusy(btn, true);
        try {
            const params = new URLSearchParams();
            if (wizard.mode === 'edit' && wizard.mount?.key) params.set('mount_key', wizard.mount.key);
            params.set('account_id', value('s3-wizard-account-id'));
            params.set('jurisdiction', value('s3-wizard-jurisdiction') || 'default');
            const data = await apiGet('/s3/buckets?' + params.toString());
            wizard.buckets = data.buckets || [];
            renderBucketSelect(wizard.buckets, value('s3-wizard-bucket-name'));
            showAlert(t('s3_buckets_loaded'), 'ok');
        } catch (err) {
            showAlert(t('s3_bucket_load_failed') + ': ' + err.message, 'error');
        } finally {
            setBusy(btn, false);
        }
    }

    function renderBucketSelect(buckets = [], selected = '') {
        const sel = $('s3-wizard-bucket-select');
        if (!sel) return;
        const current = selected || value('s3-wizard-bucket-name');
        sel.innerHTML = '';
        const empty = document.createElement('option');
        empty.value = '';
        empty.textContent = t('s3_bucket_choose');
        sel.appendChild(empty);
        const names = new Set();
        for (const bucket of buckets) {
            names.add(bucket.name);
            const opt = document.createElement('option');
            opt.value = bucket.name;
            opt.textContent = bucket.location ? `${bucket.name} (${bucket.location})` : bucket.name;
            sel.appendChild(opt);
        }
        if (current && !names.has(current)) {
            const opt = document.createElement('option');
            opt.value = current;
            opt.textContent = current;
            sel.appendChild(opt);
        }
        sel.value = current;
    }

    async function createBucket() {
        const btn = $('s3-wizard-create-bucket');
        const name = value('s3-wizard-create-bucket-name');
        if (!name) {
            showAlert(t('s3_bucket_name_required'), 'error');
            return;
        }
        setBusy(btn, true, t('creating'));
        try {
            const body = {
                mount_key: wizard.mode === 'edit' ? wizard.mount?.key || '' : '',
                account_id: value('s3-wizard-account-id'),
                jurisdiction: value('s3-wizard-jurisdiction') || 'default',
                name,
            };
            const bucket = await apiSend('/s3/buckets', 'POST', body);
            wizard.buckets = [...wizard.buckets.filter((b) => b.name !== bucket.name), bucket];
            setValue('s3-wizard-bucket-name', bucket.name);
            $('s3-wizard-skip-bucket').checked = false;
            renderBucketSelect(wizard.buckets, bucket.name);
            setValue('s3-wizard-create-bucket-name', '');
            setCreateBucketPanel(false);
            showAlert(t('s3_bucket_created'), 'ok');
        } catch (err) {
            showAlert(t('s3_bucket_create_failed') + ': ' + err.message, 'error');
        } finally {
            setBusy(btn, false);
        }
    }

    function setCreateBucketPanel(open) {
        const panel = $('s3-wizard-create-bucket-panel');
        const toggle = $('s3-wizard-toggle-create-bucket');
        if (!panel || !toggle) return;
        panel.hidden = !open;
        toggle.setAttribute('aria-expanded', String(open));
        if (open) $('s3-wizard-create-bucket-name')?.focus();
    }

    function applyR2EndpointPreset(force) {
        if (wizard.provider !== PROVIDER_R2) return;
        const accountID = value('s3-wizard-account-id');
        const endpoint = $('s3-wizard-endpoint-url');
        if (!endpoint || !accountID) return;
        if (force || !endpoint.value.trim() || endpoint.value.includes('.r2.cloudflarestorage.com')) {
            endpoint.value = r2EndpointFor(accountID, value('s3-wizard-jurisdiction') || 'default');
        }
        renderR2TokenPath();
    }

    function r2EndpointFor(accountID, jurisdiction) {
        if (jurisdiction === 'eu') return `https://${accountID}.eu.r2.cloudflarestorage.com`;
        if (jurisdiction === 'fedramp') return `https://${accountID}.fedramp.r2.cloudflarestorage.com`;
        return `https://${accountID}.r2.cloudflarestorage.com`;
    }

    function renderR2TokenPath() {
        const link = $('s3-wizard-r2-token-link');
        const placeholder = $('s3-wizard-r2-token-placeholder');
        if (!link || !placeholder) return;
        const accountID = value('s3-wizard-account-id');
        if (!accountID) {
            link.hidden = true;
            link.removeAttribute('href');
            link.textContent = '';
            placeholder.hidden = false;
            return;
        }
        const url = `https://dash.cloudflare.com/${encodeURIComponent(accountID)}/r2/api-tokens`;
        link.href = url;
        link.textContent = url;
        link.hidden = false;
        placeholder.hidden = true;
    }

    function updateEndpointPreview() {
        const origin = $('s3-wizard-webdav-origin');
        const path = $('s3-wizard-webdav-endpoint');
        if (origin) origin.value = window.cfui.s3WebDAVOrigin?.() || window.location.origin;
        if (path) path.value = value('s3-wizard-mount-path') || DEFAULT_MOUNT;
    }

    function applyWebDAVControls() {
        const endpointOn = $('s3-wizard-webdav-enabled')?.checked !== false;
        const auth = $('s3-wizard-webdav-auth-enabled');
        const authOn = endpointOn && auth?.checked !== false;
        if (auth) auth.disabled = !endpointOn;
        ['s3-wizard-webdav-username', 's3-wizard-webdav-password'].forEach((id) => {
            const el = $(id);
            if (el) el.disabled = !authOn;
        });
        const passwordToggle = $('s3-wizard-password-toggle');
        if (passwordToggle) passwordToggle.disabled = !authOn;
        const state = $('s3-wizard-password-state');
        if (state && !authOn) state.textContent = endpointOn ? t('s3_webdav_auth_disabled_help') : t('s3_webdav_disabled_help');
        else if (state) state.textContent = t(wizard.mode === 'edit' && wizard.mount?.password_set ? 's3_password_set' : 's3_password_not_set');
    }

    function renderReview() {
        const target = $('s3-wizard-review');
        if (!target) return;
        const body = payload();
        target.innerHTML = '';
        target.append(
            reviewRow(t('s3_provider_label'), window.cfui.s3ProviderLabel?.(body.provider) || body.provider),
            reviewRow(t('s3_config_status'), body.enabled ? t('enabled') : t('disabled')),
            reviewRow(t('s3_endpoint_url'), body.endpoint_url || t('s3_endpoint_required')),
            reviewRow(t('s3_bucket_name'), body.bucket_name || t('s3_wizard_bucket_skipped')),
            reviewRow(t('s3_webdav_status'), body.webdav_enabled ? t('enabled') : t('disabled')),
            reviewRow(t('s3_webdav_auth_status'), body.webdav_auth_enabled ? t('s3_webdav_auth_enabled') : t('s3_webdav_auth_disabled')),
            reviewRow(t('s3_webdav_endpoint'), window.cfui.s3WebDAVEndpointFor?.(body.mount_path) || body.mount_path),
            reviewRow(t('s3_webdav_username'), body.webdav_auth_enabled ? (body.webdav_username || t('s3_webdav_credentials_required')) : t('s3_webdav_auth_disabled'))
        );
    }

    function renderTestButton() {
        const btn = $('s3-wizard-test');
        if (!btn) return;
        const label = t(wizard.step >= 3 ? 's3_test_webdav' : 's3_test_s3');
        const text = btn.querySelector('.text');
        if (text) text.textContent = label;
        btn.setAttribute('aria-label', label);
    }

    function reviewRow(label, valueText) {
        const row = document.createElement('div');
        row.className = 's3-review-row';
        const k = document.createElement('span');
        k.textContent = label;
        const v = document.createElement('strong');
        v.textContent = valueText;
        row.append(k, v);
        return row;
    }

    function showAlert(message, kind = 'warn') {
        const el = $('s3-wizard-alert');
        if (!el) return;
        el.textContent = message || '';
        el.dataset.state = kind;
    }

    function clearAlert() {
        showAlert('');
    }

    function validMountPath(path) {
        return /^\/webdav\/[^/].*/.test((path || '').trim());
    }

    function value(id) {
        return ($(id)?.value || '').trim();
    }

    function setValue(id, val) {
        const el = $(id);
        if (el) el.value = val == null ? '' : String(val);
    }

    function bindVisibility(buttonID, inputID) {
        const btn = $(buttonID);
        const input = $(inputID);
        if (!btn || !input) return;
        btn.addEventListener('click', (e) => {
            e.preventDefault();
            setTokenVisible(input, btn, input.type === 'password');
        });
    }

    function wireS3Wizard() {
        if (wireS3Wizard.done) return;
        wireS3Wizard.done = true;
        document.querySelectorAll('.s3-provider-card').forEach((card) => {
            card.addEventListener('click', () => {
                setProvider(card.dataset.provider);
                applyProviderDefaults();
                clearAlert();
            });
        });
        document.querySelectorAll('.s3-step').forEach((step) => {
            step.addEventListener('click', () => {
                const idx = Number(step.dataset.step);
                if (idx <= wizard.step) {
                    wizard.step = idx;
                    clearAlert();
                    renderWizard();
                }
            });
        });
        $('s3-wizard-prev')?.addEventListener('click', prevStep);
        $('s3-wizard-next')?.addEventListener('click', nextStep);
        $('s3-wizard-save')?.addEventListener('click', saveWizard);
        $('s3-wizard-test')?.addEventListener('click', testConnection);
        $('s3-wizard-refresh-buckets')?.addEventListener('click', loadBuckets);
        $('s3-wizard-toggle-create-bucket')?.addEventListener('click', () => setCreateBucketPanel(!!$('s3-wizard-create-bucket-panel')?.hidden));
        $('s3-wizard-create-bucket')?.addEventListener('click', createBucket);
        $('s3-wizard-bucket-select')?.addEventListener('change', () => {
            setValue('s3-wizard-bucket-name', value('s3-wizard-bucket-select'));
            if (value('s3-wizard-bucket-select')) $('s3-wizard-skip-bucket').checked = false;
        });
        $('s3-wizard-skip-bucket')?.addEventListener('change', (e) => {
            if (e.target.checked) setValue('s3-wizard-bucket-name', '');
        });
        $('s3-wizard-account-id')?.addEventListener('input', renderR2TokenPath);
        $('s3-wizard-account-id')?.addEventListener('blur', () => applyR2EndpointPreset(true));
        $('s3-wizard-jurisdiction')?.addEventListener('change', () => applyR2EndpointPreset(true));
        $('s3-wizard-mount-path')?.addEventListener('input', updateEndpointPreview);
        $('s3-wizard-webdav-enabled')?.addEventListener('change', applyWebDAVControls);
        $('s3-wizard-webdav-auth-enabled')?.addEventListener('change', applyWebDAVControls);
        $('s3-wizard-copy-endpoint')?.addEventListener('click', () => {
            updateEndpointPreview();
            const endpoint = window.cfui.s3WebDAVEndpointFor?.(value('s3-wizard-mount-path')) || value('s3-wizard-mount-path');
            navigator.clipboard?.writeText(endpoint).then(() => toast.ok(t('copied_to_clipboard')), () => toast.err(t('copy_failed')));
        });
        bindVisibility('s3-wizard-secret-toggle', 's3-wizard-secret-access-key');
        bindVisibility('s3-wizard-password-toggle', 's3-wizard-webdav-password');
    }

    const ns = window.cfui;
    ns.openS3Wizard = openS3Wizard;
    ns.wireS3Wizard = wireS3Wizard;
})();
