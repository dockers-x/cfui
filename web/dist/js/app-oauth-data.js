/* =========================================================================
   CloudFlared UI — OAuth Cloudflare console data
   ========================================================================= */
(() => {
    'use strict';
    const { t } = window.cfui;

    const resourceDefinitions = [
        { id: 'overview', public: true },
        { id: 'zones', feature: 'zones' },
        { id: 'dns', feature: 'dns', needsZone: true },
        { id: 'tunnels', feature: 'tunnels', needsAccount: true },
        { id: 'workers', feature: 'workers', needsAccount: true },
        { id: 'storage', anyFeature: ['r2', 'd1', 'kv'], needsAccount: true },
        { id: 'usage', feature: 'analytics', needsAccount: true },
        { id: 'snippets', feature: 'snippets', needsZone: true },
        { id: 'waf', feature: 'waf', needsZone: true },
        { id: 'analytics', feature: 'analytics', needsZone: true },
        { id: 'settings', feature: 'zone_settings', needsZone: true },
        { id: 'status', public: true },
    ];

    const dnsTypes = ['A', 'AAAA', 'CNAME', 'TXT'];
    const wafActions = ['block', 'challenge', 'managed_challenge', 'js_challenge', 'log', 'skip'];
    const wafManagedOverrideActions = ['', 'block', 'challenge', 'managed_challenge', 'js_challenge', 'log'];
    const wafManagedSensitivityLevels = ['', 'default', 'high', 'medium', 'low', 'eoff'];
    const wafSkipProducts = [
        ['zoneLockdown', 'oauth_waf_skip_product_zone_lockdown'],
        ['uaBlock', 'oauth_waf_skip_product_ua_block'],
        ['bic', 'oauth_waf_skip_product_bic'],
        ['hot', 'oauth_waf_skip_product_hot'],
        ['securityLevel', 'oauth_waf_skip_product_security_level'],
        ['rateLimit', 'oauth_waf_skip_product_rate_limit'],
        ['waf', 'oauth_waf_skip_product_waf'],
    ];
    const wafSkipPhases = [
        ['http_ratelimit', 'oauth_waf_skip_phase_rate_limit'],
        ['http_request_sbfm', 'oauth_waf_skip_phase_sbfm'],
        ['http_request_firewall_managed', 'oauth_waf_skip_phase_managed_waf'],
    ];
    const maxR2ObjectUploadBytes = 128 * 1024 * 1024;
    const maxR2ChunkedUploadBytes = 5 * 1024 * 1024 * 1024;
    const r2ObjectUploadChunkBytes = 8 * 1024 * 1024;
    const maxR2InlinePreviewBytes = 50 * 1024 * 1024;
    const maxKVValueUploadBytes = 25 * 1024 * 1024;
    const analyticsRanges = ['24h', '7d', '30d'];
    const overviewMetricDefinitions = [
        ['accounts', 'oauth_overview_metric_accounts'],
        ['zones', 'oauth_overview_metric_zones'],
        ['active_zones', 'oauth_overview_metric_active_zones'],
        ['dns_records', 'oauth_overview_metric_dns_records'],
        ['workers', 'oauth_overview_metric_workers'],
        ['tunnels', 'oauth_overview_metric_tunnels'],
        ['r2_buckets', 'oauth_overview_metric_r2_buckets'],
        ['d1_databases', 'oauth_overview_metric_d1_databases'],
        ['kv_namespaces', 'oauth_overview_metric_kv_namespaces'],
        ['snippets', 'oauth_overview_metric_snippets'],
        ['waf_rules', 'oauth_overview_metric_waf_rules'],
    ];
    const oauthPermissionDefinitions = [
        {
            id: 'account',
            title: () => t('oauth_account'),
            description: () => t('oauth_permission_account_desc'),
            readScopes: ['account-settings.read'],
            writeScopes: [],
            required: true,
        },
        {
            id: 'zones',
            title: () => t('oauth_zones'),
            description: () => t('oauth_permission_zones_desc'),
            readScopes: ['zone.read'],
            writeScopes: ['zone.write'],
            required: true,
        },
        {
            id: 'dns',
            title: () => t('oauth_dns'),
            description: () => t('oauth_permission_dns_desc'),
            readScopes: ['dns.read'],
            writeScopes: ['dns.write'],
        },
        {
            id: 'workers',
            title: () => t('oauth_workers'),
            description: () => t('oauth_permission_workers_desc'),
            readScopes: ['workers-scripts.read'],
            writeScopes: [],
        },
        {
            id: 'workers_tail',
            title: () => t('oauth_worker_tail_live'),
            description: () => t('oauth_permission_workers_tail_desc'),
            readScopes: ['workers-tail.read'],
            writeScopes: [],
        },
        {
            id: 'snippets',
            title: () => t('oauth_snippets'),
            description: () => t('oauth_permission_snippets_desc'),
            readScopes: ['snippets.read'],
            writeScopes: ['snippets.write'],
        },
        {
            id: 'r2',
            title: () => t('oauth_r2_buckets'),
            description: () => t('oauth_permission_r2_desc'),
            readScopes: ['workers-r2.read'],
            writeScopes: ['workers-r2.write'],
        },
        {
            id: 'd1',
            title: () => t('oauth_d1_databases'),
            description: () => t('oauth_permission_d1_desc'),
            readScopes: ['d1.read'],
            writeScopes: ['d1.write'],
        },
        {
            id: 'kv',
            title: () => t('oauth_kv_namespaces'),
            description: () => t('oauth_permission_kv_desc'),
            readScopes: ['workers-kv-storage.read'],
            writeScopes: ['workers-kv-storage.write'],
        },
        {
            id: 'tunnels',
            title: () => t('oauth_tunnels'),
            description: () => t('oauth_permission_tunnels_desc'),
            readScopes: ['argotunnel.read'],
            writeScopes: ['argotunnel.write'],
        },
        {
            id: 'waf',
            title: () => t('oauth_waf'),
            description: () => t('oauth_permission_waf_desc'),
            readScopes: ['zone-waf.read'],
            writeScopes: ['zone-waf.write'],
        },
        {
            id: 'zone_settings',
            title: () => t('oauth_zone_settings'),
            description: () => t('oauth_permission_zone_settings_desc'),
            readScopes: ['zone-settings.read'],
            writeScopes: ['zone-settings.write'],
        },
        {
            id: 'cache_purge',
            title: () => t('oauth_cache_purge'),
            description: () => t('oauth_permission_cache_purge_desc'),
            readScopes: [],
            writeOnly: true,
            acceptedWriteScopes: ['cache_purge.write', 'cache.purge'],
            writeScopes: ['cache_purge.write'],
        },
        {
            id: 'analytics',
            title: () => t('oauth_analytics'),
            description: () => t('oauth_permission_analytics_desc'),
            readScopes: ['account-analytics.read', 'analytics.read'],
            writeScopes: [],
        },
    ];
    const oauthMinimumSetupScopes = [
        'account-settings.read',
        'zone.read',
        'dns.read',
    ];
    const oauthFullConsoleSetupScopes = [
        'account-settings.read',
        'zone.read',
        'dns.read',
        'dns.write',
        'argotunnel.read',
        'argotunnel.write',
        'workers-scripts.read',
        'workers-tail.read',
        'workers-r2.read',
        'workers-r2.write',
        'd1.read',
        'd1.write',
        'workers-kv-storage.read',
        'workers-kv-storage.write',
        'snippets.read',
        'snippets.write',
        'zone-waf.read',
        'zone-waf.write',
        'zone-settings.read',
        'zone-settings.write',
        'cache_purge.write',
        'analytics.read',
        'account-analytics.read',
    ];
    const securityLevels = [
        ['essentially_off', 'oauth_security_essentially_off'],
        ['low', 'oauth_security_low'],
        ['medium', 'oauth_security_medium'],
        ['high', 'oauth_security_high'],
        ['under_attack', 'oauth_security_under_attack'],
    ];
    const zoneSettingToggles = [
        ['development_mode', 'oauth_development_mode'],
        ['always_use_https', 'oauth_always_use_https'],
        ['automatic_https_rewrites', 'oauth_automatic_https_rewrites'],
        ['brotli', 'oauth_brotli'],
        ['rocket_loader', 'oauth_rocket_loader'],
        ['ipv6', 'oauth_ipv6_compatibility'],
        ['websockets', 'oauth_websockets'],
        ['http2', 'oauth_http2'],
        ['http3', 'oauth_http3'],
        ['early_hints', 'oauth_early_hints'],
        ['email_obfuscation', 'oauth_email_obfuscation'],
        ['hotlink_protection', 'oauth_hotlink_protection'],
        ['server_side_exclude', 'oauth_server_side_exclude'],
        ['always_online', 'oauth_always_online'],
        ['browser_check', 'oauth_browser_check'],
        ['ip_geolocation', 'oauth_ip_geolocation'],
        ['opportunistic_encryption', 'oauth_opportunistic_encryption'],
        ['0rtt', 'oauth_0rtt'],
    ];
    const cacheLevels = [
        ['aggressive', 'oauth_cache_level_aggressive'],
        ['basic', 'oauth_cache_level_basic'],
        ['simplified', 'oauth_cache_level_simplified'],
    ];
    const sslModes = [
        ['off', 'oauth_ssl_off'],
        ['flexible', 'oauth_ssl_flexible'],
        ['full', 'oauth_ssl_full'],
        ['strict', 'oauth_ssl_strict'],
        ['origin_pull', 'oauth_ssl_origin_pull'],
    ];
    const tls13Modes = [
        ['off', 'oauth_tls_1_3_off'],
        ['on', 'oauth_tls_1_3_on'],
        ['zrt', 'oauth_tls_1_3_zrt'],
    ];
    const minimumTLSVersions = [
        ['1.0', 'oauth_min_tls_1_0'],
        ['1.1', 'oauth_min_tls_1_1'],
        ['1.2', 'oauth_min_tls_1_2'],
        ['1.3', 'oauth_min_tls_1_3'],
    ];
    const browserCacheTTLs = [
        [0, 'oauth_browser_cache_ttl_origin'],
        [7200, 'oauth_browser_cache_ttl_2h'],
        [14400, 'oauth_browser_cache_ttl_4h'],
        [28800, 'oauth_browser_cache_ttl_8h'],
        [43200, 'oauth_browser_cache_ttl_12h'],
        [86400, 'oauth_browser_cache_ttl_1d'],
        [259200, 'oauth_browser_cache_ttl_3d'],
        [604800, 'oauth_browser_cache_ttl_1w'],
        [2678400, 'oauth_browser_cache_ttl_1mo'],
        [31536000, 'oauth_browser_cache_ttl_1y'],
    ];
    const writableZoneSettings = new Set([
        'ssl',
        'security_level',
        'development_mode',
        'cache_level',
        'browser_cache_ttl',
        'always_use_https',
        'automatic_https_rewrites',
        'brotli',
        'rocket_loader',
        'ipv6',
        'websockets',
        'http2',
        'http3',
        'early_hints',
        'email_obfuscation',
        'hotlink_protection',
        'server_side_exclude',
        'always_online',
        'browser_check',
        'ip_geolocation',
        'opportunistic_encryption',
        '0rtt',
        'tls_1_3',
        'min_tls_version',
    ]);

    window.cfui.oauthData = {
        resourceDefinitions,
        dnsTypes,
        wafActions,
        wafManagedOverrideActions,
        wafManagedSensitivityLevels,
        wafSkipProducts,
        wafSkipPhases,
        maxR2ObjectUploadBytes,
        maxR2ChunkedUploadBytes,
        r2ObjectUploadChunkBytes,
        maxR2InlinePreviewBytes,
        maxKVValueUploadBytes,
        analyticsRanges,
        overviewMetricDefinitions,
        oauthPermissionDefinitions,
        oauthMinimumSetupScopes,
        oauthFullConsoleSetupScopes,
        securityLevels,
        zoneSettingToggles,
        cacheLevels,
        sslModes,
        tls13Modes,
        minimumTLSVersions,
        browserCacheTTLs,
        writableZoneSettings,
    };
})();
