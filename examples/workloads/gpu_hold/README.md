# gpu_hold — synthetic CUDA workload for bridge per-PID tests

A 130-line PyTorch script that allocates GPU memory and runs periodic
matrix multiplies. Use it to populate the bridge's `gpu_per_pid` /
`gpu_per_pid_per_device` maps when no real CUDA workload is available
(e.g. when validating the bridge or a consumer gadget on a fresh GPU
VM).

## Prerequisites

- NVIDIA driver + CUDA runtime on the host (the bridge needs these
  too, so if the bridge runs in `--mode=real` you already have them).
- PyTorch with CUDA support:

  ```sh
  pip install --break-system-packages torch
  ```

  Other CUDA Python packages (`cuda-python`, `cupy`, `numba`) work too
  but require slightly different code. PyTorch is the easiest because
  most GPU VM images already have it.

## Run

```sh
# Defaults: 2 GiB VRAM + periodic 4096x4096 matmul for 120 s.
python3 gpu_hold.py

# 4 GiB for 5 minutes
GPU_HOLD_MIB=4096 GPU_HOLD_SECONDS=300 python3 gpu_hold.py

# Memory-only (no compute) — useful to test that mem_used / per-PID
# memory accounting works without the noise of utilization.
GPU_HOLD_COMPUTE=0 python3 gpu_hold.py
```

All env vars are documented at the top of `gpu_hold.py`.

## Verify against the bridge

In another shell while `gpu_hold.py` is running:

```sh
# Bridge maps (built-in inspector, no bpftool required).
sudo ~/gpu-ebpf-bridge/bin/gpu-ebpf-bridge-nvml --dump

# NVML ground truth.
nvidia-smi --query-compute-apps=pid,used_memory --format=csv
nvidia-smi --query-gpu=utilization.gpu,memory.used --format=csv,nounits

# gpu_top gadget (requires Inspektor Gadget + the alban_map_pinning_and_iter branch).
sudo ig run gpu_top:alban_map_pinning_and_iter --verify-image=false \
     --timeout=2 -o json | jq .
```

Expected in `--dump`:

- `# gpu_per_pid` shows your `pid` with non-zero `UsedGpuMemoryTotal`
  and (if `GPU_HOLD_COMPUTE=1`) non-zero `SmUtilPctMax`.
- `# gpu_per_pid_per_device` shows the same `pid` with `Dev=0`.
- `# gpu_device[0]` shows non-zero `MemUsed` and (if compute is on)
  non-zero `SmUtilPct`.

If the bridge `--dump` is empty for `gpu_per_pid` but the workload is
clearly running (nvidia-smi shows it), the most common cause is that
NVML's `GetProcessUtilization` / `GetComputeRunningProcesses` is
restricted on the SKU (Azure vGPU profiles sometimes hide other
tenants' processes; this is documented in the NVML docs).
