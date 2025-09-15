package cdi

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	CDIVendor = "canhazgpu.com"
	CDIClass  = "gpu"
)

// CDISpec represents a Container Device Interface specification
type CDISpec struct {
	Version     string      `json:"cdiVersion"`
	Kind        string      `json:"kind"`
	Devices     []CDIDevice `json:"devices"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

// CDIDevice represents a single device in the CDI spec
type CDIDevice struct {
	Name        string                 `json:"name"`
	Annotations map[string]string      `json:"annotations,omitempty"`
	ContainerEdits CDIContainerEdits  `json:"containerEdits,omitempty"`
}

// CDIContainerEdits specifies edits to be made to the container
type CDIContainerEdits struct {
	Env     []string                `json:"env,omitempty"`
	Mounts  []CDIMount             `json:"mounts,omitempty"`
	Hooks   []CDIHook              `json:"hooks,omitempty"`
	DeviceNodes []CDIDeviceNode    `json:"deviceNodes,omitempty"`
}

// CDIMount represents a mount point
type CDIMount struct {
	HostPath      string   `json:"hostPath"`
	ContainerPath string   `json:"containerPath"`
	Options       []string `json:"options,omitempty"`
}

// CDIHook represents a container hook
type CDIHook struct {
	HookName string   `json:"hookName"`
	Path     string   `json:"path"`
	Args     []string `json:"args,omitempty"`
	Env      []string `json:"env,omitempty"`
}

// CDIDeviceNode represents a device node to be created
type CDIDeviceNode struct {
	Path        string      `json:"path"`
	Type        string      `json:"type,omitempty"`
	Major       int64       `json:"major,omitempty"`
	Minor       int64       `json:"minor,omitempty"`
	FileMode    *os.FileMode `json:"fileMode,omitempty"`
	Permissions string      `json:"permissions,omitempty"`
	UID         *uint32     `json:"uid,omitempty"`
	GID         *uint32     `json:"gid,omitempty"`
}

// GenerateGPUSpec generates a CDI spec for the given number of GPUs
func GenerateGPUSpec(gpuCount int) *CDISpec {
	spec := &CDISpec{
		Version: "0.5.0",
		Kind:    fmt.Sprintf("%s/%s", CDIVendor, CDIClass),
		Devices: make([]CDIDevice, gpuCount),
		Annotations: map[string]string{
			"canhazgpu.com/generator": "k8shazgpu-nodeagent",
			"canhazgpu.com/version":   "1.0.0",
		},
	}

	for i := 0; i < gpuCount; i++ {
		spec.Devices[i] = CDIDevice{
			Name: fmt.Sprintf("gpu%d", i),
			Annotations: map[string]string{
				"canhazgpu.com/gpu-id": fmt.Sprintf("%d", i),
			},
			ContainerEdits: CDIContainerEdits{
				Env: []string{
					fmt.Sprintf("CUDA_VISIBLE_DEVICES=%d", i),
				},
			},
		}
	}

	return spec
}

// WriteSpecToFile writes the CDI spec to the specified file path
func (spec *CDISpec) WriteSpecToFile(filePath string) error {
	// Ensure directory exists
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Marshal spec to JSON
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal CDI spec: %w", err)
	}

	// Write to file
	if err := os.WriteFile(filePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write CDI spec to %s: %w", filePath, err)
	}

	return nil
}

// DefaultCDIPath returns the default path for CDI specs
func DefaultCDIPath() string {
	return "/var/run/cdi/canhazgpu.json"
}

// GetDeviceReference returns the CDI device reference for a given GPU ID
func GetDeviceReference(gpuID int) string {
	return fmt.Sprintf("%s/%s=gpu%d", CDIVendor, CDIClass, gpuID)
}

// GetDeviceReferences returns CDI device references for multiple GPU IDs
func GetDeviceReferences(gpuIDs []int) []string {
	refs := make([]string, len(gpuIDs))
	for i, gpuID := range gpuIDs {
		refs[i] = GetDeviceReference(gpuID)
	}
	return refs
}