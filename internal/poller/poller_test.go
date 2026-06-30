// SPDX-License-Identifier: Apache-2.0

package poller_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/rlimit"

	"github.com/alban/gpu-ebpf-bridge/internal/maps"
	"github.com/alban/gpu-ebpf-bridge/internal/nvml"
	"github.com/alban/gpu-ebpf-bridge/internal/poller"
)

func requireRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("test requires root for bpffs pin operations")
	}
	if err := rlimit.RemoveMemlock(); err != nil {
		t.Fatalf("RemoveMemlock: %v", err)
	}
}

func pinDirForTest(t *testing.T) string {
	t.Helper()
	dir := filepath.Join("/sys/fs/bpf", "test-poller-"+t.Name())
	t.Cleanup(func() {
		// Best-effort cleanup of any lingering pins.
		for _, name := range []string{
			maps.MapNameMeta, maps.MapNameDevice,
			maps.MapNamePerPid, maps.MapNamePerPidPerDevice,
		} {
			_ = os.Remove(filepath.Join(dir, name))
		}
		_ = os.Remove(dir)
	})
	return dir
}

// TestPollerWritesMockDataToMaps drives the full poll loop with the
// mock NVML backend and verifies that the bridge's pinned maps are
// populated with the synthetic data.
func TestPollerWritesMockDataToMaps(t *testing.T) {
	requireRoot(t)

	pinDir := pinDirForTest(t)
	bridge, err := maps.Open(pinDir)
	if err != nil {
		t.Fatalf("maps.Open: %v", err)
	}
	t.Cleanup(func() { _ = bridge.Unpin(); _ = bridge.Close() })

	const (
		numDevices    = 2
		pidsPerDevice = 3
	)
	mock := nvml.NewMock()
	mock.NumDevices = numDevices
	mock.PidsPerDevice = pidsPerDevice
	mock.FirstPid = 200000

	p, err := poller.New(poller.Config{
		PollInterval: 50 * time.Millisecond,
		Source:       mock,
		Bridge:       bridge,
	})
	if err != nil {
		t.Fatalf("poller.New: %v", err)
	}

	// Run the poller for 250 ms (≥3 ticks), then cancel.
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := p.Run(ctx); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// gpu_meta: schema_version + n_devices + helper_pid set, fresh.
	metaMap, err := ebpf.LoadPinnedMap(filepath.Join(pinDir, maps.MapNameMeta), nil)
	if err != nil {
		t.Fatalf("LoadPinnedMap meta: %v", err)
	}
	defer metaMap.Close()
	var meta maps.Meta
	key0 := uint32(0)
	if err := metaMap.Lookup(&key0, &meta); err != nil {
		t.Fatalf("meta lookup: %v", err)
	}
	if meta.SchemaVersion != maps.SchemaVersion {
		t.Errorf("meta.SchemaVersion: got %d want %d", meta.SchemaVersion, maps.SchemaVersion)
	}
	if meta.N_devices != numDevices {
		t.Errorf("meta.N_devices: got %d want %d", meta.N_devices, numDevices)
	}
	if meta.HelperPid != uint32(os.Getpid()) {
		t.Errorf("meta.HelperPid: got %d want %d", meta.HelperPid, os.Getpid())
	}
	if meta.LastUpdateNs == 0 {
		t.Error("meta.LastUpdateNs is zero")
	}

	// gpu_device: each device index should have a non-zero snapshot.
	devMap, err := ebpf.LoadPinnedMap(filepath.Join(pinDir, maps.MapNameDevice), nil)
	if err != nil {
		t.Fatalf("LoadPinnedMap device: %v", err)
	}
	defer devMap.Close()
	for idx := uint32(0); idx < numDevices; idx++ {
		var d maps.DeviceMetrics
		if err := devMap.Lookup(&idx, &d); err != nil {
			t.Errorf("device[%d] lookup: %v", idx, err)
			continue
		}
		if d.MemTotal == 0 {
			t.Errorf("device[%d].MemTotal is zero", idx)
		}
		if d.TimestampNs == 0 {
			t.Errorf("device[%d].TimestampNs is zero", idx)
		}
	}

	// gpu_per_pid: expect (numDevices * pidsPerDevice) PIDs starting at
	// mock.FirstPid. Verify each is present.
	perPidMap, err := ebpf.LoadPinnedMap(filepath.Join(pinDir, maps.MapNamePerPid), nil)
	if err != nil {
		t.Fatalf("LoadPinnedMap per_pid: %v", err)
	}
	defer perPidMap.Close()
	expectedPids := numDevices * pidsPerDevice
	for i := uint32(0); i < uint32(expectedPids); i++ {
		pid := mock.FirstPid + i
		var p maps.PidMetricsAggregated
		if err := perPidMap.Lookup(&pid, &p); err != nil {
			t.Errorf("per_pid[%d] lookup: %v", pid, err)
			continue
		}
		if p.TimestampNs == 0 {
			t.Errorf("per_pid[%d].TimestampNs is zero", pid)
		}
		if p.DeviceCount == 0 {
			t.Errorf("per_pid[%d].DeviceCount is zero", pid)
		}
	}

	// gpu_per_pid_per_device: same PIDs but keyed by (pid << 32 | dev).
	// Each PID lives on exactly one mock device, so only one (pid, dev)
	// pair should exist per PID — try the first pid on device 0.
	pddMap, err := ebpf.LoadPinnedMap(filepath.Join(pinDir, maps.MapNamePerPidPerDevice), nil)
	if err != nil {
		t.Fatalf("LoadPinnedMap per_pid_per_device: %v", err)
	}
	defer pddMap.Close()
	firstKey := maps.PerPidPerDeviceKey(mock.FirstPid, 0)
	var pd maps.PidMetrics
	if err := pddMap.Lookup(&firstKey, &pd); err != nil {
		t.Errorf("per_pid_per_device first lookup: %v", err)
	}
	if pd.UsedGpuMemory == 0 {
		t.Errorf("per_pid_per_device first entry has zero UsedGpuMemory")
	}
}
