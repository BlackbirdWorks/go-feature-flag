package flagstate

import (
	"encoding/json"
	"fmt"
)

// AllFlags is a snapshot of the state of multiple feature flags with regard to a specific user.
// This is the return type of the AllFlagsState method of the client.
// Serializing this object to JSON MarshalJSON() will produce a JSON you can sent to your front-end.
//
//nolint:recvcheck // MarshalJSON uses a value receiver for json.Marshal when AllFlags is passed by value.
type AllFlags struct {
	// flags is the list of flag for the user.
	flags map[string]FlagState

	// Valid if false it means there was an error (such as the data store not being available),
	// in which case no flag data is in this object.
	valid bool
}

// NewAllFlags will create a new AllFlags for a specific user.
// It sets valid to true, because until we have an error everything is valid.
func NewAllFlags() AllFlags {
	return AllFlags{
		valid: true,
		flags: map[string]FlagState{},
	}
}

// AddFlag is adding a flag in the list for the specific user.
func (a *AllFlags) AddFlag(flagKey string, state *FlagState) {
	a.flags[flagKey] = *state
	if a.valid && state.Failed {
		a.valid = false
	}
}

// MarshalJSON is serializing the object to JSON, it will produce a JSON you can sent to your front-end.
func (a AllFlags) MarshalJSON() ([]byte, error) {
	res := struct {
		Flags map[string]FlagState `json:"flags,omitempty"`
		Valid bool                 `json:"valid"`
	}{
		Flags: a.sent(),
		Valid: a.IsValid(),
	}

	data, err := json.Marshal(res)
	if err != nil {
		return nil, fmt.Errorf("encoding the flags: %w", err)
	}

	return data, nil
}

// IsValid is a getter to know if the AllFlags object is valid.
func (a *AllFlags) IsValid() bool {
	return a.valid
}

// GetFlags returns the state of every flag by name.
func (a *AllFlags) GetFlags() map[string]FlagState {
	return a.flags
}

// sent is the flags as they are sent: the flags themselves when no status is to be reported (no allocation), else a
// copy where the flags of an unhealthy segment status carry it in their metadata. A map of a flag is never changed.
func (a AllFlags) sent() map[string]FlagState {
	reported := false

	for key := range a.flags {
		if a.flags[key].SegmentStatus.Reported() {
			reported = true

			break
		}
	}

	if !reported {
		return a.flags
	}

	out := make(map[string]FlagState, len(a.flags))

	for key, state := range a.flags {
		state.Metadata = state.MetadataWithStatus()
		out[key] = state
	}

	return out
}
