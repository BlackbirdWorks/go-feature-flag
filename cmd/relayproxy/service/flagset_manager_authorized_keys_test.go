package service_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/config"
	"github.com/blackbirdworks/semaphore/cmdhelpers/retrieverconf"
)

func authorizedKeysFlagset() config.FlagSet {
	return config.FlagSet{
		Name:      "fs-keys",
		Retriever: &retrieverconf.RetrieverConf{Kind: "file", Path: "../../../testdata/flag-config.yaml"},
		APIKeys:   []string{"flagset-key"},
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
		wantEvaluation    []string
		wantNotEvaluation []string
	}{
		{
			name:         "an added admin key is accepted",
			current:      config.APIKeys{Admin: []string{"admin-1"}},
			next:         config.APIKeys{Admin: []string{"admin-1", "admin-2"}},
			wantAdmin:    []string{"admin-1", "admin-2"},
			wantNotAdmin: []string{"unknown"},
		},
		{
			name:         "a removed admin key is rejected",
			current:      config.APIKeys{Admin: []string{"admin-1", "admin-2"}},
			next:         config.APIKeys{Admin: []string{"admin-1"}},
			wantAdmin:    []string{"admin-1"},
			wantNotAdmin: []string{"admin-2"},
		},
		{
			name:         "a replaced admin key swaps the accepted key",
			current:      config.APIKeys{Admin: []string{"admin-old"}},
			next:         config.APIKeys{Admin: []string{"admin-new"}},
			wantAdmin:    []string{"admin-new"},
			wantNotAdmin: []string{"admin-old"},
		},
		{
			name:         "admin keys can be added when none were configured",
			current:      config.APIKeys{},
			next:         config.APIKeys{Admin: []string{"admin-new"}},
			wantAdmin:    []string{"admin-new"},
			wantNotAdmin: []string{"unknown"},
		},
		{
			name:         "all the admin keys can be removed",
			current:      config.APIKeys{Admin: []string{"admin-old"}},
			next:         config.APIKeys{},
			wantNotAdmin: []string{"admin-old"},
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
			assert.Equal(t, tt.next.Admin, cfg.GetAuthorizedKeys().Admin)
			assert.True(t, cfg.APIKeyExists("flagset-key"), "the flagset key must stay accepted")
		})
	}
}

func TestFlagsetManager_OnConfigChange_AuthorizedKeysAreAppliedAtomically(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		FlagSets:       []config.FlagSet{authorizedKeysFlagset()},
		AuthorizedKeys: config.APIKeys{Admin: []string{"admin-a"}},
	}
	cfg.ForceReloadAPIKeys()
	manager, _ := setupManager(t, cfg)

	withA := &config.Config{
		FlagSets:       []config.FlagSet{authorizedKeysFlagset()},
		AuthorizedKeys: config.APIKeys{Admin: []string{"admin-a"}},
	}
	withB := &config.Config{
		FlagSets:       []config.FlagSet{authorizedKeysFlagset()},
		AuthorizedKeys: config.APIKeys{Admin: []string{"admin-b"}},
	}

	done := make(chan struct{})
	var neither atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
					// a key is always accepted: either the old one or the new one.
					if !cfg.APIKeysAdminExists("admin-a") && !cfg.APIKeysAdminExists("admin-b") {
						neither.Add(1)
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

	assert.Equal(t, int64(0), neither.Load(), "no admin key must be accepted-less window during a reload")
}
