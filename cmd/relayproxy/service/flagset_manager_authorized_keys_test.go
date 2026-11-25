package service_test

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
	"github.com/blackbirdworks/semaphore/testhelpers/fixtures"
	"github.com/blackbirdworks/semaphore/testhelpers/slogutil"
)

const (
	adminOne      = "admin-1"
	adminTwo      = "admin-2"
	adminA        = "admin-a"
	adminKeep     = "admin-keep"
	adminNew      = "admin-new"
	adminOld      = "admin-old"
	evalOne       = "eval-1"
	evalTwo       = "eval-2"
	evaluationKey = "evaluation-key"
	flagsetKey    = "flagset-key"
)

func authorizedKeysFlagset() config.FlagSet {
	return config.FlagSet{
		Name:      "fs-keys",
		Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: fixtures.FlagConfigYAMLParentPath},
		APIKeys:   []string{flagsetKey},
	}
}

func TestFlagsetManager_OnConfigChange_AuthorizedKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		current           config.APIKeys
		next              config.APIKeys
		wantAdmin         []string
		wantNotAdmin      []string
		wantNotEvaluation []string
	}{
		{
			name:         "an added admin key is accepted",
			current:      config.APIKeys{Admin: []string{adminOne}},
			next:         config.APIKeys{Admin: []string{adminOne, adminTwo}},
			wantAdmin:    []string{adminOne, adminTwo},
			wantNotAdmin: []string{"unknown"},
		},
		{
			name:         "a removed admin key is rejected",
			current:      config.APIKeys{Admin: []string{adminOne, adminTwo}},
			next:         config.APIKeys{Admin: []string{adminOne}},
			wantAdmin:    []string{adminOne},
			wantNotAdmin: []string{adminTwo},
		},
		{
			name:         "a replaced admin key swaps the accepted key",
			current:      config.APIKeys{Admin: []string{adminOld}},
			next:         config.APIKeys{Admin: []string{adminNew}},
			wantAdmin:    []string{adminNew},
			wantNotAdmin: []string{adminOld},
		},
		{
			name:         "admin keys can be added when none were configured",
			current:      config.APIKeys{},
			next:         config.APIKeys{Admin: []string{adminNew}},
			wantAdmin:    []string{adminNew},
			wantNotAdmin: []string{"unknown"},
		},
		{
			// flag sets route an evaluation request by the key of its flag set: evaluation keys are never accepted
			name:              "an added evaluation key is not accepted in flag set mode",
			current:           config.APIKeys{Evaluation: []string{evalOne}},
			next:              config.APIKeys{Evaluation: []string{evalOne, evalTwo}},
			wantNotEvaluation: []string{evalOne, evalTwo},
		},
		{
			name:              "a removed evaluation key stays rejected in flag set mode",
			current:           config.APIKeys{Evaluation: []string{evalOne, evalTwo}},
			next:              config.APIKeys{Evaluation: []string{evalOne}},
			wantNotEvaluation: []string{evalOne, evalTwo},
		},
		{
			name:         "all the admin keys can be removed",
			current:      config.APIKeys{Admin: []string{adminOld}},
			next:         config.APIKeys{},
			wantNotAdmin: []string{adminOld},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &config.Config{
				FlagSets:       []config.FlagSet{authorizedKeysFlagset()},
				AuthorizedKeys: tt.current,
			}
			cfg.ForceReloadAPIKeys()
			manager, _ := setupManager(t, cfg)

			manager.OnConfigChange(&config.Config{
				FlagSets:       []config.FlagSet{authorizedKeysFlagset()},
				AuthorizedKeys: tt.next,
			})

			for _, key := range tt.wantAdmin {
				assert.Truef(t, cfg.APIKeysAdminExists(key), "admin key %q must be accepted", key)
			}

			for _, key := range tt.wantNotAdmin {
				assert.Falsef(t, cfg.APIKeysAdminExists(key), "admin key %q must be rejected", key)
			}

			for _, key := range tt.wantNotEvaluation {
				assert.Falsef(t, cfg.APIKeyExists(key), "evaluation key %q must be rejected in flag set mode", key)
			}

			assert.Equal(t, tt.next.Admin, cfg.GetAuthorizedKeys().Admin)
			assert.Equal(t, tt.next.Evaluation, cfg.GetAuthorizedKeys().Evaluation)
			assert.True(t, cfg.APIKeyExists(flagsetKey), "the flagset key must stay accepted")
		})
	}
}

