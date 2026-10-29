package config

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// ErrReloadInvalidConfig wraps why a Reload's new config failed to parse or
// validate; divisor keeps serving the running config.
var ErrReloadInvalidConfig = errors.New("reload rejected, the new config is invalid")

// RestartRequiredError rejects a Reload whose new config changes keys outside
// the Backend set and Probe settings (docs/adr/0006-reload-scope.md).
type RestartRequiredError struct {
	ChangedKeys []string
}

func (e *RestartRequiredError) Error() string {
	return "reload rejected, these keys only take effect on restart: " + strings.Join(e.ChangedKeys, ", ")
}

// The only keys a Reload may change; backends entries carry the per-Backend
// Probe settings (health_check_path, health_check_status).
var reloadableKeys = map[string]bool{
	"backends":               true,
	"health_checker_time":    true,
	"health_checker_timeout": true,
}

// KeptBackend is a Backend the Reload keeps, with its live state: an entry
// identical in both configs, possibly at another position.
type KeptBackend struct {
	RunningPosition int
	NextPosition    int
}

// ReloadPlan is an accepted Reload: the prepared next config and how its
// Backend set differs from the running one.
type ReloadPlan struct {
	NextConfig                     *Config
	KeptBackends                   []KeptBackend
	AddedBackendNextPositions      []int
	RemovedBackendRunningPositions []int
	ProbeSettingsChanged           bool
}

// PlanReload decides whether nextConfigYAML can replace the running (prepared) config without a restart. A changed
// backends entry is a new Backend: its old entry is removed and the new one added.
func PlanReload(running *Config, nextConfigYAML []byte) (*ReloadPlan, error) {
	next, err := ParseConfig(nextConfigYAML)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrReloadInvalidConfig, err)
	}
	if err := next.PrepareConfig(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrReloadInvalidConfig, err)
	}

	if changedKeys := changedRestartOnlyKeys(running, next); len(changedKeys) > 0 {
		return nil, &RestartRequiredError{ChangedKeys: changedKeys}
	}

	plan := &ReloadPlan{
		NextConfig: next,
		ProbeSettingsChanged: running.HealthCheckerTime != next.HealthCheckerTime ||
			running.HealthCheckerTimeout != next.HealthCheckerTimeout,
	}
	runningPositionKept := make([]bool, len(running.Backends))
	for nextPosition := range next.Backends {
		runningPosition := unkeptPositionOf(running.Backends, runningPositionKept, &next.Backends[nextPosition])
		if runningPosition < 0 {
			plan.AddedBackendNextPositions = append(plan.AddedBackendNextPositions, nextPosition)
			continue
		}
		runningPositionKept[runningPosition] = true
		plan.KeptBackends = append(plan.KeptBackends, KeptBackend{RunningPosition: runningPosition, NextPosition: nextPosition})
	}
	for runningPosition, kept := range runningPositionKept {
		if !kept {
			plan.RemovedBackendRunningPositions = append(plan.RemovedBackendRunningPositions, runningPosition)
		}
	}
	return plan, nil
}

func unkeptPositionOf(runningBackends []Backend, runningPositionKept []bool, wanted *Backend) int {
	for position := range runningBackends {
		if !runningPositionKept[position] && runningBackends[position] == *wanted {
			return position
		}
	}
	return -1
}

// changedRestartOnlyKeys compares two prepared configs key by key, so a
// default written out explicitly is no change; keys come back sorted.
func changedRestartOnlyKeys(running, next *Config) []string {
	changedKeys := appendChangedKeys(nil, "", reflect.ValueOf(*running), reflect.ValueOf(*next))
	slices.Sort(changedKeys)
	return changedKeys
}

func appendChangedKeys(changedKeys []string, keyPrefix string, running, next reflect.Value) []string {
	for i := range running.NumField() {
		yamlName, _, _ := strings.Cut(running.Type().Field(i).Tag.Get("yaml"), ",")
		if yamlName == "" || yamlName == "-" {
			continue
		}
		key := keyPrefix + yamlName
		if reloadableKeys[key] {
			continue
		}
		runningValue, nextValue := running.Field(i), next.Field(i)
		if runningValue.Kind() == reflect.Struct {
			changedKeys = appendChangedKeys(changedKeys, key+".", runningValue, nextValue)
			continue
		}
		if !sameConfigValue(runningValue, nextValue) {
			changedKeys = append(changedKeys, key)
		}
	}
	return changedKeys
}

// An absent list or map and an empty one configure the same thing.
func sameConfigValue(running, next reflect.Value) bool {
	switch running.Kind() {
	case reflect.Map, reflect.Slice:
		if running.Len() == 0 && next.Len() == 0 {
			return true
		}
	}
	return reflect.DeepEqual(running.Interface(), next.Interface())
}
