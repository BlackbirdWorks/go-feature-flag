package valkeysegment_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/valkey-io/valkey-go"
)

const (
	replyOK    = "+OK\r\n"
	protoMax   = 1 << 20
	maxEntered = 1024
)

// stubServer is a loopback server that speaks enough RESP for the provider: PING, SMISMEMBER and EXISTS over a
// fake keyspace, and the handshake of valkey-go. It keeps its listener open from the start, counts the commands it
// served, and can fail, hang or answer wrongly on request.
type stubServer struct {
	ln       net.Listener
	sets     map[string]map[string]bool
	values   map[string]string   // string keys: the version keys
	sorted   map[string][]string // the sorted members of a set, cached for SSCAN, dropped when the set is written
	keys     map[string]bool
	counts   map[string]int
	failSeg  map[string]bool
	conns    map[net.Conn]struct{}
	gate     chan struct{}
	between  func(name string) // see OnCommand
	entered  chan struct{}
	only     string   // when set, garbage applies to this command only
	garbage  string   // the reply to SMISMEMBER and EXISTS when set: bytes written as they are
	pong     string   // the reply to PING when set
	noCmd    string   // the name of a command that the server refuses with an error reply
	seq      []string // replies to the next data commands, in order, before anything else
	wg       sync.WaitGroup
	mu       sync.Mutex
	writes   atomic.Int64 // writes of the client side connections, see clientOption
	pageSize int          // the most members of an SSCAN page, zero means the COUNT of the command
	allow    int          // data commands still answered before the connection is cut, when cutting
	stopOnce sync.Once
	failed   bool
	cutting  bool // see CutAfter
}

// txState is the MULTI block of one connection.
type txState struct {
	queued [][]string
	active bool
}

// OnCommand makes the server call fn after it served a data command outside a transaction, and when a data command
// is queued inside one (before EXEC runs them): the test removes a key there, between the two commands of a lookup.
func (s *stubServer) OnCommand(fn func(name string)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.between = fn
}

// CutAfter makes the server answer n more data commands completely and then drop the connection at the next one.
func (s *stubServer) CutAfter(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cutting, s.allow = true, n
}

// Fail makes the server drop the connection at every command until Heal.
func (s *stubServer) Fail() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failed = true
}

// FailSegment answers WRONGTYPE for the keys that hold the name.
func (s *stubServer) FailSegment(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failSeg[name] = true
}

// Hang holds every command until Release. The channel gets a value for each command that waits.
func (s *stubServer) Hang() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.gate, s.entered = make(chan struct{}), make(chan struct{}, maxEntered)

	return s.entered
}

// Release lets the held commands go on and stops holding.
func (s *stubServer) Release() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.gate != nil {
		close(s.gate)

		s.gate = nil
	}
}

// Heal undoes Fail and FailSegment.
func (s *stubServer) Heal() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failed = false
	s.failSeg = map[string]bool{}
}

// refuse makes the server answer an error reply to the command.
func (s *stubServer) refuse(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.noCmd = name
}

// answer makes the server reply to SMISMEMBER and EXISTS with these bytes.
func (s *stubServer) answer(raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.garbage = raw
}

// answerSequence makes the server reply to the next data commands with these bytes, one for each, in order.
func (s *stubServer) answerSequence(raw ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.seq = raw
}

// answerOnly is answer for one command.
func (s *stubServer) answerOnly(name, raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.only, s.garbage = name, raw
}

// pongWith makes the server reply to PING with these bytes.
func (s *stubServer) pongWith(raw string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.pong = raw
}

// cutNow tells whether this command is the one that the connection is cut at.
func (s *stubServer) cutNow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.cutting {
		return false
	}

	if s.allow == 0 {
		return true
	}

	s.allow--

	return false
}

func (s *stubServer) addr() string { return s.ln.Addr().String() }

func (s *stubServer) stop() {
	s.stopOnce.Do(func() {
		_ = s.ln.Close()

		s.mu.Lock()

		if s.gate != nil {
			close(s.gate)

			s.gate = nil
		}

		for conn := range s.conns {
			_ = conn.Close()
		}

		s.mu.Unlock()
		s.wg.Wait()
	})
}

