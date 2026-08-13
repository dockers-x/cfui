/* =========================================================================
   CloudFlared UI — Optional local access protection
   ========================================================================= */
(() => {
    'use strict';
    const { state, $, t, apiGet, apiSend, toast, setBusy, openDialog, closeDialog } = window.cfui;
    let loginStatusPoll = null;

    function authStatus() {
        return state.localAuth?.status || { enabled: false, authenticated: false, username: '' };
    }

    async function fetchLocalAuthStatus() {
        const status = await apiGet('/auth/status');
        state.localAuth.status = status;
        renderLocalAuthFeature();
        return status;
    }

    function renderLocalAuthFeature() {
        const status = authStatus();
        const pill = $('feature-local-auth-status');
        if (pill) {
            pill.dataset.state = status.enabled ? 'ok' : 'disabled';
            const text = pill.querySelector('.text');
            if (text) text.textContent = t(status.enabled ? 'local_auth_enabled' : 'local_auth_disabled');
        }
        const button = $('feature-local-auth-open');
        if (button) button.textContent = t(status.enabled ? 'local_auth_manage' : 'local_auth_enable');
    }

    function setAuthError(id, message = '') {
        const node = $(id);
        if (!node) return;
        node.textContent = message;
        node.hidden = !message;
    }

    function openLoginDialog() {
        const dialog = $('local-auth-login-dialog');
        if (!dialog || !authStatus().enabled || authStatus().authenticated) return;
        if (!dialog.hidden) return;
        $('local-auth-login-username').value = authStatus().username || '';
        $('local-auth-login-password').value = '';
        setAuthError('local-auth-login-error');
        openDialog(dialog);
    }

    function waitForLogin() {
        if (authStatus().authenticated || !authStatus().enabled) return Promise.resolve();
        return new Promise((resolve) => {
            state.localAuth.loginWaiters.push(resolve);
            openLoginDialog();
            startLoginStatusPolling();
        });
    }

    function resolveLoginWaiters() {
        stopLoginStatusPolling();
        const waiters = state.localAuth.loginWaiters.splice(0);
        waiters.forEach((resolve) => resolve());
    }

    function startLoginStatusPolling() {
        if (loginStatusPoll) return;
        loginStatusPoll = window.setInterval(async () => {
            try {
                const status = await fetchLocalAuthStatus();
                if (!status.enabled || status.authenticated) {
                    closeDialog($('local-auth-login-dialog'));
                    resolveLoginWaiters();
                }
            } catch { /* a transient status failure keeps the login gate in place */ }
        }, 5000);
    }

    function stopLoginStatusPolling() {
        if (!loginStatusPoll) return;
        window.clearInterval(loginStatusPoll);
        loginStatusPoll = null;
    }

    async function initializeLocalAuth() {
        const status = await fetchLocalAuthStatus();
        if (status.enabled && !status.authenticated) await waitForLogin();
    }

    async function submitLogin(event) {
        event.preventDefault();
        const button = $('local-auth-login-submit');
        setBusy(button, true, t('local_auth_signing_in'));
        setAuthError('local-auth-login-error');
        try {
            const status = await apiSend('/auth/login', 'POST', {
                username: $('local-auth-login-username').value.trim(),
                password: $('local-auth-login-password').value,
            });
            state.localAuth.status = status;
            closeDialog($('local-auth-login-dialog'));
            renderLocalAuthFeature();
            resolveLoginWaiters();
        } catch (err) {
            try {
                const status = await fetchLocalAuthStatus();
                if (!status.enabled || status.authenticated) {
                    closeDialog($('local-auth-login-dialog'));
                    resolveLoginWaiters();
                    return;
                }
            } catch { /* retain the original login error */ }
            setAuthError('local-auth-login-error', err.message);
            $('local-auth-login-password').select();
        } finally {
            setBusy(button, false);
        }
    }

    function openLocalAuthSettings() {
        const status = authStatus();
        if (!status.enabled) {
            $('local-auth-setup-form').reset();
            $('local-auth-setup-username').value = 'admin';
            setAuthError('local-auth-setup-error');
            openDialog($('local-auth-setup-dialog'));
            return;
        }
        $('local-auth-manage-username').textContent = status.username || '—';
        ['local-auth-password-form', 'local-auth-revoke-form', 'local-auth-disable-form'].forEach((id) => $(id)?.reset());
        ['local-auth-password-error', 'local-auth-revoke-error', 'local-auth-disable-error'].forEach((id) => setAuthError(id));
        openDialog($('local-auth-manage-dialog'));
    }

    async function submitSetup(event) {
        event.preventDefault();
        const password = $('local-auth-setup-password').value;
        if (password !== $('local-auth-setup-confirm').value) {
            setAuthError('local-auth-setup-error', t('local_auth_password_mismatch'));
            return;
        }
        const button = $('local-auth-setup-submit');
        setBusy(button, true, t('saving'));
        setAuthError('local-auth-setup-error');
        try {
            const status = await apiSend('/auth/setup', 'POST', {
                username: $('local-auth-setup-username').value.trim(),
                password,
            });
            state.localAuth.status = status;
            closeDialog($('local-auth-setup-dialog'));
            renderLocalAuthFeature();
            toast.ok(t('local_auth_enabled_message'));
            openLoginDialog();
            startLoginStatusPolling();
        } catch (err) {
            try {
                const recovered = await fetchLocalAuthStatus();
                if (recovered.enabled && !recovered.authenticated) {
                    closeDialog($('local-auth-setup-dialog'));
                    openLoginDialog();
                    startLoginStatusPolling();
                    return;
                }
            } catch { /* retain the original setup error */ }
            setAuthError('local-auth-setup-error', err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function submitPasswordChange(event) {
        event.preventDefault();
        const newPassword = $('local-auth-new-password').value;
        if (newPassword !== $('local-auth-new-password-confirm').value) {
            setAuthError('local-auth-password-error', t('local_auth_password_mismatch'));
            return;
        }
        const button = $('local-auth-password-submit');
        setBusy(button, true, t('saving'));
        setAuthError('local-auth-password-error');
        try {
            const status = await apiSend('/auth/password', 'POST', {
                current_password: $('local-auth-current-password').value,
                new_password: newPassword,
            });
            state.localAuth.status = status;
            event.currentTarget.reset();
            toast.ok(t('local_auth_password_changed'));
        } catch (err) {
            setAuthError('local-auth-password-error', err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function submitRevokeOthers(event) {
        event.preventDefault();
        const button = $('local-auth-revoke-submit');
        setBusy(button, true, t('local_auth_revoking'));
        setAuthError('local-auth-revoke-error');
        try {
            await apiSend('/auth/sessions/revoke-others', 'POST', { password: $('local-auth-revoke-password').value });
            event.currentTarget.reset();
            toast.ok(t('local_auth_sessions_revoked'));
        } catch (err) {
            setAuthError('local-auth-revoke-error', err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function submitDisable(event) {
        event.preventDefault();
        const button = $('local-auth-disable-submit');
        setBusy(button, true, t('local_auth_disabling'));
        setAuthError('local-auth-disable-error');
        try {
            const status = await apiSend('/auth/disable', 'POST', { password: $('local-auth-disable-password').value });
            state.localAuth.status = status;
            closeDialog($('local-auth-manage-dialog'));
            renderLocalAuthFeature();
            toast.ok(t('local_auth_disabled_message'));
        } catch (err) {
            setAuthError('local-auth-disable-error', err.message);
        } finally {
            setBusy(button, false);
        }
    }

    async function logoutCurrentSession() {
        const button = $('local-auth-logout');
        setBusy(button, true);
        try {
            await apiSend('/auth/logout', 'POST');
            state.localAuth.status = { ...authStatus(), authenticated: false };
            closeDialog($('local-auth-manage-dialog'));
            openLoginDialog();
            startLoginStatusPolling();
        } catch (err) {
            toast.err(err.message);
        } finally {
            setBusy(button, false);
        }
    }

    function wireLocalAuth() {
        $('feature-local-auth-open')?.addEventListener('click', openLocalAuthSettings);
        $('local-auth-login-form')?.addEventListener('submit', submitLogin);
        $('local-auth-setup-form')?.addEventListener('submit', submitSetup);
        $('local-auth-password-form')?.addEventListener('submit', submitPasswordChange);
        $('local-auth-revoke-form')?.addEventListener('submit', submitRevokeOthers);
        $('local-auth-disable-form')?.addEventListener('submit', submitDisable);
        $('local-auth-logout')?.addEventListener('click', logoutCurrentSession);
        document.addEventListener('localauthrequired', () => {
            fetchLocalAuthStatus().then((status) => {
                if (status.enabled && !status.authenticated) {
                    openLoginDialog();
                    startLoginStatusPolling();
                }
            }).catch(() => {});
        });
        document.addEventListener('localechange', renderLocalAuthFeature);
    }

    const ns = window.cfui;
    ns.fetchLocalAuthStatus = fetchLocalAuthStatus;
    ns.renderLocalAuthFeature = renderLocalAuthFeature;
    ns.initializeLocalAuth = initializeLocalAuth;
    ns.wireLocalAuth = wireLocalAuth;
})();
