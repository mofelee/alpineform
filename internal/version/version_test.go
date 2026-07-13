package version

import "testing"

func TestShort(t *testing.T) {
	if got, want := (Info{Version: "dev", Dirty: true}).Short(), "dev-dirty"; got != want {
		t.Fatalf("Short() = %q, want %q", got, want)
	}
}

func TestShortenCommit(t *testing.T) {
	if got, want := shortenCommit("0123456789abcdef"), "0123456789ab"; got != want {
		t.Fatalf("shortenCommit() = %q, want %q", got, want)
	}
}
