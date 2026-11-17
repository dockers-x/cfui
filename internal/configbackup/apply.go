package configbackup

import (
	"cfui/internal/config"
	"fmt"
	"reflect"
	"slices"
	"sort"
)

func Apply(current config.Config, payload Payload, selected []Section) (ApplyResult, error) {
	var err error
	payload, err = canonicalizePayload(payload)
	if err != nil {
		return ApplyResult{}, err
	}
	if err := validatePayload(payload); err != nil {
		return ApplyResult{}, err
	}
	selection, err := validateImportSelection(payload, selected)
	if err != nil {
		return ApplyResult{}, err
	}

	before := cloneConfig(current)
	next := cloneConfig(current)
	var warnings []string
	if selection[SectionTunnels] {
		next = applyTunnelSection(next, *payload.Tunnels)
	}
	if selection[SectionRemoteManagement] {
		var sectionWarnings []string
		next, sectionWarnings = applyRemoteSection(next, *payload.RemoteManagement)
		warnings = append(warnings, sectionWarnings...)
	}
	if selection[SectionDDNS] {
		next.DDNS = ddnsConfig(*payload.DDNS)
	}
	if selection[SectionS3WebDAV] {
		next = applyS3Section(next, *payload.S3WebDAV)
	}
	if selection[SectionApplication] {
		next.MCPEnabled = payload.Application.MCPEnabled
		next.OAuthClientID = payload.Application.OAuthClientID
		next.OAuthRelayCallbackURL = payload.Application.OAuthRelayCallbackURL
	}
	if selection[SectionSensitive] {
		var sectionWarnings []string
		next, sectionWarnings = applySensitive(next, *payload.Sensitive, availableSections(payload))
		warnings = append(warnings, sectionWarnings...)
	}

	result := ApplyResult{
		Config:            next,
		Warnings:          sortedUnique(warnings),
		RemovedTunnelKeys: removedTunnelKeys(before, next),
		ChangedTunnelKeys: changedTunnelKeys(before, next),
	}
	for _, section := range sectionOrder {
		if selection[section] && sectionChanged(section, before, next) {
			result.ChangedSections = append(result.ChangedSections, section)
		}
	}
	return result, nil
}

func validateImportSelection(payload Payload, selected []Section) (map[Section]bool, error) {
	if len(selected) == 0 {
		return nil, ErrInvalidSelection
	}
	available := make(map[Section]bool, len(payload.Sections))
	for _, section := range payload.Sections {
		available[section] = true
	}
	selection := make(map[Section]bool, len(selected))
	for _, section := range selected {
		if sectionIndex(section) < 0 || selection[section] || !available[section] {
			return nil, ErrInvalidSelection
		}
		selection[section] = true
	}
	return selection, nil
}

func applyTunnelSection(current config.Config, imported TunnelSection) config.Config {
	existing := make(map[string]config.TunnelProfileConfig, len(current.Tunnels))
	for _, tunnel := range current.Tunnels {
		existing[config.NormalizeTunnelKey(tunnel.Key)] = tunnel
	}
	profiles := make([]config.TunnelProfileConfig, 0, len(imported.Profiles))
	for _, profile := range imported.Profiles {
		tunnel := tunnelConfig(profile)
		if previous, ok := existing[tunnel.Key]; ok {
			tunnel.Token = previous.Token
			tunnel.RemoteManagementEnabled = previous.RemoteManagementEnabled
			tunnel.AccountID = previous.AccountID
			tunnel.TunnelID = previous.TunnelID
		}
		profiles = append(profiles, tunnel)
	}
	current.ActiveTunnelKey = imported.ActiveKey
	current.Tunnels = profiles
	return current
}

func applyRemoteSection(cfg config.Config, imported RemoteManagementSection) (config.Config, []string) {
	indexes := make(map[string]int, len(cfg.Tunnels))
	for i, tunnel := range cfg.Tunnels {
		indexes[config.NormalizeTunnelKey(tunnel.Key)] = i
	}
	var warnings []string
	for _, profile := range imported.Profiles {
		index, ok := indexes[profile.Key]
		if !ok {
			warnings = append(warnings, fmt.Sprintf("remote settings target %q was not found", profile.Key))
			continue
		}
		cfg.Tunnels[index].RemoteManagementEnabled = profile.Enabled
		cfg.Tunnels[index].AccountID = profile.AccountID
		cfg.Tunnels[index].TunnelID = profile.TunnelID
	}
	cfg.TunnelManagement.APIEmail = imported.APIEmail
	return cfg, warnings
}

