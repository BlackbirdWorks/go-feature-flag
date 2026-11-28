package valkeysegment_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/valkeysegment"
)

const (
	cmdEXISTS     = "EXISTS"
	cmdSMISMEMBER = "SMISMEMBER"
	cmdEXEC       = "EXEC"
	replyOneIn    = "*1\r\n:1\r\n"
)

func TestOptionsDisableAtomic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		wantStates    []segment.State
		lookups       []segment.Lookup
		wantMulti     int
		wantExec      int
		disable       bool
		removeBetween bool
	}{
		{
			name:       "a member of an existing set",
			lookups:    []segment.Lookup{lk(segBeta, idAlice)},
			wantStates: []segment.State{segment.Member},
			wantMulti:  1, wantExec: 1,
		},
		{
			name:       "a non-member of an existing set",
			lookups:    []segment.Lookup{lk(segBeta, "zed")},
			wantStates: []segment.State{segment.NotMember},
			wantMulti:  1, wantExec: 1,
		},
		{
			name:       "a set that does not exist is unknown",
			lookups:    []segment.Lookup{lk(segGhost, idAlice)},
			wantStates: []segment.State{segment.Unknown},
			wantMulti:  1, wantExec: 1,
		},
		{
			name:          "a set removed between the commands is never a non-member",
			lookups:       []segment.Lookup{lk(segBeta, idAlice), lk(segBeta, "zed")},
			wantStates:    []segment.State{segment.Unknown, segment.Unknown},
			wantMulti:     1,
			wantExec:      1,
			removeBetween: true,
		},
		{
			name:       "off: a member of an existing set",
			lookups:    []segment.Lookup{lk(segBeta, idAlice)},
			wantStates: []segment.State{segment.Member},
			disable:    true,
		},
		{
			name:          "off: a set removed between the commands answers not-a-member",
			lookups:       []segment.Lookup{lk(segBeta, "zed")},
			wantStates:    []segment.State{segment.NotMember},
			disable:       true,
			removeBetween: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)

			if tt.removeBetween {
				srv.OnCommand(func(name string) {
					if name == cmdEXISTS {
						srv.removeSet(keyBeta)
					}
				})
			}

			opts := atomicOptions(nil)
			opts.DisableAtomic = tt.disable
			prov := newProvider(t, srv, opts)

			require.NoError(t, prov.Contains(context.Background(), tt.lookups))
			assert.Equal(t, tt.wantStates, states(tt.lookups))
			assert.Equal(t, tt.wantMulti, srv.count("multi"), "MULTI")
			assert.Equal(t, tt.wantExec, srv.count("exec"), cmdEXEC)
			assert.Equal(t, 1, srv.count("exists"), "one EXISTS")
			assert.Equal(t, 1, srv.count("smismember"), "one SMISMEMBER")
		})
	}
}

func TestProviderContainsRejectsAReplyOfTheWrongShape(t *testing.T) {
	t.Parallel()

	tests := []struct {
		wantSentry error
		name       string
		command    string
		reply      string
		wantFailed int
		wantClass  valkeysegment.Class
		disable    bool
	}{
		{
			name: "a RESP set instead of the SMISMEMBER array", command: cmdSMISMEMBER, reply: "~1\r\n:1\r\n",
			disable: true, wantClass: valkeysegment.ClassProtocol, wantSentry: valkeysegment.ErrProtocol, wantFailed: 1,
		},
		{
			name: "a RESP set inside EXEC", command: cmdEXEC, reply: "*2\r\n:1\r\n~1\r\n:1\r\n",
			wantClass: valkeysegment.ClassProtocol, wantSentry: valkeysegment.ErrProtocol, wantFailed: 1,
		},
		{
			name: "a RESP set instead of the EXEC array", command: cmdEXEC, reply: "~2\r\n:1\r\n*1\r\n:1\r\n",
			wantClass: valkeysegment.ClassProtocol, wantSentry: valkeysegment.ErrProtocol, wantFailed: 1,
		},
		{
			name: "an EXEC array of the wrong length", command: cmdEXEC, reply: replyOneIn,
			wantClass: valkeysegment.ClassProtocol, wantSentry: valkeysegment.ErrProtocol, wantFailed: 1,
		},
		{
			name: "an EXISTS that is a string inside EXEC", command: cmdEXEC, reply: "*2\r\n$1\r\n1\r\n*1\r\n:1\r\n",
			wantClass: valkeysegment.ClassProtocol, wantSentry: valkeysegment.ErrProtocol, wantFailed: 1,
		},
		{
			name: "an EXISTS that is not 0 or 1 inside EXEC", command: cmdEXEC, reply: "*2\r\n:7\r\n*1\r\n:1\r\n",
			wantClass: valkeysegment.ClassProtocol, wantSentry: valkeysegment.ErrProtocol, wantFailed: 1,
		},
		{
			name: "a nil EXEC (aborted) is a protocol error", command: cmdEXEC, reply: "_\r\n",
			wantClass: valkeysegment.ClassProtocol, wantSentry: valkeysegment.ErrProtocol, wantFailed: 1,
		},
		{
			name: "an error reply of SMISMEMBER inside EXEC fails its lookups", command: cmdEXEC,
			reply:     "*2\r\n:1\r\n-WRONGTYPE bad\r\n",
			wantClass: valkeysegment.ClassServer, wantSentry: valkeysegment.ErrServer, wantFailed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)
			opts := atomicOptions(nil)
			opts.DisableAtomic = tt.disable
			prov := newProvider(t, srv, opts)

			srv.answerOnly(tt.command, tt.reply)

			lookups := []segment.Lookup{lk(segBeta, idAlice)}
			err := prov.Contains(context.Background(), lookups)

			require.ErrorIs(t, err, tt.wantSentry)

			var lookupErr *valkeysegment.LookupError

			require.ErrorAs(t, err, &lookupErr)
			assert.Equal(t, tt.wantClass, lookupErr.Class)
			assert.Equal(t, tt.wantFailed, lookupErr.Failed)
			assert.Equal(t, []segment.State{segment.Unknown}, states(lookups))
		})
	}
}

