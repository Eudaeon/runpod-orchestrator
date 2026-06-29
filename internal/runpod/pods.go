package runpod

import "context"

// PodInfo is a summary of a pod as returned by the pods listing.
type PodInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	DesiredStatus string `json:"desiredStatus"`
}

const listPodsQuery = `query myPods {
  myself {
    pods {
      id
      name
      desiredStatus
    }
  }
}`

// ListPods returns all pods on the account.
func (c *Client) ListPods(ctx context.Context) ([]PodInfo, error) {
	var out struct {
		Myself struct {
			Pods []PodInfo `json:"pods"`
		} `json:"myself"`
	}
	if err := c.do(ctx, "myPods", listPodsQuery, map[string]any{}, &out); err != nil {
		return nil, err
	}
	return out.Myself.Pods, nil
}

const stopPodMutation = `mutation stopPod($input: PodStopInput!) {
  podStop(input: $input) {
    id
    desiredStatus
  }
}`

const terminatePodMutation = `mutation terminatePod($input: PodTerminateInput!) {
  podTerminate(input: $input)
}`

// StopPod stops a running pod (it can be resumed later). RunPod still bills for
// the container disk of a stopped pod, so callers that are done should also
// terminate it.
func (c *Client) StopPod(ctx context.Context, podID string) error {
	input := map[string]any{"input": map[string]any{"podId": podID}}
	return c.do(ctx, "stopPod", stopPodMutation, input, nil)
}

// TerminatePod permanently destroys a pod and stops all billing for it.
func (c *Client) TerminatePod(ctx context.Context, podID string) error {
	input := map[string]any{"input": map[string]any{"podId": podID}}
	return c.do(ctx, "terminatePod", terminatePodMutation, input, nil)
}
