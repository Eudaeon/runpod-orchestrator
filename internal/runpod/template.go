package runpod

import "context"

// PodTemplate is the subset of a RunPod template the orchestrator needs to
// derive a pod deployment from.
type PodTemplate struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	ImageName         string `json:"imageName"`
	DockerArgs        string `json:"dockerArgs"`
	ContainerDiskInGb int    `json:"containerDiskInGb"`
	VolumeInGb        int    `json:"volumeInGb"`
	VolumeMountPath   string `json:"volumeMountPath"`
	Ports             string `json:"ports"`
	StartSsh          bool   `json:"startSsh"`
	StartJupyter      bool   `json:"startJupyter"`
}

const getPodTemplateQuery = `query getPodTemplate($id: String!) {
  podTemplate(id: $id) {
    id
    name
    imageName
    dockerArgs
    containerDiskInGb
    volumeInGb
    volumeMountPath
    ports
    startSsh
    startJupyter
  }
}`

// GetPodTemplate fetches a template by id, e.g. the Hashcat template.
func (c *Client) GetPodTemplate(ctx context.Context, id string) (*PodTemplate, error) {
	var out struct {
		PodTemplate *PodTemplate `json:"podTemplate"`
	}
	if err := c.do(ctx, "getPodTemplate", getPodTemplateQuery, map[string]any{"id": id}, &out); err != nil {
		return nil, err
	}
	if out.PodTemplate == nil {
		return nil, errTemplateNotFound
	}
	return out.PodTemplate, nil
}
