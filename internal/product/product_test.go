package product

import "testing"

func TestProductContractNames(t *testing.T) {
	tests := map[string]struct {
		got  string
		want string
	}{
		"Name":               {Name, "AlpineForm"},
		"CLIName":            {CLIName, "apf"},
		"ConfigSuffix":       {ConfigSuffix, ".apf.hcl"},
		"VarFileSuffix":      {VarFileSuffix, ".apfvars"},
		"VarJSONSuffix":      {VarJSONSuffix, ".apfvars.json"},
		"DefaultVarFile":     {DefaultVarFile, "alpineform.apfvars"},
		"DefaultVarJSONFile": {DefaultVarJSONFile, "alpineform.apfvars.json"},
		"AutoVarSuffix":      {AutoVarSuffix, ".auto.apfvars"},
		"AutoVarJSONSuffix":  {AutoVarJSONSuffix, ".auto.apfvars.json"},
		"EnvironmentPrefix":  {EnvironmentPrefix, "APF_VAR_"},
		"DefaultStatePath":   {DefaultStatePath, "/var/lib/alpineform/state.json"},
		"DefaultLockPath":    {DefaultLockPath, "/run/lock/alpineform/lock"},
		"DefaultInstallDir":  {DefaultInstallDir, "/usr/local/share/alpineform"},
		"TargetOSID":         {TargetOSID, "alpine"},
		"SupportedBranch":    {SupportedBranch, "v3.24"},
		"TargetLibc":         {TargetLibc, "musl"},
	}
	for name, test := range tests {
		if test.got != test.want {
			t.Fatalf("%s = %q, want %q", name, test.got, test.want)
		}
	}
}

func TestUserAgent(t *testing.T) {
	if got, want := UserAgent("v0.1.0"), "AlpineForm/v0.1.0"; got != want {
		t.Fatalf("UserAgent() = %q, want %q", got, want)
	}
}
