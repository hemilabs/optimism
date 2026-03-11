package fusaka

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"

	"github.com/ethereum-optimism/optimism/op-devstack/presets"
)

// ConfigureDevstackEnvVars sets the appropriate env vars to use a mise-installed geth binary for
// the L1 EL. This is useful in Osaka acceptance tests since op-geth does not include full Osaka
// support. This is meant to run before constructing a devstack target in the test. It will log to
// stdout. ResetDevstackEnvVars should be used to reset the environment variables when the test
// exits.
//
// Note that this is a no-op if either [sysgo.DevstackL1ELKindVar] or [sysgo.GethExecPathEnvVar]
// are set.
//
// The returned callback resets any modified environment variables.
func ConfigureDevstackEnvVars() func() {
	if _, ok := os.LookupEnv(sysgo.DevstackL1ELKindEnvVar); ok {
		return func() {}
	}
	if _, ok := os.LookupEnv(sysgo.GethExecPathEnvVar); ok {
		return func() {}
	}

	cmd := exec.Command("mise", "which", "geth")
	buf := bytes.NewBuffer([]byte{})
	cmd.Stdout = buf
	if err := cmd.Run(); err != nil {
		panic(fmt.Sprintf("failed to find mise-installed geth: %v", err))
	}
	execPath := strings.TrimSpace(buf.String())
	return presets.WithL1Geth(execPath)
}
