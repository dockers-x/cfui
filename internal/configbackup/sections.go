package configbackup

import (
	"cfui/internal/config"
	"time"
)

func Build(cfg config.Config, options ExportOptions, appVersion string, now time.Time) (Payload, error) {
	selected, err := validateExportSelection(options.Sections)
	if err != nil {
		return Payload{}, err
	}

	payload := Payload{
		SchemaVersion: PayloadVersion,
		CreatedAt:     now.UTC(),
		AppVersion:    appVersion,
	}
	for _, section := range normalSectionOrder {
		if !selected[section] {
			continue
		}
		payload.Sections = append(payload.Sections, section)
		switch section {
		case SectionTunnels:
			value := tunnelSection(cfg)
			payload.Tunnels = &value
		case SectionRemoteManagement:
			value := remoteSection(cfg)
			payload.RemoteManagement = &value
		case SectionDDNS:
			value := ddnsSection(cfg.DDNS)
			payload.DDNS = &value
		case SectionS3WebDAV:
			value := s3Section(cfg)
			payload.S3WebDAV = &value
		case SectionApplication:
			payload.Application = &ApplicationSection{
				MCPEnabled:            cfg.MCPEnabled,
				OAuthClientID:         cfg.OAuthClientID,
				OAuthRelayCallbackURL: cfg.OAuthRelayCallbackURL,
			}
		}
	}
	if options.IncludeSensitive {
		payload.Sections = append(payload.Sections, SectionSensitive)
		payload.Sensitive = sensitiveSection(cfg, selected)
	}
	payload, err = canonicalizePayload(payload)
	if err != nil {
		return Payload{}, err
	}
	if err := validatePayload(payload); err != nil {
		return Payload{}, err
	}
	return payload, nil
}

func validateExportSelection(sections []Section) (map[Section]bool, error) {
	if len(sections) == 0 {
		return nil, ErrInvalidSelection
	}
	selected := make(map[Section]bool, len(sections))
	for _, section := range sections {
		if section == SectionSensitive || sectionIndex(section) < 0 || selected[section] {
			return nil, ErrInvalidSelection
		}
		selected[section] = true
	}
	return selected, nil
}

func tunnelSection(cfg config.Config) TunnelSection {
	profiles := make([]TunnelProfile, 0, len(cfg.Tunnels))
	for _, tunnel := range cfg.Tunnels {
		profiles = append(profiles, tunnelProfileFromConfig(tunnel))
	}
	return TunnelSection{ActiveKey: cfg.ActiveTunnelKey, Profiles: profiles}
}

func tunnelProfileFromConfig(tunnel config.TunnelProfileConfig) TunnelProfile {
	return TunnelProfile{
		Key:             tunnel.Key,
		Name:            tunnel.Name,
		LocalEnabled:    tunnel.LocalEnabled,
		AutoStart:       tunnel.AutoStart,
		AutoRestart:     tunnel.AutoRestart,
		CustomTag:       tunnel.CustomTag,
		SoftwareName:    tunnel.SoftwareName,
		Protocol:        tunnel.Protocol,
		GracePeriod:     tunnel.GracePeriod,
		Region:          tunnel.Region,
		Retries:         tunnel.Retries,
		MetricsEnable:   tunnel.MetricsEnable,
		MetricsPort:     tunnel.MetricsPort,
		LogLevel:        tunnel.LogLevel,
		LogFile:         tunnel.LogFile,
		LogJSON:         tunnel.LogJSON,
		EdgeIPVersion:   tunnel.EdgeIPVersion,
		EdgeBindAddress: tunnel.EdgeBindAddress,
		PostQuantum:     tunnel.PostQuantum,
		NoTLSVerify:     tunnel.NoTLSVerify,
		ExtraArgs:       tunnel.ExtraArgs,
	}
}

func remoteSection(cfg config.Config) RemoteManagementSection {
	profiles := make([]RemoteProfile, 0, len(cfg.Tunnels))
	for _, tunnel := range cfg.Tunnels {
		profiles = append(profiles, RemoteProfile{
			Key:       tunnel.Key,
			Enabled:   tunnel.RemoteManagementEnabled,
			AccountID: tunnel.AccountID,
			TunnelID:  tunnel.TunnelID,
		})
	}
	return RemoteManagementSection{Profiles: profiles, APIEmail: cfg.TunnelManagement.APIEmail}
}

