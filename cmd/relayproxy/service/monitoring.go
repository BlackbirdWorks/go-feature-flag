package service

import (
	"errors"
	"fmt"
	"time"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/model"
)

var (
	errFlagsetManagerIsNot = errors.New("flagset manager is not initialized")
)

// Monitoring is the interface of the monitoring service.
type Monitoring interface {
	Health() model.HealthResponse
	Info() (model.InfoResponse, error)
}

// MonitoringImpl is one implementation of the Monitoring interface.
type MonitoringImpl struct {
	flagsetManager FlagsetManager
}

// NewMonitoring creates a new implementation of Monitoring.
func NewMonitoring(flagsetManager FlagsetManager) *MonitoringImpl {
	return &MonitoringImpl{
		flagsetManager: flagsetManager,
	}
}

// Health returns a static object to show that the server is initialized.
func (*MonitoringImpl) Health() model.HealthResponse {
	return model.HealthResponse{
		Initialized: true,
	}
}

// Info returns information about the relay-proxy.
func (m *MonitoringImpl) Info() (model.InfoResponse, error) {
	if m.flagsetManager == nil {
		return model.InfoResponse{}, errFlagsetManagerIsNot
	}

	flagSets, err := m.flagsetManager.AllFlagSets()
	if err != nil {
		return model.InfoResponse{}, fmt.Errorf("listing the flag sets for the information: %w", err)
	}

	if m.flagsetManager.IsDefaultFlagSet() {
		if m.flagsetManager.Default() == nil {
			return model.InfoResponse{}, errNoDefaultFlagsetConfigured
		}

		cacheRefreshDate := m.flagsetManager.Default().GetCacheRefreshDate()

		return model.InfoResponse{
			LatestCacheRefresh: &cacheRefreshDate,
			Flagsets:           nil,
		}, nil
	}

	refreshDates := make(map[string]time.Time, len(flagSets))
	latestRefreshDate := time.Time{}

	for flagsetName, flagset := range flagSets {
		refreshDates[flagsetName] = flagset.GetCacheRefreshDate()
		if refreshDates[flagsetName].After(latestRefreshDate) {
			latestRefreshDate = refreshDates[flagsetName]
		}
	}

	return model.InfoResponse{
		Flagsets:           refreshDates,
		LatestCacheRefresh: &latestRefreshDate,
	}, nil
}
