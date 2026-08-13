package cli

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/bobmcallan/satelle/internal/app"
	"github.com/bobmcallan/satelle/internal/config"
	"github.com/bobmcallan/satelle/internal/hosted"
	"github.com/bobmcallan/satelle/internal/ledger"
	"github.com/bobmcallan/satelle/internal/verb"
	"github.com/bobmcallan/satelle/internal/workitem"
)

// workstateApplyLog, if set, receives Apply transport errors (tests).
var workstateApplyLog func(string)

func workstateApplyEnabled(cfg config.Config) (bool, error) {
	optIn, err := workstateOptIn(cfg)
	if err != nil {
		return false, err
	}
	if len(optIn) == 0 {
		return false, nil
	}
	server := config.ResolveHostedServer(cfg)
	if server == "" {
		return false, nil
	}
	if _, err := (hosted.FileStore{}).Load(server); err != nil {
		return false, nil
	}
	return true, nil
}

func wireWorkstateApplier(a *app.App) {
	ok, err := workstateApplyEnabled(a.Config)
	if err != nil || !ok {
		verb.ClearWorkstateApplier()
		return
	}
	verb.SetWorkstateApplier(func(ctx context.Context, items []workitem.Item, entries []ledger.Entry) {
		ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		if err := applyWorkstateNow(ctx, a, items, entries); err != nil {
			msg := "satelle: workstate apply: " + err.Error()
			if workstateApplyLog != nil {
				workstateApplyLog(msg)
			} else {
				fmt.Fprintln(os.Stderr, msg)
			}
		}
	})
}

func applyWorkstateNow(ctx context.Context, a *app.App, items []workitem.Item, entries []ledger.Entry) error {
	server := config.ResolveHostedServer(a.Config)
	if server == "" {
		return nil
	}
	project, err := resolveBoundProject(a.Config, a.RepoRoot)
	if err != nil {
		return err
	}
	batch := hosted.WorkstateIngest{}
	for _, it := range items {
		rec, merr := marshalWorkstateItem(it)
		if merr != nil {
			return merr
		}
		batch.Items = append(batch.Items, rec)
	}
	for _, e := range entries {
		rec, merr := marshalWorkstateLedger(e)
		if merr != nil {
			return merr
		}
		batch.Ledger = append(batch.Ledger, rec)
	}
	client := hosted.NewClient(server, hosted.FileStore{}, nil)
	_, err = client.Apply(ctx, project, batch)
	return err
}
