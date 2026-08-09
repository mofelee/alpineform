package ir

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// ComponentBuildIdentityDocument retains the normalized, in-memory inputs used
// to derive a source-build identity. It is never serialized because command
// arguments may contain protected values.
type ComponentBuildIdentityDocument struct {
	Template           string
	Instance           string
	Inputs             []ComponentBuildInputIdentity
	Commands           []ComponentBuildCommandIdentity
	WorkingDirectory   string
	Environment        [][]string
	EnvironmentVersion string
	Output             string
	OutputSHA256       string
	MaxOutputBytes     int64
	Executable         bool
	Dependencies       []string
	Network            string
	Install            ComponentBuildInstallIdentity
	Platform           any
}

type ComponentBuildInputIdentity struct {
	Name            string
	Kind            string
	Identity        string
	Destination     string
	ExtractFormat   string
	StripComponents int
}

type ComponentBuildCommandIdentity struct {
	Argv          []string
	StdinIdentity string
}

type ComponentBuildInstallIdentity struct {
	Path  string
	Owner string
	Group string
	Mode  string
}

func (document ComponentBuildIdentityDocument) DigestForInstance(instance string) string {
	document.Instance = instance
	encoded, _ := json.Marshal(document)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (component ComponentInstanceSpec) PhysicalComponentName() string {
	if component.PhysicalName != "" {
		return component.PhysicalName
	}
	return component.Name
}

func (component ComponentInstanceSpec) WithPhysicalName(name string) ComponentInstanceSpec {
	if name == "" {
		name = component.Name
	}
	out := component
	out.PhysicalName = name
	if component.Build != nil {
		build := *component.Build
		if build.IdentityDocument != nil {
			document := cloneComponentBuildIdentityDocument(*build.IdentityDocument)
			build.IdentityDocument = &document
			build.Identity = document.DigestForInstance(name)
		}
		out.Build = &build
	}
	return out
}

func cloneComponentBuildIdentityDocument(document ComponentBuildIdentityDocument) ComponentBuildIdentityDocument {
	document.Inputs = append([]ComponentBuildInputIdentity(nil), document.Inputs...)
	document.Commands = append([]ComponentBuildCommandIdentity(nil), document.Commands...)
	for i := range document.Commands {
		document.Commands[i].Argv = append([]string(nil), document.Commands[i].Argv...)
	}
	document.Environment = append([][]string(nil), document.Environment...)
	for i := range document.Environment {
		document.Environment[i] = append([]string(nil), document.Environment[i]...)
	}
	document.Dependencies = append([]string(nil), document.Dependencies...)
	return document
}
