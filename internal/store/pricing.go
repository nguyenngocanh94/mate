package store

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// PricingModel is one row of `pricing.yaml`: what one model costs per
// million tokens, and how large its context window is. Money fields are
// zero until the captain fills them in - a zero price renders as unpriced
// (mvp.md M5's `v_task_ledger` gives NULL cost, not a free one), never as
// "this model is free".
type PricingModel struct {
	Model          string  `yaml:"model"`
	InputPerM      float64 `yaml:"input_per_m"`
	CacheReadPerM  float64 `yaml:"cache_read_per_m"`
	CacheWritePerM float64 `yaml:"cache_write_per_m"`
	OutputPerM     float64 `yaml:"output_per_m"`
	// ContextWindow is the model's context size in tokens, used to compute
	// `context_pct` in `v_now` and `v_task_ledger`. Unlike the price
	// columns this is a technical fact rather than something the captain
	// pays for, so a reasonable known value is seeded; zero means "not
	// known" and a context percentage is not computed for that model.
	ContextWindow int64 `yaml:"context_window"`
}

// PricingConfig is `.mate/pricing.yaml`.
type PricingConfig struct {
	Models []PricingModel `yaml:"models"`
}

// ErrNoPricingFile is returned by LoadPricing when the workspace has no
// pricing.yaml at all - a workspace `store.Init` did not seed, or one a
// caller removed. It is distinct from a file that parses to zero rows.
var ErrNoPricingFile = fmt.Errorf("store: no pricing.yaml in this workspace")

// LoadPricing reads `.mate/pricing.yaml`. A missing file is
// ErrNoPricingFile, not a zero PricingConfig: the caller (internal/timeline)
// needs to tell "the captain has not priced anything yet" (an empty file)
// apart from "there is no file at all" only for its own error message, so
// both are handled by treating either as no rows - see
// timeline.Ingester.ingestPricing.
func (w *Workspace) LoadPricing() (PricingConfig, error) {
	data, err := os.ReadFile(w.PricingFile())
	if err != nil {
		if os.IsNotExist(err) {
			return PricingConfig{}, ErrNoPricingFile
		}
		return PricingConfig{}, err
	}
	var cfg PricingConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return PricingConfig{}, fmt.Errorf("store: %s: %w", w.PricingFile(), err)
	}
	for i := range cfg.Models {
		cfg.Models[i].Model = strings.TrimSpace(cfg.Models[i].Model)
	}
	return cfg, nil
}

// SavePricing writes `.mate/pricing.yaml` atomically. It is used by tests
// and by a future `mate pricing set`; `store.Init` writes the seed
// directly so the seed's comments survive (yaml.Marshal would drop them).
func (w *Workspace) SavePricing(cfg PricingConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return w.writeFile(w.PricingFile(), data, 0o644)
}

// pricingFileSeed is the pricing.yaml a new workspace starts with: the model
// ids mate has actually seen in a transcript, priced at zero. The captain
// owns these numbers - mate does not know what anyone pays - so every
// price is 0 (unpriced: the ledger shows `?` for cost, not free) until the
// captain edits this file. Context windows are a technical fact rather than
// a price and are seeded with the vendor's published size, so `ctx%` works
// out of the box; correct them here if a model's real window differs.
const pricingFileSeed = `# .mate/pricing.yaml
#
# Token prices for mate usage/2/v_task_ledger, in $ per million tokens.
# mate does not know what you pay for any model: every price below is 0
# (unpriced) until you fill it in. A model with no row here, or a price of
# 0, shows cost as "?" in "mate usage" - never as free.
#
# context_window is the model's context size in tokens, used for the CTX%
# column; it is a technical fact, not a price, so a reasonable default is
# filled in. Add a row for any other model you run crews or a Mate on.
models:
  - model: claude-fable-5-1
    input_per_m: 0
    cache_read_per_m: 0
    cache_write_per_m: 0
    output_per_m: 0
    context_window: 200000
  - model: gpt-5.6-terra
    input_per_m: 0
    cache_read_per_m: 0
    cache_write_per_m: 0
    output_per_m: 0
    context_window: 400000
`
