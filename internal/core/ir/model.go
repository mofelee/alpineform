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
	Name        string                  `json:"name"`
	SSH         SSHSpec                 `json:"ssh"`
	State       StateSpec               `json:"state"`
	Platform    *PlatformSpec           `json:"platform,omitempty"`
	Facts       *HostFacts              `json:"facts,omitempty"`
	APK         *APKSpec                `json:"apk,omitempty"`
	OpenRC      []OpenRCServiceSpec     `json:"openrc,omitempty"`
	Components  []ComponentInstanceSpec `json:"components,omitempty"`
	Files       []ManagedFileSpec       `json:"files,omitempty"`
	Directories []ManagedDirectorySpec  `json:"directories,omitempty"`
	Groups      []ManagedGroupSpec      `json:"groups,omitempty"`
	Users       []ManagedUserSpec       `json:"users,omitempty"`
	Packages    []PackageSpec           `json:"packages,omitempty"`
	Services    []ServiceSpec           `json:"services,omitempty"`
	Source      SourceRef               `json:"source"`
}

type ServiceSpec struct {
	Name      string        `json:"name"`
	Enabled   bool          `json:"enabled"`
	Runlevel  string        `json:"runlevel"`
	State     string        `json:"state"`
	Operation string        `json:"operation,omitempty"`
	Package   string        `json:"package,omitempty"`
	User      string        `json:"user,omitempty"`
	Group     string        `json:"group,omitempty"`
	Lifecycle LifecycleSpec `json:"lifecycle"`
	Source    SourceRef     `json:"source"`
}

type OpenRCServiceSpec struct {
	Name              string        `json:"name"`
	Command           string        `json:"command"`
	CommandArgs       []string      `json:"command_args,omitempty"`
	CommandUser       string        `json:"command_user,omitempty"`
	Directory         string        `json:"directory,omitempty"`
	CommandBackground bool          `json:"command_background,omitempty"`
	PIDFile           string        `json:"pidfile,omitempty"`
	Description       string        `json:"description,omitempty"`
	Need              []string      `json:"need,omitempty"`
	Use               []string      `json:"use,omitempty"`
	Want              []string      `json:"want,omitempty"`
	After             []string      `json:"after,omitempty"`
	Before            []string      `json:"before,omitempty"`
	Conf              string        `json:"-"`
	Lifecycle         LifecycleSpec `json:"lifecycle"`
	Source            SourceRef     `json:"source"`
}

type PackageSpec struct {
	Name          string        `json:"name"`
	RepositoryTag string        `json:"repository,omitempty"`
	WorldIntent   string        `json:"world_intent"`
	Ensure        string        `json:"ensure"`
	Lifecycle     LifecycleSpec `json:"lifecycle"`
	Source        SourceRef     `json:"source"`
}

type APKSpec struct {
	Ownership    string              `json:"ownership"`
	Repositories []APKRepositorySpec `json:"repositories,omitempty"`
	Keys         []APKKeySpec        `json:"keys,omitempty"`
	Source       SourceRef           `json:"source"`
}

type APKRepositorySpec struct {
	Name      string        `json:"name"`
	URL       string        `json:"url"`
	Branch    string        `json:"branch"`
	Component string        `json:"component"`
	Tag       string        `json:"tag,omitempty"`
	Line      string        `json:"line"`
	Ensure    string        `json:"ensure"`
	Lifecycle LifecycleSpec `json:"lifecycle"`
	Source    SourceRef     `json:"source"`
}

type APKKeySpec struct {
	Filename   string        `json:"filename"`
	SourcePath string        `json:"source_path,omitempty"`
	SHA256     string        `json:"sha256,omitempty"`
	Content    []byte        `json:"-"`
	Ensure     string        `json:"ensure"`
	Lifecycle  LifecycleSpec `json:"lifecycle"`
	Source     SourceRef     `json:"source"`
}

type ManagedUserSpec struct {
	Name           string                     `json:"name"`
	UID            string                     `json:"uid,omitempty"`
	PrimaryGroup   string                     `json:"group,omitempty"`
	Groups         []ManagedMembershipSpec    `json:"groups,omitempty"`
	AuthorizedKeys []ManagedAuthorizedKeySpec `json:"ssh_authorized_keys,omitempty"`
	Home           string                     `json:"home,omitempty"`
	Shell          string                     `json:"shell,omitempty"`
	System         bool                       `json:"system,omitempty"`
	Ensure         string                     `json:"ensure"`
	OnRemove       string                     `json:"on_remove"`
	Lifecycle      LifecycleSpec              `json:"lifecycle"`
	Source         SourceRef                  `json:"source"`
}

type ManagedMembershipSpec struct {
	Group  string    `json:"group"`
	Ensure string    `json:"ensure"`
	Source SourceRef `json:"source"`
}

type ManagedAuthorizedKeySpec struct {
	Line        string    `json:"-"`
	KeyType     string    `json:"key_type"`
	KeyBlob     string    `json:"key_blob"`
	Fingerprint string    `json:"fingerprint"`
	Ensure      string    `json:"ensure"`
	Source      SourceRef `json:"source"`
}

type ManagedGroupSpec struct {
	Name      string        `json:"name"`
	GID       string        `json:"gid,omitempty"`
	System    bool          `json:"system,omitempty"`
	Ensure    string        `json:"ensure"`
	OnRemove  string        `json:"on_remove"`
	Lifecycle LifecycleSpec `json:"lifecycle"`
	Source    SourceRef     `json:"source"`
}

type ManagedDirectorySpec struct {
	Path            string        `json:"path"`
	Owner           string        `json:"owner"`
	Group           string        `json:"group"`
	Mode            string        `json:"mode"`
	Ensure          string        `json:"ensure"`
	OnRemove        string        `json:"on_remove"`
	RecursiveDelete bool          `json:"recursive_delete,omitempty"`
	Lifecycle       LifecycleSpec `json:"lifecycle"`
	Source          SourceRef     `json:"source"`
}

type ManagedFileSpec struct {
	Path             string        `json:"path"`
	Content          string        `json:"-"`
	ContentSHA256    string        `json:"content_sha256,omitempty"`
	ContentBytes     int64         `json:"content_bytes,omitempty"`
	ContentVersion   string        `json:"content_version,omitempty"`
	ContentWriteOnly bool          `json:"content_write_only,omitempty"`
	Owner            string        `json:"owner"`
	Group            string        `json:"group"`
	Mode             string        `json:"mode"`
	Ensure           string        `json:"ensure"`
	OnRemove         string        `json:"on_remove"`
	Sensitive        bool          `json:"sensitive,omitempty"`
	Ephemeral        bool          `json:"ephemeral,omitempty"`
	Lifecycle        LifecycleSpec `json:"lifecycle"`
	Source           SourceRef     `json:"source"`
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
