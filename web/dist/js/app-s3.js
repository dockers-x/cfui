/* =========================================================================
   CloudFlared UI - S3 WebDAV overview and file browser
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, t, API_BASE, apiGet, apiSend, toast, setBusy, sleep } = window.cfui;

    const PROVIDER_R2 = 'cloudflare_r2';
    const DEFAULT_MOUNT = '/webdav/s3/';
    const ACCESS_MAIN = 'main';
    const ACCESS_DEDICATED = 'dedicated';
    const DOMAIN_NONE = 'none';
    const DOMAIN_CUSTOM = 'custom';
    const DOMAIN_TUNNEL = 'tunnel';
    const TUNNEL_COMMENT_MARKER = 'cfui:s3-webdav';

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
        renderS3Access(settings);
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
        if (state.s3.mountSwitcherObserver) {
            state.s3.mountSwitcherObserver.disconnect();
            state.s3.mountSwitcherObserver = null;
        }
        list.innerHTML = '';
        if (!mounts.length) {
            const empty = document.createElement('div');
            empty.className = 'empty';
            empty.textContent = t('s3_empty_mounts');
            list.appendChild(empty);
            return;
        }
        const selected = mounts.find((m) => m.key === activeKey) || mounts[0];
        const switcherShell = document.createElement('div');
        switcherShell.className = 's3-mount-switcher-shell';
        const switcher = document.createElement('div');
        switcher.className = 's3-mount-switcher';
        switcher.setAttribute('role', 'list');
        for (const mount of mounts) switcher.appendChild(mountSwitchItem(mount, mount.key === selected.key));
        const prev = mountPagerButton('prev');
        const next = mountPagerButton('next');
        prev.addEventListener('click', () => scrollMountSwitcher(switcher, -1));
        next.addEventListener('click', () => scrollMountSwitcher(switcher, 1));
        switcher.addEventListener('scroll', () => updateMountPager(switcherShell, switcher, prev, next), { passive: true });
        switcherShell.append(prev, switcher, next);

        const detail = document.createElement('div');
        detail.className = 's3-mount-detail-panel';
        detail.appendChild(mountDetail(settings, selected));
        list.append(switcherShell, detail);
        if (window.ResizeObserver) {
            const ro = new ResizeObserver(() => updateMountPager(switcherShell, switcher, prev, next));
            ro.observe(switcher);
            state.s3.mountSwitcherObserver = ro;
        }
        requestAnimationFrame(() => {
            switcher.querySelector('[data-active="true"]')?.scrollIntoView({ behavior: 'auto', block: 'nearest', inline: 'nearest' });
            updateMountPager(switcherShell, switcher, prev, next);
        });
    }

    function mountSwitchItem(mount, active) {
        const item = document.createElement('article');
        item.className = 's3-mount-switch-item';
        item.dataset.active = String(active);
        item.setAttribute('role', 'listitem');

        const summary = document.createElement('button');
        summary.type = 'button';
        summary.className = 's3-mount-switch-btn';
        summary.setAttribute('aria-pressed', String(active));
        summary.addEventListener('click', () => selectMount(mount.key));

        const copy = document.createElement('span');
        copy.className = 's3-mount-switch-copy';
        const name = document.createElement('span');
        name.className = 's3-mount-item__name';
        name.textContent = mount.name || mount.key;
        const meta = document.createElement('span');
        meta.className = 's3-mount-switch-meta';
        meta.textContent = `${providerLabel(mount.provider)} · ${mount.bucket_name || t('s3_bucket_required')}`;
        copy.append(name, meta);
        summary.append(copy, mountHealthDots(mount));
        item.appendChild(summary);
        return item;
    }

    function mountPagerButton(direction) {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = `s3-mount-pager s3-mount-pager--${direction}`;
        btn.hidden = true;
        btn.setAttribute('aria-label', t(direction === 'prev' ? 's3_mount_scroll_prev' : 's3_mount_scroll_next'));
        btn.innerHTML = direction === 'prev' ? chevronLeftIcon() : chevronRightIcon();
        return btn;
    }

    function scrollMountSwitcher(switcher, direction) {
        const amount = Math.max(180, switcher.clientWidth - 56) * direction;
        const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
        switcher.scrollBy({ left: amount, behavior: reduceMotion ? 'auto' : 'smooth' });
    }

    function updateMountPager(shell, switcher, prev, next) {
        const max = Math.max(0, switcher.scrollWidth - switcher.clientWidth);
        const overflow = max > 2;
        const canPrev = overflow && switcher.scrollLeft > 2;
        const canNext = overflow && switcher.scrollLeft < max - 2;
        shell.dataset.overflow = String(overflow);
        prev.hidden = !canPrev;
        next.hidden = !canNext;
    }

    function mountHealthDots(mount) {
        const wrap = document.createElement('span');
        wrap.className = 's3-mount-health';
        wrap.append(
            healthDot(mount.enabled && mount.availability?.can_enable ? 'ok' : mount.enabled ? 'warn' : 'neutral', t('s3_config_status')),
            healthDot(mount.webdav_enabled && mount.webdav_availability?.can_enable ? 'ok' : mount.webdav_enabled ? 'warn' : 'neutral', t('s3_webdav_status')),
            healthDot(!mount.webdav_auth_enabled ? 'warn' : mount.webdav_username && mount.password_set ? 'ok' : 'warn', t('s3_webdav_auth_status'))
        );
        return wrap;
    }

    function healthDot(state, label) {
        const dot = document.createElement('span');
        dot.className = 's3-health-dot';
        dot.dataset.state = state;
        dot.title = label;
        dot.setAttribute('aria-label', label);
        return dot;
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

        const endpoint = copyField(t('s3_webdav_endpoint'), webDAVEndpointForMount(settings, mount), () => copyMountEndpoint(settings, mount), webDAVEndpointState(settings));
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

    function renderS3Access(settings) {
        const mode = accessMode(settings);
        const main = $('s3-access-main');
        const dedicated = $('s3-access-dedicated');
        const dedicatedSettings = $('s3-dedicated-settings');
        const host = $('s3-dedicated-host');
        const port = $('s3-dedicated-port');
        const autoStart = $('s3-dedicated-autostart');
        const action = $('s3-dedicated-action');
        const warning = $('s3-dedicated-warning');
        const domainMode = dedicatedDomainMode(settings);
        if (main) main.checked = mode === ACCESS_MAIN;
        if (dedicated) dedicated.checked = mode === ACCESS_DEDICATED;
        if (host) {
            host.value = settings.dedicated_bind_host || '';
            host.disabled = mode !== ACCESS_DEDICATED;
        }
        if (port) {
            port.value = String(settings.dedicated_port || 14334);
            port.disabled = mode !== ACCESS_DEDICATED;
        }
        if (autoStart) {
            autoStart.checked = !!settings.dedicated_auto_start;
            autoStart.disabled = mode !== ACCESS_DEDICATED;
        }
        renderS3DomainSettings(settings, mode, domainMode);
        $('s3-dedicated-apply')?.toggleAttribute('disabled', mode !== ACCESS_DEDICATED);
        renderS3DedicatedAction(settings, action, mode);
        if (dedicatedSettings) {
            dedicatedSettings.hidden = mode !== ACCESS_DEDICATED;
            dedicatedSettings.dataset.enabled = String(mode === ACCESS_DEDICATED);
        }
        if (warning) {
            if (mode === ACCESS_DEDICATED && settings.dedicated_error) {
                warning.hidden = false;
                warning.dataset.state = 'error';
                warning.textContent = `${t('s3_dedicated_error_help')}: ${settings.dedicated_error}`;
            } else {
                warning.dataset.state = 'warn';
                warning.textContent = t('s3_dedicated_auth_warning');
                warning.hidden = !(mode === ACCESS_DEDICATED && (settings.mounts || []).some((m) => m.webdav_enabled && !m.webdav_auth_enabled));
            }
        }
        renderS3DedicatedStatus(settings);
    }

    function renderS3DedicatedStatus(settings) {
        const el = $('s3-dedicated-status');
        if (!el) return;
        const text = el.querySelector('.text');
        const mode = accessMode(settings);
        if (mode === ACCESS_MAIN) {
            el.dataset.state = 'neutral';
            if (text) text.textContent = t('s3_access_mode_main');
            return;
        }
        if (!settings.enabled) {
            el.dataset.state = 'warn';
            if (text) text.textContent = t('s3_dedicated_waiting_feature');
            return;
        }
        if (settings.dedicated_error) {
            el.dataset.state = 'error';
            if (text) text.textContent = t('s3_dedicated_failed');
            return;
        }
        el.dataset.state = settings.dedicated_running ? 'ok' : 'warn';
        if (text) text.textContent = settings.dedicated_running ? t('s3_dedicated_running') : t('s3_dedicated_stopped');
    }

    function renderS3DedicatedAction(settings, btn, mode = accessMode(settings)) {
        if (!btn) return;
        settings = settings || {};
        const canControl = mode === ACCESS_DEDICATED && !!settings.enabled;
        const running = !!settings.dedicated_running;
        btn.disabled = !canControl;
        btn.dataset.action = running ? 'stop' : 'start';
        btn.classList.toggle('btn--primary', !running);
        btn.classList.toggle('btn--danger', running);
        const label = t(running ? 's3_dedicated_stop' : 's3_dedicated_start');
        const text = btn.querySelector('.text');
        if (text) text.textContent = label;
        btn.setAttribute('aria-label', label);
        btn.title = canControl ? label : t('s3_dedicated_waiting_feature');
    }

    function renderS3DomainSettings(settings, mode, domainMode = dedicatedDomainMode(settings)) {
        const enabled = mode === ACCESS_DEDICATED;
        const radios = {
            [DOMAIN_NONE]: $('s3-domain-none'),
            [DOMAIN_CUSTOM]: $('s3-domain-custom'),
            [DOMAIN_TUNNEL]: $('s3-domain-tunnel'),
        };
        for (const [key, radio] of Object.entries(radios)) {
            if (!radio) continue;
            radio.checked = domainMode === key;
            radio.disabled = !enabled;
        }
        const customPanel = $('s3-domain-custom-panel');
        const tunnelPanel = $('s3-domain-tunnel-panel');
        if (customPanel) customPanel.hidden = !enabled || domainMode !== DOMAIN_CUSTOM;
        if (tunnelPanel) tunnelPanel.hidden = !enabled || domainMode !== DOMAIN_TUNNEL;
        const customInput = $('s3-dedicated-custom-domain');
        if (customInput) {
            customInput.value = settings.dedicated_custom_domain || '';
            customInput.disabled = !enabled || domainMode !== DOMAIN_CUSTOM;
        }
        $('s3-domain-apply')?.toggleAttribute('disabled', !enabled || domainMode !== DOMAIN_CUSTOM);
        renderS3TunnelDomainInputs(settings, enabled && domainMode === DOMAIN_TUNNEL);
        renderS3TunnelStatus(settings, enabled && domainMode === DOMAIN_TUNNEL);
    }

    function renderS3TunnelDomainInputs(settings, enabled) {
        const host = normalizeHost(settings?.dedicated_tunnel_hostname || '');
        const split = splitHostnameForZones(host);
        const subdomain = $('s3-dedicated-tunnel-subdomain');
        if (subdomain) {
            subdomain.value = split.subdomain;
            subdomain.disabled = !enabled;
        }
        renderS3TunnelDomainOptions(split.domain);
        const domain = $('s3-dedicated-tunnel-domain');
        if (domain) domain.disabled = !enabled;
        $('s3-dedicated-tunnel-open')?.toggleAttribute('disabled', !enabled);
    }

    function renderS3TunnelDomainOptions(selected = '') {
        const select = $('s3-dedicated-tunnel-domain');
        if (!select) return;
        const zones = state.tunnelManager?.zones || [];
        const existing = selected || select.value;
        select.innerHTML = '';
        const placeholder = document.createElement('option');
        placeholder.value = '';
        placeholder.textContent = zones.length ? t('select_domain') : t('s3_tunnel_domains_unloaded');
        select.appendChild(placeholder);
        for (const zone of zones) {
            const opt = document.createElement('option');
            opt.value = zone.name;
            opt.textContent = zone.status ? `${zone.name} (${zone.status})` : zone.name;
            select.appendChild(opt);
        }
        if (existing && !zones.some((zone) => zone.name === existing)) {
            const opt = document.createElement('option');
            opt.value = existing;
            opt.textContent = existing;
            select.appendChild(opt);
        }
        select.value = existing || '';
    }

    function renderS3TunnelStatus(settings, visible) {
        const el = $('s3-dedicated-tunnel-state');
        if (!el) return;
        el.hidden = !visible;
        if (!visible) return;
        const status = settings?.dedicated_tunnel_status || (!settings?.dedicated_tunnel_hostname ? 'missing' : '');
        const text = el.querySelector('.text');
        const stateName = status === 'synced' ? 'ok' : status === 'unavailable' ? 'warn' : status ? 'error' : 'neutral';
        el.dataset.state = stateName;
        if (text) text.textContent = tunnelStatusText(settings);
        el.title = settings?.dedicated_tunnel_status_message || '';
    }

    function tunnelStatusText(settings) {
        switch (settings?.dedicated_tunnel_status) {
        case 'synced':
            return t('s3_tunnel_synced');
        case 'unavailable':
            return t('s3_tunnel_unavailable');
        case 'error':
            return t('s3_tunnel_error');
        case 'missing':
            return t('s3_tunnel_missing');
        default:
            return t('s3_tunnel_not_configured');
        }
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
        const sync = iconTextButton(t('s3_sync'), syncIcon(), 'btn--sm');
        sync.addEventListener('click', () => openS3Sync({ path: state.s3.path || '/', is_dir: true, name: pathName(state.s3.path || '/') || '/' }));
        const folder = iconTextButton(t('s3_new_folder'), plusIcon(), 'btn--sm btn--primary');
        folder.addEventListener('click', createS3Folder);
        const refresh = iconButton('refresh', refreshIcon());
        refresh.addEventListener('click', () => loadS3Files(state.s3.path || '/'));
        tools.append(upload, sync, folder, refresh);
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

    function copyField(label, value, onCopy, stateName = '') {
        const endpoint = document.createElement('div');
        endpoint.className = 's3-mount-endpoint';
        const endpointLabel = document.createElement('div');
        endpointLabel.className = 's3-mount-label';
        endpointLabel.textContent = label;
        const endpointBox = document.createElement('div');
        endpointBox.className = 's3-copy-line';
        if (stateName) endpointBox.dataset.state = stateName;
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

    function accessMode(settings) {
        return settings?.webdav_access_mode === ACCESS_DEDICATED ? ACCESS_DEDICATED : ACCESS_MAIN;
    }

    function dedicatedDomainMode(settings) {
        const mode = settings?.dedicated_domain_mode;
        return mode === DOMAIN_CUSTOM || mode === DOMAIN_TUNNEL ? mode : DOMAIN_NONE;
    }

    function activeWebDAVOrigin(settings) {
        if (accessMode(settings) !== ACCESS_DEDICATED) return window.location.origin;
        const domainMode = dedicatedDomainMode(settings);
        if (domainMode === DOMAIN_CUSTOM && settings?.dedicated_custom_domain) {
            return settings.dedicated_custom_domain;
        }
        if (domainMode === DOMAIN_TUNNEL && settings?.dedicated_tunnel_hostname) {
            return `https://${normalizeHost(settings.dedicated_tunnel_hostname)}`;
        }
        return dedicatedOrigin(settings);
    }

    function dedicatedOrigin(settings) {
        let host = (settings?.dedicated_bind_host || '').trim();
        if (!host || host === '0.0.0.0' || host === '::' || host === '[::]') host = window.location.hostname || 'localhost';
        if (host.includes(':') && !host.startsWith('[')) host = `[${host}]`;
        const port = Number(settings?.dedicated_port || 14334);
        return `http://${host}:${port}`;
    }

    function dedicatedTunnelService(settings) {
        return `http://127.0.0.1:${Number(settings?.dedicated_port || 14334)}`;
    }

    function webDAVEndpointFor(path, origin = activeWebDAVOrigin(state.s3.settings)) {
        const normalized = (path || DEFAULT_MOUNT).trim() || DEFAULT_MOUNT;
        try {
            const base = new URL(origin);
            base.pathname = joinURLPath(base.pathname, normalized);
            base.search = '';
            base.hash = '';
            return base.toString();
        } catch {
            return normalized;
        }
    }

    function webDAVEndpointForMount(settings, mount) {
        return webDAVEndpointFor(mount?.mount_path || DEFAULT_MOUNT, activeWebDAVOrigin(settings));
    }

    function webDAVEndpointState(settings) {
        if (accessMode(settings) !== ACCESS_DEDICATED || dedicatedDomainMode(settings) !== DOMAIN_TUNNEL) return '';
        const status = settings?.dedicated_tunnel_status;
        return status && status !== 'synced' ? 'error' : '';
    }

    function copyMountEndpoint(settings, mount) {
        const value = webDAVEndpointForMount(settings, mount);
        navigator.clipboard?.writeText(value).then(() => toast.ok(t('copied_to_clipboard')), () => toast.err(t('copy_failed')));
    }

    async function saveS3AccessSettings(overrides = {}, control) {
        const settings = state.s3.settings;
        if (!settings) return;
        if (control) setBusy(control, true);
        const hostValue = $('s3-dedicated-host')?.value ?? settings.dedicated_bind_host ?? '';
        const portValue = Number($('s3-dedicated-port')?.value || settings.dedicated_port || 14334);
        const domainModeValue = overrides.dedicated_domain_mode || dedicatedDomainMode(settings);
        const customDomainValue = $('s3-dedicated-custom-domain')?.value.trim() ?? settings.dedicated_custom_domain ?? '';
        const tunnelHostnameValue = tunnelHostnameForSave(settings, domainModeValue, overrides);
        if (!Number.isInteger(portValue) || portValue < 1 || portValue > 65535) {
            toast.err(t('s3_dedicated_port_invalid'));
            if (control) setBusy(control, false);
            return;
        }
        try {
            const data = await apiSend('/s3/settings', 'POST', {
                enabled: !!settings.enabled,
                active_key: state.s3.activeKey || settings.active_key,
                webdav_access_mode: overrides.webdav_access_mode || accessMode(settings),
                dedicated_bind_host: overrides.dedicated_bind_host ?? hostValue,
                dedicated_port: overrides.dedicated_port ?? portValue,
                dedicated_auto_start: overrides.dedicated_auto_start ?? !!($('s3-dedicated-autostart')?.checked ?? settings.dedicated_auto_start),
                dedicated_domain_mode: domainModeValue,
                dedicated_custom_domain: overrides.dedicated_custom_domain ?? customDomainValue,
                dedicated_tunnel_hostname: tunnelHostnameValue,
            });
            state.s3.settings = data;
            state.s3.activeKey = data.active_key || state.s3.activeKey;
            renderS3Settings(data);
            const mount = activeMount();
            if (canBrowseFiles(data, mount)) await loadS3Files(state.s3.path || '/');
            toast.ok(t('s3_settings_saved'));
        } catch (err) {
            toast.err(t('s3_settings_save_failed') + ': ' + err.message);
            renderS3Settings(state.s3.settings);
        } finally {
            if (control) setBusy(control, false);
        }
    }

    async function controlS3Dedicated(action, control) {
        if (!action || !['start', 'stop'].includes(action)) return;
        if (control) setBusy(control, true, t(action === 'start' ? 's3_dedicated_start' : 's3_dedicated_stop'));
        try {
            const data = await apiSend('/s3/webdav-control', 'POST', { action });
            state.s3.settings = data;
            state.s3.activeKey = data.active_key || state.s3.activeKey;
            renderS3Settings(data);
            const mount = activeMount();
            if (canBrowseFiles(data, mount)) await loadS3Files(state.s3.path || '/');
            toast.ok(t(action === 'start' ? 's3_dedicated_started' : 's3_dedicated_stopped_done'));
        } catch (err) {
            toast.err(t(action === 'start' ? 's3_dedicated_start_failed' : 's3_dedicated_stop_failed') + ': ' + err.message);
            await fetchS3Settings();
        } finally {
            if (control) {
                setBusy(control, false);
                renderS3DedicatedAction(state.s3.settings, control);
            }
        }
    }

    async function openDedicatedTunnelRule(control) {
        const settings = state.s3.settings;
        if (!settings) return;
        if (control) setBusy(control, true, t('s3_open_tunnel_rule'));
        try {
            if (!(await ensureTunnelManagerReady())) return;
            await window.cfui.maybeLoadTunnelManagerZones?.(true);
            renderS3TunnelDomainOptions($('s3-dedicated-tunnel-domain')?.value || splitHostnameForZones(settings.dedicated_tunnel_hostname).domain);
            const subdomain = $('s3-dedicated-tunnel-subdomain')?.value;
            const domain = $('s3-dedicated-tunnel-domain')?.value;
            const hostname = buildHostname(subdomain, domain);
            if (!String(subdomain || '').trim() || !String(domain || '').trim() || !hostname || !hostname.includes('.')) {
                toast.err(t('s3_tunnel_hostname_required'));
                return;
            }
            const service = dedicatedTunnelService(settings);
            const comment = `${TUNNEL_COMMENT_MARKER} hostname=${hostname} service=${service}`;
            const cfg = await window.cfui.loadTunnelManagerConfig?.(true);
            if (!cfg) throw new Error(t('manager_config_load_failed'));
            const existing = (cfg?.entries || []).find((entry) => normalizeHost(entry.hostname) === hostname && !String(entry.path || '').trim());
            if (existing) {
                const ok = await window.cfui.confirm({
                    title: t('s3_tunnel_overwrite_title'),
                    message: t('s3_tunnel_overwrite_message', { hostname }),
                    okText: t('continue'),
                    okClass: 'btn--primary',
                });
                if (!ok) return;
            }
            await saveS3AccessSettings({
                dedicated_domain_mode: DOMAIN_TUNNEL,
                dedicated_tunnel_hostname: hostname,
            });
            window.cfui.activateTab?.('manager');
            window.cfui.openTunnelEntryDialog?.({
                index: existing?.index,
                hostname,
                path: '',
                service,
                comment,
                no_tls_verify: false,
                http_host_header: '',
                origin_server_name: '',
            });
            toast.ok(t('s3_tunnel_rule_prefilled'));
        } catch (err) {
            toast.err(t('s3_tunnel_open_failed') + ': ' + err.message);
        } finally {
            if (control) setBusy(control, false);
        }
    }

    async function ensureTunnelManagerReady() {
        if (!state.features?.tunnel_manager) {
            toast.err(t('s3_tunnel_feature_required'));
            window.cfui.activateTab?.('features');
            return false;
        }
        await window.cfui.fetchTunnelManagerSettings?.();
        const settings = state.tunnelManager?.settings || {};
        if (!settings.enabled || !settings.account_id || !settings.tunnel_id || !(settings.api_token_set || settings.api_key_set)) {
            toast.err(t('s3_tunnel_manager_required'));
            window.cfui.activateTab?.('manager');
            return false;
        }
        const verify = await apiSend('/tunnel-manager/verify-token', 'POST', { auth_mode: settings.auth_mode || 'token' });
        const missing = (verify.permissions || []).filter((perm) => perm.required && !perm.granted);
        if (!verify.valid || missing.length) {
            toast.err(t('s3_tunnel_permission_required'));
            window.cfui.activateTab?.('manager');
            return false;
        }
        await window.cfui.maybeLoadTunnelManagerZones?.(true);
        if (!(state.tunnelManager?.zones || []).length) {
            toast.err(t('s3_tunnel_domain_required'));
            window.cfui.activateTab?.('manager');
            return false;
        }
        return true;
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
            actionIconButton('s3_sync', syncIcon(), () => openS3Sync(entry)),
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

    async function openS3Sync(entry) {
        const sourceMount = activeMount();
        if (!sourceMount || !canBrowseFiles(state.s3.settings, sourceMount)) {
            toast.err(t('s3_files_disabled_title'));
            return;
        }
        const targets = syncTargetMounts(sourceMount.key);
        const sourcePath = cleanClientPath(entry?.path || state.s3.path || '/');
        const sourceIsDir = entry ? !!entry.is_dir : true;
        state.s3.sync = {
            sourceMountKey: sourceMount.key,
            sourcePath,
            sourceIsDir,
            targetMountKey: targets[0]?.key || '',
            targetDir: '/',
            overwrite: false,
            running: false,
            job: null,
            source: syncTreeState(),
            target: syncTreeState(),
        };
        const overwrite = $('s3-sync-overwrite');
        if (overwrite) overwrite.checked = false;
        renderS3SyncDialog();
        window.cfui.openDialog?.($('s3-sync-dialog'));
        try {
            await Promise.all([
                expandSyncPath('source', sourceMount.key, sourcePath, sourceIsDir),
                targets[0] ? loadSyncTreeNode('target', targets[0].key, '/') : Promise.resolve(),
            ]);
        } catch (err) {
            toast.err(t('s3_sync_tree_load_failed') + ': ' + err.message);
        }
        renderS3SyncDialog();
    }

    function syncTreeState() {
        return { entries: new Map(), expanded: new Set(['/']), loading: new Set() };
    }

    function syncTargetMounts(sourceKey = state.s3.sync?.sourceMountKey) {
        return (state.s3.settings?.mounts || []).filter((mount) => mount.key !== sourceKey && canBrowseFiles(state.s3.settings, mount));
    }

    async function expandSyncPath(role, mountKey, selectedPath, selectedIsDir) {
        const parts = cleanClientPath(selectedPath).split('/').filter(Boolean);
        await loadSyncTreeNode(role, mountKey, '/');
        let acc = '';
        const limit = selectedIsDir ? parts.length : Math.max(0, parts.length - 1);
        for (let i = 0; i < limit; i += 1) {
            acc = joinPath(acc || '/', parts[i]);
            state.s3.sync?.[role]?.expanded.add(acc);
            await loadSyncTreeNode(role, mountKey, acc);
        }
    }

    async function loadSyncTreeNode(role, mountKey, path) {
        const sync = state.s3.sync;
        if (!sync || !mountKey) return;
        const tree = sync[role];
        const cleaned = cleanClientPath(path);
        if (tree.entries.has(cleaned) || tree.loading.has(cleaned)) return;
        tree.loading.add(cleaned);
        renderS3SyncTree(role);
        try {
            const data = await apiGet('/s3/files?mount_key=' + encodeURIComponent(mountKey) + '&path=' + encodeURIComponent(cleaned));
            tree.entries.set(cleaned, sanitizeEntries(data.entries || []));
        } finally {
            tree.loading.delete(cleaned);
        }
    }

    function renderS3SyncDialog() {
        const sync = state.s3.sync;
        if (!sync) return;
        const sourceMount = mountByKey(sync.sourceMountKey);
        const targetMount = mountByKey(sync.targetMountKey);
        const targets = syncTargetMounts(sync.sourceMountKey);
        const sourceLabel = $('s3-sync-source-label');
        const targetLabel = $('s3-sync-target-label');
        if (sourceLabel) sourceLabel.textContent = `${mountDisplayName(sourceMount)}: ${sync.sourcePath}`;
        if (targetLabel) targetLabel.textContent = sync.targetMountKey ? `${mountDisplayName(targetMount)}: ${sync.targetDir}` : t('s3_sync_no_targets');
        const sourcePath = $('s3-sync-source-path');
        const targetPath = $('s3-sync-target-path');
        if (sourcePath) sourcePath.textContent = sync.sourcePath;
        if (targetPath) targetPath.textContent = sync.targetDir || '/';
        renderSyncTargetOptions(targets);
        renderS3SyncTree('source');
        renderS3SyncTree('target');
        renderS3SyncDestination();
        renderS3SyncProgress(sync.job);
        const start = $('s3-sync-start');
        if (start) {
            start.disabled = sync.running || !sync.targetMountKey || !sync.sourcePath || !sync.targetDir;
            start.title = !sync.targetMountKey ? t('s3_sync_no_targets') : '';
        }
    }

    function renderSyncTargetOptions(targets) {
        const select = $('s3-sync-target-mount');
        const sync = state.s3.sync;
        if (!select || !sync) return;
        const current = sync.targetMountKey;
        select.innerHTML = '';
        if (!targets.length) {
            sync.targetMountKey = '';
            const opt = document.createElement('option');
            opt.value = '';
            opt.textContent = t('s3_sync_no_targets');
            select.appendChild(opt);
            select.disabled = true;
            return;
        }
        for (const mount of targets) {
            const opt = document.createElement('option');
            opt.value = mount.key;
            opt.textContent = `${mountDisplayName(mount)} · ${mount.bucket_name || ''}`;
            select.appendChild(opt);
        }
        select.disabled = sync.running;
        select.value = targets.some((mount) => mount.key === current) ? current : targets[0].key;
        sync.targetMountKey = select.value;
    }

    function renderS3SyncTree(role) {
        const sync = state.s3.sync;
        const container = $(role === 'source' ? 's3-sync-source-tree' : 's3-sync-target-tree');
        if (!sync || !container) return;
        container.innerHTML = '';
        const mountKey = role === 'source' ? sync.sourceMountKey : sync.targetMountKey;
        if (!mountKey) {
            const empty = document.createElement('div');
            empty.className = 's3-file-empty';
            empty.textContent = t('s3_sync_no_targets');
            container.appendChild(empty);
            return;
        }
        container.appendChild(syncTreeNode(role, '/', 0, true));
    }

    function syncTreeNode(role, path, depth, isDir, entry = null) {
        const sync = state.s3.sync;
        const tree = sync[role];
        const cleaned = cleanClientPath(path);
        const expanded = tree.expanded.has(cleaned);
        const selected = role === 'source' ? sync.sourcePath === cleaned : sync.targetDir === cleaned;
        const disabled = role === 'target' && !isDir;
        const wrap = document.createElement('div');
        wrap.className = 's3-sync-tree__children';

        const row = document.createElement('div');
        row.className = 's3-sync-tree-row';
        row.dataset.selected = String(selected);
        row.dataset.disabled = String(disabled);
        row.style.paddingLeft = `${Math.min(depth * 16, 96)}px`;
        row.setAttribute('role', 'treeitem');
        row.setAttribute('aria-selected', String(selected));
        if (isDir) row.setAttribute('aria-expanded', String(expanded));

        const toggle = document.createElement('button');
        toggle.type = 'button';
        toggle.className = 's3-sync-tree-toggle';
        toggle.innerHTML = chevronRightIcon();
        toggle.dataset.expanded = String(expanded);
        toggle.hidden = !isDir;
        toggle.setAttribute('aria-label', expanded ? t('s3_sync_collapse') : t('s3_sync_expand'));
        toggle.addEventListener('click', async () => {
            if (!isDir) return;
            if (expanded) tree.expanded.delete(cleaned);
            else {
                tree.expanded.add(cleaned);
                await loadSyncTreeNode(role, role === 'source' ? sync.sourceMountKey : sync.targetMountKey, cleaned);
            }
            renderS3SyncDialog();
        });

        const name = document.createElement('button');
        name.type = 'button';
        name.className = 's3-sync-tree-name';
        name.disabled = disabled || sync.running;
        name.innerHTML = (isDir ? folderIcon() : fileIcon()) + `<span>${escapeHTML(treeLabel(role, cleaned, entry))}</span>`;
        name.addEventListener('click', () => {
            if (role === 'source') {
                sync.sourcePath = cleaned;
                sync.sourceIsDir = isDir;
            } else if (isDir) {
                sync.targetDir = cleaned;
            }
            renderS3SyncDialog();
        });
        row.append(toggle, name);
        wrap.appendChild(row);

        if (isDir && expanded) {
            const loading = tree.loading.has(cleaned);
            const entries = tree.entries.get(cleaned);
            if (loading) {
                wrap.appendChild(syncTreeMessage(t('s3_files_loading'), depth + 1));
            } else if (!entries) {
                loadSyncTreeNode(role, role === 'source' ? sync.sourceMountKey : sync.targetMountKey, cleaned).then(() => renderS3SyncDialog()).catch((err) => toast.err(t('s3_sync_tree_load_failed') + ': ' + err.message));
            } else if (!entries.length) {
                wrap.appendChild(syncTreeMessage(t('s3_files_empty'), depth + 1));
            } else {
                for (const child of entries) {
                    wrap.appendChild(syncTreeNode(role, child.path, depth + 1, !!child.is_dir, child));
                }
            }
        }
        return wrap;
    }

    function syncTreeMessage(text, depth) {
        const msg = document.createElement('div');
        msg.className = 's3-sync-progress__current';
        msg.style.paddingLeft = `${Math.min(depth * 16 + 30, 126)}px`;
        msg.textContent = text;
        return msg;
    }

    function treeLabel(role, path, entry) {
        if (path === '/') {
            const key = role === 'source' ? state.s3.sync?.sourceMountKey : state.s3.sync?.targetMountKey;
            return breadcrumbRootLabel(mountByKey(key));
        }
        return entry?.name || pathName(path);
    }

    function renderS3SyncDestination() {
        const sync = state.s3.sync;
        const el = $('s3-sync-destination');
        const warning = $('s3-sync-warning');
        if (!sync || !el) return;
        el.textContent = syncDestinationPath(sync);
        if (warning) warning.hidden = !$('s3-sync-overwrite')?.checked;
    }

    function syncDestinationPath(sync) {
        const targetDir = cleanClientPath(sync.targetDir || '/');
        if (sync.sourcePath === '/') return targetDir;
        return joinPath(targetDir, pathName(sync.sourcePath));
    }

    async function startS3Sync(control) {
        const sync = state.s3.sync;
        if (!sync || sync.running) return;
        const overwrite = !!$('s3-sync-overwrite')?.checked;
        if (overwrite) {
            const ok = await window.cfui.confirm({
                title: t('s3_sync_overwrite_title'),
                message: t('s3_sync_overwrite_confirm'),
                okText: t('continue'),
                okClass: 'btn--primary',
            });
            if (!ok) return;
        }
        sync.overwrite = overwrite;
        sync.running = true;
        setBusy(control, true, t('s3_sync_running'));
        renderS3SyncDialog();
        try {
            const job = await apiSend('/s3/files/sync', 'POST', {
                source_mount_key: sync.sourceMountKey,
                target_mount_keys: [sync.targetMountKey],
                source_path: sync.sourcePath,
                destination_path: syncDestinationPath(sync),
                overwrite,
            });
            sync.job = job;
            renderS3SyncDialog();
            await pollS3SyncJob(job.job_id);
        } catch (err) {
            toast.err(t('s3_sync_failed') + ': ' + err.message);
        } finally {
            if (state.s3.sync === sync) {
                sync.running = false;
                setBusy(control, false);
                renderS3SyncDialog();
            }
        }
    }

    async function pollS3SyncJob(jobID) {
        const sync = state.s3.sync;
        if (!sync || !jobID) return;
        for (;;) {
            await sleep(700);
            if (!state.s3.sync || state.s3.sync.job?.job_id !== jobID) return;
            const job = await apiGet('/s3/files/sync/' + encodeURIComponent(jobID));
            state.s3.sync.job = job;
            renderS3SyncDialog();
            if (job.status === 'completed') {
                const message = t('s3_sync_done', { copied: job.copied, skipped: job.skipped, failed: job.failed });
                if (job.failed > 0) toast.warn(message);
                else toast.ok(message);
                await loadS3Files(state.s3.path || '/');
                return;
            }
            if (job.status === 'failed') {
                toast.err(t('s3_sync_failed') + ': ' + (job.error || t('s3_sync_failed')));
                return;
            }
        }
    }

    function renderS3SyncProgress(job) {
        const panel = $('s3-sync-progress');
        if (!panel) return;
        panel.hidden = !job;
        if (!job) return;
        const total = Number(job.total || 0);
        const processed = Number(job.processed || 0);
        const percent = total > 0 ? Math.min(100, Math.round((processed / total) * 100)) : (job.status === 'completed' ? 100 : 0);
        const status = $('s3-sync-progress-status');
        const count = $('s3-sync-progress-count');
        const fill = $('s3-sync-progress-fill');
        const current = $('s3-sync-progress-current');
        const bytes = $('s3-sync-progress-bytes');
        const results = $('s3-sync-result-list');
        if (status) status.textContent = syncJobStatusText(job);
        if (count) count.textContent = `${processed} / ${total}`;
        if (fill) fill.style.width = `${percent}%`;
        if (current) {
            current.textContent = job.current_source_path
                ? t('s3_sync_current', { source: job.current_source_path, target: job.current_destination_path || '/', mount: mountDisplayName(mountByKey(job.current_mount_key)) })
                : '';
        }
        if (bytes) {
            const currentBytes = job.current_size > 0 ? ` · ${formatBytes(job.current_bytes || 0)} / ${formatBytes(job.current_size)}` : '';
            bytes.textContent = `${formatBytes(job.bytes_copied || 0)} / ${formatBytes(job.bytes_total || 0)}${currentBytes}`;
        }
        if (results) {
            results.innerHTML = '';
            for (const result of job.results || []) {
                const line = document.createElement('div');
                line.textContent = t('s3_sync_result_line', {
                    mount: mountDisplayName(mountByKey(result.mount_key)),
                    copied: result.copied || 0,
                    skipped: result.skipped || 0,
                    failed: result.failed || 0,
                });
                results.appendChild(line);
                for (const err of result.errors || []) {
                    const errLine = document.createElement('div');
                    errLine.textContent = err;
                    errLine.dataset.state = 'error';
                    results.appendChild(errLine);
                }
            }
        }
    }

    function syncJobStatusText(job) {
        if (job.status === 'completed') return t('s3_sync_completed');
        if (job.status === 'failed') return t('s3_sync_failed');
        return t('s3_sync_running');
    }

    function mountByKey(key) {
        return (state.s3.settings?.mounts || []).find((mount) => mount.key === key) || null;
    }

    function mountDisplayName(mount) {
        if (!mount) return '';
        return mount.name || mount.key || '';
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

    function cleanClientPath(path) {
        const parts = String(path || '/').split('/').filter(Boolean);
        return parts.length ? '/' + parts.join('/') : '/';
    }

    function pathName(path) {
        const parts = cleanClientPath(path).split('/').filter(Boolean);
        return parts[parts.length - 1] || '';
    }

    function normalizeHost(value) {
        value = String(value || '').trim();
        try {
            const url = new URL(value.includes('://') ? value : `https://${value}`);
            value = url.host;
        } catch {
            value = value.replace(/^https?:\/\//i, '');
        }
        return value.replace(/^\/+|\/+$/g, '').split('/')[0].split(':')[0].toLowerCase();
    }

    function splitHostnameForZones(hostname) {
        hostname = normalizeHost(hostname);
        if (!hostname) return { subdomain: '', domain: '' };
        const zones = (state.tunnelManager?.zones || []).map((zone) => zone.name).sort((a, b) => b.length - a.length);
        for (const zone of zones) {
            const suffix = `.${zone}`;
            if (hostname === zone) return { subdomain: '', domain: zone };
            if (hostname.endsWith(suffix)) return { subdomain: hostname.slice(0, -suffix.length), domain: zone };
        }
        const parts = hostname.split('.');
        if (parts.length <= 2) return { subdomain: parts.length === 2 ? parts[0] : '', domain: parts.length === 2 ? parts[1] : hostname };
        return { subdomain: parts.slice(0, -2).join('.'), domain: parts.slice(-2).join('.') };
    }

    function buildHostname(subdomain, domain) {
        subdomain = String(subdomain || '').trim().replace(/^\.+|\.+$/g, '');
        domain = String(domain || '').trim().replace(/^\.+|\.+$/g, '');
        return normalizeHost(subdomain ? `${subdomain}.${domain}` : domain);
    }

    function joinURLPath(basePath, mountPath) {
        const base = String(basePath || '/').replace(/\/+$/g, '');
        const mount = String(mountPath || '').replace(/^\/+/g, '');
        const joined = [base === '' ? '' : base, mount].filter(Boolean).join('/');
        return joined.startsWith('/') ? joined : `/${joined}`;
    }

    function tunnelHostnameForSave(settings, domainMode, overrides) {
        if (Object.prototype.hasOwnProperty.call(overrides, 'dedicated_tunnel_hostname')) {
            return normalizeHost(overrides.dedicated_tunnel_hostname || '');
        }
        if (domainMode !== DOMAIN_TUNNEL) return settings.dedicated_tunnel_hostname || '';
        const next = buildHostname($('s3-dedicated-tunnel-subdomain')?.value, $('s3-dedicated-tunnel-domain')?.value);
        return next || settings.dedicated_tunnel_hostname || '';
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

    function chevronLeftIcon() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.25" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m15 18-6-6 6-6"></path></svg>';
    }

    function chevronRightIcon() {
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

    function syncIcon() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 7h11"></path><path d="m12 4 3 3-3 3"></path><path d="M20 17H9"></path><path d="m12 14-3 3 3 3"></path></svg>';
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
        $('s3-access-main')?.addEventListener('change', (e) => {
            if (e.target.checked) saveS3AccessSettings({ webdav_access_mode: ACCESS_MAIN }, e.target);
        });
        $('s3-access-dedicated')?.addEventListener('change', (e) => {
            if (e.target.checked) saveS3AccessSettings({ webdav_access_mode: ACCESS_DEDICATED }, e.target);
        });
        $('s3-dedicated-autostart')?.addEventListener('change', (e) => saveS3AccessSettings({ dedicated_auto_start: e.target.checked }, e.target));
        $('s3-dedicated-apply')?.addEventListener('click', (e) => saveS3AccessSettings({}, e.currentTarget));
        $('s3-dedicated-action')?.addEventListener('click', (e) => controlS3Dedicated(e.currentTarget.dataset.action, e.currentTarget));
        $('s3-domain-none')?.addEventListener('change', (e) => {
            if (e.target.checked) saveS3AccessSettings({ dedicated_domain_mode: DOMAIN_NONE }, e.target);
        });
        $('s3-domain-custom')?.addEventListener('change', (e) => {
            if (e.target.checked) saveS3AccessSettings({ dedicated_domain_mode: DOMAIN_CUSTOM }, e.target);
        });
        $('s3-domain-tunnel')?.addEventListener('change', (e) => {
            if (e.target.checked) saveS3AccessSettings({ dedicated_domain_mode: DOMAIN_TUNNEL }, e.target);
        });
        $('s3-domain-apply')?.addEventListener('click', (e) => saveS3AccessSettings({ dedicated_domain_mode: DOMAIN_CUSTOM }, e.currentTarget));
        $('s3-dedicated-tunnel-open')?.addEventListener('click', (e) => openDedicatedTunnelRule(e.currentTarget));
        $('s3-upload-input')?.addEventListener('change', (e) => {
            const file = e.target.files?.[0];
            e.target.value = '';
            uploadS3File(file);
        });
        $('s3-sync-target-mount')?.addEventListener('change', async (e) => {
            if (!state.s3.sync) return;
            state.s3.sync.targetMountKey = e.target.value;
            state.s3.sync.targetDir = '/';
            state.s3.sync.target = syncTreeState();
            renderS3SyncDialog();
            try {
                await loadSyncTreeNode('target', e.target.value, '/');
                renderS3SyncDialog();
            } catch (err) {
                toast.err(t('s3_sync_tree_load_failed') + ': ' + err.message);
            }
        });
        $('s3-sync-overwrite')?.addEventListener('change', renderS3SyncDestination);
        $('s3-sync-start')?.addEventListener('click', (e) => startS3Sync(e.currentTarget));
        window.cfui.wireS3Wizard?.();
    }

    const ns = window.cfui;
    ns.s3AvailabilityText = s3AvailabilityText;
    ns.s3ProviderLabel = providerLabel;
    ns.s3WebDAVEndpointFor = webDAVEndpointFor;
    ns.s3WebDAVOrigin = () => activeWebDAVOrigin(state.s3.settings);
    ns.s3NextMountPath = nextMountPath;
    ns.s3ActiveMount = activeMount;
    ns.renderS3Settings = renderS3Settings;
    ns.fetchS3Settings = fetchS3Settings;
    ns.loadS3Files = loadS3Files;
    ns.wireS3 = wireS3;
})();
