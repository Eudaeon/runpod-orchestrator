package runpod

import (
	"context"
	"fmt"
)

// CpuFlavor describes a RunPod CPU instance family (e.g. cpu5g, "General
// Purpose" on the CPU5 / 5 GHz generation). vCPU/RAM are chosen within the
// flavor: RAM is vCPU × RamMultiplier.
type CpuFlavor struct {
	ID            string `json:"id"`
	GroupID       string `json:"groupId"`
	GroupName     string `json:"groupName"`
	DisplayName   string `json:"displayName"`
	MinVcpu       int    `json:"minVcpu"`
	MaxVcpu       int    `json:"maxVcpu"`
	RamMultiplier int    `json:"ramMultiplier"`
}

const cpuFlavorsQuery = `query CpuFlavors {
  cpuFlavors {
    id
    groupId
    groupName
    displayName
    minVcpu
    maxVcpu
    ramMultiplier
  }
}`

// ListCpuFlavors returns the available CPU instance families.
func (c *Client) ListCpuFlavors(ctx context.Context) ([]CpuFlavor, error) {
	var out struct {
		CpuFlavors []CpuFlavor `json:"cpuFlavors"`
	}
	if err := c.do(ctx, "CpuFlavors", cpuFlavorsQuery, map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.CpuFlavors, nil
}

// CpuInstance is a concrete CPU offering: a flavor sized to a vCPU/RAM pair,
// with its current secure-cloud price and stock. InstanceID is the deploy
// handle RunPod expects, formatted "<flavor>-<vcpu>-<ram>" (e.g. cpu5g-8-32).
type CpuInstance struct {
	InstanceID  string
	FlavorID    string
	DisplayName string // flavor display name, e.g. "General Purpose"
	GroupName   string // generation, e.g. "CPU5"
	Vcpu        int
	RamGb       int
	Price       float64 // USD/hr secure cloud; 0 when out of stock
	StockStatus string  // "High"/"Medium"/"Low", or "" when unavailable
}

const secureCpuTypesQuery = `query SecureCpuTypes($cpuFlavorInput: CpuFlavorInput, $specificsInput: SpecificsInput) {
  cpuFlavors(input: $cpuFlavorInput) {
    id
    groupId
    groupName
    specifics(input: $specificsInput) {
      stockStatus
      securePrice
      slsPrice
    }
  }
}`

// GetCpuInstance resolves a flavor id (e.g. "cpu5g") and a vCPU count into a
// concrete, priced CPU offering. RAM is derived from the flavor's multiplier.
// It returns an error if the flavor is unknown or the vCPU count is outside the
// flavor's range; a returned instance with an empty StockStatus / zero Price
// means RunPod has no secure-cloud stock for it right now.
func (c *Client) GetCpuInstance(ctx context.Context, flavorID string, vcpu int) (*CpuInstance, error) {
	flavors, err := c.ListCpuFlavors(ctx)
	if err != nil {
		return nil, err
	}
	var flavor *CpuFlavor
	for i := range flavors {
		if flavors[i].ID == flavorID {
			flavor = &flavors[i]
			break
		}
	}
	if flavor == nil {
		return nil, fmt.Errorf("runpod: unknown CPU flavor %q", flavorID)
	}
	if vcpu < flavor.MinVcpu || vcpu > flavor.MaxVcpu {
		return nil, fmt.Errorf("runpod: %s supports %d–%d vCPUs (asked for %d)",
			flavor.ID, flavor.MinVcpu, flavor.MaxVcpu, vcpu)
	}

	ram := vcpu * flavor.RamMultiplier
	instanceID := fmt.Sprintf("%s-%d-%d", flavor.ID, vcpu, ram)

	variables := map[string]any{
		"cpuFlavorInput": map[string]any{"id": flavor.ID},
		"specificsInput": map[string]any{
			"dataCenterId": "",
			"instanceId":   instanceID,
			"isSls":        false,
		},
	}
	var out struct {
		CpuFlavors []struct {
			Specifics struct {
				StockStatus string  `json:"stockStatus"`
				SecurePrice float64 `json:"securePrice"`
			} `json:"specifics"`
		} `json:"cpuFlavors"`
	}
	if err := c.do(ctx, "SecureCpuTypes", secureCpuTypesQuery, variables, &out); err != nil {
		return nil, err
	}

	inst := &CpuInstance{
		InstanceID:  instanceID,
		FlavorID:    flavor.ID,
		DisplayName: flavor.DisplayName,
		GroupName:   flavor.GroupName,
		Vcpu:        vcpu,
		RamGb:       ram,
	}
	if len(out.CpuFlavors) > 0 {
		inst.Price = out.CpuFlavors[0].Specifics.SecurePrice
		inst.StockStatus = out.CpuFlavors[0].Specifics.StockStatus
	}
	return inst, nil
}

// CpuDeployInput describes a secure-cloud CPU pod to create. It mirrors the
// fields of RunPod's deployCpuPodInput that we use.
type CpuDeployInput struct {
	Name       string
	InstanceID string
	TemplateID string
	ImageName  string
	// DockerArgs is the container start command (a shell script), used to inject
	// the reverse-ssh dial-back on top of the template's own setup.
	DockerArgs        string
	ContainerDiskInGb int
	VolumeMountPath   string
	Ports             string
	DeployCost        float64
	StartSsh          bool
	StartJupyter      bool
}

const deployCpuPodMutation = `mutation DeployCpuPod($input: deployCpuPodInput!) {
  deployCpuPod(input: $input) {
    id
    imageName
    machineId
  }
}`

// DeployCpuPod provisions a secure-cloud CPU pod and returns it.
func (c *Client) DeployCpuPod(ctx context.Context, in CpuDeployInput) (*Pod, error) {
	input := map[string]any{
		"instanceId":        in.InstanceID,
		"cloudType":         "SECURE",
		"name":              in.Name,
		"templateId":        in.TemplateID,
		"imageName":         in.ImageName,
		"dockerArgs":        in.DockerArgs,
		"containerDiskInGb": in.ContainerDiskInGb,
		"volumeMountPath":   in.VolumeMountPath,
		"ports":             in.Ports,
		"deployCost":        in.DeployCost,
		"startSsh":          in.StartSsh,
		"startJupyter":      in.StartJupyter,
		"dataCenterId":      "",
		"networkVolumeId":   nil,
		"volumeKey":         nil,
	}

	var out struct {
		Pod *Pod `json:"deployCpuPod"`
	}
	if err := c.do(ctx, "DeployCpuPod", deployCpuPodMutation, map[string]any{"input": input}, &out); err != nil {
		return nil, err
	}
	if out.Pod == nil {
		return nil, errDeployFailed
	}
	return out.Pod, nil
}
