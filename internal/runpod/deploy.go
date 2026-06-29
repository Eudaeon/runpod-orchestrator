package runpod

import "context"

// DeployInput describes an on-demand secure-cloud pod to create. It mirrors the
// fields of RunPod's PodFindAndDeployOnDemandInput that we use.
type DeployInput struct {
	Name      string
	ImageName string
	// DockerArgs is the container start command (a shell script). It takes the
	// place of the template's dockerArgs.
	DockerArgs string

	GpuTypeID         string
	GpuCount          int
	MinVcpuCount      int
	MinMemoryInGb     int
	ContainerDiskInGb int
	VolumeInGb        int
	VolumeMountPath   string
	Ports             string
	DeployCost        float64

	StartSsh     bool
	StartJupyter bool

	// AllowedCudaVersions, when set, restricts placement to machines reporting
	// one of these CUDA versions (e.g. ["12.8","12.9"]).
	AllowedCudaVersions []string
}

// Pod is the result of a successful deployment.
type Pod struct {
	ID        string `json:"id"`
	ImageName string `json:"imageName"`
	MachineID string `json:"machineId"`
}

const deployOnDemandMutation = `mutation DeployOnDemand($input: PodFindAndDeployOnDemandInput) {
  podFindAndDeployOnDemand(input: $input) {
    id
    imageName
    machineId
  }
}`

// DeployOnDemand provisions a secure-cloud on-demand pod and returns it.
func (c *Client) DeployOnDemand(ctx context.Context, in DeployInput) (*Pod, error) {
	input := map[string]any{
		"cloudType":         "SECURE",
		"name":              in.Name,
		"imageName":         in.ImageName,
		"dockerArgs":        in.DockerArgs,
		"gpuTypeId":         in.GpuTypeID,
		"gpuCount":          in.GpuCount,
		"minVcpuCount":      in.MinVcpuCount,
		"minMemoryInGb":     in.MinMemoryInGb,
		"containerDiskInGb": in.ContainerDiskInGb,
		"volumeInGb":        in.VolumeInGb,
		"volumeMountPath":   in.VolumeMountPath,
		"ports":             in.Ports,
		"deployCost":        in.DeployCost,
		"startSsh":          in.StartSsh,
		"startJupyter":      in.StartJupyter,
		"globalNetwork":     false,
		"dataCenterId":      "",
		"networkVolumeId":   nil,
		"volumeKey":         nil,
	}
	if len(in.AllowedCudaVersions) > 0 {
		input["allowedCudaVersions"] = in.AllowedCudaVersions
	}

	var out struct {
		Pod *Pod `json:"podFindAndDeployOnDemand"`
	}
	if err := c.do(ctx, "DeployOnDemand", deployOnDemandMutation, map[string]any{"input": input}, &out); err != nil {
		return nil, err
	}
	if out.Pod == nil {
		return nil, errDeployFailed
	}
	return out.Pod, nil
}
