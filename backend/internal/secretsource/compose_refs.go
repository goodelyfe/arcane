package secretsource

import (
	"context"
	"slices"

	secretsourcetypes "github.com/getarcaneapp/arcane/types/v2/secretsource"

	"github.com/getarcaneapp/arcane/backend/v2/internal/common"
	"github.com/getarcaneapp/arcane/backend/v2/internal/secretsource/children/setupplan"
)

// ComposeServices lists the services of compose content and the keys each
// already gets.
func ComposeServices(content string) ([]secretsourcetypes.ComposeService, error) {
	services, err := setupplan.ComposeServices(content)
	if err != nil {
		return nil, common.Classify(common.ErrValidation, err)
	}
	result := make([]secretsourcetypes.ComposeService, 0, len(services))
	for _, service := range services {
		result = append(result, secretsourcetypes.ComposeService{
			Name:      service.Name,
			Available: service.Available,
			Editable:  service.Editable,
			Reason:    service.Reason,
		})
	}
	return result, nil
}

// AddComposeRefs adds environment references for secrets to compose content.
// It only transforms the content; callers save it.
func AddComposeRefs(req secretsourcetypes.ComposeRefsRequest) (secretsourcetypes.ComposeRefsResult, error) {
	updated, added, skipped, err := setupplan.InjectEnvironmentRefs(req.Compose, req.Assignments)
	if err != nil {
		return secretsourcetypes.ComposeRefsResult{}, common.Classify(common.ErrValidation, err)
	}
	result := secretsourcetypes.ComposeRefsResult{Compose: updated, Added: added, Skipped: make([]secretsourcetypes.ComposeSkippedRef, 0, len(skipped))}
	for _, ref := range skipped {
		result.Skipped = append(result.Skipped, secretsourcetypes.ComposeSkippedRef{Service: ref.Service, Key: ref.Key, Reason: ref.Reason})
	}
	return result, nil
}

// TargetKeys fetches a target and returns only its variable names, so a
// project can be prepared before it is bound.
func (s *SecretSourceService) TargetKeys(ctx context.Context, sourceID string, target secretsourcetypes.BindingTarget) (secretsourcetypes.TargetKeys, error) {
	source, err := s.loadSourceInternal(ctx, sourceID)
	if err != nil {
		return secretsourcetypes.TargetKeys{}, err
	}
	normalized, err := normalizeTargetInternal(source.Provider, target)
	if err != nil {
		return secretsourcetypes.TargetKeys{}, err
	}
	provider, err := s.providerForSourceInternal(source)
	if err != nil {
		return secretsourcetypes.TargetKeys{}, err
	}
	fetchCtx, cancel := context.WithTimeout(ctx, providerTimeout)
	defer cancel()
	values, skipped, err := provider.Fetch(fetchCtx, normalized)
	if err != nil {
		return secretsourcetypes.TargetKeys{}, common.Classify(common.ErrUnavailable, err)
	}

	result := secretsourcetypes.TargetKeys{Keys: []string{}, Skipped: slices.Clone(skipped)}
	for key := range values {
		if envKeyPattern.MatchString(key) {
			result.Keys = append(result.Keys, key)
		} else {
			result.Skipped = append(result.Skipped, key)
		}
	}
	if result.Skipped == nil {
		result.Skipped = []string{}
	}
	slices.Sort(result.Keys)
	slices.Sort(result.Skipped)
	return result, nil
}