func (s *stubServer) accept() {
	defer s.wg.Done()

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

// command reads one RESP array of bulk strings.
func command(reader *bufio.Reader) ([]string, error) {
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
	block := &txState{}

	for {
		args, err := command(reader)
		if err != nil || len(args) == 0 {
			return
		}

		reply, keep := s.handle(conn, block, args)
		if !keep {
			_ = writer.Flush() // the replies of the commands before the cut were complete

			return
		}

		_, _ = writer.WriteString(reply)

		if reader.Buffered() == 0 {
			if writer.Flush() != nil {
				return
			}
		}
	}
}

func bulkInts(vals []int) string {
	var out strings.Builder

	out.WriteString("*" + strconv.Itoa(len(vals)) + "\r\n")

	for _, val := range vals {
		out.WriteString(":" + strconv.Itoa(val) + "\r\n")
	}

	return out.String()
}

func boolInt(flag bool) int {
	if flag {
		return 1
	}

	return 0
}

// hold tells the test that a command arrived and waits while the server is hung.
func hold(entered chan<- struct{}, gate <-chan struct{}) {
	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}

	if gate != nil {
		<-gate
	}
}

// handshake answers the commands that valkey-go sends when it opens a connection. CLIENT is counted by its
// sub-command, so a test can see that client-side caching (CLIENT TRACKING) is off.
func (s *stubServer) handshake(name string, args []string) (string, bool) {
	switch name {
	case "HELLO":
		return "%1\r\n+server\r\n+valkey\r\n", true
	case "CLIENT", "SELECT":
		if name == "CLIENT" && len(args) > 1 {
			s.mu.Lock()
			s.counts["client "+strings.ToLower(args[1])]++
			s.mu.Unlock()
		}

		return replyOK, true
	case "CLUSTER":
		return "-ERR This instance has cluster support disabled\r\n", true // a standalone store
	}

	return "", false
}

// handle returns the reply to a command and false when the connection must be dropped.
func (s *stubServer) handle(conn net.Conn, block *txState, args []string) (string, bool) {
	name := strings.ToUpper(args[0])

	reply, handled := s.handshake(name, args)
	if handled {
		return reply, true
	}

	s.mu.Lock()
	s.counts[strings.ToLower(name)]++
	gate, entered, failed, garbage, noCmd, pong := s.gate, s.entered, s.failed, s.garbage, s.noCmd, s.pong
	only, between := s.only, s.between
	next := ""

	if len(s.seq) > 0 {
		next, s.seq = s.seq[0], s.seq[1:]
	}
	s.mu.Unlock()

	hold(entered, gate)

	if failed {
		_ = conn.Close()

		return "", false
	}

	if next != "" {
		return next, true
	}

	if s.cutNow() {
		return "", false // serve flushes the replies before this command, then closes the connection
	}

	if strings.EqualFold(noCmd, name) {
		return "-ERR unknown command '" + name + "'\r\n", true
	}

	if inTx, done := s.transactionReply(block, name, args, garbage, only, between); done {
		return inTx, true
	}

	switch name {
	case "PING":
		if pong != "" {
			return pong, true
		}

		return "+PONG\r\n", true
	case cmdSMISMEMBER, cmdEXISTS, "SSCAN", "GET":
		data := s.readData(name, args, garbage, only)

		if between != nil {
			between(name)
		}

		return data, true
	}

	return "-ERR unknown command\r\n", true
}

// transactionReply answers MULTI, EXEC and the data commands queued between them.
func (s *stubServer) transactionReply(
	block *txState, name string, args []string, garbage, only string, between func(string),
) (string, bool) {
	switch {
	case name == "MULTI":
		block.active, block.queued = true, nil

		return replyOK, true
	case name == cmdEXEC:
		queued := block.queued
		block.active, block.queued = false, nil

		if garbage != "" && strings.EqualFold(only, cmdEXEC) {
			return garbage, true
		}

		return s.transact(queued, garbage, only), true
	case block.active && (name == cmdSMISMEMBER || name == cmdEXISTS):
		block.queued = append(block.queued, args)

		if between != nil {
			between(name)
		}

		return "+QUEUED\r\n", true
	}

	return "", false
}

func (s *stubServer) wrongType(key string) bool {
	for seg := range s.failSeg {
		if strings.Contains(key, seg) {
			return true
		}
	}

	return false
}

func (s *stubServer) read(name string, args []string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.wrongType(args[0]) {
		return "-WRONGTYPE Operation against a key holding the wrong kind of value\r\n"
	}

	set, isSet := s.sets[args[0]]

	if name == "EXISTS" {
		return ":" + strconv.Itoa(boolInt(isSet || s.keys[args[0]])) + "\r\n"
	}

	vals := make([]int, 0, len(args)-1)
	for _, id := range args[1:] {
		vals = append(vals, boolInt(set[id]))
	}

	return bulkInts(vals)
}

// count returns how many times the server served the command.
func (s *stubServer) count(name string) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.counts[name]
}

// setMembers stores a set, and one key for each id of keysPrefix when it is not empty.
func (s *stubServer) setMembers(key string, ids ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sets[key] = map[string]bool{}
	delete(s.sorted, key)

	for _, id := range ids {
		s.sets[key][id] = true
	}
}

