package runpod

import (
	"context"
	"sort"
	"strings"
)

// GpuType describes a GPU offering and the minimum resources / price RunPod will
// pair with it for an on-demand secure-cloud deployment.
type GpuType struct {
	ID           string
	DisplayName  string
	Manufacturer string // e.g. "Nvidia", "AMD"
	MemoryInGb   int
	SecureCloud  bool // offered in RunPod's secure cloud (vs community-only)

	// GPU-count bounds for a secure pod. vCPU/RAM/price scale with the count.
	MinPodGpuCount    int // smallest deployable count (0 from API means 1)
	MaxGpuCountSecure int // largest secure-cloud count (0 means single-GPU only)

	// SecurePrice is RunPod's listed secure-cloud price (USD/hr for one GPU). It
	// is populated even when the GPU is out of stock, unlike OnDemandPrice.
	SecurePrice float64

	// Derived from lowestPrice for an on-demand (uninterruptable) secure pod.
	// These reflect current stock: OnDemandPrice/MinVcpu are zero when the GPU
	// has no available machines for the requested filters.
	OnDemandPrice float64 // USD/hr for gpuCount GPUs
	MinVcpu       int
	MinMemoryInGb int
	StockStatus   string // e.g. "High", "Medium", "Low", or "" when unavailable
}

const secureGpuTypesQuery = `query SecureGpuTypes($lowestPriceInput: GpuLowestPriceInput, $gpuTypesInput: GpuTypeFilter) {
  gpuTypes(input: $gpuTypesInput) {
    id
    displayName
    manufacturer
    memoryInGb
    secureCloud
    securePrice
    minPodGpuCount
    maxGpuCountSecureCloud
    lowestPrice(input: $lowestPriceInput) {
      uninterruptablePrice
      minVcpu
      minMemory
      stockStatus
    }
  }
}`

// queryGpuTypes runs the secure-cloud pricing query. When id is non-empty it is
// used to filter to a single GPU type; otherwise every GPU type is returned.
//
// minVcpu, when > 0, asks RunPod to price machines that can supply at least that
// many vCPUs — this both validates a requested core count and makes the returned
// price/availability reflect it. allowedCudaVersions, when non-empty, restricts
// pricing/availability to machines reporting one of those CUDA versions.
func (c *Client) queryGpuTypes(ctx context.Context, id string, gpuCount, minVcpu int, allowedCudaVersions []string) ([]GpuType, error) {
	vcpuFloor := 2
	if minVcpu > vcpuFloor {
		vcpuFloor = minVcpu
	}
	lowestPriceInput := map[string]any{
		"gpuCount":      gpuCount,
		"minDisk":       0,
		"minMemoryInGb": 8,
		"minVcpuCount":  vcpuFloor,
		"secureCloud":   true,
		"compliance":    []string{},
		"dataCenterId":  "",
		"globalNetwork": false,
	}
	if len(allowedCudaVersions) > 0 {
		lowestPriceInput["allowedCudaVersions"] = allowedCudaVersions
	}
	gpuTypesInput := map[string]any{}
	if id != "" {
		gpuTypesInput["id"] = id
	}
	variables := map[string]any{
		"gpuTypesInput":    gpuTypesInput,
		"lowestPriceInput": lowestPriceInput,
	}

	var out struct {
		GpuTypes []struct {
			ID                string  `json:"id"`
			DisplayName       string  `json:"displayName"`
			Manufacturer      string  `json:"manufacturer"`
			MemoryInGb        int     `json:"memoryInGb"`
			SecureCloud       bool    `json:"secureCloud"`
			SecurePrice       float64 `json:"securePrice"`
			MinPodGpuCount    int     `json:"minPodGpuCount"`
			MaxGpuCountSecure int     `json:"maxGpuCountSecureCloud"`
			LowestPrice       struct {
				UninterruptablePrice float64 `json:"uninterruptablePrice"`
				MinVcpu              int     `json:"minVcpu"`
				MinMemory            int     `json:"minMemory"`
				StockStatus          string  `json:"stockStatus"`
			} `json:"lowestPrice"`
		} `json:"gpuTypes"`
	}
	if err := c.do(ctx, "SecureGpuTypes", secureGpuTypesQuery, variables, &out); err != nil {
		return nil, err
	}

	gpus := make([]GpuType, 0, len(out.GpuTypes))
	for _, g := range out.GpuTypes {
		gpus = append(gpus, GpuType{
			ID:                g.ID,
			DisplayName:       g.DisplayName,
			Manufacturer:      g.Manufacturer,
			MemoryInGb:        g.MemoryInGb,
			SecureCloud:       g.SecureCloud,
			SecurePrice:       g.SecurePrice,
			MinPodGpuCount:    g.MinPodGpuCount,
			MaxGpuCountSecure: g.MaxGpuCountSecure,
			OnDemandPrice:     g.LowestPrice.UninterruptablePrice,
			MinVcpu:           g.LowestPrice.MinVcpu,
			MinMemoryInGb:     g.LowestPrice.MinMemory,
			StockStatus:       g.LowestPrice.StockStatus,
		})
	}
	return gpus, nil
}

// GetGpuType resolves a GPU type id (e.g. "NVIDIA GeForce RTX 4090") into its
// on-demand price and the minimum vCPU/memory RunPod requires for it. These feed
// directly into a DeployOnDemand request. It returns (nil, nil) when no GPU type
// matches the id, so callers can distinguish an unknown id from a transport
// error.
//
// See queryGpuTypes for the meaning of minVcpu and allowedCudaVersions; the
// latter must match the version passed to DeployOnDemand, or the deploy can land
// on a machine whose CUDA is too old for the image.
func (c *Client) GetGpuType(ctx context.Context, id string, gpuCount, minVcpu int, allowedCudaVersions []string) (*GpuType, error) {
	gpus, err := c.queryGpuTypes(ctx, id, gpuCount, minVcpu, allowedCudaVersions)
	if err != nil {
		return nil, err
	}
	if len(gpus) == 0 {
		return nil, nil
	}
	return &gpus[0], nil
}

// ListGpuTypes returns every secure-cloud GPU type with its on-demand price and
// availability, sorted by price (cheapest first, then unpriced/unavailable). It
// backs the table shown when a --gpu or --cores value can't be satisfied.
func (c *Client) ListGpuTypes(ctx context.Context, gpuCount, minVcpu int, allowedCudaVersions []string) ([]GpuType, error) {
	gpus, err := c.queryGpuTypes(ctx, "", gpuCount, minVcpu, allowedCudaVersions)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(gpus, func(i, j int) bool {
		pi, pj := gpus[i].SecurePrice, gpus[j].SecurePrice
		switch {
		case pi <= 0 && pj <= 0:
			return gpus[i].DisplayName < gpus[j].DisplayName
		case pi <= 0:
			return false
		case pj <= 0:
			return true
		default:
			return pi < pj
		}
	})
	return gpus, nil
}

// IsNvidia reports whether the GPU is an NVIDIA card.
func (g GpuType) IsNvidia() bool {
	return strings.EqualFold(g.Manufacturer, "Nvidia")
}
