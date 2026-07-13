package backend

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mofelee/alpineform/internal/core/ir"
)

type sshInvocation struct {
	binary string
	args   []string
	stdin  []byte
}

type fakeSSHExecutor struct {
	stdout      []byte
	stderr      []byte
	err         error
	invocations []sshInvocation
}

func (executor *fakeSSHExecutor) Execute(ctx context.Context, binary string, args []string, stdin []byte) ([]byte, []byte, error) {
	executor.invocations = append(executor.invocations, sshInvocation{binary: binary, args: append([]string(nil), args...), stdin: append([]byte(nil), stdin...)})
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return append([]byte(nil), executor.stdout...), append([]byte(nil), executor.stderr...), executor.err
}

func sshHost() ir.HostSpec {
	return ir.HostSpec{Name: "node", SSH: ir.SSHSpec{Host: "alpine-prod", Port: 2222, User: "root", IdentityFile: "~/.ssh/alpine"}}
}

func TestSSHRunnerPassesConfigAndRootOverrides(t *testing.T) {
	executor := &fakeSSHExecutor{stdout: []byte("ok\n")}
	runner, err := NewSSHRunner(sshHost(), SSHOptions{Binary: "/usr/bin/ssh", ConfigPath: "/tmp/ssh_config", ConnectTimeout: 7 * time.Second, Executor: executor})
	if err != nil {
		t.Fatal(err)
	}
	stdin := []byte("payload\n")
	output, err := runner.Run(context.Background(), Command{Name: "test", Script: "printf '%s\\n' ok", Stdin: stdin})
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "ok\n" || len(executor.invocations) != 1 {
		t.Fatalf("output = %q, invocations = %#v", output, executor.invocations)
	}
	invocation := executor.invocations[0]
	wantArgs := []string{
		"-o", "BatchMode=yes",
		"-o", "ClearAllForwardings=yes",
		"-o", "ConnectTimeout=7",
		"-F", "/tmp/ssh_config",
		"-p", "2222",
		"-i", "~/.ssh/alpine",
		"-l", "root",
		"--", "alpine-prod",
		`sh -c 'printf '"'"'%s\n'"'"' ok'`,
	}
	if invocation.binary != "/usr/bin/ssh" || !reflect.DeepEqual(invocation.args, wantArgs) || !reflect.DeepEqual(invocation.stdin, stdin) {
		t.Fatalf("invocation = %#v, want args %#v", invocation, wantArgs)
	}
}

func TestSSHRunnerReadUsesRemoteScript(t *testing.T) {
	executor := &fakeSSHExecutor{stdout: []byte("x86_64\n")}
	host := sshHost()
	host.SSH.Port = 0
	host.SSH.IdentityFile = ""
	runner, err := NewSSHRunner(host, SSHOptions{Executor: executor})
	if err != nil {
		t.Fatal(err)
	}
	output, err := runner.Read(context.Background(), "apk --print-arch")
	if err != nil {
		t.Fatal(err)
	}
	if output != "x86_64\n" || strings.Contains(strings.Join(executor.invocations[0].args, " "), " -p ") {
		t.Fatalf("output = %q, args = %#v", output, executor.invocations[0].args)
	}
}

func TestSSHRunnerRedactsProtectedErrors(t *testing.T) {
	secret := "not-a-real-ssh-secret"
	executor := &fakeSSHExecutor{stderr: []byte(secret), err: errors.New("exit status 1")}
	runner, err := NewSSHRunner(sshHost(), SSHOptions{Executor: executor})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(context.Background(), Command{Name: "secret.write", Script: "false", RedactOutput: true})
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), `SSH command "secret.write" failed`) {
		t.Fatalf("redacted error = %v", err)
	}
	_, err = runner.Run(context.Background(), Command{Name: "inspect", Script: "false"})
	if err == nil || !strings.Contains(err.Error(), secret) {
		t.Fatalf("plain error = %v", err)
	}
}

func TestSSHRunnerPropagatesCancellation(t *testing.T) {
	executor := &fakeSSHExecutor{}
	runner, err := NewSSHRunner(sshHost(), SSHOptions{Executor: executor})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runner.Run(ctx, Command{Name: "canceled", Script: "true"})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
}

func TestNewSSHRunnerRejectsNonRootAndInvalidAlias(t *testing.T) {
	host := sshHost()
	host.SSH.User = "admin"
	if _, err := NewSSHRunner(host, SSHOptions{}); err == nil || !strings.Contains(err.Error(), "requires root") {
		t.Fatalf("non-root error = %v", err)
	}
	host = sshHost()
	host.SSH.Host = "-bad"
	if _, err := NewSSHRunner(host, SSHOptions{}); err == nil || !strings.Contains(err.Error(), "invalid SSH alias") {
		t.Fatalf("alias error = %v", err)
	}
}
