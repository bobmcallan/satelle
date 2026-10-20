package cli

// The secrets a remote run reads from satelle.dev, held for the life of one
// `satelle story drive` process (sty_0b1c72a9). The values live in this process's
// memory and reach exactly one place: the environment of the step performer's
// agent process, through agentstep's request builder. They are not written to
// disk, the ledger or any log, and no error here carries one.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/bobmcallan/satelle/internal/remotenode"
)

// runSecretState is what this process hands its engines.
var runSecretState struct {
	mu     sync.Mutex
	values map[string]string
	unset  []string
}

// runSecretsSource is the source every engine of this process asks (see
// agentstep.Engine.SetRunSecretsSource): the values read for the run, and the
// variable of the remote's private key, which no agent may inherit.
func runSecretsSource() (map[string]string, []string) {
	runSecretState.mu.Lock()
	defer runSecretState.mu.Unlock()
	return runSecretState.values, runSecretState.unset
}

func setRunSecrets(values map[string]string, unset []string) {
	runSecretState.mu.Lock()
	defer runSecretState.mu.Unlock()
	runSecretState.values, runSecretState.unset = values, unset
}

// wireRunSecrets reads the secrets the environment's carrier names, for the run it
// names, and installs them for this process's engines. With no carrier it does
// nothing. A refused read fails the drive before any agent is started, printing
// the server's own code and reason. The returned stop keeps the run alive until it
// is called and clears the values.
func wireRunSecrets(ctx context.Context, errOut io.Writer, readerOpts ...remotenode.ReaderOption) (stop func(), err error) {
	rs := remotenode.RunSecretsFromEnv(os.Getenv)
	if env := rs.IdentityEnv(); env != "" {
		// The key is for this process alone, whether or not there is a run to read
		// for: no agent it starts may inherit it.
		setRunSecrets(nil, []string{env})
	}
	if !rs.Active() {
		return func() {}, nil
	}
	values, reader, err := remotenode.SecretsForRun(ctx, rs, os.Getenv, readerOpts...)
	if err != nil {
		var refused *remotenode.SecretRefusal
		if errors.As(err, &refused) {
			fmt.Fprintln(errOut, err)
		}
		return nil, fmt.Errorf("the run's secrets were not read, so no agent was started: %w", err)
	}
	setRunSecrets(values, unsetOf(rs))
	stopKeepAlive := reader.KeepAlive(ctx, remotenode.RefreshEvery)
	return func() {
		stopKeepAlive()
		setRunSecrets(nil, nil)
	}, nil
}

func unsetOf(rs remotenode.RunSecrets) []string {
	if env := rs.IdentityEnv(); env != "" {
		return []string{env}
	}
	return nil
}