// recorder is a client that keeps the names and slots of the commands of every DoMulti (valkey-go recycles the
// commands once they are sent), and reports the mode that the test gives it.
type recorder struct {
	valkey.Client

	mode  valkey.ClientMode
	names []string // one entry for each DoMulti: its command names, space separated
	slots [][]uint16
	mu    sync.Mutex
}

func (r *recorder) Mode() valkey.ClientMode { return r.mode }

func (r *recorder) DoMulti(ctx context.Context, multi ...valkey.Completed) []valkey.ValkeyResult {
	names, slots := make([]string, len(multi)), make([]uint16, len(multi))

	for i := range multi {
		names[i], slots[i] = multi[i].Commands()[0], multi[i].Slot()
	}

	r.mu.Lock()
	r.names, r.slots = append(r.names, strings.Join(names, " ")), append(r.slots, slots)
	r.mu.Unlock()

	return r.Client.DoMulti(ctx, multi...)
}

const txNames = "MULTI EXISTS SMISMEMBER EXEC"

func TestNewSendsOneTransactionOnOneSlotPerSet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mode       valkey.ClientMode
		lookups    []segment.Lookup
		wantCalls  []string
		wantStates []segment.State
	}{
		{
			name:       "standalone, one set: one DoMulti",
			mode:       valkey.ClientModeStandalone,
			lookups:    []segment.Lookup{lk(segBeta, idAlice)},
			wantCalls:  []string{txNames},
			wantStates: []segment.State{segment.Member},
		},
		{
			name:       "standalone, two sets and a key: one DoMulti, a block for each set",
			mode:       valkey.ClientModeStandalone,
			lookups:    []segment.Lookup{lk(segBeta, idAlice), lk(segGhost, idAlice), lk(segFlags, idAlice)},
			wantCalls:  []string{txNames + " " + txNames + " EXISTS"},
			wantStates: []segment.State{segment.Member, segment.Unknown, segment.Member},
		},
		{
			name:       "cluster, two sets and a key: a DoMulti for each transaction and one for the rest",
			mode:       valkey.ClientModeCluster,
			lookups:    []segment.Lookup{lk(segBeta, idAlice), lk(segGhost, idAlice), lk(segFlags, idAlice)},
			wantCalls:  []string{"EXISTS", txNames, txNames},
			wantStates: []segment.State{segment.Member, segment.Unknown, segment.Member},
		},
		{
			name:       "cluster, one set: its transaction alone",
			mode:       valkey.ClientModeCluster,
			lookups:    []segment.Lookup{lk(segBeta, "zed")},
			wantCalls:  []string{txNames},
			wantStates: []segment.State{segment.NotMember},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := loaded(t)
			srv.setKey(flagPrefix + idAlice)

			client, err := valkey.NewClient(*srv.clientOption())
			require.NoError(t, err)
			t.Cleanup(client.Close)

			rec := &recorder{Client: client, mode: tt.mode}
			prov, err := valkeysegment.New(rec, atomicOptions(nil))
			require.NoError(t, err)

			require.NoError(t, prov.Contains(context.Background(), tt.lookups))
			assert.Equal(t, tt.wantStates, states(tt.lookups))

			// EXISTS and SMISMEMBER of a transaction name one key: one slot
			for i, call := range rec.names {
				if strings.HasPrefix(call, "MULTI") {
					assert.Equal(t, rec.slots[i][1], rec.slots[i][2], "EXISTS and SMISMEMBER hash to one slot")
				}
			}

			slices.Sort(rec.names)
			slices.Sort(tt.wantCalls)
			assert.Equal(t, tt.wantCalls, rec.names)
		})
	}
}
