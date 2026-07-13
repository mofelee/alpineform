package merge

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/mofelee/alpineform/internal/core/ir"
	"github.com/mofelee/alpineform/internal/core/parser"
)

var openRCNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func compileOpenRC(declaration parser.OpenRC, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) ([]ir.OpenRCServiceSpec, []ir.ManagedFileSpec, error) {
	services := make([]ir.OpenRCServiceSpec, 0, len(declaration.Services))
	files := make([]ir.ManagedFileSpec, 0, len(declaration.Services)*2)
	for _, serviceDeclaration := range declaration.Services {
		service, err := compileOpenRCService(serviceDeclaration, host, facts, ctx)
		if err != nil {
			return nil, nil, err
		}
		services = append(services, service)
		files = append(files, generatedOpenRCFile("/etc/init.d/"+service.Name, renderOpenRCInit(service), "0755", service.Source, service.Lifecycle))
		if service.Conf != "" {
			files = append(files, generatedOpenRCFile("/etc/conf.d/"+service.Name, service.Conf, "0644", service.Source, service.Lifecycle))
		}
	}
	return services, files, nil
}

func compileOpenRCService(declaration parser.ResourceDeclaration, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) (ir.OpenRCServiceSpec, error) {
	name := declaration.Label
	if !openRCNamePattern.MatchString(name) {
		return ir.OpenRCServiceSpec{}, resourceError(declaration.Source, "OpenRC service name %q is invalid", name)
	}
	command, exists, err := resourceString(declaration, "command", host, facts, ctx)
	if err != nil {
		return ir.OpenRCServiceSpec{}, err
	}
	if !exists || !validOpenRCAbsolutePath(command) {
		return ir.OpenRCServiceSpec{}, resourceAttributeError(declaration, "command", "must be a required clean absolute path")
	}
	args, err := resourceStringListDefault(declaration, "command_args", host, facts, ctx)
	if err != nil {
		return ir.OpenRCServiceSpec{}, err
	}
	for _, argument := range args {
		if strings.ContainsAny(argument, "\x00\r\n") {
			return ir.OpenRCServiceSpec{}, resourceAttributeError(declaration, "command_args", "must not contain NUL or line breaks")
		}
	}
	commandUser, err := resourceStringDefault(declaration, "command_user", "", host, facts, ctx)
	if err != nil {
		return ir.OpenRCServiceSpec{}, err
	}
	if commandUser != "" && !accountNamePattern.MatchString(commandUser) {
		return ir.OpenRCServiceSpec{}, resourceAttributeError(declaration, "command_user", "must be a valid Alpine account name or numeric ID")
	}
	directory, err := resourceStringDefault(declaration, "directory", "", host, facts, ctx)
	if err != nil {
		return ir.OpenRCServiceSpec{}, err
	}
	if directory != "" && !validOpenRCAbsolutePath(directory) {
		return ir.OpenRCServiceSpec{}, resourceAttributeError(declaration, "directory", "must be a clean absolute path")
	}
	background, err := resourceBoolDefault(declaration, "command_background", false, host, facts, ctx)
	if err != nil {
		return ir.OpenRCServiceSpec{}, err
	}
	pidfile, err := resourceStringDefault(declaration, "pidfile", "", host, facts, ctx)
	if err != nil {
		return ir.OpenRCServiceSpec{}, err
	}
	if pidfile != "" && !validOpenRCAbsolutePath(pidfile) {
		return ir.OpenRCServiceSpec{}, resourceAttributeError(declaration, "pidfile", "must be a clean absolute path")
	}
	if background && pidfile == "" {
		return ir.OpenRCServiceSpec{}, resourceAttributeError(declaration, "pidfile", "is required when command_background is true")
	}
	description, err := resourceStringDefault(declaration, "description", "", host, facts, ctx)
	if err != nil {
		return ir.OpenRCServiceSpec{}, err
	}
	if strings.ContainsAny(description, "\x00\r\n") {
		return ir.OpenRCServiceSpec{}, resourceAttributeError(declaration, "description", "must not contain NUL or line breaks")
	}
	dependencies := map[string][]string{}
	for _, attribute := range []string{"need", "use", "want", "after", "before"} {
		values, err := resourceStringListDefault(declaration, attribute, host, facts, ctx)
		if err != nil {
			return ir.OpenRCServiceSpec{}, err
		}
		seen := map[string]bool{}
		for _, value := range values {
			if !openRCNamePattern.MatchString(value) {
				return ir.OpenRCServiceSpec{}, resourceAttributeError(declaration, attribute, "contains invalid OpenRC dependency name %q", value)
			}
			if seen[value] {
				return ir.OpenRCServiceSpec{}, resourceAttributeError(declaration, attribute, "contains duplicate OpenRC dependency %q", value)
			}
			seen[value] = true
		}
		sort.Strings(values)
		dependencies[attribute] = values
	}
	conf, err := resourceStringDefault(declaration, "conf", "", host, facts, ctx)
	if err != nil {
		return ir.OpenRCServiceSpec{}, err
	}
	return ir.OpenRCServiceSpec{
		Name: name, Command: command, CommandArgs: args, CommandUser: commandUser, Directory: directory,
		CommandBackground: background, PIDFile: pidfile, Description: description,
		Need: dependencies["need"], Use: dependencies["use"], Want: dependencies["want"], After: dependencies["after"], Before: dependencies["before"],
		Conf: conf, Lifecycle: ir.LifecycleSpec{PreventDestroy: declaration.Lifecycle.PreventDestroy, Source: declaration.Lifecycle.Source}, Source: declaration.Source,
	}, nil
}

