package revssh

import (
	"context"
	"fmt"

	"runpod-orchestrator/internal/binstore"
)

// ensureLocalBinary returns the path to the cached reverse-ssh binary for the
// local platform, downloading it on first use.
func ensureLocalBinary(ctx context.Context) (string, error) {
	asset, err := localBinaryAsset()
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("https://github.com/Fahrj/reverse-ssh/releases/download/%s/%s", ReleaseVersion, asset)
	return binstore.Ensure(ctx, asset, url)
}
