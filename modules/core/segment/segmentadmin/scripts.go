package segmentadmin

// The scripts of the lock and of the conditional swap. Each runs on the node of one slot, with every key it touches
// declared in KEYS, so they work on a cluster. The batch of a stage and the swap are the only scripts; none of them
// walks a set.
const (
	// lockScript: KEYS lock. ARGV token, ttl in ms. Takes the lock when it is free, and again when it holds the token
	// (a call sent twice). Returns 1 when the lock is the run's, else 0.
	lockScript = `-- lock
local current = redis.call('GET', KEYS[1])
if current == false then
  redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
  return 1
end
if current == ARGV[1] then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
  return 1
end
return 0
`

	// claimScript: KEYS lock, last, stale. ARGV token, tmp, stale ("1" when KEYS[3] is a key to remove). Does nothing
	// and returns 0 unless the lock holds the token.
	claimScript = `-- claim
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 0 end
if ARGV[3] == '1' and not pcall(redis.call, 'UNLINK', KEYS[3]) then redis.call('DEL', KEYS[3]) end
redis.call('SET', KEYS[2], ARGV[2])
return 1
`

	// stageScript: KEYS lock, tmp. ARGV token, ttl in ms, first ("1" for the first batch), members. Returns -1 when the
	// lock is not the run's, -2 when a batch after the first finds no temporary key, else the members that were new.
	stageScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return -1 end
if ARGV[3] ~= '1' and redis.call('EXISTS', KEYS[2]) == 0 then return -2 end
local added = 0
local first = 4
while first <= #ARGV do
  local last = math.min(first + 999, #ARGV)
  added = added + redis.call('SADD', KEYS[2], unpack(ARGV, first, last))
  first = last + 1
end
redis.call('PEXPIRE', KEYS[2], ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[2])
return added
`

	// commitScript: KEYS lock, tmp, live, old, committed, source. ARGV token, want, ttl in ms, empty ("1" for the
	// explicit empty import), source (the fingerprint of the input, empty for an input that has none). Returns the
	// codes below. A run that was swapped in once is answered 5 when it asks again. The swap, the mark of the run and
	// the record of the source are one step: a source fingerprint is stored exactly when the set it describes is live,
	// and a swap without one removes the old fingerprint, which no longer describes the set.
	commitScript = `
local function record()
  redis.call('SET', KEYS[5], ARGV[1], 'PX', ARGV[3])
  if ARGV[5] == '' then return redis.call('DEL', KEYS[6]) end
  return redis.call('SET', KEYS[6], ARGV[5])
end
if redis.call('GET', KEYS[5]) == ARGV[1] then return 5 end
if redis.call('GET', KEYS[1]) ~= ARGV[1] then return 1 end
local want = tonumber(ARGV[2])
if ARGV[4] == '1' then
  local code = 0
  if not pcall(redis.call, 'UNLINK', KEYS[3]) then
    redis.call('DEL', KEYS[3])
    code = 4
  end
  record()
  return code
end
if redis.call('EXISTS', KEYS[2]) == 0 then return 2 end
if redis.call('SCARD', KEYS[2]) < want then return 3 end
if redis.call('EXISTS', KEYS[3]) == 1 then
  redis.call('RENAME', KEYS[3], KEYS[4])
  redis.call('PEXPIRE', KEYS[4], ARGV[3])
end
redis.call('RENAME', KEYS[2], KEYS[3])
redis.call('PERSIST', KEYS[3])
record()
return 0
`

	// editScript: KEYS lock, key. ARGV "add" or "rem", members. Returns -1 when an import holds the lock.
	editScript = `
if redis.call('EXISTS', KEYS[1]) == 1 then return -1 end
local changed = 0
local first = 2
while first <= #ARGV do
  local last = math.min(first + 999, #ARGV)
  if ARGV[1] == 'add' then
    changed = changed + redis.call('SADD', KEYS[2], unpack(ARGV, first, last))
  else
    changed = changed + redis.call('SREM', KEYS[2], unpack(ARGV, first, last))
  end
  first = last + 1
end
return changed
`

	// unlockScript: KEYS lock, last, committed. ARGV token. Removes the lock and the last key when the lock is the
	// run's, and the committed key when it is.
	unlockScript = `
local removed = 0
if redis.call('GET', KEYS[1]) == ARGV[1] then removed = redis.call('DEL', KEYS[1], KEYS[2]) end
if redis.call('GET', KEYS[3]) == ARGV[1] then removed = removed + redis.call('DEL', KEYS[3]) end
return removed
`

	codeCommitted    = 0
	codeLockLost     = 1
	codeTempGone     = 2
	codeCountOff     = 3
	codeAlready      = 5
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
	case codeAlready:
		return AlreadyCommitted, false
	case codeCommittedDel:
		return Committed, true
	}

	return Committed, false
}
