package segmentadmin

// The scripts of the lock and of the conditional swap. Each runs on the node of one slot, with every key it touches
// declared in KEYS, so they work on a cluster. The batch of a stage and the swap are the only scripts; none of them
// walks a set.
const (
	// stageScript: KEYS lock, tmp. ARGV token, ttl in ms, members. Returns -1 when the lock is not the run's.
	stageScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
local added = 0
local first = 3
while first <= #ARGV do
  local last = math.min(first + 999, #ARGV)
  added = added + redis.call('SADD', KEYS[2], unpack(ARGV, first, last))
  first = last + 1
end
redis.call('PEXPIRE', KEYS[2], ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return added
`

	// commitScript: KEYS lock, tmp, live, old. ARGV token, want, old ttl in ms. Returns the codes below.
	commitScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 1 end
local want = tonumber(ARGV[2])
if want == 0 then
  if pcall(redis.call, 'UNLINK', KEYS[3]) then return 0 end
  redis.call('DEL', KEYS[3])
  return 4
end
if redis.call('EXISTS', KEYS[2]) == 0 then return 2 end
if redis.call('SCARD', KEYS[2]) ~= want then return 3 end
if redis.call('EXISTS', KEYS[3]) == 1 then
  redis.call('RENAME', KEYS[3], KEYS[4])
  redis.call('PEXPIRE', KEYS[4], ARGV[3])
end
redis.call('RENAME', KEYS[2], KEYS[3])
redis.call('PERSIST', KEYS[3])
return 0
`

	// unlockScript: KEYS lock, last. ARGV token. Removes both when the lock is the run's.
	unlockScript = `
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1], KEYS[2]) end
return 0
`

	codeCommitted    = 0
	codeLockLost     = 1
	codeTempGone     = 2
	codeCountOff     = 3
	codeCommittedDel = 4
)

// commitResult reads the code that commitScript returns.
func commitResult(code int64) (CommitResult, bool) {
	switch code {
	case codeLockLost:
		return LockLost, false
	case codeTempGone:
		return TempGone, false
	case codeCountOff:
		return CountMismatch, false
	case codeCommittedDel:
		return Committed, true
	}

	return Committed, false
}
