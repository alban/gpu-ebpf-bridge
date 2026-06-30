# gpu_hold — synthetic CUDA workload for bridge per-PID tests

Allocates a chunk of VRAM and (optionally) drives the SMs with a busy
kernel, so the bridge populates `gpu_per_pid` and
`gpu_per_pid_per_device` with realistic data. Use it to validate the
bridge or any consumer gadget without a real CUDA workload at hand.

Two equivalent implementations are provided. Pick whichever is least
painful given what's already on the host:

| Implementation        | File              | Extra install                            | Disk |
|-----------------------|-------------------|------------------------------------------|------|
| C / CUDA (recommended)| `gpu_hold.cu`     | none (uses the toolchain you already have)| ~50 KiB |
| Python (PyTorch)      | `gpu_hold.py`     | `pip install torch` (with CUDA wheel)    | ~3 GiB |

Both honour the same env vars; see source comments for the full list.

## Option A: nvcc-compiled C/CUDA (no extra deps)

The CUDA toolkit is already required to install the NVIDIA driver, so
`nvcc` is normally available on a GPU host. If `which nvcc` finds it,
this is the right path.

```sh
cd examples/workloads/gpu_hold
make                       # nvcc -O2 -o gpu_hold gpu_hold.cu

./gpu_hold                 # 2 GiB + busy kernel for 120 s
GPU_HOLD_MIB=4096 GPU_HOLD_SECONDS=300 ./gpu_hold
GPU_HOLD_COMPUTE=0 ./gpu_hold    # memory hold only, no SM activity
```

If `nvcc` is missing but the CUDA libs are present, install just the
toolkit (Ubuntu: `apt install nvidia-cuda-toolkit`); no full SDK
needed.

## Option B: PyTorch (Python)

```sh
pip install --break-system-packages torch
python3 examples/workloads/gpu_hold/gpu_hold.py

# Same env vars as the C version
GPU_HOLD_MIB=4096 GPU_HOLD_SECONDS=300 python3 gpu_hold.py
GPU_HOLD_COMPUTE=0 python3 gpu_hold.py
```

The torch wheel with CUDA support is ~3 GiB. On disk-constrained
hosts, use Option A instead — or run `pip cache purge` before retrying
if a previous install left partial downloads in `~/.cache/pip`.

## Verify against the bridge

In another shell while the workload is running:

```sh
# Bridge maps (built-in inspector, no bpftool required).
sudo ~/gpu-ebpf-bridge/bin/gpu-ebpf-bridge-nvml --dump

# NVML ground truth.
nvidia-smi --query-compute-apps=pid,used_memory --format=csv
nvidia-smi --query-gpu=utilization.gpu,memory.used --format=csv,nounits

# gpu_top gadget (requires IG + the alban_map_pinning_and_iter branch).
sudo ig run gpu_top:alban_map_pinning_and_iter --verify-image=false \
     --timeout=2 -o json | jq .
```

Expected in `--dump` while `gpu_hold` is running:

- `# gpu_per_pid` has an entry keyed by the workload's PID with
  `UsedGpuMemoryTotal > 0` and (if `GPU_HOLD_COMPUTE=1`)
  `SmUtilPctMax > 0`.
- `# gpu_per_pid_per_device` has the same PID with `Dev=0`.
- `# gpu_device[0]` shows non-zero `MemUsed` and (with compute on)
  non-zero `SmUtilPct`.

If `gpu_per_pid` stays empty even though `nvidia-smi
--query-compute-apps` lists the PID, the most common cause is NVML's
`GetProcessUtilization` / `GetComputeRunningProcesses` being
restricted on the SKU (Azure vGPU profiles sometimes hide other
tenants' processes; this is documented in the NVML docs).

