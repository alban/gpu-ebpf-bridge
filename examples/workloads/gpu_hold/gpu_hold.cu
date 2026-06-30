/* SPDX-License-Identifier: Apache-2.0
 *
 * gpu_hold.cu -- minimal CUDA workload to populate the gpu-ebpf-bridge
 * per-PID maps (gpu_per_pid, gpu_per_pid_per_device).
 *
 * Allocates a chunk of VRAM and optionally launches a busy kernel that
 * does floating-point work on every SM, so the per-PID utilization
 * counter goes non-zero.
 *
 * Why this exists as a .cu file: the equivalent Python (PyTorch)
 * workload pulls ~3 GiB of wheels, which doesn't fit on tight VM disk
 * images. nvcc + this file produce a ~50 KiB binary with zero runtime
 * Python dependencies; the CUDA toolkit is already required to install
 * the driver on the host, so nvcc is normally available.
 *
 * Build:
 *     nvcc -O2 -o gpu_hold gpu_hold.cu
 *     # or: make -C examples/workloads/gpu_hold
 *
 * Run:
 *     ./gpu_hold                # 2 GiB + busy kernel for 120 s
 *     GPU_HOLD_MIB=4096 GPU_HOLD_SECONDS=300 ./gpu_hold
 *     GPU_HOLD_COMPUTE=0 ./gpu_hold    # memory hold only, no SM activity
 */

#include <cuda_runtime.h>
#include <math.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>
#include <unistd.h>

/* ---- environment-variable helpers ----------------------------------- */

static long env_long(const char *name, long defval)
{
	const char *v = getenv(name);
	if (!v || !*v)
		return defval;
	char *end = NULL;
	long out = strtol(v, &end, 10);
	if (end == v || *end != '\0') {
		fprintf(stderr, "warning: %s=%s is not a number; using %ld\n",
			name, v, defval);
		return defval;
	}
	return out;
}

static double env_double(const char *name, double defval)
{
	const char *v = getenv(name);
	if (!v || !*v)
		return defval;
	char *end = NULL;
	double out = strtod(v, &end);
	if (end == v || *end != '\0') {
		fprintf(stderr, "warning: %s=%s is not a number; using %g\n",
			name, v, defval);
		return defval;
	}
	return out;
}

static int env_bool(const char *name, int defval)
{
	const char *v = getenv(name);
	if (!v)
		return defval;
	if (strcmp(v, "0") == 0 || strcasecmp(v, "false") == 0 || *v == '\0')
		return 0;
	return 1;
}

#define CUDA_CHECK(call)                                                  \
	do {                                                              \
		cudaError_t _err = (call);                                \
		if (_err != cudaSuccess) {                                \
			fprintf(stderr, "%s:%d: %s -> %s\n", __FILE__,    \
				__LINE__, #call, cudaGetErrorString(_err)); \
			exit(EXIT_FAILURE);                               \
		}                                                         \
	} while (0)

/* ---- kernel --------------------------------------------------------- *
 *
 * Does FP add/mul/sin/cos in a tight loop on every thread. Total work
 * scales with `inner_iters` to keep occupancy high. The compiler cannot
 * fold the loop away because the result is written back to global memory.
 */
__global__ void busy_kernel(float *data, int n, int inner_iters)
{
	int tid = blockIdx.x * blockDim.x + threadIdx.x;
	if (tid >= n)
		return;
	float x = data[tid];
	for (int i = 0; i < inner_iters; i++) {
		x = x * 1.0001f + 1e-6f;
		x = sinf(x) + cosf(x);
	}
	data[tid] = x;
}

/* --------------------------------------------------------------------- */

static double now_seconds(void)
{
	struct timespec ts;
	clock_gettime(CLOCK_MONOTONIC, &ts);
	return ts.tv_sec + ts.tv_nsec * 1e-9;
}

int main(void)
{
	long mib       = env_long  ("GPU_HOLD_MIB",       2048);
	long duration  = env_long  ("GPU_HOLD_SECONDS",   120);
	int  compute   = env_bool  ("GPU_HOLD_COMPUTE",   1);
	long ksize     = env_long  ("GPU_HOLD_KERNEL_SIZE", 1024 * 1024); /* threads */
	long inner     = env_long  ("GPU_HOLD_INNER_ITERS", 4096);
	double interval = env_double("GPU_HOLD_INTERVAL", 0.5);

	int device = 0;
	CUDA_CHECK(cudaSetDevice(device));

	cudaDeviceProp prop;
	CUDA_CHECK(cudaGetDeviceProperties(&prop, device));

	pid_t pid = getpid();
	printf("pid=%d gpu='%s' mib=%ld duration=%ld s compute=%s "
	       "kernel_threads=%ld inner_iters=%ld interval=%g s\n",
	       (int)pid, prop.name, mib, duration, compute ? "on" : "off",
	       ksize, inner, interval);
	fflush(stdout);

	size_t hold_bytes = (size_t)mib * 1024 * 1024;
	void *hold = NULL;
	CUDA_CHECK(cudaMalloc(&hold, hold_bytes));
	printf("allocated %.2f GiB at %p on device %d\n",
	       hold_bytes / (double)(1ULL << 30), hold, device);
	fflush(stdout);

	float *kbuf = NULL;
	if (compute) {
		CUDA_CHECK(cudaMalloc(&kbuf, ksize * sizeof(float)));
		CUDA_CHECK(cudaMemset(kbuf, 0, ksize * sizeof(float)));
	}

	double start = now_seconds();
	long ticks = 0;
	while (now_seconds() - start < duration) {
		if (compute) {
			int block = 256;
			int grid  = (int)((ksize + block - 1) / block);
			busy_kernel<<<grid, block>>>(kbuf, (int)ksize, (int)inner);
			CUDA_CHECK(cudaGetLastError());
			CUDA_CHECK(cudaDeviceSynchronize());
			ticks++;
			if (interval > 0) {
				struct timespec req;
				req.tv_sec  = (time_t)interval;
				req.tv_nsec = (long)((interval - req.tv_sec) * 1e9);
				nanosleep(&req, NULL);
			}
		} else {
			sleep(5);
		}

		double elapsed = now_seconds() - start;
		if (((long)elapsed) % 10 == 0 && ((long)(elapsed * 2)) % 20 == 0) {
			printf("  ... t=%6.1f s ticks=%ld\n", elapsed, ticks);
			fflush(stdout);
		}
	}

	double elapsed = now_seconds() - start;
	printf("done after %.1f s, %ld kernel launches\n", elapsed, ticks);

	if (kbuf)
		cudaFree(kbuf);
	cudaFree(hold);
	return 0;
}
