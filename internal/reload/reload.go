package reload

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/aaydin-tr/divisor/pkg/config"
	"go.uber.org/zap"
)

// Trigger names what started a Reload, for the log.
type Trigger string

const TriggerSIGHUP Trigger = "SIGHUP"

var ErrReloaderClosed = errors.New("reload refused, divisor is shutting down")

// Target is the running part of divisor a Reload changes.
type Target interface {
	ApplyReload(plan *config.ReloadPlan)
}

// Reloader is the one entry point every Reload trigger calls: it re-reads the
// config file, applies an accepted Reload to the Target and logs the outcome.
type Reloader struct {
	reloadMu      sync.Mutex
	configPath    string
	runningConfig *config.Config
	target        Target
	closed        bool
}

// NewReloader takes the prepared config divisor booted on, read from
// configPath.
func NewReloader(configPath string, runningConfig *config.Config, target Target) *Reloader {
	return &Reloader{configPath: configPath, runningConfig: runningConfig, target: target}
}

// Reload is safe to call from any goroutine; concurrent calls run one after
// the other. A rejected Reload leaves divisor on the running config.
func (r *Reloader) Reload(trigger Trigger) error {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	if r.closed {
		return ErrReloaderClosed
	}

	plan, err := r.planReload()
	if err != nil {
		zap.S().Errorw("Reload rejected, divisor keeps serving the running config", "trigger", string(trigger), "error", err.Error())
		return err
	}

	r.target.ApplyReload(plan)
	zap.S().Infow("Reload applied",
		"trigger", string(trigger),
		"added_backends", backendAddresses(plan.NextConfig, plan.AddedBackendNextPositions),
		"removed_backends", backendAddresses(r.runningConfig, plan.RemovedBackendRunningPositions),
		"kept_backends", len(plan.KeptBackends),
		"probe_settings_changed", plan.ProbeSettingsChanged,
	)
	r.runningConfig = plan.NextConfig
	return nil
}

func (r *Reloader) planReload() (*config.ReloadPlan, error) {
	nextConfigYAML, err := os.ReadFile(r.configPath)
	if err != nil {
		return nil, fmt.Errorf("reading config file: %w", err)
	}
	return config.PlanReload(r.runningConfig, nextConfigYAML)
}

func backendAddresses(cfg *config.Config, positions []int) []string {
	addresses := make([]string, 0, len(positions))
	for _, position := range positions {
		addresses = append(addresses, cfg.Backends[position].Url)
	}
	return addresses
}

// Close waits for a Reload in flight and refuses every later one, so nothing
// touches the Pool once shutdown begins.
func (r *Reloader) Close() {
	r.reloadMu.Lock()
	defer r.reloadMu.Unlock()
	r.closed = true
}
