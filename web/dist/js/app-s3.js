/* =========================================================================
   CloudFlared UI - S3 WebDAV overview and file browser
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, t, API_BASE, apiGet, apiSend, toast, setBusy } = window.cfui;

    const PROVIDER_R2 = 'cloudflare_r2';
    const DEFAULT_MOUNT = '/webdav/s3/';

    const availabilityKeys = {
        READY: 's3_ready',
        S3_ENDPOINT_REQUIRED: 's3_endpoint_required',
        S3_CREDENTIALS_REQUIRED: 's3_credentials_required',
        S3_MOUNT_PATH_INVALID: 's3_mount_path_invalid',
        BUCKET_REQUIRED: 's3_bucket_required',
        WEBDAV_CREDENTIALS_REQUIRED: 's3_webdav_credentials_required',
        WEBDAV_DISABLED: 's3_webdav_disabled',
        WEBDAV_AUTH_DISABLED: 's3_webdav_auth_disabled',
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
            const mount = activeMount();
            if (canBrowseFiles(data, mount)) await loadS3Files(state.s3.path || '/');
        } catch (err) {
            setS3Status('error', err.message);
        }
    }

    function renderS3Settings(settings) {
        if (!settings) return;
        renderMountList(settings);
        const mount = activeMount();
        const ready = !!mount?.availability?.can_enable;
        const featureOn = !!settings.enabled || !!state.features?.s3_webdav;
        setS3Status(ready ? 'ok' : 'warn', ready && featureOn ? t('s3_status_enabled') : ready ? t('s3_status_ready_to_enable') : t('s3_status_setup'));
        const notice = $('s3-status-message');
        if (notice) {
            notice.textContent = mount ? s3AvailabilityText(mount.availability) : t('s3_configure_first');
            notice.dataset.state = ready ? 'ok' : 'warn';
        }
        if (canBrowseFiles(settings, mount) && state.s3.fileData?.mountKey === mount.key) {
            renderS3Files(state.s3.fileData);
        } else if (mount?.key === state.s3.activeKey) {
            renderS3Files({ mountKey: mount.key, path: state.s3.path || '/', parent: '', entries: [] });
        }
    }

    function activeMount() {
        const settings = state.s3.settings;
        if (!settings?.mounts?.length) return null;
        const key = state.s3.activeKey || settings.active_key;
        return settings.mounts.find((m) => m.key === key) || settings.mounts[0];
    }

    function canBrowseFiles(settings, mount) {
        return !!settings?.enabled && !!mount?.enabled && !!mount?.availability?.can_enable;
    }

    function renderMountList(settings) {
        const list = $('s3-mount-list');
        if (!list) return;
        const mounts = settings.mounts || [];
        const activeKey = state.s3.activeKey || settings.active_key;
        list.innerHTML = '';
        if (!mounts.length) {
            const empty = document.createElement('div');
            empty.className = 'empty';
            empty.textContent = t('s3_empty_mounts');
            list.appendChild(empty);
            return;
        }
        for (const mount of mounts) list.appendChild(mountItem(settings, mount, mount.key === activeKey));
    }

    function mountItem(settings, mount, active) {
        const item = document.createElement('article');
        item.className = 's3-mount-item';
        item.dataset.active = String(active);
        item.setAttribute('role', 'listitem');

        const summary = document.createElement('button');
        summary.type = 'button';
        summary.className = 's3-mount-summary';
        summary.setAttribute('aria-expanded', String(active));
        summary.addEventListener('click', () => selectMount(mount.key));

        const name = document.createElement('span');
        name.className = 's3-mount-item__name';
        name.textContent = mount.name || mount.key;
        const chevron = document.createElement('span');
        chevron.className = 's3-mount-chevron';
        chevron.innerHTML = chevronIcon();
        summary.append(name, chevron);
        item.appendChild(summary);

        if (active) item.appendChild(mountDetail(settings, mount));
        return item;
    }

    function mountDetail(settings, mount) {
        const detail = document.createElement('div');
        detail.className = 's3-mount-detail';

        const top = document.createElement('div');
        top.className = 's3-mount-detail__top';
        const identity = document.createElement('div');
        identity.className = 's3-mount-identity';
        const provider = document.createElement('div');
        provider.className = 's3-mount-item__meta';
        provider.textContent = `${providerLabel(mount.provider)} · ${mount.bucket_name || t('s3_bucket_required')}${mount.root_prefix ? '/' + mount.root_prefix : ''}`;
        const badges = document.createElement('div');
        badges.className = 's3-mount-badges';
        badges.append(
            badge(mount.access_key_id && mount.secret_access_key_set ? t('s3_s3_keys_ready') : t('s3_s3_keys_missing'), mount.access_key_id && mount.secret_access_key_set ? 'ok' : 'warn'),
            badge(webDAVStatusLabel(mount), webDAVStatusState(mount))
        );
        identity.append(provider, badges);

        const actions = document.createElement('div');
        actions.className = 's3-mount-actions';
        const edit = iconTextButton(t('edit'), editIcon(), 'btn--sm');
        edit.addEventListener('click', () => window.cfui.openS3Wizard?.({ mode: 'edit', mount }));
        const del = iconTextButton(t('delete'), trashIcon(), 'btn--sm btn--danger');
        del.addEventListener('click', () => deleteS3Mount(mount.key, del));
        actions.append(edit, del);
        top.append(identity, actions);

        const endpoint = copyField(t('s3_webdav_endpoint'), webDAVEndpointFor(mount.mount_path || DEFAULT_MOUNT), () => copyMountEndpoint(mount));
        const toggles = document.createElement('div');
        toggles.className = 's3-state-grid';
        toggles.append(
            toggleStateCard({
                label: t('s3_config_status'),
                checked: !!mount.enabled,
                state: mount.enabled && mount.availability?.can_enable ? 'ok' : mount.enabled ? 'warn' : 'neutral',
                message: mount.enabled ? s3AvailabilityText(mount.availability) : t('s3_config_disabled'),
                onChange: (checked, control) => updateS3MountState(mount, { enabled: checked }, control),
            }),
            toggleStateCard({
                label: t('s3_webdav_status'),
                checked: !!mount.webdav_enabled,
                state: mount.webdav_enabled && mount.webdav_availability?.can_enable ? 'ok' : mount.webdav_enabled ? 'warn' : 'neutral',
                message: mount.webdav_enabled ? s3AvailabilityText(mount.webdav_availability) : t('s3_webdav_disabled'),
                onChange: (checked, control) => updateS3MountState(mount, { webdav_enabled: checked }, control),
            }),
            toggleStateCard({
                label: t('s3_webdav_auth_status'),
                checked: !!mount.webdav_auth_enabled,
                disabled: !mount.webdav_enabled,
                state: !mount.webdav_auth_enabled ? 'warn' : mount.webdav_username && mount.password_set ? 'ok' : 'warn',
                message: !mount.webdav_auth_enabled ? t('s3_webdav_auth_disabled') : mount.webdav_username && mount.password_set ? t('s3_webdav_login_ready') : t('s3_webdav_login_missing'),
                onChange: (checked, control) => updateS3MountState(mount, { webdav_auth_enabled: checked }, control),
            })
        );

        detail.append(top, endpoint, toggles, fileBrowser(settings, mount));
        return detail;
    }

    function fileBrowser(settings, mount) {
        const ready = canBrowseFiles(settings, mount);
        const wrap = document.createElement('section');
        wrap.className = 's3-file-browser';

        const toolbar = document.createElement('div');
        toolbar.className = 's3-file-toolbar';
        const title = document.createElement('div');
        title.className = 's3-file-toolbar__title';
        const heading = document.createElement('div');
        heading.className = 'section-title';
        heading.textContent = t('s3_files');
        const subtitle = document.createElement('div');
        subtitle.className = 'section-subtitle';
        subtitle.textContent = t('s3_files_help');
        title.append(heading, subtitle);

        const tools = document.createElement('div');
        tools.className = 'btn-row';
        const upload = document.createElement('label');
        upload.className = 'btn btn--sm';
        upload.setAttribute('for', 's3-upload-input');
        upload.innerHTML = uploadIcon() + `<span>${escapeHTML(t('upload'))}</span>`;
        const folder = iconTextButton(t('s3_new_folder'), plusIcon(), 'btn--sm btn--primary');
        folder.addEventListener('click', createS3Folder);
        const refresh = iconButton('refresh', refreshIcon());
        refresh.addEventListener('click', () => loadS3Files(state.s3.path || '/'));
        tools.append(upload, folder, refresh);
        toolbar.append(title);
        if (ready) toolbar.appendChild(tools);
        wrap.appendChild(toolbar);

        if (!ready) {
            const disabled = document.createElement('div');
            disabled.className = 'disabled-panel';
            const h = document.createElement('h3');
            h.textContent = t('s3_files_disabled_title');
            const p = document.createElement('p');
            p.textContent = mount.enabled ? s3AvailabilityText(mount.availability) : t('s3_files_disabled_help');
            disabled.append(h, p);
            wrap.appendChild(disabled);
            return wrap;
        }

        const manager = document.createElement('div');
        manager.className = 's3-file-manager';
        const crumb = document.createElement('div');
        crumb.className = 's3-breadcrumb';
        crumb.id = 's3-breadcrumb';
        const tableWrap = document.createElement('div');
        tableWrap.className = 's3-file-table-wrap';
        tableWrap.innerHTML = `
            <table class="s3-file-table">
                <thead>
                    <tr>
                        <th scope="col">${escapeHTML(t('s3_file_object'))}</th>
                        <th scope="col">${escapeHTML(t('s3_file_type'))}</th>
                        <th scope="col">${escapeHTML(t('s3_file_size'))}</th>
                        <th scope="col">${escapeHTML(t('s3_file_modified'))}</th>
                        <th scope="col" class="s3-file-actions-head">${escapeHTML(t('s3_file_actions'))}</th>
                    </tr>
                </thead>
                <tbody id="s3-file-list"></tbody>
            </table>`;
        manager.append(crumb, tableWrap);
        wrap.appendChild(manager);
        return wrap;
    }

    function toggleStateCard({ label, checked, state: stateName, message, disabled = false, onChange }) {
        const card = document.createElement('div');
        card.className = 's3-state-card';
        card.dataset.state = stateName || 'neutral';

        const copy = document.createElement('div');
        copy.className = 's3-state-card__copy';
        const title = document.createElement('div');
        title.className = 's3-state-card__title';
        title.textContent = label;
        const desc = document.createElement('div');
        desc.className = 's3-state-card__desc';
        desc.textContent = message || '';
        copy.append(title, desc);

        const toggle = document.createElement('label');
        toggle.className = 'toggle';
        const input = document.createElement('input');
        input.type = 'checkbox';
        input.checked = checked;
        input.disabled = disabled;
        input.setAttribute('aria-label', label);
        input.setAttribute('title', label);
        const track = document.createElement('span');
        track.className = 'track';
        toggle.append(input, track);
        input.addEventListener('change', () => onChange?.(input.checked, input));
        card.append(copy, toggle);
        return card;
    }

    function copyField(label, value, onCopy) {
        const endpoint = document.createElement('div');
        endpoint.className = 's3-mount-endpoint';
        const endpointLabel = document.createElement('div');
        endpointLabel.className = 's3-mount-label';
        endpointLabel.textContent = label;
        const endpointBox = document.createElement('div');
        endpointBox.className = 's3-copy-line';
        const endpointInput = document.createElement('input');
        endpointInput.type = 'text';
        endpointInput.className = 'input';
        endpointInput.readOnly = true;
        endpointInput.value = value;
        endpointInput.setAttribute('aria-label', label);
        const copy = iconButton('copy_webdav_endpoint', copyIcon());
        copy.addEventListener('click', onCopy);
        endpointBox.append(endpointInput, copy);
        endpoint.append(endpointLabel, endpointBox);
        return endpoint;
    }

    function badge(text, stateName) {
        const el = document.createElement('span');
        el.className = 's3-badge';
        el.dataset.state = stateName;
        el.textContent = text;
        return el;
    }

    function providerLabel(provider) {
        return provider === PROVIDER_R2 ? t('s3_provider_r2') : t('s3_provider_generic');
    }

    function webDAVStatusLabel(mount) {
        if (!mount.webdav_enabled) return t('s3_webdav_disabled');
        if (!mount.webdav_auth_enabled) return t('s3_webdav_auth_disabled');
        return mount.webdav_username && mount.password_set ? t('s3_webdav_login_ready') : t('s3_webdav_login_missing');
    }

    function webDAVStatusState(mount) {
        if (!mount.webdav_enabled) return 'neutral';
        if (!mount.webdav_auth_enabled) return 'warn';
        return mount.webdav_username && mount.password_set ? 'ok' : 'warn';
    }

    function setS3Status(stateName, text) {
        const el = $('s3-status');
        if (!el) return;
        el.dataset.state = stateName;
        el.querySelector('.text').textContent = text;
    }

    async function selectMount(key) {
        if (!key) return;
        state.s3.activeKey = key;
        state.s3.path = '/';
        state.s3.fileData = null;
        renderS3Settings(state.s3.settings);
        try {
            const data = await apiSend('/s3/settings', 'POST', { enabled: !!state.s3.settings?.enabled || !!state.features?.s3_webdav, active_key: key });
            state.s3.settings = data;
            state.s3.activeKey = data.active_key || key;
            renderS3Settings(data);
            const mount = activeMount();
            if (canBrowseFiles(data, mount)) await loadS3Files('/');
        } catch (err) {
            toast.err(err.message);
        }
    }

    async function updateS3MountState(mount, overrides, control) {
        if (!mount) return;
        if (control) control.disabled = true;
        try {
            const data = await apiSend(`/s3/mounts/${encodeURIComponent(mount.key)}`, 'PUT', mountPayload(mount, overrides));
            state.s3.settings = data;
            state.s3.activeKey = data.active_key || mount.key;
            const next = activeMount();
            if (!canBrowseFiles(data, next)) state.s3.fileData = null;
            renderS3Settings(data);
            if (canBrowseFiles(data, next)) await loadS3Files(state.s3.path || '/');
            toast.ok(t('s3_settings_saved'));
        } catch (err) {
            toast.err(t('s3_settings_save_failed') + ': ' + err.message);
            renderS3Settings(state.s3.settings);
        } finally {
            if (control) control.disabled = false;
        }
    }

    function mountPayload(mount, overrides = {}) {
        return {
            key: mount.key || '',
            name: mount.name || '',
            enabled: overrides.enabled ?? !!mount.enabled,
            webdav_enabled: overrides.webdav_enabled ?? !!mount.webdav_enabled,
            webdav_auth_enabled: overrides.webdav_auth_enabled ?? !!mount.webdav_auth_enabled,
            provider: mount.provider || 'generic_s3',
            endpoint_url: mount.endpoint_url || '',
            region: mount.region || 'auto',
            path_style: mount.path_style !== false,
            account_id: mount.account_id || '',
            bucket_name: mount.bucket_name || '',
            root_prefix: mount.root_prefix || '',
            mount_path: mount.mount_path || DEFAULT_MOUNT,
            jurisdiction: mount.jurisdiction || 'default',
            access_key_id: mount.access_key_id || '',
            secret_access_key: '',
            webdav_username: mount.webdav_username || '',
            webdav_password: '',
        };
    }

    async function deleteS3Mount(key, btn) {
        const mount = (state.s3.settings?.mounts || []).find((m) => m.key === key);
        if (!mount) return;
        const ok = await window.cfui.confirm({
            title: t('s3_delete_mount_title'),
            message: t('s3_delete_mount_message', { name: mount.name || mount.key }),
            okText: t('delete'),
        });
        if (!ok) return;
        setBusy(btn, true);
        try {
            const data = await apiSend(`/s3/mounts/${encodeURIComponent(mount.key)}`, 'DELETE');
            state.s3.settings = data;
            state.s3.activeKey = data.active_key;
            state.s3.path = '/';
            state.s3.fileData = null;
            renderS3Settings(data);
            const next = activeMount();
            if (canBrowseFiles(data, next)) await loadS3Files('/');
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
            const p = `/webdav/s3-${i}/`;
            if (!used.has(p)) return p;
        }
        return `/webdav/s3-${Date.now()}/`;
    }

    function webDAVEndpointFor(path) {
        const normalized = (path || DEFAULT_MOUNT).trim() || DEFAULT_MOUNT;
        try {
            return new URL(normalized, window.location.origin).toString();
        } catch {
            return normalized;
        }
    }

    function copyMountEndpoint(mount) {
        const value = webDAVEndpointFor(mount.mount_path || DEFAULT_MOUNT);
        navigator.clipboard?.writeText(value).then(() => toast.ok(t('copied_to_clipboard')), () => toast.err(t('copy_failed')));
    }

    async function loadS3Files(path = state.s3.path || '/') {
        const mount = activeMount();
        if (!canBrowseFiles(state.s3.settings, mount)) {
            state.s3.fileData = null;
            renderS3Settings(state.s3.settings);
            return;
        }
        state.s3.loading = true;
        renderS3Files({ mountKey: mount.key, path, parent: parentPath(path), entries: [] });
        try {
            const data = await apiGet('/s3/files?mount_key=' + encodeURIComponent(mount.key) + '&path=' + encodeURIComponent(path));
            const fileData = {
                mountKey: mount.key,
                path: data.path || '/',
                parent: data.parent || '',
                entries: sanitizeEntries(data.entries || []),
            };
            state.s3.path = fileData.path;
            state.s3.files = fileData.entries;
            state.s3.fileData = fileData;
            state.s3.loading = false;
            renderS3Files(fileData);
        } catch (err) {
            state.s3.loading = false;
            renderS3Files(state.s3.fileData || { mountKey: mount.key, path: state.s3.path || '/', parent: '', entries: [] });
            toast.err(t('s3_files_load_failed') + ': ' + err.message);
        } finally {
            state.s3.loading = false;
        }
    }

    function renderS3Files(data = { path: '/', parent: '', entries: [] }) {
        renderBreadcrumb(data.path || '/');
        const body = $('s3-file-list');
        if (!body) return;
        body.innerHTML = '';
        if (state.s3.loading) {
            body.appendChild(emptyFileRow(t('s3_files_loading')));
            return;
        }
        const entries = sanitizeEntries(data.entries || []);
        if (!entries.length) {
            body.appendChild(emptyFileRow(t('s3_files_empty')));
            return;
        }
        for (const entry of entries) body.appendChild(fileRow(entry));
    }

    function renderBreadcrumb(path) {
        const el = $('s3-breadcrumb');
        if (!el) return;
        const mount = activeMount();
        el.innerHTML = '';
        const root = document.createElement('button');
        root.type = 'button';
        root.textContent = breadcrumbRootLabel(mount);
        root.addEventListener('click', () => loadS3Files('/'));
        el.appendChild(root);
        const parts = path.split('/').filter(Boolean);
        let acc = '';
        for (const part of parts) {
            const sep = document.createElement('span');
            sep.className = 's3-breadcrumb-sep';
            sep.textContent = '/';
            el.appendChild(sep);
            acc += '/' + part;
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.textContent = part;
            const target = acc;
            btn.addEventListener('click', () => loadS3Files(target));
            el.appendChild(btn);
        }
    }

    function breadcrumbRootLabel(mount) {
        if (!mount?.bucket_name) return '/';
        return mount.root_prefix ? `${mount.bucket_name}/${mount.root_prefix}` : mount.bucket_name;
    }

    function fileRow(entry) {
        const row = document.createElement('tr');

        const object = document.createElement('td');
        object.dataset.label = t('s3_file_object');
        const objectControl = entry.is_dir ? document.createElement('button') : document.createElement('a');
        objectControl.className = 's3-file-object';
        if (entry.is_dir) {
            objectControl.type = 'button';
            objectControl.addEventListener('click', () => loadS3Files(entry.path));
        } else {
            objectControl.href = `${API_BASE}/s3/files/download?${fileQuery(entry.path)}`;
        }
        objectControl.innerHTML = (entry.is_dir ? folderIcon() : fileIcon()) + `<span>${escapeHTML(entry.name)}</span>`;
        object.appendChild(objectControl);

        const type = document.createElement('td');
        type.dataset.label = t('s3_file_type');
        type.textContent = fileType(entry);
        const size = document.createElement('td');
        size.dataset.label = t('s3_file_size');
        size.textContent = entry.is_dir ? '--' : formatBytes(entry.size);
        const modified = document.createElement('td');
        modified.dataset.label = t('s3_file_modified');
        modified.textContent = entry.is_dir ? '--' : formatDate(entry.mod_time);

        const actions = document.createElement('td');
        actions.dataset.label = t('s3_file_actions');
        const actionWrap = document.createElement('div');
        actionWrap.className = 's3-file-actions';
        if (!entry.is_dir) actionWrap.append(actionIconLink('download', downloadIcon(), `${API_BASE}/s3/files/download?${fileQuery(entry.path)}`));
        actionWrap.append(
            actionIconButton('rename', editIcon(), () => renameS3Path(entry)),
            actionIconButton('delete', trashIcon(), () => deleteS3Path(entry), 'danger')
        );
        actions.appendChild(actionWrap);
        row.append(object, type, size, modified, actions);
        return row;
    }

    function emptyFileRow(text) {
        const row = document.createElement('tr');
        const cell = document.createElement('td');
        cell.colSpan = 5;
        const empty = document.createElement('div');
        empty.className = 's3-file-empty';
        empty.textContent = text;
        cell.appendChild(empty);
        row.appendChild(cell);
        return row;
    }

    function sanitizeEntries(entries) {
        return entries.filter((entry) => {
            const name = (entry?.name || '').trim();
            const path = (entry?.path || '').trim();
            return name && name !== '/' && name !== '.' && path !== '//' && path !== '/.';
        });
    }

    function iconButton(labelKey, svg) {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'icon-btn';
        btn.setAttribute('aria-label', t(labelKey));
        btn.setAttribute('title', t(labelKey));
        btn.innerHTML = svg;
        return btn;
    }

    function iconTextButton(label, svg, extra = '') {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = `btn ${extra}`.trim();
        btn.innerHTML = svg + `<span>${escapeHTML(label)}</span>`;
        return btn;
    }

    function actionIconButton(labelKey, svg, fn, stateName = '') {
        const btn = iconButton(labelKey, svg);
        btn.classList.add('s3-row-action');
        if (stateName) btn.dataset.state = stateName;
        btn.addEventListener('click', fn);
        return btn;
    }

    function actionIconLink(labelKey, svg, href) {
        const a = document.createElement('a');
        a.className = 'icon-btn s3-row-action';
        a.href = href;
        a.setAttribute('aria-label', t(labelKey));
        a.setAttribute('title', t(labelKey));
        a.innerHTML = svg;
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

    function fileType(entry) {
        if (entry.is_dir) return t('s3_folder');
        const ext = entry.name.split('.').pop()?.toLowerCase();
        const types = {
            json: 'application/json',
            zip: 'application/zip',
            sql: 'application/sql',
            txt: 'text/plain',
            log: 'text/plain',
            csv: 'text/csv',
            html: 'text/html',
            css: 'text/css',
            js: 'text/javascript',
            png: 'image/png',
            jpg: 'image/jpeg',
            jpeg: 'image/jpeg',
            webp: 'image/webp',
            pdf: 'application/pdf',
        };
        return types[ext] || '--';
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
        if (Number.isNaN(d.getTime())) return '--';
        return d.toLocaleString();
    }

    function escapeHTML(value) {
        return String(value ?? '')
            .replaceAll('&', '&amp;')
            .replaceAll('<', '&lt;')
            .replaceAll('>', '&gt;')
            .replaceAll('"', '&quot;')
            .replaceAll("'", '&#39;');
    }

    function copyIcon() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>';
    }

    function chevronIcon() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m9 18 6-6-6-6"></path></svg>';
    }

    function uploadIcon() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><path d="m17 8-5-5-5 5"></path><path d="M12 3v12"></path></svg>';
    }

    function plusIcon() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 5v14"></path><path d="M5 12h14"></path></svg>';
    }

    function refreshIcon() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"></path><path d="M3 21v-5h5"></path><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"></path><path d="M16 8h5V3"></path></svg>';
    }

    function folderIcon() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.2a2 2 0 0 1-1.6-.8L10 4H4a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2Z"></path></svg>';
    }

    function fileIcon() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8Z"></path><path d="M14 2v6h6"></path></svg>';
    }

    function downloadIcon() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path><path d="M7 10l5 5 5-5"></path><path d="M12 15V3"></path></svg>';
    }

    function editIcon() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20h9"></path><path d="M16.5 3.5a2.12 2.12 0 0 1 3 3L7 19l-4 1 1-4Z"></path></svg>';
    }

    function trashIcon() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 6h18"></path><path d="M8 6V4h8v2"></path><path d="m19 6-1 14H6L5 6"></path><path d="M10 11v6"></path><path d="M14 11v6"></path></svg>';
    }

    function wireS3() {
        $('s3-new-mount')?.addEventListener('click', () => window.cfui.openS3Wizard?.({ mode: 'create' }));
        $('s3-upload-input')?.addEventListener('change', (e) => {
            const file = e.target.files?.[0];
            e.target.value = '';
            uploadS3File(file);
        });
        window.cfui.wireS3Wizard?.();
    }

    const ns = window.cfui;
    ns.s3AvailabilityText = s3AvailabilityText;
    ns.s3ProviderLabel = providerLabel;
    ns.s3WebDAVEndpointFor = webDAVEndpointFor;
    ns.s3NextMountPath = nextMountPath;
    ns.s3ActiveMount = activeMount;
    ns.renderS3Settings = renderS3Settings;
    ns.fetchS3Settings = fetchS3Settings;
    ns.loadS3Files = loadS3Files;
    ns.wireS3 = wireS3;
})();