func applyS3Section(current config.Config, imported S3WebDAVSection) config.Config {
	existing := make(map[string]config.S3WebDAVMountConfig, len(current.S3WebDAV.Mounts))
	for _, mount := range current.S3WebDAV.Mounts {
		existing[config.NormalizeS3MountKey(mount.Key)] = mount
	}
	mounts := make([]config.S3WebDAVMountConfig, 0, len(imported.Mounts))
	for _, importedMount := range imported.Mounts {
		mount := s3MountConfig(importedMount)
		if previous, ok := existing[mount.Key]; ok {
			mount.AccessKeyID = previous.AccessKeyID
			mount.SecretAccessKey = previous.SecretAccessKey
			mount.WebDAVPasswordHash = previous.WebDAVPasswordHash
		}
		mounts = append(mounts, mount)
	}
	current.S3WebDAV = config.S3WebDAVConfig{
		Enabled:                 imported.Enabled,
		ActiveKey:               imported.ActiveKey,
		WebDAVAccessMode:        imported.WebDAVAccessMode,
		DedicatedBindHost:       imported.DedicatedBindHost,
		DedicatedPort:           imported.DedicatedPort,
		DedicatedAutoStart:      imported.DedicatedAutoStart,
		DedicatedDomainMode:     imported.DedicatedDomainMode,
		DedicatedCustomDomain:   imported.DedicatedCustomDomain,
		DedicatedTunnelHostname: imported.DedicatedTunnelHostname,
		Mounts:                  mounts,
	}
	return current
}

func applySensitive(cfg config.Config, imported SensitiveSection, owners map[Section]bool) (config.Config, []string) {
	tunnelIndexes := make(map[string]int, len(cfg.Tunnels))
	for i, tunnel := range cfg.Tunnels {
		tunnelIndexes[config.NormalizeTunnelKey(tunnel.Key)] = i
	}
	var warnings []string
	if owners[SectionTunnels] {
		for key, token := range imported.TunnelTokens {
			index, ok := tunnelIndexes[key]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("tunnel credential target %q was not found", key))
				continue
			}
			cfg.Tunnels[index].Token = token
		}
	}
	if owners[SectionRemoteManagement] {
		cfg.TunnelManagement.APIToken = imported.APIToken
		cfg.TunnelManagement.APIKey = imported.APIKey
	}

	mountIndexes := make(map[string]int, len(cfg.S3WebDAV.Mounts))
	for i, mount := range cfg.S3WebDAV.Mounts {
		mountIndexes[config.NormalizeS3MountKey(mount.Key)] = i
	}
	if owners[SectionS3WebDAV] {
		for key, credentials := range imported.S3 {
			index, ok := mountIndexes[key]
			if !ok {
				warnings = append(warnings, fmt.Sprintf("S3 credential target %q was not found", key))
				continue
			}
			cfg.S3WebDAV.Mounts[index].AccessKeyID = credentials.AccessKeyID
			cfg.S3WebDAV.Mounts[index].SecretAccessKey = credentials.SecretAccessKey
			cfg.S3WebDAV.Mounts[index].WebDAVPasswordHash = credentials.WebDAVPasswordHash
		}
	}
	return cfg, warnings
}

func availableSections(payload Payload) map[Section]bool {
	available := make(map[Section]bool, len(payload.Sections))
	for _, section := range payload.Sections {
		available[section] = true
	}
	return available
}

func DiffTunnels(before, after config.Config) TunnelDiff {
	return TunnelDiff{
		RemovedKeys: removedTunnelKeys(before, after),
		ChangedKeys: changedTunnelKeys(before, after),
	}
}

func removedTunnelKeys(before, after config.Config) []string {
	afterKeys := make(map[string]bool, len(after.Tunnels))
	for _, tunnel := range after.Tunnels {
		afterKeys[tunnel.Key] = true
	}
	var removed []string
	for _, tunnel := range before.Tunnels {
		if !afterKeys[tunnel.Key] {
			removed = append(removed, tunnel.Key)
		}
	}
	sort.Strings(removed)
	return removed
}

func changedTunnelKeys(before, after config.Config) []string {
	beforeByKey := make(map[string]config.TunnelProfileConfig, len(before.Tunnels))
	for _, tunnel := range before.Tunnels {
		beforeByKey[tunnel.Key] = tunnel
	}
	var changed []string
	for _, tunnel := range after.Tunnels {
		previous, ok := beforeByKey[tunnel.Key]
		if ok && !tunnelRuntimeEqual(previous, tunnel) {
			changed = append(changed, tunnel.Key)
		}
	}
	sort.Strings(changed)
	return changed
}

func tunnelRuntimeEqual(a, b config.TunnelProfileConfig) bool {
	return a.Token == b.Token &&
		a.LocalEnabled == b.LocalEnabled &&
		a.AutoStart == b.AutoStart &&
		a.AutoRestart == b.AutoRestart &&
		a.CustomTag == b.CustomTag &&
		a.SoftwareName == b.SoftwareName &&
		a.Protocol == b.Protocol &&
		a.GracePeriod == b.GracePeriod &&
		a.Region == b.Region &&
		a.Retries == b.Retries &&
		a.MetricsEnable == b.MetricsEnable &&
		a.MetricsPort == b.MetricsPort &&
		a.LogLevel == b.LogLevel &&
		a.LogFile == b.LogFile &&
		a.LogJSON == b.LogJSON &&
		a.EdgeIPVersion == b.EdgeIPVersion &&
		a.EdgeBindAddress == b.EdgeBindAddress &&
		a.PostQuantum == b.PostQuantum &&
		a.NoTLSVerify == b.NoTLSVerify &&
		a.ExtraArgs == b.ExtraArgs
}

