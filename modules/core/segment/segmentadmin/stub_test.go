package segmentadmin_test

import (
	"bufio"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	protoMax = 1 << 20
	respOK   = "+OK\r\n"
)

// stubServer is a loopback server that speaks the RESP of the commands that the write side sends, over a small
// keyspace. Its listener is open from the start. It lets the two client implementations run without a container.
type stubServer struct {
	ln        net.Listener
	sets      map[string]map[string]bool
	strs      map[string]string
	ttl       map[string]string
	conns     map[net.Conn]struct{}
	noUnlink  bool
	wg        sync.WaitGroup
	accepting sync.WaitGroup
	mu        sync.Mutex
}

func startStub(t *testing.T, noUnlink bool) *stubServer {
	t.Helper()

	lis, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := &stubServer{
		ln: lis, sets: map[string]map[string]bool{}, strs: map[string]string{}, ttl: map[string]string{},
		conns: map[net.Conn]struct{}{}, noUnlink: noUnlink,
	}

	srv.accepting.Add(1)

	go srv.accept()

	t.Cleanup(func() {
		_ = lis.Close()

		srv.accepting.Wait() // no connection is accepted (and tracked) after this

		srv.mu.Lock()
		for conn := range srv.conns {
			_ = conn.Close()
		}
		srv.mu.Unlock()

		srv.wg.Wait()
	})

	return srv
}

func (s *stubServer) addr() string { return s.ln.Addr().String() }

func (s *stubServer) accept() {
	defer s.accepting.Done()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}

		s.mu.Lock()
		s.conns[conn] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)

		go s.serve(conn)
	}
}

