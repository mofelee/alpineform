// Package product owns names that are part of AlpineForm's external contract.
package product

const (
	Name                               = "AlpineForm"
	CLIName                            = "apf"
	ConfigSuffix                       = ".apf.hcl"
	VarFileSuffix                      = ".apfvars"
	VarJSONSuffix                      = ".apfvars.json"
	DefaultVarFile                     = "alpineform.apfvars"
	DefaultVarJSONFile                 = "alpineform.apfvars.json"
	AutoVarSuffix                      = ".auto.apfvars"
	AutoVarJSONSuffix                  = ".auto.apfvars.json"
	EnvironmentPrefix                  = "APF_VAR_"
	DefaultStatePath                   = "/var/lib/alpineform/state.json"
	DefaultLockPath                    = "/run/lock/alpineform/lock"
	DefaultInstallDir                  = "/usr/local/share/alpineform"
	DefaultComponentBuildWorkspaceRoot = "/var/tmp/alpineform/builds"
	ComponentBuildProtectedInputRoot   = "/run/alpineform/build-inputs"
	ComponentBuildStateRoot            = "/var/lib/alpineform/builds"
	TargetOSID                         = "alpine"
	MinimumSupportedBranch             = "v3.21"
	MaximumSupportedBranch             = "v3.24"
	PrimarySupportedBranch             = "v3.24"
	SupportedBranchRange               = "v3.21 through v3.24"
	TargetLibc                         = "musl"
)

func SupportsBranch(branch string) bool {
	switch branch {
	case "v3.21", "v3.22", "v3.23", "v3.24":
		return true
	default:
		return false
	}
}

func UserAgent(version string) string {
	if version == "" {
		version = "dev"
	}
	return Name + "/" + version
}
