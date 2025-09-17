package redisstate

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/russellb/canhazgpu/internal/types"
)

// Client wraps the existing Redis client with k8s-specific extensions
type Client struct {
	rdb *redis.Client
}

// NewClient creates a new Redis client for k8s integration
func NewClient(redisHost string, redisPort int, redisDB int) *Client {
	rdb := redis.NewClient(&redis.Options{
		Addr: fmt.Sprintf("%s:%d", redisHost, redisPort),
		DB:   redisDB,
	})

	return &Client{rdb: rdb}
}

// NewClientWithSocket creates a new Redis client using Unix socket
func NewClientWithSocket(socketPath string, redisDB int) *Client {
	rdb := redis.NewClient(&redis.Options{
		Network: "unix",
		Addr:    socketPath,
		DB:      redisDB,
	})

	return &Client{rdb: rdb}
}

func (c *Client) Close() error {
	return c.rdb.Close()
}

func (c *Client) Ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

// ReservationInfo holds information about a k8s-managed reservation
type ReservationInfo struct {
	ClaimUID    string    `json:"claim_uid"`
	PodName     string    `json:"pod_name,omitempty"`
	Namespace   string    `json:"namespace"`
	ReservedAt  time.Time `json:"reserved_at"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
}

// ReserveGPUsForClaim reserves GPUs for a specific Kubernetes ResourceClaim
func (c *Client) ReserveGPUsForClaim(ctx context.Context, gpuIDs []int, claimUID, podName, namespace string) error {
	now := time.Now()

	for _, gpuID := range gpuIDs {
		gpuState := &types.GPUState{
			User:          fmt.Sprintf("k8s:%s", claimUID),
			StartTime:     types.FlexibleTime{Time: now},
			LastHeartbeat: types.FlexibleTime{Time: now},
			Type:          "k8s",
		}

		// Store GPU state
		key := fmt.Sprintf("%sgpu:%d", types.RedisKeyPrefix, gpuID)
		data, err := json.Marshal(gpuState)
		if err != nil {
			return fmt.Errorf("failed to marshal GPU state: %w", err)
		}

		if err := c.rdb.Set(ctx, key, data, 0).Err(); err != nil {
			return fmt.Errorf("failed to set GPU %d state: %w", gpuID, err)
		}

		// Store claim-specific info
		claimKey := fmt.Sprintf("%sk8s:claim:%s:gpu:%d", types.RedisKeyPrefix, claimUID, gpuID)
		reservationInfo := &ReservationInfo{
			ClaimUID:   claimUID,
			PodName:    podName,
			Namespace:  namespace,
			ReservedAt: now,
		}

		infoData, err := json.Marshal(reservationInfo)
		if err != nil {
			return fmt.Errorf("failed to marshal reservation info: %w", err)
		}

		if err := c.rdb.Set(ctx, claimKey, infoData, 0).Err(); err != nil {
			return fmt.Errorf("failed to set claim info: %w", err)
		}
	}

	// Store claim -> GPU mapping
	claimGPUsKey := fmt.Sprintf("%sk8s:claim:%s:gpus", types.RedisKeyPrefix, claimUID)
	gpuStrs := make([]interface{}, len(gpuIDs))
	for i, gpuID := range gpuIDs {
		gpuStrs[i] = fmt.Sprintf("%d", gpuID)
	}

	if err := c.rdb.SAdd(ctx, claimGPUsKey, gpuStrs...).Err(); err != nil {
		return fmt.Errorf("failed to store claim GPU mapping: %w", err)
	}

	return nil
}

// ReleaseGPUsForClaim releases GPUs associated with a ResourceClaim
func (c *Client) ReleaseGPUsForClaim(ctx context.Context, claimUID string) error {
	// Get GPUs for this claim
	claimGPUsKey := fmt.Sprintf("%sk8s:claim:%s:gpus", types.RedisKeyPrefix, claimUID)
	gpuIDs, err := c.rdb.SMembers(ctx, claimGPUsKey).Result()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("failed to get claim GPUs: %w", err)
	}

	now := time.Now()

	// Release each GPU
	for _, gpuIDStr := range gpuIDs {
		gpuID := gpuIDStr // Already a string

		// Update GPU state to available
		gpuKey := fmt.Sprintf("%sgpu:%s", types.RedisKeyPrefix, gpuID)
		gpuState := &types.GPUState{
			LastReleased: types.FlexibleTime{Time: now},
		}

		data, err := json.Marshal(gpuState)
		if err != nil {
			return fmt.Errorf("failed to marshal GPU state: %w", err)
		}

		if err := c.rdb.Set(ctx, gpuKey, data, 0).Err(); err != nil {
			return fmt.Errorf("failed to release GPU %s: %w", gpuID, err)
		}

		// Remove claim-specific info
		claimKey := fmt.Sprintf("%sk8s:claim:%s:gpu:%s", types.RedisKeyPrefix, claimUID, gpuID)
		c.rdb.Del(ctx, claimKey)
	}

	// Remove claim GPU mapping
	c.rdb.Del(ctx, claimGPUsKey)

	return nil
}

// GetAvailableGPUs returns the list of available GPU IDs
func (c *Client) GetAvailableGPUs(ctx context.Context) ([]int, error) {
	// Get total GPU count
	gpuCount, err := c.rdb.Get(ctx, types.RedisKeyGPUCount).Int()
	if err == redis.Nil {
		return nil, fmt.Errorf("GPU pool not initialized")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get GPU count: %w", err)
	}

	var available []int

	for i := 0; i < gpuCount; i++ {
		// Check both K8s namespace and host canhazgpu reservations
		k8sKey := fmt.Sprintf("%sgpu:%d", types.RedisKeyPrefix, i)
		hostKey := fmt.Sprintf("canhazgpu:gpu:%d", i)

		// Check K8s reservation first
		k8sData, k8sErr := c.rdb.Get(ctx, k8sKey).Result()
		if k8sErr != nil && k8sErr != redis.Nil {
			continue // Skip on error
		}

		// Check host canhazgpu reservation
		hostData, hostErr := c.rdb.Get(ctx, hostKey).Result()
		if hostErr != nil && hostErr != redis.Nil {
			continue // Skip on error
		}

		// GPU is available only if both K8s and host show it as available
		k8sAvailable := (k8sErr == redis.Nil)
		hostAvailable := (hostErr == redis.Nil)

		// If K8s has data, check if it's truly available
		if !k8sAvailable {
			var k8sState types.GPUState
			if err := json.Unmarshal([]byte(k8sData), &k8sState); err == nil {
				k8sAvailable = (k8sState.User == "" && k8sState.Type == "")
			}
		}

		// If host has data, check if it's truly available
		if !hostAvailable {
			var hostState types.GPUState
			if err := json.Unmarshal([]byte(hostData), &hostState); err == nil {
				hostAvailable = (hostState.User == "" && hostState.Type == "")
			}
		}

		// GPU is available only if both K8s and host consider it available
		if k8sAvailable && hostAvailable {
			available = append(available, i)
		}
	}

	return available, nil
}

// GetGPUState returns the current state of a GPU
func (c *Client) GetGPUState(ctx context.Context, gpuID int) (*types.GPUState, error) {
	key := fmt.Sprintf("%sgpu:%d", types.RedisKeyPrefix, gpuID)
	data, err := c.rdb.Get(ctx, key).Result()
	if err == redis.Nil {
		return &types.GPUState{}, nil // Available
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get GPU state: %w", err)
	}

	var state types.GPUState
	if err := json.Unmarshal([]byte(data), &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal GPU state: %w", err)
	}

	return &state, nil
}

// UpdateHeartbeat updates the heartbeat for k8s-managed GPUs
func (c *Client) UpdateHeartbeat(ctx context.Context, claimUID string) error {
	// Get GPUs for this claim
	claimGPUsKey := fmt.Sprintf("%sk8s:claim:%s:gpus", types.RedisKeyPrefix, claimUID)
	gpuIDs, err := c.rdb.SMembers(ctx, claimGPUsKey).Result()
	if err != nil && err != redis.Nil {
		return fmt.Errorf("failed to get claim GPUs: %w", err)
	}

	now := time.Now()

	// Update heartbeat for each GPU
	for _, gpuIDStr := range gpuIDs {
		gpuKey := fmt.Sprintf("%sgpu:%s", types.RedisKeyPrefix, gpuIDStr)

		// Get current state
		data, err := c.rdb.Get(ctx, gpuKey).Result()
		if err != nil {
			continue // Skip if GPU state missing
		}

		var state types.GPUState
		if err := json.Unmarshal([]byte(data), &state); err != nil {
			continue // Skip malformed data
		}

		// Update heartbeat
		state.LastHeartbeat = types.FlexibleTime{Time: now}

		newData, err := json.Marshal(state)
		if err != nil {
			continue // Skip on marshal error
		}

		c.rdb.Set(ctx, gpuKey, newData, 0)
	}

	return nil
}