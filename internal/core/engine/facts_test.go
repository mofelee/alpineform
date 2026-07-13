package engine

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type factsReader struct {
	outputs  map[string]string
	errors   map[string]error
	commands []string
}

func (reader *factsReader) Read(_ context.Context, command string) (string, error) {
	reader.commands = append(reader.commands, command)
	if err := reader.errors[command]; err != nil {
		return "", err
	}
	return reader.outputs[command], nil
}

func TestDiscoverHostFacts(t *testing.T) {
	tests := []struct {
		name       string
		apkArch    string
		kernelArch string
		wantArch   string
		wantNative string
	}{
		{name: "x86_64", apkArch: "x86_64\n", kernelArch: "x86_64\n", wantArch: "amd64", wantNative: "x86_64"},
		{name: "aarch64", apkArch: "aarch64\n", kernelArch: "aarch64\n", wantArch: "arm64", wantNative: "aarch64"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &factsReader{outputs: map[string]string{
				osReleaseCommand:  "NAME=\"Alpine Linux\"\nID=alpine\nVERSION_ID=3.24.1\n",
				apkArchCommand:    test.apkArch,
				kernelArchCommand: test.kernelArch,
			}}
			now := time.Date(2026, 7, 13, 8, 0, 0, 0, time.FixedZone("offset", 3600))
			facts, err := DiscoverHostFacts(context.Background(), reader, FactDiscoveryOptions{Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if facts.OSID != "alpine" || facts.Version != "3.24.1" || facts.Branch != "v3.24" || facts.Architecture != test.wantArch || facts.NativeArchitecture != test.wantNative || facts.KernelArchitecture != strings.TrimSpace(test.kernelArch) || facts.Libc != "musl" || facts.DetectedAt != "2026-07-13T07:00:00Z" {
				t.Fatalf("facts = %#v", facts)
			}
			if want := []string{osReleaseCommand, apkArchCommand, kernelArchCommand}; !reflect.DeepEqual(reader.commands, want) {
				t.Fatalf("commands = %#v, want %#v", reader.commands, want)
			}
		})
	}
}

func TestDiscoverHostFactsRejectsBeforeAnyWriteCapabilityExists(t *testing.T) {
	tests := []struct {
		name      string
		osRelease string
		apkArch   string
		kernel    string
		want      string
	}{
		{name: "Debian", osRelease: "ID=debian\nVERSION_ID=13\n", apkArch: "x86_64", kernel: "x86_64", want: `unsupported target OS "debian"`},
		{name: "Ubuntu", osRelease: "ID=ubuntu\nVERSION_ID=24.04\n", apkArch: "x86_64", kernel: "x86_64", want: `unsupported target OS "ubuntu"`},
		{name: "missing ID", osRelease: "VERSION_ID=3.24.1\n", apkArch: "x86_64", kernel: "x86_64", want: "missing required ID"},
		{name: "edge", osRelease: "ID=alpine\nVERSION_ID=edge\n", apkArch: "x86_64", kernel: "x86_64", want: "invalid Alpine VERSION_ID"},
		{name: "old branch", osRelease: "ID=alpine\nVERSION_ID=3.23.4\n", apkArch: "x86_64", kernel: "x86_64", want: `unsupported Alpine branch "v3.23"`},
		{name: "unsupported APK architecture", osRelease: "ID=alpine\nVERSION_ID=3.24.1\n", apkArch: "armv7", kernel: "armv7", want: "unsupported architecture"},
		{name: "APK alias", osRelease: "ID=alpine\nVERSION_ID=3.24.1\n", apkArch: "amd64", kernel: "x86_64", want: "non-native architecture"},
		{name: "architecture mismatch", osRelease: "ID=alpine\nVERSION_ID=3.24.1\n", apkArch: "x86_64", kernel: "aarch64", want: "architecture mismatch"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &factsReader{outputs: map[string]string{osReleaseCommand: test.osRelease, apkArchCommand: test.apkArch, kernelArchCommand: test.kernel}}
			_, err := DiscoverHostFacts(context.Background(), reader, FactDiscoveryOptions{})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("DiscoverHostFacts() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDiscoverHostFactsWrapsReaderError(t *testing.T) {
	reader := &factsReader{outputs: map[string]string{}, errors: map[string]error{osReleaseCommand: errors.New("connection closed")}}
	_, err := DiscoverHostFacts(context.Background(), reader, FactDiscoveryOptions{})
	if err == nil || !strings.Contains(err.Error(), "read /etc/os-release: connection closed") {
		t.Fatalf("DiscoverHostFacts() error = %v", err)
	}
}

func TestParseOSReleaseSupportsStandardQuoting(t *testing.T) {
	values, err := parseOSRelease("ID='alpine'\nVERSION_ID=\"3.24.1\"\nPRETTY_NAME=\"Alpine Linux 3.24\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if values["ID"] != "alpine" || values["VERSION_ID"] != "3.24.1" || values["PRETTY_NAME"] != "Alpine Linux 3.24" {
		t.Fatalf("os-release = %#v", values)
	}
}
