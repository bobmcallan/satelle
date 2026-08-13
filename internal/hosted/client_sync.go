package hosted

import "context"

// Apply is the checkout-sync write adapter: the existing workstate ingest POST.
// Named Apply so callers speak the checkout-sync contract without a new route.
func (c *Client) Apply(ctx context.Context, project string, batch WorkstateIngest) (WorkstateIngestResult, error) {
	return c.PushWorkstate(ctx, project, batch)
}

// Snapshot composes the existing workstate list GETs into one pull.
func (c *Client) Snapshot(ctx context.Context, project, kind string) (items []WorkstateItem, ledger []WorkstateLedgerRow, err error) {
	items, err = c.ListWorkstateItems(ctx, project, kind)
	if err != nil {
		return nil, nil, err
	}
	ledger, err = c.ListWorkstateLedger(ctx, project, "")
	if err != nil {
		return nil, nil, err
	}
	return items, ledger, nil
}
