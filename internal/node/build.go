package node

import "strings"

var buildVersion = "dev"

func SetBuildInfo(version, _ string, _ string) {
	version = strings.TrimSpace(version)
	if version == "" {
		version = "dev"
	}
	buildVersion = version
}

func backendVersion() string {
	return "knode-" + buildVersion
}
