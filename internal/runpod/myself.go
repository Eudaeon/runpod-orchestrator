package runpod

import "context"

// Myself describes the authenticated RunPod account.
type Myself struct {
	ID                string  `json:"id"`
	Email             string  `json:"email"`
	ClientBalance     float64 `json:"clientBalance"`
	CurrentSpendPerHr float64 `json:"currentSpendPerHr"`
	SpendLimit        float64 `json:"spendLimit"`
}

const getMyselfQuery = `query getMyself {
  myself {
    id
    email
    clientBalance
    currentSpendPerHr
    spendLimit
  }
}`

// GetMyself returns the authenticated account, including its current balance.
// A successful call confirms the credentials are valid.
func (c *Client) GetMyself(ctx context.Context) (*Myself, error) {
	var out struct {
		Myself *Myself `json:"myself"`
	}
	if err := c.do(ctx, "getMyself", getMyselfQuery, map[string]any{}, &out); err != nil {
		return nil, err
	}
	if out.Myself == nil {
		return nil, errNotAuthenticated
	}
	return out.Myself, nil
}
