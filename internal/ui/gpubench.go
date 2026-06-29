package ui

import "runpod-orchestrator/internal/runpod"

// gpuBenchGHs maps a RunPod GPU id to its measured MD4 benchmark speed in GH/s,
// from `hashcat -b -w 4` (benchmark mode, hash-mode 900) on a single GPU at
// workload profile 4. Values are the raw hashcat rates; the picker renders them
// to two decimals and derives Hash/$ from them and the live listed price. A GPU
// without an entry has not been benchmarked.
var gpuBenchGHs = map[string]float64{
	"NVIDIA GeForce RTX 4090":                           88.9871,
	"NVIDIA GeForce RTX 5090":                           126.9,
	"NVIDIA RTX 4000 Ada Generation":                    27.2320,
	"NVIDIA RTX A5000":                                  27.5714,
	"NVIDIA RTX 6000 Ada Generation":                    77.5390,
	"NVIDIA RTX A4500":                                  24.0421,
	"NVIDIA L40S":                                       81.9795,
	"NVIDIA A40":                                        34.5485,
	"NVIDIA RTX A6000":                                  36.7106,
	"NVIDIA L4":                                         23.3122,
	"NVIDIA RTX PRO 6000 Blackwell Server Edition":      123.6,
	"NVIDIA RTX PRO 6000 Blackwell Workstation Edition": 41.6332,
	"NVIDIA RTX 2000 Ada Generation":                    13.3584,
	"NVIDIA A100 80GB PCIe":                             38.4782,
	"NVIDIA A100-SXM4-80GB":                             38.8501,
	"NVIDIA H100 80GB HBM3":                             75.1543,
	"NVIDIA H100 NVL":                                   67.4542,
	"NVIDIA H200":                                       74.8677,
	"NVIDIA B200":                                       85.5872,
}

// gpuHashPerDollar returns a GPU's MD4 cost-efficiency in TH per dollar
// (GH/s × 3600 ÷ price ÷ 1000) and whether it could be computed, which needs
// both a benchmark and a known live price.
func gpuHashPerDollar(g runpod.GpuType) (float64, bool) {
	ghs, ok := gpuBenchGHs[g.ID]
	if !ok || g.SecurePrice <= 0 {
		return 0, false
	}
	return ghs * 3.6 / g.SecurePrice, true
}
