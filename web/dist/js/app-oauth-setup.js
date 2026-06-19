/* =========================================================================
   CloudFlared UI - OAuth setup and relay controls
   ========================================================================= */
(() => {
    'use strict';

    const defaultOAuthRelayCallbackURL = 'https://oauth.omarchy.qzz.io/oauth/callback';

    function createOAuthSetup(deps) {
        const {
            state,
            $,
            t,
            smallButton,
            iconButton,
            copyOAuthText,
            saveOAuthRelayCallback,
            checkOAuthRelayCallback,
            minimumScopes,
            fullConsoleScopes,
        } = deps;

        function relayCallbackNode(status) {
            const savedRelay = status?.config?.relay_callback_url || '';
            const configuredRelay = savedRelay || defaultOAuthRelayCallbackURL;
            const isDefaultRelay = configuredRelay === defaultOAuthRelayCallbackURL;
            const form = document.createElement('form');
            form.className = 'oauth-relay-editor';
            form.dataset.mode = isDefaultRelay ? 'default' : 'custom';
            const field = document.createElement('div');
            field.className = 'oauth-relay-field';
            const inputRow = document.createElement('div');
            inputRow.className = 'oauth-relay-input-row';
            const input = document.createElement('input');
            input.className = 'input oauth-relay-input mono';
            input.id = 'oauth-relay-callback-input';
            input.type = 'url';
            input.required = true;
            input.spellcheck = false;
            input.autocomplete = 'off';
            input.placeholder = defaultOAuthRelayCallbackURL;
            input.setAttribute('aria-label', t('oauth_relay_callback'));
            input.setAttribute('aria-describedby', 'oauth-relay-callback-help oauth-relay-callback-status oauth-relay-callback-assist');
            input.value = configuredRelay;
            const primaryActions = document.createElement('span');
            primaryActions.className = 'oauth-relay-primary-actions';
            const copy = iconButton(t('oauth_relay_copy_title'), iconCopySVG(), () => copyOAuthText(input.value.trim()));
            copy.classList.add('oauth-relay-copy');
            const check = smallButton(t('oauth_relay_check'), 'btn btn--sm btn--ghost oauth-relay-check', async (event) => {
                const relayURL = input.value.trim();
                if (relayURL !== configuredRelay) {
                    const saved = await saveOAuthRelayCallback(relayURL, event.currentTarget);
                    if (!saved) return;
                }
                await checkOAuthRelayCallback(event.currentTarget);
            });
            check.title = t('oauth_relay_check_title');
            check.setAttribute('aria-label', t('oauth_relay_check_title'));
            const save = smallButton(t('save'), 'btn btn--sm btn--primary oauth-relay-save');
            save.type = 'submit';
            primaryActions.append(copy, check, save);
            inputRow.append(input, primaryActions);

            const statusLine = relayStatusLine(isDefaultRelay);

            const helper = document.createElement('div');
            helper.className = 'oauth-relay-helper';
            const helperCopy = document.createElement('div');
            helperCopy.className = 'oauth-relay-helper-copy';
            const helperText = document.createElement('span');
            helperText.className = 'oauth-relay-helper-text';
            helperText.id = 'oauth-relay-callback-help';
            helperText.textContent = t('oauth_relay_config_hint');
            const assistText = document.createElement('span');
            assistText.className = 'oauth-relay-assist-text';
            assistText.id = 'oauth-relay-callback-assist';
            assistText.textContent = t('oauth_relay_assist_text');
            helperCopy.append(helperText, assistText);
            const assistActions = document.createElement('span');
            assistActions.className = 'oauth-relay-assist-actions';
            const useDefault = smallButton(t('oauth_relay_use_default'), 'btn btn--xs btn--text oauth-relay-inline-action oauth-relay-text-action', (event) => {
                input.value = defaultOAuthRelayCallbackURL;
                if (savedRelay === defaultOAuthRelayCallbackURL) {
                    input.focus();
                    input.select();
                    return;
                }
                saveOAuthRelayCallback(input.value, event.currentTarget);
            });
            useDefault.title = t('oauth_relay_use_default_title');
            useDefault.setAttribute('aria-label', t('oauth_relay_use_default_title'));
            const selfHost = smallButton(t('oauth_relay_self_host'), 'btn btn--xs btn--text oauth-relay-inline-action oauth-relay-text-action', () => openWorkerScriptDialog());
            selfHost.title = t('oauth_relay_self_host_title');
            selfHost.setAttribute('aria-label', t('oauth_relay_self_host_title'));
            assistActions.append(useDefault, selfHost);
            helper.append(helperCopy, assistActions);

            field.append(inputRow, statusLine, helper);
            form.appendChild(field);
            form.addEventListener('submit', (event) => {
                event.preventDefault();
                saveOAuthRelayCallback(input.value, save);
            });
            return form;
        }

        function relayStatusLine(isDefaultRelay) {
            const line = document.createElement('div');
            line.className = 'oauth-relay-status-line';
            line.id = 'oauth-relay-callback-status';
            line.setAttribute('role', 'status');
            line.setAttribute('aria-live', 'polite');

            const source = document.createElement('span');
            source.className = 'pill oauth-relay-source-pill';
            source.dataset.state = isDefaultRelay ? 'ok' : 'info';
            const sourceDot = document.createElement('span');
            sourceDot.className = 'dot';
            const sourceText = document.createElement('span');
            sourceText.className = 'text';
            sourceText.textContent = isDefaultRelay ? t('oauth_relay_badge_default') : t('oauth_relay_badge_custom');
            source.append(sourceDot, sourceText);

            const message = document.createElement('span');
            message.className = 'oauth-relay-status-message';
            message.textContent = isDefaultRelay ? t('oauth_relay_status_default') : t('oauth_relay_status_custom');

            line.append(source, message);
            const check = relayCheckStatusNode();
            if (check) line.appendChild(check);
            return line;
        }

        function relayCheckStatusNode() {
            const check = state.oauth.relayCheck;
            const loading = state.oauth.relayCheckLoading;
            const error = state.oauth.relayCheckError;
            if (!loading && !error && !check) return null;

            const wrap = document.createElement('span');
            wrap.className = 'oauth-relay-check-status';

            const pill = document.createElement('span');
            pill.className = 'pill oauth-relay-check-pill';
            const dot = document.createElement('span');
            dot.className = 'dot';
            const text = document.createElement('span');
            text.className = 'text';

            const detail = document.createElement('span');
            detail.className = 'oauth-relay-check-detail';
            if (loading) {
                pill.dataset.state = 'loading';
                text.textContent = t('oauth_relay_checking');
            } else if (error) {
                pill.dataset.state = 'error';
                text.textContent = t('oauth_relay_check_failed');
                detail.textContent = error;
            } else if (check?.reachable && check?.supports_state_callback) {
                pill.dataset.state = 'ok';
                text.textContent = t('oauth_relay_check_ok');
            } else if (check?.reachable) {
                pill.dataset.state = 'warn';
                text.textContent = t('oauth_relay_check_outdated');
                detail.textContent = t('oauth_relay_check_outdated_hint');
            } else {
                pill.dataset.state = 'error';
                text.textContent = t('oauth_relay_check_failed');
                detail.textContent = check?.message || '';
            }

            pill.append(dot, text);
            wrap.appendChild(pill);
            if (detail.textContent) wrap.appendChild(detail);
            return wrap;
        }

        function workerScriptURL() {
            return window.location.origin + '/cloudflare-oauth-worker.js';
        }

        function openWorkerScript() {
            window.open('/cloudflare-oauth-worker.js', '_blank', 'noopener');
        }

        async function openWorkerScriptDialog() {
            const dialog = $('oauth-worker-script-dialog');
            if (!dialog) {
                openWorkerScript();
                return;
            }
            window.cfui.openDialog?.(dialog);
            await loadWorkerScript();
        }

        async function loadWorkerScript(force = false) {
            if (state.oauth.workerScriptContent && !force) {
                renderWorkerScriptDialog();
                return;
            }
            state.oauth.workerScriptLoading = true;
            state.oauth.workerScriptError = '';
            renderWorkerScriptDialog();
            try {
                const resp = await fetch('/cloudflare-oauth-worker.js', { cache: 'no-store' });
                if (!resp.ok) throw new Error(`HTTP ${resp.status}`);
                state.oauth.workerScriptContent = await resp.text();
            } catch (err) {
                state.oauth.workerScriptError = err.message || String(err);
            } finally {
                state.oauth.workerScriptLoading = false;
                renderWorkerScriptDialog();
            }
        }

        function renderWorkerScriptDialog() {
            const code = $('oauth-worker-script-content');
            if (!code) return;
            const defaultRelay = $('oauth-worker-default-relay');
            if (defaultRelay) defaultRelay.textContent = defaultOAuthRelayCallbackURL;
            if (state.oauth.workerScriptLoading) {
                code.textContent = t('oauth_worker_script_loading');
            } else if (state.oauth.workerScriptError) {
                code.textContent = t('oauth_worker_script_load_failed', { error: state.oauth.workerScriptError });
            } else {
                code.textContent = state.oauth.workerScriptContent || '';
            }
            const copy = $('oauth-worker-script-copy');
            if (copy) copy.disabled = !state.oauth.workerScriptContent;
        }

        function focusRelayInput() {
            const input = $('oauth-relay-url')?.querySelector('input');
            if (!input) return;
            input.focus();
            input.select();
        }

        function renderSetupGuide(status) {
            const guide = $('oauth-setup-guide');
            if (!guide) return;
            guide.innerHTML = '';
            const configured = !!status?.config?.configured;
            guide.hidden = configured;
            if (configured) return;

            const relayURL = status?.config?.relay_callback_url || '';
            const minimumScopeList = minimumScopes.join(' ');
            const fullConsoleScopeList = fullConsoleScopes.join(' ');
            const envSnippet = [
                `CFUI_OAUTH_CLIENT_ID=${t('oauth_setup_client_id_placeholder')}`,
                `CFUI_OAUTH_RELAY_URL=${relayURL || t('oauth_setup_relay_url_placeholder')}`,
                'CFUI_RUN_MODE=oauth',
            ].join('\n');

            const title = document.createElement('div');
            title.className = 'oauth-setup-title';
            title.textContent = t('oauth_setup_title');
            const subtitle = document.createElement('div');
            subtitle.className = 'oauth-setup-subtitle';
            subtitle.textContent = t('oauth_setup_subtitle');
            guide.append(title, subtitle);

            const steps = document.createElement('div');
            steps.className = 'oauth-setup-steps';
            steps.append(
                setupGuideStep(
                    '1',
                    t('oauth_setup_relay_title'),
                    t('oauth_setup_relay_desc'),
                    [setupGuideNote(t('oauth_setup_relay_input_note'))]
                ),
                setupGuideStep(
                    '2',
                    t('oauth_setup_oauth_app_title'),
                    t('oauth_setup_oauth_app_desc'),
                    [
                        setupGuideCodeRow(t('oauth_setup_cloudflare_path'), t('oauth_setup_cloudflare_path_value'), { copy: false }),
                        setupGuideCodeRow(t('oauth_setup_client_name'), 'cfui'),
                        setupGuideCodeRow(t('oauth_setup_response_type'), t('oauth_setup_response_type_value'), { copy: false }),
                        setupGuideCodeRow(t('oauth_setup_grant_type'), t('oauth_setup_grant_type_value'), { copy: false }),
                        setupGuideCodeRow(t('oauth_setup_token_auth_method'), t('oauth_setup_token_auth_method_value'), { copy: false }),
                        setupGuideCodeRow(t('oauth_setup_redirect_uri'), relayURL || defaultOAuthRelayCallbackURL, {
                            actionLabel: t('oauth_relay_configure'),
                            actionTitle: t('oauth_relay_edit'),
                            action: focusRelayInput,
                        }),
                        setupGuideNote(t('oauth_setup_redirect_uri_note')),
                        setupGuideCodeRow(t('oauth_setup_client_url'), t('oauth_setup_client_url_value'), { copy: false }),
                    ]
                ),
                setupGuideStep(
                    '3',
                    t('oauth_setup_permissions_title'),
                    t('oauth_setup_permissions_desc'),
                    [
                        setupGuideCodeRow(t('oauth_setup_permissions_minimum'), minimumScopeList),
                        setupGuideCodeRow(t('oauth_setup_permissions_full'), fullConsoleScopeList),
                        setupGuideNote(t('oauth_setup_permissions_scope_model')),
                        setupGuideNote(t('oauth_setup_permissions_categories')),
                        setupGuideNote(t('oauth_setup_permissions_write_note')),
                    ]
                ),
                setupGuideStep(
                    '4',
                    t('oauth_setup_env_title'),
                    t('oauth_setup_env_desc'),
                    [setupGuideCodeRow(t('oauth_setup_env_vars'), envSnippet)]
                ),
            );
            guide.appendChild(steps);
        }

        function setupGuideStep(index, titleText, descText, rows = []) {
            const step = document.createElement('section');
            step.className = 'oauth-setup-step';
            const badge = document.createElement('div');
            badge.className = 'oauth-setup-index';
            badge.textContent = index;
            const body = document.createElement('div');
            const title = document.createElement('div');
            title.className = 'oauth-setup-step-title';
            title.textContent = titleText;
            const desc = document.createElement('p');
            desc.className = 'oauth-setup-step-desc';
            desc.textContent = descText;
            body.append(title, desc);
            for (const row of rows) body.appendChild(row);
            step.append(badge, body);
            return step;
        }

        function setupGuideCodeRow(labelText, value, options = {}) {
            const row = document.createElement('div');
            row.className = 'oauth-setup-code-row';
            const label = document.createElement('div');
            label.className = 'oauth-setup-code-label';
            label.textContent = labelText;
            const code = document.createElement('pre');
            code.className = 'oauth-setup-code mono';
            code.textContent = value || '';
            row.append(label, code);
            if (options.action && options.actionLabel) {
                const action = smallButton(options.actionLabel, 'btn btn--sm btn--ghost', options.action);
                if (options.actionTitle) {
                    action.title = options.actionTitle;
                    action.setAttribute('aria-label', options.actionTitle);
                }
                row.appendChild(action);
                return row;
            }
            const actions = document.createElement('div');
            actions.className = 'oauth-config-actions';
            if (Array.isArray(options.actions)) {
                for (const item of options.actions) {
                    const action = smallButton(item.label, 'btn btn--sm btn--ghost', item.action);
                    if (item.title) {
                        action.title = item.title;
                        action.setAttribute('aria-label', item.title);
                    }
                    actions.appendChild(action);
                }
            }
            if (options.copy !== false) {
                const copy = smallButton(t('copy'), 'btn btn--sm btn--ghost', () => copyOAuthText(value || ''));
                actions.appendChild(copy);
            }
            if (actions.childElementCount) row.appendChild(actions);
            return row;
        }

        function setupGuideNote(text) {
            const note = document.createElement('div');
            note.className = 'oauth-setup-note';
            note.textContent = text;
            return note;
        }

        return {
            relayCallbackNode,
            renderSetupGuide,
            loadWorkerScript,
            openWorkerScript,
            openWorkerScriptDialog,
            renderWorkerScriptDialog,
            workerScriptURL,
        };
    }

    function iconCopySVG() {
        return '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="13" height="13" rx="2" ry="2"></rect><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"></path></svg>';
    }

    window.cfui.oauthSetup = {
        defaultOAuthRelayCallbackURL,
        create: createOAuthSetup,
    };
})();
