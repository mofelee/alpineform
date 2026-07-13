// Package product owns names that are part of AlpineForm's external contract.
package product

const (
	Name               = "AlpineForm"
	CLIName            = "apf"
	ConfigSuffix       = ".apf.hcl"
	VarFileSuffix      = ".apfvars"
	VarJSONSuffix      = ".apfvars.json"
	DefaultVarFile     = "alpineform.apfvars"
	DefaultVarJSONFile = "alpineform.apfvars.json"
	AutoVarSuffix      = ".auto.apfvars"
	AutoVarJSONSuffix  = ".auto.apfvars.json"
	EnvironmentPrefix  = "APF_VAR_"
	DefaultStatePath   = "/var/lib/alpineform/state.json"
	DefaultLockPath    = "/run/lock/alpineform/lock"
	DefaultInstallDir  = "/usr/local/share/alpineform"
	TargetOSID         = "alpine"
	SupportedBranch    = "v3.24"
	TargetLibc         = "musl"
)

func UserAgent(version string) string {
	if version == "" {
		version = "dev"
	}
	return Name + "/" + version
}
