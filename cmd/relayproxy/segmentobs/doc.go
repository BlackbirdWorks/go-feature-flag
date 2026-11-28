// Package segmentobs observes the resolvers of the dynamic segments for Prometheus.
//
// The default series carry the connection (a name of the bootstrap file, a handful) and bounded enums (result, reason),
// never a flag set, a segment, a member or a key. The series per segment name are opt-in and capped: the names beyond
// the cap share the series of one bucket, [OtherSegment].
//
// The observer of a connection counts with atomics only: no lock and no allocation on the evaluation path. The
// per-segment series take a read lock once for each segment of a call, and only when they are enabled.
package segmentobs
