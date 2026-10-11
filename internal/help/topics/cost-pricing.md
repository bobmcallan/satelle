# Cost pricing: what `satelle story cost` charges for a story's tokens

A story's true cost is the tokens each seat spent, by model and token class.
Dollars are not recorded: `satelle story cost` (and the story's cost panel on the
web page) prices those tokens when you look, from the `[pricing]` section of
`~/.satelle/config.toml`. Change the section and every story, finished or not,
reads its new price on the next look. Nothing in the ledger is rewritten.

Satelle ships no rate. With no `[pricing]` section the cost reads
`unavailable (no pricing configured)`.

## Pick one mode

Rates, in USD per million tokens, per model id the transport reported. All four
keys are required:

```toml
[pricing.models."<model id>"]
fresh_input = 3.0
cache_read = 0.3
cache_write = 3.75
output = 15.0
```

A subscription instead spreads the plan fee over the plan's allowance, so a
story's cost is `fee_usd × story tokens ÷ tokens_per_period`:

```toml
[pricing.subscription]
fee_usd = 20.0
tokens_per_period = 1000000
```

A subscription figure is labelled `subscription share, not a metered price`.

A negative value, both modes at once, a missing key or an unknown key stops the
config loading and names the key.

## What is shown

- `cost` is the one primary dollar figure, with the low/medium/high band after
  it as a size indicator.
- One line per seat: a dispatched agent is `agent@model`; the driving session is
  `driver@model`, or `driver` when its model was not recorded.
- `reported` is the provider's own dollar figure, kept beside it for comparison.
  The driver-session and gate-value `$` columns are headed `REPORTED $`.

A token is never priced as zero. A model with no rate, a row that recorded no
model (named by its adapter), and a row that only reported a cache-inclusive
total each show as `unavailable`, with the rest of the story still priced:
`$5.1000 + unavailable (model <id> has no rate)`.