func TestFlagsetManager_OnConfigChange_AuthorizedKeysConcurrentReload(t *testing.T) {
	t.Parallel()

	reloadWith := func(admin ...string) *config.Config {
		return &config.Config{
			FlagSets:       []config.FlagSet{authorizedKeysFlagset()},
			AuthorizedKeys: config.APIKeys{Admin: admin},
		}
	}
	cfg := reloadWith(adminA, adminKeep)
	cfg.ForceReloadAPIKeys()
	manager, _ := setupManager(t, cfg)

	withA := reloadWith(adminA, adminKeep)
	withB := reloadWith("admin-b", adminKeep)

	done := make(chan struct{})

	var (
		violations atomic.Int64
		wg         sync.WaitGroup
	)
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
					// The keys present before and after the reload are accepted throughout it: the
					// accepted set is never seen empty or half built.
					if !cfg.APIKeysAdminExists(adminKeep) || !cfg.APIKeyExists(flagsetKey) {
						violations.Add(1)
					}
				}
			}
		})
	}

	wg.Go(func() {
		for i := range 200 {
			if i%2 == 0 {
				manager.OnConfigChange(withB)
			} else {
				manager.OnConfigChange(withA)
			}
		}

		close(done)
	})
	wg.Wait()

	assert.Equal(t, int64(0), violations.Load(), "the keys must never be seen half updated during a reload")
	assert.True(t, cfg.APIKeysAdminExists(adminA), "the last applied configuration has the admin-a key")
	assert.False(t, cfg.APIKeysAdminExists("admin-b"), "the last applied configuration drops the admin-b key")
}

// TestFlagsetManager_OnConfigChange_EvaluationKeysStayRejectedWithoutRunningFlagset checks that a reload which
// removes the last running flag set, whose replacement fails to start, does not make the top-level evaluation
// keys valid again: the manager still routes by flag set.
func TestFlagsetManager_OnConfigChange_EvaluationKeysStayRejectedWithoutRunningFlagset(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		FlagSets:       []config.FlagSet{authorizedKeysFlagset()},
		AuthorizedKeys: config.APIKeys{Evaluation: []string{evaluationKey}},
	}
	cfg.ForceReloadAPIKeys()
	manager, _ := setupManager(t, cfg)

	broken := config.FlagSet{
		Name:      "fs-broken",
		Retriever: &retrieverconf.RetrieverConf{Kind: fixtures.File, Path: "/does/not/exist/flags.yaml"},
		APIKeys:   []string{brokenKey},
	}
	manager.OnConfigChange(&config.Config{
		FlagSets:       []config.FlagSet{broken},
		AuthorizedKeys: config.APIKeys{Evaluation: []string{evaluationKey}},
	})

	assert.Empty(t, cfg.GetFlagSets(), "no flag set is running anymore")
	assert.False(t, cfg.APIKeyExists(evaluationKey))
}

func TestFlagsetManager_WarnsAboutUnroutableEvaluationKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		keys     config.APIKeys
		wantWarn int
	}{
		{name: "no evaluation key", keys: config.APIKeys{Admin: []string{"admin"}}, wantWarn: 0},
		{name: "authorizedKeys.evaluation", keys: config.APIKeys{Evaluation: []string{"k"}}, wantWarn: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			obs, logs := slogutil.NewObserver(slog.LevelWarn)
			manager, err := service.NewFlagsetManager(&config.Config{
				FlagSets:       []config.FlagSet{authorizedKeysFlagset()},
				AuthorizedKeys: tt.keys,
			}, obs, nil, nil, nil)
			require.NoError(t, err)
			t.Cleanup(manager.Close)

			assert.Equal(t, tt.wantWarn, logs.FilterMessageSnippet("is ignored in flag set mode").Len())
		})
	}
}
