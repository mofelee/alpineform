package ir

type Program struct {
	Hosts      []HostSpec                       `json:"hosts"`
	Variables  map[string]VariableSpec          `json:"variables,omitempty"`
	Components map[string]ComponentTemplateSpec `json:"components,omitempty"`
	Scripts    map[string]ScriptSpec            `json:"scripts,omitempty"`
}

type VariableSpec struct {
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Default     any       `json:"default,omitempty"`
	Nullable    bool      `json:"nullable"`
	Sensitive   bool      `json:"sensitive"`
	Ephemeral   bool      `json:"ephemeral"`
	Deprecated  string    `json:"deprecated,omitempty"`
	Description string    `json:"description,omitempty"`
	Source      SourceRef `json:"source"`
}

type ComponentTemplateSpec struct {
	Name        string                        `json:"name"`
	Description string                        `json:"description,omitempty"`
	Inputs      map[string]ComponentInputSpec `json:"inputs,omitempty"`
	Source      SourceRef                     `json:"source"`
}

type ComponentInputSpec struct {
	Name        string    `json:"name"`
	Type        string    `json:"type"`
	Default     any       `json:"default,omitempty"`
	Nullable    bool      `json:"nullable"`
	Sensitive   bool      `json:"sensitive"`
	Ephemeral   bool      `json:"ephemeral"`
	Deprecated  string    `json:"deprecated,omitempty"`
	Description string    `json:"description,omitempty"`
	Source      SourceRef `json:"source"`
}

type ScriptSpec struct {
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Source      SourceRef `json:"source"`
}

type HostSpec struct {
	Name       string                  `json:"name"`
	SSH        SSHSpec                 `json:"ssh"`
	State      StateSpec               `json:"state"`
	Platform   *PlatformSpec           `json:"platform,omitempty"`
	Facts      *HostFacts              `json:"facts,omitempty"`
	Components []ComponentInstanceSpec `json:"components,omitempty"`
	Source     SourceRef               `json:"source"`
}

type SSHSpec struct {
	Host         string    `json:"host"`
	Port         int       `json:"port,omitempty"`
	User         string    `json:"user"`
	IdentityFile string    `json:"identity_file,omitempty"`
	Source       SourceRef `json:"source"`
}

type StateSpec struct {
	Path     string `json:"path"`
	LockPath string `json:"lock_path"`
}

type HostFacts struct {
	OSID               string `json:"os_id"`
	Version            string `json:"version"`
	Branch             string `json:"branch"`
	Architecture       string `json:"architecture"`
	NativeArchitecture string `json:"native_architecture"`
	KernelArchitecture string `json:"kernel_architecture"`
	Libc               string `json:"libc"`
	DetectedAt         string `json:"detected_at"`
}

type PlatformSpec struct {
	Architecture       string    `json:"architecture,omitempty"`
	Version            string    `json:"version,omitempty"`
	Branch             string    `json:"branch,omitempty"`
	Libc               string    `json:"libc"`
	NativeArchitecture string    `json:"native_architecture,omitempty"`
	Source             SourceRef `json:"source"`
}

type ComponentInstanceSpec struct {
	Name            string        `json:"name"`
	Template        string        `json:"template"`
	InputNames      []string      `json:"input_names,omitempty"`
	ProtectedInputs []string      `json:"protected_inputs,omitempty"`
	DependsOn       []string      `json:"depends_on,omitempty"`
	Lifecycle       LifecycleSpec `json:"lifecycle"`
	Source          SourceRef     `json:"source"`
}

type LifecycleSpec struct {
	PreventDestroy bool      `json:"prevent_destroy,omitempty"`
	Source         SourceRef `json:"source,omitempty"`
}