func (s *stubServer) setKey(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.keys[key] = true
}

// countingConn counts the writes of the client to the server: one write is one flush of a pipeline.
type countingConn struct {
	net.Conn

	writes *atomic.Int64
}

func (c *countingConn) Write(buf []byte) (int, error) {
	c.writes.Add(1)

	return c.Conn.Write(buf)
}

// clientOption is the valkey-go options for the server, counting the writes of every connection.
func (s *stubServer) clientOption() *valkey.ClientOption {
	return &valkey.ClientOption{
		InitAddress:  []string{s.addr()},
		DisableCache: true,
		DisableRetry: true,
		// one connection: the setup writes of the lazily opened connections of a multiplex would count as calls
		PipelineMultiplex: -1,
		DialCtxFn: func(ctx context.Context, addr string, dialer *net.Dialer, _ *tls.Config) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, "tcp", addr)
			if err != nil {
				return nil, err
			}

			return &countingConn{Conn: conn, writes: &s.writes}, nil
		},
	}
}

// get answers GET from the string keys: a bulk string, or nil for a missing key.
func (s *stubServer) get(key string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	val, ok := s.values[key]
	if !ok {
		return "$-1\r\n"
	}

	return "$" + strconv.Itoa(len(val)) + "\r\n" + val + "\r\n"
}

// sscan answers one SSCAN page over the sorted members of a set: the cursor is an index.
func (s *stubServer) sscan(args []string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	members, ok := s.sorted[args[0]]
	if !ok {
		members = make([]string, 0, len(s.sets[args[0]]))
		for member := range s.sets[args[0]] {
			members = append(members, member)
		}

		slices.Sort(members)

		s.sorted[args[0]] = members
	}

	page := s.pageSize
	if page == 0 {
		page, _ = strconv.Atoi(args[3]) // SSCAN key cursor COUNT n
	}

	start, _ := strconv.Atoi(args[1])
	end := min(start+page, len(members))
	next := "0"

	if end < len(members) {
		next = strconv.Itoa(end)
	}

	var out strings.Builder

	out.WriteString("*2\r\n$" + strconv.Itoa(len(next)) + "\r\n" + next + "\r\n*" + strconv.Itoa(end-start) + "\r\n")

	for _, member := range members[start:end] {
		out.WriteString("$" + strconv.Itoa(len(member)) + "\r\n" + member + "\r\n")
	}

	return out.String()
}

// seed writes a set (none when ids is empty, as in a real server) and bumps the version key of the segment.
func (s *stubServer) seed(name string, ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := "seg:{" + name + "}"
	delete(s.sets, key)
	delete(s.sorted, key)

	if len(ids) > 0 {
		s.sets[key] = map[string]bool{}
		for _, id := range ids {
			s.sets[key][id] = true
		}

		sorted := slices.Clone(ids)
		slices.Sort(sorted)
		s.sorted[key] = slices.Compact(sorted) // sorted ahead of the first page, which has a short deadline
	}

	s.values["seg:"+name+":version"] = "v" + strconv.Itoa(len(s.counts)+int(s.writes.Add(1)))
}

// readData answers SSCAN and GET, or the garbage when it is aimed at the command.
func (s *stubServer) readData(name string, args []string, garbage, only string) string {
	switch {
	case garbage != "" && (only == "" || strings.EqualFold(only, name)):
		return garbage
	case name == nameGet:
		return s.get(args[1])
	case name == "SSCAN":
		return s.sscan(args[1:])
	default:
		return s.read(name, args[1:])
	}
}

// removeSet deletes a set, as an expiry does.
func (s *stubServer) removeSet(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sets, key)
	delete(s.sorted, key)
}

// transact runs the queued commands of EXEC and returns the array of their replies.
func (s *stubServer) transact(queued [][]string, garbage, only string) string {
	var out strings.Builder

	out.WriteString("*" + strconv.Itoa(len(queued)) + "\r\n")

	for _, args := range queued {
		out.WriteString(s.readData(strings.ToUpper(args[0]), args, garbage, only))
	}

	return out.String()
}

func newStubServer(tb testing.TB) *stubServer {
	tb.Helper()

	lc := net.ListenConfig{}
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	require.NoError(tb, err)

	srv := &stubServer{
		ln:      ln,
		sets:    map[string]map[string]bool{},
		values:  map[string]string{},
		sorted:  map[string][]string{},
		keys:    map[string]bool{},
		counts:  map[string]int{},
		failSeg: map[string]bool{},
		conns:   map[net.Conn]struct{}{},
	}

	srv.wg.Add(1)

	go srv.accept()

	tb.Cleanup(srv.stop)

	return srv
}
