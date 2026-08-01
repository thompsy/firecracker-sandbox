package firevm

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestBuildConfigCmd(t *testing.T) {
	// With a command, KernelArgs carries the base64-encoded firevm_cmd token
	// (base64 so the kernel doesn't split the command on spaces).
	cmd := "ls -lah"
	want := "firevm_cmd=" + base64.StdEncoding.EncodeToString([]byte(cmd))
	if got := BuildConfig(0, cmd).KernelArgs; !strings.Contains(got, want) {
		t.Errorf("BuildConfig(0, %q).KernelArgs = %q, want it to contain %q", cmd, got, want)
	}

	// Without a command, no firevm_cmd token is added.
	if got := BuildConfig(0, "").KernelArgs; strings.Contains(got, "firevm_cmd") {
		t.Errorf("BuildConfig(0, \"\").KernelArgs = %q, should not contain firevm_cmd", got)
	}
}
