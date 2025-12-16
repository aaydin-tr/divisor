package reloader

import (
	"fmt"

	"github.com/aaydin-tr/divisor/core"
	"github.com/aaydin-tr/divisor/internal/balancer"
	"github.com/aaydin-tr/divisor/internal/proxy"
	"github.com/aaydin-tr/divisor/pkg/config"
	"github.com/aaydin-tr/divisor/pkg/middleware"
	"go.uber.org/zap"
)

// ConfigReloader handles config file reloading and balancer updates
type ConfigReloader struct {
	configPath         string
	swappableBalancer  *balancer.SwappableBalancer
	middlewareExecutor *middleware.Executor
	proxyFunc          proxy.ProxyFunc
}

// NewConfigReloader creates a new config reloader
func NewConfigReloader(
	configPath string,
	sb *balancer.SwappableBalancer,
	mwExecutor *middleware.Executor,
	pf proxy.ProxyFunc,
) *ConfigReloader {
	return &ConfigReloader{
		configPath:         configPath,
		swappableBalancer:  sb,
		middlewareExecutor: mwExecutor,
		proxyFunc:          pf,
	}
}

// Reload handles the complete config reload workflow
// Validates new config before applying, keeps old config on error
func (cr *ConfigReloader) Reload() error {
	zap.S().Info("Starting config reload...")

	// Step 1: Parse new config
	newConfig, err := config.ParseConfigFile(cr.configPath)
	if err != nil {
		zap.S().Errorf("Config reload failed: parse error - %s", err)
		return fmt.Errorf("config parse failed: %w", err)
	}

	// Step 2: Validate new config
	if err := newConfig.PrepareConfig(); err != nil {
		zap.S().Errorf("Config reload failed: validation error - %s", err)
		return fmt.Errorf("config validation failed: %w", err)
	}
	zap.S().Info("New config validated successfully")

	// Step 3: Create new middleware executor (may have changed)
	newMwExecutor, err := middleware.NewExecutor(newConfig.Middlewares)
	if err != nil {
		zap.S().Errorf("Config reload failed: middleware creation error - %s", err)
		return fmt.Errorf("middleware creation failed: %w", err)
	}

	// Step 4: Create new balancer instance
	newBalancer := core.NewBalancer(newConfig, newMwExecutor, cr.proxyFunc)
	if newBalancer == nil {
		zap.S().Error("Config reload failed: no available servers")
		return fmt.Errorf("balancer creation failed: no available servers")
	}
	zap.S().Infof("New balancer created with algorithm: %s", newConfig.Type)

	// Step 5: Atomic swap
	if err := cr.swappableBalancer.Swap(newBalancer); err != nil {
		// Cleanup new balancer if swap fails
		newBalancer.Shutdown()
		zap.S().Errorf("Config reload failed: balancer swap error - %s", err)
		return fmt.Errorf("balancer swap failed: %w", err)
	}

	// Step 6: Update middleware executor reference (for future reloads)
	cr.middlewareExecutor = newMwExecutor

	zap.S().Info("Config reload completed successfully")
	return nil
}
