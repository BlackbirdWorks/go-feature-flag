package redissegment_test

import (
	"bufio"
	"context"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/blackbirdworks/semaphore/modules/core/segment"
	"github.com/blackbirdworks/semaphore/modules/core/segment/redissegment"
)

// readCommand reads one RESP array of bulk strings.
func readCommand(reader *bufio.Reader) ([]string, error) {
	head, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}

	count, err := strconv.Atoi(strings.TrimSpace(head[1:]))
	if err != nil {
		return nil, io.ErrUnexpectedEOF
	}

	args := make([]string, 0, count)

	for range count {
		line, lineErr := reader.ReadString('\n')
		if lineErr != nil {
			return nil, lineErr
		}

		size, sizeErr := strconv.Atoi(strings.TrimSpace(line[1:]))
		if sizeErr != nil {
			return nil, io.ErrUnexpectedEOF
		}

		buf := make([]byte, size+2)

		_, readErr := io.ReadFull(reader, buf)
		if readErr != nil {
			return nil, readErr
		}

		args = append(args, string(buf[:size]))
	}

	return args, nil
}

// TestAnExtraReplyIsAnErrorNotAPanic documents the difference with the valkey-go provider: a server that sends a
// reply that nobody asked for does not crash go-redis. The call that follows gets a clean connection and works.
func TestAnExtraReplyIsAnErrorNotAPanic(t *testing.T) {
	t.Parallel()

	lc := net.ListenConfig{}
	listener, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	var (
		conns sync.WaitGroup
		once  sync.Once
	)

	conns.Go(func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}

			conns.Go(func() {
				defer conn.Close()

				reader := bufio.NewReader(conn)

				for {
					args, cmdErr := readCommand(reader)
					if cmdErr != nil {
						return
					}

					reply := "+OK\r\n"

					switch strings.ToUpper(args[0]) {
					case "HELLO":
						reply = "-ERR unknown command\r\n"
					case "SMISMEMBER":
						reply = "*" + strconv.Itoa(len(args)-2) + "\r\n" + strings.Repeat(":1\r\n", len(args)-2)

						once.Do(func() { reply += ":1\r\n" }) // a reply that nobody asked for
					}

					_, _ = conn.Write([]byte(reply))
				}
			})
		}
	})

	t.Cleanup(func() {
		_ = listener.Close()

		conns.Wait()
	})

	prov, err := redissegment.Dial(&redis.UniversalOptions{Addrs: []string{listener.Addr().String()}, MaxRetries: -1},
		&redissegment.Options{
			Timeout:    time.Second,
			MissingKey: redissegment.MissingNotMember,
			Segments:   map[string]redissegment.SegmentOptions{segBeta: {}},
		})
	require.NoError(t, err)
	t.Cleanup(func() { _ = prov.Close() })

	first := []segment.Lookup{{Segment: segBeta, ID: "alice"}}
	require.NoError(t, prov.Contains(context.Background(), first))
	assert.Equal(t, []segment.State{segment.Member}, []segment.State{first[0].State})

	second := []segment.Lookup{{Segment: segBeta, ID: "alice"}}
	require.NoError(t, prov.Contains(context.Background(), second), "go-redis drops the dirty connection")
	assert.Equal(t, []segment.State{segment.Member}, []segment.State{second[0].State})
}
