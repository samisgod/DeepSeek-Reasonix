package agent

import "context"

// ContinuationPolicy is the internal host policy for synthetic same-Run
// continuation. It is not a user configuration key.
type ContinuationPolicy uint8

const (
	// ContinuationDisabled is the default: a clean terminal ends the Run after
	// hard safety/readiness gates. Ordinary agents leave this unset.
	ContinuationDisabled ContinuationPolicy = iota
	// ContinuationExplicitFlow opts a dedicated Goal/review/guardian/typed-report
	// run into host-owned continuation helpers.
	ContinuationExplicitFlow
)

type continuationPolicyKey struct{}

// WithContinuationPolicy opts one Run into an explicit continuation flow.
func WithContinuationPolicy(ctx context.Context, policy ContinuationPolicy) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, continuationPolicyKey{}, policy)
}

// hostContinuationEnabled reports whether this Run opted into host-owned
// synthetic continuation, which is what gates host progress checkpoints:
// ordinary chat turns must never carry a todo-stall continuation.
func (a *Agent) hostContinuationEnabled(ctx context.Context) bool {
	if a == nil {
		return false
	}
	if ctx != nil {
		if policy, ok := ctx.Value(continuationPolicyKey{}).(ContinuationPolicy); ok {
			return policy == ContinuationExplicitFlow
		}
	}
	return a.continuationPolicy == ContinuationExplicitFlow
}