func sectionChanged(section Section, before, after config.Config) bool {
	switch section {
	case SectionTunnels:
		return !reflect.DeepEqual(tunnelSection(before), tunnelSection(after))
	case SectionRemoteManagement:
		return !reflect.DeepEqual(remoteSection(before), remoteSection(after))
	case SectionDDNS:
		return !reflect.DeepEqual(ddnsSection(before.DDNS), ddnsSection(after.DDNS))
	case SectionS3WebDAV:
		return !reflect.DeepEqual(s3Section(before), s3Section(after))
	case SectionApplication:
		return before.MCPEnabled != after.MCPEnabled || before.OAuthClientID != after.OAuthClientID || before.OAuthRelayCallbackURL != after.OAuthRelayCallbackURL
	case SectionSensitive:
		selected := map[Section]bool{SectionTunnels: true, SectionRemoteManagement: true, SectionS3WebDAV: true}
		return !reflect.DeepEqual(sensitiveSection(before, selected), sensitiveSection(after, selected))
	default:
		return false
	}
}

func tunnelConfig(profile TunnelProfile) config.TunnelProfileConfig {
	return config.TunnelProfileConfig{
		Key: profile.Key, Name: profile.Name, LocalEnabled: profile.LocalEnabled,
		AutoStart: profile.AutoStart, AutoRestart: profile.AutoRestart,
		CustomTag: profile.CustomTag, SoftwareName: profile.SoftwareName,
		Protocol: profile.Protocol, GracePeriod: profile.GracePeriod, Region: profile.Region,
		Retries: profile.Retries, MetricsEnable: profile.MetricsEnable, MetricsPort: profile.MetricsPort,
		LogLevel: profile.LogLevel, LogFile: profile.LogFile, LogJSON: profile.LogJSON,
		EdgeIPVersion: profile.EdgeIPVersion, EdgeBindAddress: profile.EdgeBindAddress,
		PostQuantum: profile.PostQuantum, NoTLSVerify: profile.NoTLSVerify, ExtraArgs: profile.ExtraArgs,
	}
}

func ddnsConfig(imported DDNSSection) config.DDNSConfig {
	sources := make([]config.IPSource, 0, len(imported.IPSources))
	for _, source := range imported.IPSources {
		sources = append(sources, config.IPSource{URL: source.URL, IPType: source.IPType})
	}
	records := make([]config.DDNSRecord, 0, len(imported.Records))
	for _, record := range imported.Records {
		records = append(records, config.DDNSRecord{
			Name: record.Name, ZoneID: record.ZoneID, ZoneName: record.ZoneName,
			Type: record.Type, Value: record.Value, Comment: record.Comment,
			Proxied: record.Proxied, TTL: record.TTL,
		})
	}
	return config.DDNSConfig{
		Enabled: imported.Enabled, IPSources: sources, Records: records,
		IntervalMins: imported.IntervalMins, OnlyOnChange: imported.OnlyOnChange, MaxRetries: imported.MaxRetries,
		UseProxy: imported.UseProxy,
	}
}

func s3MountConfig(mount S3Mount) config.S3WebDAVMountConfig {
	return config.S3WebDAVMountConfig{
		Key: mount.Key, Name: mount.Name, Enabled: mount.Enabled,
		WebDAVEnabled: mount.WebDAVEnabled, WebDAVAuthEnabled: mount.WebDAVAuthEnabled,
		MountType: mount.MountType, Provider: mount.Provider, EndpointURL: mount.EndpointURL,
		Region: mount.Region, PathStyle: mount.PathStyle, AccountID: mount.AccountID,
		BucketName: mount.BucketName, RootPrefix: mount.RootPrefix, MountPath: mount.MountPath,
		Jurisdiction: mount.Jurisdiction, WebDAVUsername: mount.WebDAVUsername,
	}
}

func cloneConfig(cfg config.Config) config.Config {
	cfg.Tunnels = slices.Clone(cfg.Tunnels)
	cfg.DDNS.IPSources = slices.Clone(cfg.DDNS.IPSources)
	cfg.DDNS.Records = slices.Clone(cfg.DDNS.Records)
	cfg.S3WebDAV.Mounts = slices.Clone(cfg.S3WebDAV.Mounts)
	return cfg
}

func sortedUnique(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	sort.Strings(values)
	return slices.Compact(values)
}
