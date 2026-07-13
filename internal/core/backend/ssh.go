package backend

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mofelee/alpineform/internal/core/ir"
)

const maxSSHErrorOutput = 32 * 1024

type SSHOptions struct {
	Binary         string
	ConfigPath     string
	ConnectTimeout time.Duration
	Executor       SSHExecutor
}

type SSHExecutor interface {
	Execute(ctx context.Context, binary string, args []string, stdin []byte) (stdout, stderr []byte, err error)
}

type SSHRunner struct {
	host    ir.HostSpec
	options SSHOptions
}

func NewSSHRunner(host ir.HostSpec, options SSHOptions) (*SSHRunner, error) {
	if host.Name == "" || host.SSH.Host == "" {
		return nil, fmt.Errorf("SSH runner requires a host name and SSH alias")
	}
	if host.SSH.User != "root" {
		return nil, fmt.Errorf("host %q uses unsupported SSH user %q; AlpineForm v0.1 requires root", host.Name, host.SSH.User)
	}
	if strings.HasPrefix(host.SSH.Host, "-") || strings.ContainsAny(host.SSH.Host, " \t\r\n") {
		return nil, fmt.Errorf("host %q has invalid SSH alias %q", host.Name, host.SSH.Host)
	}
	if host.SSH.Port < 0 || host.SSH.Port > 65535 {
		return nil, fmt.Errorf("host %q has invalid SSH port %d", host.Name, host.SSH.Port)
	}
	if strings.ContainsAny(options.ConfigPath, "\x00\r\n") || strings.ContainsAny(host.SSH.IdentityFile, "\x00\r\n") {
		return nil, fmt.Errorf("host %q has an invalid SSH path", host.Name)
	}
	if options.Binary == "" {
		options.Binary = "ssh"
	}
	if options.ConnectTimeout == 0 {
		options.ConnectTimeout = 10 * time.Second
	}
	if options.ConnectTimeout < time.Second {
		return nil, fmt.Errorf("SSH connect timeout must be at least one second")
	}
	if options.Executor == nil {
		options.Executor = processSSHExecutor{}
	}
	return &SSHRunner{host: host, options: options}, nil
}

func (runner *SSHRunner) Read(ctx context.Context, command string) (string, error) {
	output, err := runner.Run(ctx, Command{Name: "facts.read", Script: command, RedactOutput: true})
	return string(output), err
}

func (runner *SSHRunner) Run(ctx context.Context, command Command) ([]byte, error) {
	if strings.TrimSpace(command.Script) == "" {
		return nil, fmt.Errorf("SSH command %q has an empty remote script", command.Name)
	}
	args := []string{
		"-o", "BatchMode=yes",
		"-o", "ClearAllForwardings=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(int(runner.options.ConnectTimeout/time.Second)),
	}
	if runner.options.ConfigPath != "" {
		args = append(args, "-F", runner.options.ConfigPath)
	}
	if runner.host.SSH.Port != 0 {
		args = append(args, "-p", strconv.Itoa(runner.host.SSH.Port))
	}
	if runner.host.SSH.IdentityFile != "" {
		args = append(args, "-i", runner.host.SSH.IdentityFile)
	}
	args = append(args, "-l", "root", "--", runner.host.SSH.Host, "sh -c "+sshShellQuote(command.Script))
	stdout, stderr, err := runner.options.Executor.Execute(ctx, runner.options.Binary, args, command.Stdin)
	if err != nil {
		if command.RedactOutput {
			return nil, fmt.Errorf("SSH command %q failed on host %q: %w", command.Name, runner.host.Name, err)
		}
		message := strings.TrimSpace(string(stderr))
		if len(message) > maxSSHErrorOutput {
			message = message[:maxSSHErrorOutput] + "...<truncated>"
		}
		if message == "" {
			return nil, fmt.Errorf("SSH command %q failed on host %q: %w", command.Name, runner.host.Name, err)
		}
		return nil, fmt.Errorf("SSH command %q failed on host %q: %w: %s", command.Name, runner.host.Name, err, message)
	}
	return stdout, nil
}

func sshShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

type processSSHExecutor struct{}

func (processSSHExecutor) Execute(ctx context.Context, binary string, args []string, stdin []byte) ([]byte, []byte, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Stdin = bytes.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr limitedBuffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

type limitedBuffer struct {
	buffer bytes.Buffer
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	originalLength := len(data)
	remaining := maxSSHErrorOutput + len("...<truncated>") - buffer.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = buffer.buffer.Write(data)
	}
	return originalLength, nil
}

func (buffer *limitedBuffer) Bytes() []byte {
	return buffer.buffer.Bytes()
}
