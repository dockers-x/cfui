/* =========================================================================
   CloudFlared UI - S3 WebDAV
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, t, API_BASE, apiGet, apiSend, toast, setBusy, flashField, setTokenVisible } = window.cfui;

    const PROVIDER_R2 = 'cloudflare_r2';
    const DEFAULT_MOUNT = '/webdav/s3/';

    const availabilityKeys = {
        READY: 's3_ready',
        S3_ENDPOINT_REQUIRED: 's3_endpoint_required',
        S3_CREDENTIALS_REQUIRED: 's3_credentials_required',
        S3_MOUNT_PATH_INVALID: 's3_mount_path_invalid',
        BUCKET_REQUIRED: 's3_bucket_required',
        WEBDAV_CREDENTIALS_REQUIRED: 's3_webdav_credentials_required',
        S3_CONFIGURATION_INCOMPLETE: 's3_configuration_incomplete',
        S3_FILESYSTEM_UNAVAILABLE: 's3_filesystem_unavailable',
    };

    function s3AvailabilityText(availability) {
        if (!availability) return t('s3_configure_first');
        const key = availabilityKeys[availability.status];
        return key ? t(key) : (availability.message || t('s3_configure_first'));
    }

    async function fetchS3Settings() {
        try {
            const data = await apiGet('/s3/settings');
            state.s3.settings = data;
            state.s3.activeKey = data.active_key || data.mounts?.[0]?.key || 'default';
            renderS3Settings(data);
            if (data.enabled) await loadS3Files(state.s3.path || '/');
        } catch (err) {
            setS3Status('error', err.message);
        }
    }

    function renderS3Settings(settings) {
        if (!settings) return;
        renderMountList(settings);
        const mount = activeMount();
        renderMountForm(mount);

        const featureOn = !!settings.enabled || !!state.features?.s3_webdav;
        const ready = !!mount?.availability?.can_enable;
        setS3Status(ready ? 'ok' : 'warn', ready && featureOn ? t('s3_status_enabled') : ready ? t('s3_status_ready_to_enable') : t('s3_status_setup'));
        const notice = $('s3-status-message');
        if (notice) {
            notice.textContent = mount ? s3AvailabilityText(mount.availability) : t('s3_configure_first');
            notice.dataset.state = ready ? 'ok' : 'warn';
        }
        const filesReady = featureOn && ready;
        $('s3-file-disabled').hidden = filesReady;
        $('s3-file-manager').hidden = !filesReady;
    }

    function activeMount() {
        const settings = state.s3.settings;
        if (!settings?.mounts?.length) return null;
        const key = state.s3.activeKey || settings.active_key;
        return settings.mounts.find((m) => m.key === key) || settings.mounts[0];
    }

    function renderMountList(settings) {
        const list = $('s3-mount-list');
        if (!list) return;
        const activeKey = state.s3.activeKey || settings.active_key;
        list.innerHTML = '';
        for (const mount of settings.mounts || []) {
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 's3-mount-item';
            btn.setAttribute('role', 'option');
            btn.setAttribute('aria-selected', String(mount.key === activeKey));
            btn.addEventListener('click', () => selectMount(mount.key));

            const head = document.createElement('div');
            head.className = 's3-mount-item__head';
            const name = document.createElement('div');
            name.className = 's3-mount-item__name';
            name.textContent = mount.name || mount.key;
            const statePill = document.createElement('span');
            statePill.className = 'pill';
            statePill.dataset.state = mount.availability?.can_enable ? 'ok' : 'warn';
            statePill.innerHTML = '<span class="dot" aria-hidden="true"></span><span class="text"></span>';
            statePill.querySelector('.text').textContent = mount.enabled ? (mount.availability?.can_enable ? t('ready') : t('s3_status_setup')) : t('disabled');
            head.append(name, statePill);

            const meta = document.createElement('div');
            meta.className = 's3-mount-item__meta';
            meta.textContent = `${providerLabel(mount.provider)} · ${mount.mount_path || DEFAULT_MOUNT}`;
            const bucket = document.createElement('div');
            bucket.className = 's3-mount-item__meta';
            bucket.textContent = mount.bucket_name || t('s3_bucket_required');
            btn.append(head, meta, bucket);
            list.appendChild(btn);
        }
    }

    function providerLabel(provider) {
        return provider === PROVIDER_R2 ? t('s3_provider_r2') : t('s3_provider_generic');
    }

    async function selectMount(key) {
        state.s3.activeKey = key;
        state.s3.path = '/';
        renderS3Settings(state.s3.settings);
        try {
            const data = await apiSend('/s3/settings', 'POST', { enabled: !!state.features?.s3_webdav, active_key: key });
            state.s3.settings = data;
            state.s3.activeKey = data.active_key || key;
            renderS3Settings(data);
            if (data.enabled) await loadS3Files('/');
        } catch (err) {
            toast.err(err.message);
        }
    }

    function renderMountForm(mount) {
        if (!mount) return;
        $('s3-mount-name').value = mount.name || '';
        $('s3-provider').value = mount.provider || 'generic_s3';
        $('s3-endpoint-url').value = mount.endpoint_url || '';
        $('s3-region').value = mount.region || 'auto';
        $('s3-path-style').checked = mount.path_style !== false;
        $('s3-account-id').value = mount.account_id || '';
        $('s3-jurisdiction').value = mount.jurisdiction || 'default';
        $('s3-bucket-name').value = mount.bucket_name || '';
        $('s3-root-prefix').value = mount.root_prefix || '';
        $('s3-mount-path').value = mount.mount_path || DEFAULT_MOUNT;
        $('s3-access-key-id').value = mount.access_key_id || '';
        $('s3-secret-access-key').value = '';
        $('s3-secret-state').textContent = t(mount.secret_access_key_set ? 's3_secret_set' : 's3_secret_not_set');
        $('s3-webdav-username').value = mount.webdav_username || '';
        $('s3-webdav-password').value = '';
        $('s3-password-state').textContent = t(mount.password_set ? 's3_password_set' : 's3_password_not_set');
        updateWebDAVEndpoint();
        setCreateBucketPanel(false);
        renderBucketSelect(state.s3.buckets, mount.bucket_name);
        renderR2ManagementState(mount.r2_bucket_management);
        renderR2TokenPath();
        updateProviderUI();
    }

    function updateProviderUI() {
        const provider = $('s3-provider')?.value || 'generic_s3';
        const isR2 = provider === PROVIDER_R2;
        $('s3-r2-guide').hidden = !isR2;
        $('s3-r2-management-section').hidden = !isR2;
        if (isR2) {
            if (!$('s3-region').value.trim()) $('s3-region').value = 'auto';
            $('s3-path-style').checked = true;
            applyR2EndpointPreset();
        }
        renderR2TokenPath();
    }

    function applyR2EndpointPreset() {
        const endpoint = $('s3-endpoint-url');
        const accountID = $('s3-account-id')?.value.trim();
        if (!endpoint || endpoint.value.trim() || !accountID) return;
        endpoint.value = r2EndpointFor(accountID, $('s3-jurisdiction')?.value || 'default');
        renderR2TokenPath();
    }

    function r2EndpointFor(accountID, jurisdiction) {
        if (jurisdiction === 'eu') return `https://${accountID}.eu.r2.cloudflarestorage.com`;
        if (jurisdiction === 'fedramp') return `https://${accountID}.fedramp.r2.cloudflarestorage.com`;
        return `https://${accountID}.r2.cloudflarestorage.com`;
    }

    function webDAVEndpointFor(path) {
        const normalized = (path || DEFAULT_MOUNT).trim() || DEFAULT_MOUNT;
        try {
            return new URL(normalized, window.location.origin).toString();
        } catch {
            return normalized;
        }
    }

    function updateWebDAVEndpoint() {
        const origin = $('s3-webdav-origin');
        const path = $('s3-webdav-endpoint');
        if (origin) origin.value = window.location.origin;
        if (path) path.value = ($('s3-mount-path')?.value || activeMount()?.mount_path || DEFAULT_MOUNT).trim() || DEFAULT_MOUNT;
    }

    function renderR2TokenPath() {
        const link = $('s3-r2-token-link');
        const placeholder = $('s3-r2-token-placeholder');
        if (!link || !placeholder) return;
        const accountID = $('s3-account-id')?.value.trim();
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

    function renderR2ManagementState(management) {
        const el = $('s3-r2-management-state');
        if (!el) return;
        if (!management) {
            el.textContent = t('s3_r2_management_unavailable');
            return;
        }
        el.textContent = management.message || (management.enabled ? t('s3_r2_management_ready') : t('s3_r2_management_unavailable'));
    }

    function setS3Status(stateName, text) {
        const el = $('s3-status');
        if (!el) return;
        el.dataset.state = stateName;
        el.querySelector('.text').textContent = text;
    }

    async function createS3Mount() {
        const btn = $('s3-new-mount');
        setBusy(btn, true, t('creating'));
        try {
            const data = await apiSend('/s3/mounts', 'POST', {
                name: t('s3_new_mount_default'),
                enabled: true,
                provider: 'generic_s3',
                region: 'auto',
                path_style: true,
                mount_path: nextMountPath(),
            });
            state.s3.settings = data;
            state.s3.activeKey = data.active_key;
            renderS3Settings(data);
            toast.ok(t('s3_mount_created'));
        } catch (err) {
            toast.err(t('s3_mount_create_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    async function deleteS3Mount() {
        const mount = activeMount();
        if (!mount) return;
        const ok = await window.cfui.confirm({ title: t('s3_delete_mount_title'), message: t('s3_delete_mount_message', { name: mount.name || mount.key }), okText: t('delete') });
        if (!ok) return;
        const btn = $('s3-delete-mount');
        setBusy(btn, true);
        try {
            const data = await apiSend(`/s3/mounts/${encodeURIComponent(mount.key)}`, 'DELETE');
            state.s3.settings = data;
            state.s3.activeKey = data.active_key;
            state.s3.path = '/';
            renderS3Settings(data);
            toast.ok(t('s3_mount_deleted'));
        } catch (err) {
            toast.err(t('s3_mount_delete_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    function nextMountPath() {
        const used = new Set((state.s3.settings?.mounts || []).map((m) => m.mount_path));
        if (!used.has(DEFAULT_MOUNT)) return DEFAULT_MOUNT;
        for (let i = 2; i < 100; i += 1) {
            const path = `/webdav/s3-${i}/`;
            if (!used.has(path)) return path;
        }
        return `/webdav/s3-${Date.now()}/`;
    }

    async function loadS3Buckets() {
        const mount = activeMount();
        if (!mount || ($('s3-provider')?.value || '') !== PROVIDER_R2) return;
        const btn = $('s3-refresh-buckets');
        setBusy(btn, true);
        try {
            const data = await apiGet('/s3/buckets?mount_key=' + encodeURIComponent(mount.key));
            state.s3.buckets = data.buckets || [];
            renderBucketSelect(state.s3.buckets, $('s3-bucket-name')?.value || mount.bucket_name);
            toast.ok(t('s3_buckets_loaded'));
        } catch (err) {
            toast.err(t('s3_bucket_load_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    function renderBucketSelect(buckets = [], selected = '') {
        const sel = $('s3-bucket-select');
        if (!sel) return;
        const current = selected || $('s3-bucket-name')?.value || '';
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

    function setCreateBucketPanel(open) {
        const panel = $('s3-create-bucket-panel');
        const toggle = $('s3-toggle-create-bucket');
        if (!panel || !toggle) return;
        panel.hidden = !open;
        toggle.setAttribute('aria-expanded', String(open));
        if (open) $('s3-create-bucket-name')?.focus();
    }

    async function createS3Bucket() {
        const mount = activeMount();
        const btn = $('s3-create-bucket');
        const input = $('s3-create-bucket-name');
        const name = input.value.trim();
        if (!mount || !name) {
            toast.err(t('s3_bucket_name_required'));
            return;
        }
        setBusy(btn, true, t('creating'));
        try {
            const bucket = await apiSend('/s3/buckets?mount_key=' + encodeURIComponent(mount.key), 'POST', { mount_key: mount.key, name });
            state.s3.buckets = [...state.s3.buckets.filter((b) => b.name !== bucket.name), bucket];
            renderBucketSelect(state.s3.buckets, bucket.name);
            $('s3-bucket-name').value = bucket.name;
            input.value = '';
            setCreateBucketPanel(false);
            toast.ok(t('s3_bucket_created'));
        } catch (err) {
            toast.err(t('s3_bucket_create_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    function currentPayload() {
        const mount = activeMount();
        return {
            key: mount?.key || '',
            name: $('s3-mount-name').value.trim(),
            enabled: true,
            provider: $('s3-provider').value,
            endpoint_url: $('s3-endpoint-url').value.trim(),
            region: $('s3-region').value.trim(),
            path_style: $('s3-path-style').checked,
            account_id: $('s3-account-id').value.trim(),
            bucket_name: $('s3-bucket-name').value.trim(),
            root_prefix: $('s3-root-prefix').value.trim(),
            mount_path: $('s3-mount-path').value.trim(),
            jurisdiction: $('s3-jurisdiction').value,
            access_key_id: $('s3-access-key-id').value.trim(),
            secret_access_key: $('s3-secret-access-key').value,
            webdav_username: $('s3-webdav-username').value.trim(),
            webdav_password: $('s3-webdav-password').value,
        };
    }

    async function saveS3Settings() {
        const mount = activeMount();
        if (!mount) return;
        const btn = $('s3-save-settings');
        setBusy(btn, true, t('saving'));
        try {
            const data = await apiSend(`/s3/mounts/${encodeURIComponent(mount.key)}`, 'PUT', currentPayload());
            state.s3.settings = data;
            state.s3.activeKey = data.active_key || mount.key;
            renderS3Settings(data);
            await window.cfui.fetchFeatures();
            flashField('s3-save-settings');
            toast.ok(t('s3_settings_saved'));
        } catch (err) {
            toast.err(t('s3_settings_save_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    async function testS3Connection() {
        const mount = activeMount();
        if (!mount) return;
        const btn = $('s3-test-connection');
        setBusy(btn, true, t('testing'));
        try {
            const data = await apiSend('/s3/test?mount_key=' + encodeURIComponent(mount.key), 'POST', currentPayload());
            const notice = $('s3-status-message');
            if (notice) {
                notice.textContent = data.message || (data.success ? t('s3_test_success') : t('s3_test_failed'));
                notice.dataset.state = data.success ? 'ok' : 'error';
            }
            toast[data.success ? 'ok' : 'err'](data.message || (data.success ? t('s3_test_success') : t('s3_test_failed')));
        } catch (err) {
            toast.err(t('s3_test_failed') + ': ' + err.message);
        } finally {
            setBusy(btn, false);
        }
    }

    async function loadS3Files(path = state.s3.path || '/') {
        const mount = activeMount();
        if (!mount || !state.s3.settings?.enabled || !mount.availability?.can_enable) {
            renderS3Settings(state.s3.settings);
            return;
        }
        state.s3.loading = true;
        try {
            const data = await apiGet('/s3/files?mount_key=' + encodeURIComponent(mount.key) + '&path=' + encodeURIComponent(path));
            state.s3.path = data.path || '/';
            state.s3.files = data.entries || [];
            renderS3Files(data);
        } catch (err) {
            toast.err(t('s3_files_load_failed') + ': ' + err.message);
        } finally {
            state.s3.loading = false;
        }
    }

    function renderS3Files(data = { path: '/', parent: '', entries: [] }) {
        renderBreadcrumb(data.path || '/');
        const list = $('s3-file-list');
        if (!list) return;
        list.innerHTML = '';
        const entries = data.entries || [];
        if (!entries.length) {
            const empty = document.createElement('div');
            empty.className = 'empty';
            empty.textContent = t('s3_files_empty');
            list.appendChild(empty);
            return;
        }
        for (const entry of entries) {
            const row = document.createElement('div');
            row.className = 's3-file-row';
            const main = document.createElement('div');
            main.className = 's3-file-main';
            const name = document.createElement('div');
            name.className = 's3-file-name';
            name.textContent = (entry.is_dir ? '/ ' : '') + entry.name;
            const meta = document.createElement('div');
            meta.className = 's3-file-meta';
            meta.textContent = entry.is_dir ? t('s3_folder') : `${formatBytes(entry.size)} · ${formatDate(entry.mod_time)}`;
            main.append(name, meta);

            const actions = document.createElement('div');
            actions.className = 's3-file-actions';
            if (entry.is_dir) actions.append(actionButton(t('open'), () => loadS3Files(entry.path)));
            else actions.append(actionLink(t('download'), `${API_BASE}/s3/files/download?${fileQuery(entry.path)}`));
            actions.append(actionButton(t('rename'), () => renameS3Path(entry)));
            actions.append(actionButton(t('delete'), () => deleteS3Path(entry), 'btn--ghost'));
            row.append(main, actions);
            list.appendChild(row);
        }
    }

    function renderBreadcrumb(path) {
        const el = $('s3-breadcrumb');
        if (!el) return;
        el.innerHTML = '';
        const root = document.createElement('button');
        root.type = 'button';
        root.textContent = '/';
        root.addEventListener('click', () => loadS3Files('/'));
        el.appendChild(root);
        const parts = path.split('/').filter(Boolean);
        let acc = '';
        for (const part of parts) {
            acc += '/' + part;
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.textContent = part;
            const target = acc;
            btn.addEventListener('click', () => loadS3Files(target));
            el.appendChild(btn);
        }
    }

    function actionButton(label, fn, extra = '') {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = `btn btn--sm ${extra}`.trim();
        btn.textContent = label;
        btn.addEventListener('click', fn);
        return btn;
    }

    function actionLink(label, href) {
        const a = document.createElement('a');
        a.className = 'btn btn--sm';
        a.href = href;
        a.textContent = label;
        return a;
    }

    async function uploadS3File(file) {
        const mount = activeMount();
        if (!mount || !file) return;
        const target = joinPath(state.s3.path || '/', file.name);
        try {
            const res = await fetch(`${API_BASE}/s3/files/${encodeObjectPath(target)}?mount_key=${encodeURIComponent(mount.key)}`, { method: 'PUT', body: file });
            if (!res.ok) throw new Error(await responseError(res));
            toast.ok(t('s3_upload_done'));
            await loadS3Files(state.s3.path);
        } catch (err) {
            toast.err(t('s3_upload_failed') + ': ' + err.message);
        }
    }

    async function createS3Folder() {
        const mount = activeMount();
        const name = window.prompt(t('s3_new_folder_prompt'));
        if (!mount || !name) return;
        try {
            await apiSend('/s3/files/mkdir', 'POST', { mount_key: mount.key, path: joinPath(state.s3.path || '/', name.trim()) });
            toast.ok(t('s3_folder_created'));
            await loadS3Files(state.s3.path);
        } catch (err) {
            toast.err(t('s3_folder_create_failed') + ': ' + err.message);
        }
    }

    async function renameS3Path(entry) {
        const mount = activeMount();
        const nextName = window.prompt(t('s3_rename_prompt'), entry.name);
        if (!mount || !nextName || nextName === entry.name) return;
        try {
            await apiSend('/s3/files/rename', 'POST', { mount_key: mount.key, from: entry.path, to: joinPath(parentPath(entry.path), nextName.trim()) });
            toast.ok(t('s3_renamed'));
            await loadS3Files(state.s3.path);
        } catch (err) {
            toast.err(t('s3_rename_failed') + ': ' + err.message);
        }
    }

    async function deleteS3Path(entry) {
        const mount = activeMount();
        const ok = await window.cfui.confirm({ title: t('s3_delete_title'), message: t('s3_delete_message', { name: entry.name }), okText: t('delete') });
        if (!mount || !ok) return;
        try {
            const res = await fetch(`${API_BASE}/s3/files/${encodeObjectPath(entry.path)}?mount_key=${encodeURIComponent(mount.key)}`, { method: 'DELETE' });
            if (!res.ok) throw new Error(await responseError(res));
            toast.ok(t('s3_deleted'));
            await loadS3Files(state.s3.path);
        } catch (err) {
            toast.err(t('s3_delete_failed') + ': ' + err.message);
        }
    }

    function fileQuery(path) {
        const mount = activeMount();
        return `mount_key=${encodeURIComponent(mount?.key || '')}&path=${encodeURIComponent(path)}`;
    }

    function encodeObjectPath(path) {
        return path.split('/').filter(Boolean).map(encodeURIComponent).join('/');
    }

    function joinPath(base, name) {
        base = base || '/';
        name = (name || '').replace(/^\/+|\/+$/g, '');
        return base === '/' ? '/' + name : base.replace(/\/+$/g, '') + '/' + name;
    }

    function parentPath(path) {
        const parts = path.split('/').filter(Boolean);
        parts.pop();
        return parts.length ? '/' + parts.join('/') : '/';
    }

    async function responseError(res) {
        try { const data = await res.json(); return data.error || res.statusText; }
        catch { return res.statusText; }
    }

    function formatBytes(size) {
        if (!Number.isFinite(size) || size < 0) return '0 B';
        const units = ['B', 'KB', 'MB', 'GB', 'TB'];
        let value = size, unit = 0;
        while (value >= 1024 && unit < units.length - 1) { value /= 1024; unit += 1; }
        return `${value >= 10 || unit === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[unit]}`;
    }

    function formatDate(value) {
        const d = new Date(value);
        if (Number.isNaN(d.getTime())) return '';
        return d.toLocaleString();
    }

    function wireS3() {
        $('s3-new-mount')?.addEventListener('click', createS3Mount);
        $('s3-delete-mount')?.addEventListener('click', deleteS3Mount);
        $('s3-provider')?.addEventListener('change', () => updateProviderUI());
        $('s3-account-id')?.addEventListener('input', renderR2TokenPath);
        $('s3-account-id')?.addEventListener('blur', applyR2EndpointPreset);
        $('s3-jurisdiction')?.addEventListener('change', () => {
            const endpoint = $('s3-endpoint-url');
            const accountID = $('s3-account-id')?.value.trim();
            if (endpoint && accountID && $('s3-provider')?.value === PROVIDER_R2) endpoint.value = r2EndpointFor(accountID, $('s3-jurisdiction').value);
            renderR2TokenPath();
        });
        $('s3-mount-path')?.addEventListener('input', () => {
            updateWebDAVEndpoint();
        });
        $('s3-bucket-select')?.addEventListener('change', () => {
            $('s3-bucket-name').value = $('s3-bucket-select').value;
        });
        $('s3-refresh-buckets')?.addEventListener('click', loadS3Buckets);
        $('s3-toggle-create-bucket')?.addEventListener('click', () => {
            const panel = $('s3-create-bucket-panel');
            setCreateBucketPanel(!!panel?.hidden);
        });
        $('s3-create-bucket')?.addEventListener('click', createS3Bucket);
        $('s3-save-settings')?.addEventListener('click', saveS3Settings);
        $('s3-test-connection')?.addEventListener('click', testS3Connection);
        $('s3-refresh-files')?.addEventListener('click', () => loadS3Files(state.s3.path || '/'));
        $('s3-new-folder')?.addEventListener('click', createS3Folder);
        $('s3-upload-input')?.addEventListener('change', (e) => {
            const file = e.target.files?.[0];
            e.target.value = '';
            uploadS3File(file);
        });
        $('s3-copy-endpoint')?.addEventListener('click', () => {
            updateWebDAVEndpoint();
            const v = webDAVEndpointFor($('s3-webdav-endpoint')?.value || DEFAULT_MOUNT);
            navigator.clipboard?.writeText(v).then(() => toast.ok(t('copied_to_clipboard')), () => toast.err(t('copy_failed')));
        });
        bindVisibility('s3-secret-toggle', 's3-secret-access-key');
        bindVisibility('s3-password-toggle', 's3-webdav-password');
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

    const ns = window.cfui;
    ns.s3AvailabilityText = s3AvailabilityText;
    ns.fetchS3Settings = fetchS3Settings;
    ns.loadS3Buckets = loadS3Buckets;
    ns.loadS3Files = loadS3Files;
    ns.wireS3 = wireS3;
})();