func renderOpenRCInit(service ir.OpenRCServiceSpec) string {
	var lines []string
	lines = append(lines, "#!/sbin/openrc-run")
	if service.Description != "" {
		lines = append(lines, "description="+shellQuote(service.Description))
	}
	lines = append(lines, "command="+shellQuote(service.Command))
	if len(service.CommandArgs) > 0 {
		quoted := make([]string, len(service.CommandArgs))
		for i, argument := range service.CommandArgs {
			quoted[i] = shellQuote(argument)
		}
		lines = append(lines, "command_args="+shellQuote(strings.Join(quoted, " ")))
	}
	if service.CommandUser != "" {
		lines = append(lines, "command_user="+shellQuote(service.CommandUser))
	}
	if service.Directory != "" {
		lines = append(lines, "directory="+shellQuote(service.Directory))
	}
	if service.CommandBackground {
		lines = append(lines, "command_background="+shellQuote("yes"))
	}
	if service.PIDFile != "" {
		lines = append(lines, "pidfile="+shellQuote(service.PIDFile))
	}
	if len(service.Need)+len(service.Use)+len(service.Want)+len(service.After)+len(service.Before) > 0 {
		lines = append(lines, "", "depend() {")
		for _, dependency := range []struct {
			name   string
			values []string
		}{{"need", service.Need}, {"use", service.Use}, {"want", service.Want}, {"after", service.After}, {"before", service.Before}} {
			if len(dependency.values) == 0 {
				continue
			}
			quoted := make([]string, len(dependency.values))
			for i, value := range dependency.values {
				quoted[i] = shellQuote(value)
			}
			lines = append(lines, "\t"+dependency.name+" "+strings.Join(quoted, " "))
		}
		lines = append(lines, "}")
	}
	return strings.Join(lines, "\n") + "\n"
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func validOpenRCAbsolutePath(value string) bool {
	return filepath.IsAbs(value) && filepath.Clean(value) == value && value != "/" && !strings.ContainsAny(value, "\x00\r\n")
}

func generatedOpenRCFile(path, content, mode string, source ir.SourceRef, lifecycle ir.LifecycleSpec) ir.ManagedFileSpec {
	sum := sha256.Sum256([]byte(content))
	return ir.ManagedFileSpec{
		Path: path, Content: content, ContentSHA256: hex.EncodeToString(sum[:]), ContentBytes: int64(len(content)),
		Owner: "root", Group: "root", Mode: mode, Ensure: "present", OnRemove: "forget",
		Lifecycle: lifecycle, Source: source,
	}
}

func resourceStringListDefault(declaration parser.ResourceDeclaration, name string, host parser.Host, facts *ir.HostFacts, ctx parser.EvalContext) ([]string, error) {
	values, err := resourceStringList(declaration, name, host, facts, ctx)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = value.String
	}
	return out, nil
}
