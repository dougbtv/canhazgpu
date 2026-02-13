package gpu

import (
	"context"
	"testing"
)

func TestVirtualProviderName(t *testing.T) {
	provider := NewVirtualProvider()
	if provider.Name() != "virtual" {
		t.Errorf("Expected provider name 'virtual', got '%s'", provider.Name())
	}
}

func TestVirtualProviderIsAvailable(t *testing.T) {
	provider := NewVirtualProvider()
	if !provider.IsAvailable() {
		t.Error("Virtual provider should always be available")
	}
}

func TestVirtualProviderDetectGPUUsage(t *testing.T) {
	provider := NewVirtualProvider()
	ctx := context.Background()

	usage, err := provider.DetectGPUUsage(ctx)
	if err != nil {
		t.Errorf("DetectGPUUsage should not return error, got: %v", err)
	}

	if usage == nil {
		t.Error("DetectGPUUsage should return non-nil map")
	}

	if len(usage) != 0 {
		t.Errorf("DetectGPUUsage should return empty map, got %d entries", len(usage))
	}
}

func TestVirtualProviderGetGPUCount(t *testing.T) {
	provider := NewVirtualProvider()
	ctx := context.Background()

	count, err := provider.GetGPUCount(ctx)
	if err != nil {
		t.Errorf("GetGPUCount should not return error, got: %v", err)
	}

	if count != 0 {
		t.Errorf("GetGPUCount should return 0, got %d", count)
	}
}

func TestVirtualProviderInProviderManager(t *testing.T) {
	pm := NewProviderManager()

	// Check that virtual provider is registered
	found := false
	for _, provider := range pm.providers {
		if provider.Name() == "virtual" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Virtual provider should be registered in NewProviderManager()")
	}
}

func TestVirtualProviderFromNames(t *testing.T) {
	pm := NewProviderManagerFromNames([]string{"virtual"})

	if len(pm.providers) != 1 {
		t.Errorf("Expected 1 provider, got %d", len(pm.providers))
	}

	if pm.providers[0].Name() != "virtual" {
		t.Errorf("Expected virtual provider, got '%s'", pm.providers[0].Name())
	}
}

func TestVirtualProviderAlwaysAvailable(t *testing.T) {
	pm := NewProviderManager()
	availableProviders := pm.GetAvailableProviders()

	// Virtual provider should always be in available list
	found := false
	for _, provider := range availableProviders {
		if provider.Name() == "virtual" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Virtual provider should always be available")
	}
}

func TestVirtualProviderDetectAllGPUUsage(t *testing.T) {
	// Create a provider manager with only virtual provider
	pm := NewProviderManagerFromNames([]string{"virtual"})
	ctx := context.Background()

	usage, err := pm.DetectAllGPUUsage(ctx)
	if err != nil {
		t.Errorf("DetectAllGPUUsage should not return error, got: %v", err)
	}

	if len(usage) != 0 {
		t.Errorf("DetectAllGPUUsage with virtual provider should return empty map, got %d entries", len(usage))
	}
}
