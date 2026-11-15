package cmd

import "runtime/debug"

// version is Version when the build set it; otherwise dev+g<sha>[.dirty],
// from the VCS stamp Go embeds in a build inside a git checkout, or "dev".
func version() string {
	if Version != "" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return "dev"
	}
	if len(rev) > 7 {
		rev = rev[:7]
	}
	v := "dev+g" + rev
	if dirty {
		v += ".dirty"
	}
	return v
}
