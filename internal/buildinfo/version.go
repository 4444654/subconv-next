package buildinfo

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var releaseVersion string

func Version() string { return strings.TrimSpace(releaseVersion) }