func readCommand(reader *bufio.Reader) ([]string, error) {
	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}

	count, err := strconv.Atoi(strings.TrimSpace(line[1:]))
	if err != nil || count < 0 || count > protoMax {
		return nil, io.ErrUnexpectedEOF
	}

	args := make([]string, 0, count)

	for range count {
		head, headErr := reader.ReadString('\n')
		if headErr != nil {
			return nil, headErr
		}

		size, sizeErr := strconv.Atoi(strings.TrimSpace(head[1:]))
		if sizeErr != nil || size < 0 || size > protoMax {
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

func (s *stubServer) serve(conn net.Conn) {
	defer s.wg.Done()
	defer conn.Close()

	reader, writer := bufio.NewReader(conn), bufio.NewWriter(conn)

	queued := make([]string, 0, 1) // the replies of the commands of a transaction

	open := false

	for {
		args, err := readCommand(reader)
		if err != nil || len(args) == 0 {
			return
		}

		name := strings.ToUpper(args[0])

		var reply string

		switch {
		case name == "MULTI":
			open, queued, reply = true, nil, respOK
		case name == "EXEC":
			reply = "*" + strconv.Itoa(len(queued)) + "\r\n" + strings.Join(queued, "")
			open = false
		case open:
			queued = append(queued, s.run(name, args))
			reply = "+QUEUED\r\n"
		default:
			reply = s.run(name, args)
		}

		_, _ = writer.WriteString(reply)

		if reader.Buffered() == 0 && writer.Flush() != nil {
			return
		}
	}
}

func integer(n int) string { return ":" + strconv.Itoa(n) + "\r\n" }

func bulk(val string) string { return "$" + strconv.Itoa(len(val)) + "\r\n" + val + "\r\n" }

func (s *stubServer) run(name string, args []string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch name {
	case "HELLO":
		return "%1\r\n+server\r\n+valkey\r\n"
	case "CLIENT", "SELECT":
		return respOK
	case "CLUSTER":
		return "-ERR This instance has cluster support disabled\r\n"
	case "PING":
		return "+PONG\r\n"
	}

	return s.runData(name, args)
}

// runData runs the commands on the keyspace. The caller holds the lock.
func (s *stubServer) runData(name string, args []string) string {
	switch name {
	case opSADD:
		return s.sadd(args)
	case "SREM":
		return s.srem(args)
	case opSET:
		return s.set(args)
	case "RENAME":
		return s.rename(args[1], args[2])
	case opGET, "PEXPIRE", "PERSIST":
		return s.runKey(name, args)
	case "EVALSHA":
		return "-NOSCRIPT No matching script. Please use EVAL.\r\n"
	case "EVAL":
		return s.eval(args)
	case "SCARD":
		return integer(len(s.sets[args[1]]))
	case "PTTL":
		return s.pttl(args[1])
	case "SCAN":
		return s.scan(args)
	}

	return s.runDelete(name, args)
}

func (s *stubServer) runKey(name string, args []string) string {
	switch name {
	case opGET:
		if val, ok := s.strs[args[1]]; ok {
			return bulk(val)
		}

		return "$-1\r\n"
	case "PEXPIRE":
		if s.exists(args[1]) {
			s.ttl[args[1]] = args[2]

			return integer(1)
		}

		return integer(0)
	}

	delete(s.ttl, args[1])

	return integer(1)
}

func (s *stubServer) runDelete(name string, args []string) string {
	switch {
	case name == opUnlink && s.noUnlink:
		return "-ERR unknown command 'UNLINK'\r\n"
	case name == opUnlink, name == "DEL":
		return s.del(args[1])
	}

	return "-ERR unknown command '" + name + "'\r\n"
}

func (s *stubServer) exists(key string) bool {
	_, set := s.sets[key]
	_, str := s.strs[key]

	return set || str
}

func (s *stubServer) sadd(args []string) string {
	set := s.sets[args[1]]
	if set == nil {
		set = map[string]bool{}
		s.sets[args[1]] = set
	}

	added := 0

	for _, member := range args[2:] {
		if !set[member] {
			set[member] = true
			added++
		}
	}

	return integer(added)
}

func (s *stubServer) srem(args []string) string {
	removed := 0

	for _, member := range args[2:] {
		if s.sets[args[1]][member] {
			delete(s.sets[args[1]], member)

			removed++
		}
	}

	return integer(removed)
}

func (s *stubServer) set(args []string) string {
	if slices.ContainsFunc(args[3:], func(arg string) bool { return strings.EqualFold(arg, "NX") }) &&
		s.exists(args[1]) {
		return "$-1\r\n"
	}

	s.strs[args[1]] = args[2]

	return respOK
}

func (s *stubServer) rename(from, dest string) string {
	if set, ok := s.sets[from]; ok {
		delete(s.sets, from)
		s.sets[dest] = set
		s.ttl[dest] = s.ttl[from]
		delete(s.ttl, from)

		return respOK
	}

	return "-ERR no such key\r\n"
}

func (s *stubServer) del(key string) string {
	existed := 0
	if s.exists(key) {
		existed = 1
	}

	delete(s.sets, key)
	delete(s.strs, key)
	delete(s.ttl, key)

	return integer(existed)
}

func (s *stubServer) pttl(key string) string {
	switch {
	case !s.exists(key):
		return integer(-2)
	case s.ttl[key] == "":
		return integer(-1)
	}

	millis, _ := strconv.Atoi(s.ttl[key])

	return integer(millis)
}

// scan answers every matching key in one page: the pattern is a prefix, then *.
func (s *stubServer) scan(args []string) string {
	prefix := ""

	for i := 2; i+1 < len(args); i++ {
		if strings.EqualFold(args[i], "MATCH") {
			prefix = strings.TrimSuffix(args[i+1], "*")
		}
	}

	var keys []string

	for key := range s.strs {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}

	slices.Sort(keys)

	out := "*2\r\n" + bulk("0") + "*" + strconv.Itoa(len(keys)) + "\r\n"

	var outSb331 strings.Builder
	for _, key := range keys {
		outSb331.WriteString(bulk(key))
	}

	out += outSb331.String()

	return out
}

// eval runs, natively, the three scripts of the write side: they are told apart by what they call.
func (s *stubServer) eval(args []string) string {
	count, _ := strconv.Atoi(args[2])
	keys, argv := args[3:3+count], args[3+count:]

	switch {
	case strings.Contains(args[1], "UNLINK"):
		return s.evalCommit(keys, argv)
	case strings.Contains(args[1], "SADD"):
		return s.evalStage(keys, argv)
	}

	if s.strs[keys[0]] != argv[0] {
		return integer(0)
	}

	s.del(keys[0])
	s.del(keys[1])

	return integer(1)
}

func (s *stubServer) evalStage(keys, argv []string) string {
	if s.strs[keys[0]] != argv[0] {
		return integer(-1)
	}

	added, _ := strconv.Atoi(
		strings.TrimPrefix(strings.TrimSuffix(s.sadd(append([]string{"SADD", keys[1]}, argv[2:]...)), "\r\n"), ":"),
	)
	s.ttl[keys[1]], s.ttl[keys[0]] = argv[1], argv[1]

	return integer(added)
}

func (s *stubServer) evalCommit(keys, argv []string) string {
	want, _ := strconv.Atoi(argv[1])

	switch {
	case s.strs[keys[0]] != argv[0]:
		return integer(1)
	case want == 0:
		s.del(keys[2])

		return integer(0)
	case !s.exists(keys[1]):
		return integer(2)
	case len(s.sets[keys[1]]) != want:
		return integer(3)
	}

	if s.exists(keys[2]) {
		s.rename(keys[2], keys[3])
		s.ttl[keys[3]] = argv[2]
	}

	s.rename(keys[1], keys[2])
	delete(s.ttl, keys[2])

	return integer(0)
}
