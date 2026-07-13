package version

import (
	"runtime"
	"runtime/debug"
	"strings"
)

var (
	Version = ""
	Commit  = "unknown"
	Date    = "unknown"
)

type Info struct {
	Version   string
	Commit    string
	Date      string
	Dirty     bool
	GoVersion string
	Platform  string
}

func Current() Info {
	info := Info{
		Version:   resolveVersion(Version),
		Commit:    Commit,
		Date:      Date,
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
	}
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return info
	}
	if Version == "" && build.Main.Version != "" && build.Main.Version != "(devel)" {
		info.Version = build.Main.Version
	}
	for _, setting := range build.Settings {
		switch setting.Key {
		case "vcs.revision":
			if info.Commit == "unknown" {
				info.Commit = shortenCommit(setting.Value)
			}
		case "vcs.time":
			if info.Date == "unknown" {
				info.Date = setting.Value
			}
		case "vcs.modified":
			info.Dirty = setting.Value == "true"
		}
	}
	return info
}

func resolveVersion(value string) string {
	if value == "" {
		return "dev"
	}
	return value
}

func shortenCommit(commit string) string {
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}

func (i Info) Short() string {
	value := i.Version
	if i.Dirty && !strings.HasSuffix(value, "-dirty") && !strings.HasSuffix(value, "+dirty") {
		value += "-dirty"
	}
	return value
}
