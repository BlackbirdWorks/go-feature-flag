package flagruntime

// flagObject computes the state of one flags object; it returns prev itself when nothing changed.
func (st *stage) flagObject(name string, obj *Object, prev *objState) *objState {
	state := obj.state()
	if state != ObjectOK {
		return flagKept(name, obj, state, prev)
	}

	sum := obj.checksum()
	if prev != nil && prev.state == ObjectOK && prev.checksum == sum && prev.format == obj.Format {
		return prev
	}

	key := obj.Format + "\x00" + sum
	parsed, fresh := st.parsedFlags(name, obj, sum, key)
	next := flagNext(name, obj, prev, parsed, parseKey{sum: sum, key: key, fresh: fresh})

	if next == prev {
		return prev
	}

	next.refKey = key
	next.status = flagsStatus(next)

	st.swapRef(prev, next)

	return next
}

// parsedFlags returns the parse result of an object, from the cache or by parsing it once.
func (st *stage) parsedFlags(name string, obj *Object, sum, key string) (*parsedFlags, bool) {
	parsed, cached := st.lookupFlags(key)
	if !cached {
		parsed = parseFlags(st.b.svc, obj)
		st.built.flagSet[key] = parsed

		st.emit(name, KindFlags, sum)
	}

	return parsed, !cached
}

// flagNext is the state of an object whose bytes changed. It returns prev when an undecodable object stays so.
// parseKey identifies one parse: the checksum of the bytes, the cache key (format plus checksum), and whether the
// parse was made for this object just now.
type parseKey struct {
	sum, key string
	fresh    bool
}

func flagNext(name string, obj *Object, prev *objState, parsed *parsedFlags, attempt parseKey) *objState {
	if parsed.err != nil {
		if prev != nil && prev.state == ObjectInvalid && prev.attempted == attempt.key {
			return prev
		}

		next := keepFrom(name, prev)
		next.state, next.reason, next.attempted = ObjectInvalid, parsed.err.Error(), attempt.key

		return next
	}

	if prev != nil && prev.loaded && prev.checksum == attempt.sum && prev.format == obj.Format {
		// the same content as the one held (back from stale, missing or invalid): the table is still right
		held := *prev
		held.state, held.reason, held.attempted = ObjectOK, "", ""

		return &held
	}

	table := buildTable(parsed, prev, attempt.fresh)

	return &objState{
		name: name, format: obj.Format, checksum: attempt.sum, state: ObjectOK, loaded: true,
		flags: table.flags, hashes: table.hashes, rejected: table.rejected, content: tableHash(table.hashes),
	}
}

func (st *stage) swapRef(prev, next *objState) {
	if prev != nil && prev.refKey != next.refKey {
		st.release(prev.refKey, false)
	}

	if prev == nil || prev.refKey != next.refKey {
		st.hold(next.refKey, false)
	}
}

// keepFrom returns a state that holds the last good content of prev.
func keepFrom(name string, prev *objState) *objState {
	next := &objState{name: name}

	if prev != nil && prev.loaded {
		next.flags, next.checksum, next.format = prev.flags, prev.checksum, prev.format
		next.rejected, next.hashes, next.content, next.loaded = prev.rejected, prev.hashes, prev.content, true
	}

	return next
}

// flagKept handles an object that is stale, missing or pending: the last good content stays.
func flagKept(name string, obj *Object, state ObjectState, prev *objState) *objState {
	if prev != nil && prev.state == state && prev.reason == obj.Reason {
		return prev
	}

	next := keepFrom(name, prev)
	next.state, next.reason = state, obj.Reason

	if prev != nil {
		next.refKey, next.attempted = prev.refKey, prev.attempted
	}

	if next.format == "" {
		next.format = obj.Format
	}

	next.status = flagsStatus(next)

	return next
}

func flagsStatus(held *objState) *ObjectStatus {
	return &ObjectStatus{
		Name: held.name, Kind: KindFlags, State: held.state, Checksum: held.checksum, Reason: held.reason,
		Rejected: held.rejected, Flags: len(held.flags),
	}
}

// docObject computes the state of one flag-set document object.
func (st *stage) docObject(name string, obj *Object, prev *docState) *docState {
	state := obj.state()
	if state != ObjectOK {
		return docKept(name, obj, state, prev)
	}

	sum := obj.checksum()
	if prev != nil && prev.state == ObjectOK && prev.checksum == sum {
		return prev
	}

	next := docNext(name, prev, st.parsedDoc(name, obj, sum), sum)
	if next == prev {
		return prev
	}

	next.refKey = sum
	next.status = docStatus(next)

	st.swapDocRef(prev, next)

	return next
}

// parsedDoc returns the decoded document, from the cache or by decoding it once.
func (st *stage) parsedDoc(name string, obj *Object, sum string) *parsedDoc {
	parsed, cached := st.lookupDoc(sum)
	if !cached {
		parsed = parseDoc(obj)
		st.built.docSet[sum] = parsed

		st.emit(name, KindFlagSets, sum)
	}

	return parsed
}

// docNext is the state of a document whose bytes changed.
func docNext(name string, prev *docState, parsed *parsedDoc, sum string) *docState {
	if parsed.err != nil {
		if prev != nil && prev.state == ObjectInvalid && prev.attempted == sum {
			return prev
		}

		next := keepDoc(name, prev)
		next.state, next.reason, next.attempted = ObjectInvalid, parsed.err.Error(), sum

		return next
	}

	if prev != nil && prev.parsed != nil && prev.checksum == sum {
		held := *prev
		held.state, held.reason, held.attempted = ObjectOK, "", ""

		return &held
	}

	return &docState{name: name, parsed: parsed, checksum: sum, state: ObjectOK}
}

func docKept(name string, obj *Object, state ObjectState, prev *docState) *docState {
	if prev != nil && prev.state == state && prev.reason == obj.Reason {
		return prev
	}

	next := keepDoc(name, prev)
	next.state, next.reason = state, obj.Reason

	if prev != nil {
		next.refKey, next.attempted = prev.refKey, prev.attempted
	}

	next.status = docStatus(next)

	return next
}

func (st *stage) swapDocRef(prev, next *docState) {
	if prev != nil && prev.refKey != next.refKey {
		st.release(prev.refKey, true)
	}

	if prev == nil || prev.refKey != next.refKey {
		st.hold(next.refKey, true)
	}
}

func keepDoc(name string, prev *docState) *docState {
	next := &docState{name: name}

	if prev != nil && prev.parsed != nil {
		held := *prev
		next = &held
	}

	return next
}

func docStatus(held *docState) *ObjectStatus {
	flagSets := 0
	if held.parsed != nil && held.parsed.doc != nil {
		flagSets = len(held.parsed.doc.FlagSets)
	}

	return &ObjectStatus{
		Name: held.name, Kind: KindFlagSets, State: held.state, Checksum: held.checksum, Reason: held.reason,
		Flags: flagSets,
	}
}