func ddnsSection(ddns config.DDNSConfig) DDNSSection {
	sources := make([]IPSource, 0, len(ddns.IPSources))
	for _, source := range ddns.IPSources {
		sources = append(sources, IPSource{URL: source.URL, IPType: source.IPType})
	}
	records := make([]DDNSRecord, 0, len(ddns.Records))
	for _, record := range ddns.Records {
		records = append(records, DDNSRecord{
			Name: record.Name, ZoneID: record.ZoneID, ZoneName: record.ZoneName,
			Type: record.Type, Value: record.Value, Comment: record.Comment,
			Proxied: record.Proxied, TTL: record.TTL,
		})
	}
	return DDNSSection{
		Enabled: ddns.Enabled, IPSources: sources, Records: records,
		IntervalMins: ddns.IntervalMins, OnlyOnChange: ddns.OnlyOnChange, MaxRetries: ddns.MaxRetries,
		UseProxy: ddns.UseProxy,
	}
}

func s3Section(cfg config.Config) S3WebDAVSection {
	mounts := make([]S3Mount, 0, len(cfg.S3WebDAV.Mounts))
	for _, mount := range cfg.S3WebDAV.Mounts {
		mounts = append(mounts, s3MountFromConfig(mount))
	}
	return S3WebDAVSection{
		Enabled:                 cfg.S3WebDAV.Enabled,
		ActiveKey:               cfg.S3WebDAV.ActiveKey,
		WebDAVAccessMode:        cfg.S3WebDAV.WebDAVAccessMode,
		DedicatedBindHost:       cfg.S3WebDAV.DedicatedBindHost,
		DedicatedPort:           cfg.S3WebDAV.DedicatedPort,
		DedicatedAutoStart:      cfg.S3WebDAV.DedicatedAutoStart,
		DedicatedDomainMode:     cfg.S3WebDAV.DedicatedDomainMode,
		DedicatedCustomDomain:   cfg.S3WebDAV.DedicatedCustomDomain,
		DedicatedTunnelHostname: cfg.S3WebDAV.DedicatedTunnelHostname,
		Mounts:                  mounts,
	}
}

func s3MountFromConfig(mount config.S3WebDAVMountConfig) S3Mount {
	return S3Mount{
		Key: mount.Key, Name: mount.Name, Enabled: mount.Enabled,
		WebDAVEnabled: mount.WebDAVEnabled, WebDAVAuthEnabled: mount.WebDAVAuthEnabled,
		MountType: mount.MountType, Provider: mount.Provider, EndpointURL: mount.EndpointURL,
		Region: mount.Region, PathStyle: mount.PathStyle, AccountID: mount.AccountID,
		BucketName: mount.BucketName, RootPrefix: mount.RootPrefix, MountPath: mount.MountPath,
		Jurisdiction: mount.Jurisdiction, WebDAVUsername: mount.WebDAVUsername,
	}
}

func sensitiveSection(cfg config.Config, selected map[Section]bool) *SensitiveSection {
	sensitive := &SensitiveSection{}
	if selected[SectionTunnels] {
		sensitive.TunnelTokens = make(map[string]string, len(cfg.Tunnels))
		for _, tunnel := range cfg.Tunnels {
			sensitive.TunnelTokens[tunnel.Key] = tunnel.Token
		}
	}
	if selected[SectionRemoteManagement] {
		sensitive.APIToken = cfg.TunnelManagement.APIToken
		sensitive.APIKey = cfg.TunnelManagement.APIKey
	}
	if selected[SectionS3WebDAV] {
		sensitive.S3 = make(map[string]S3Credentials, len(cfg.S3WebDAV.Mounts))
		for _, mount := range cfg.S3WebDAV.Mounts {
			credentials := S3Credentials{
				AccessKeyID: mount.AccessKeyID, SecretAccessKey: mount.SecretAccessKey,
				WebDAVPasswordHash: mount.WebDAVPasswordHash,
			}
			sensitive.S3[mount.Key] = credentials
		}
	}
	return sensitive
}
