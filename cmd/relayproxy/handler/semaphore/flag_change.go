package semaphore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/blackbirdworks/semaphore/cmd/relayproxy/helper"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/metric"
	"github.com/blackbirdworks/semaphore/cmd/relayproxy/service"
	"github.com/blackbirdworks/semaphore/modules/core/utils"
)

// FlagChangeAPICtrl is the controller of the endpoint to poll to know if the flags changed.
type FlagChangeAPICtrl struct {
	flagsetManager service.FlagsetManager
	metrics        *metric.Metrics
}

// NewAPIFlagChange is the constructor of the controller of the flag change endpoint.
func NewAPIFlagChange(flagsetManager service.FlagsetManager, metrics *metric.Metrics) *FlagChangeAPICtrl {
	return &FlagChangeAPICtrl{flagsetManager: flagsetManager, metrics: metrics}
}

// FlagChangeResponse is the response of the flag change endpoint.
type FlagChangeResponse struct {
	Flags map[string]uint32 `json:"flags"`
	Hash  uint32            `json:"hash"`
}

// FlagChangeInput is the request of the flag change endpoint.
type FlagChangeInput struct {
	Request

	IfNoneMatch string `doc:"The request will be processed only if ETag doesn't match." header:"If-None-Match"`
}

// FlagChangeOutput is the response of the flag change endpoint.
type FlagChangeOutput struct {
	Body FlagChangeResponse
}

// Handle is the endpoint to poll if you want to know if there is a configuration change in the flags.
func (h *FlagChangeAPICtrl) Handle(ctx context.Context, in *FlagChangeInput) (*FlagChangeOutput, error) {
	flagset, httpErr := helper.RequestFlagSet(ctx, h.flagsetManager, in.APIKey())
	if httpErr != nil {
		return nil, fmt.Errorf("getting the flag set of the flag change: %w", httpErr)
	}

	flags, err := flagset.GetFlagsFromCache()
	if err != nil {
		return nil, fmt.Errorf(
			"reading the flags of the flag change: %w",
			helper.NewHTTPError(http.StatusInternalServerError, err.Error()),
		)
	}

	res, err := json.Marshal(flags)
	if err != nil {
		return nil, fmt.Errorf(
			"computing the hash of the flag change: %w",
			helper.NewHTTPError(http.StatusInternalServerError, err.Error()),
		)
	}

	flagHashes := map[string]uint32{}

	for key, flag := range flags {
		jsonFlag, marshalErr := json.Marshal(flag)
		if marshalErr != nil {
			return nil, fmt.Errorf(
				"encoding the flag change: %w",
				helper.NewHTTPError(http.StatusInternalServerError, marshalErr.Error()),
			)
		}

		flagHashes[key] = utils.Hash(string(jsonFlag))
	}

	return &FlagChangeOutput{Body: FlagChangeResponse{Hash: utils.Hash(string(res)), Flags: flagHashes}}, nil
}
