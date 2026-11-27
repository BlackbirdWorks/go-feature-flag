// Package resolver wraps a [segment.Provider] with what a network store needs on the evaluation path: an
// in-process cache, coalescing of identical concurrent lookups and a circuit breaker for each segment.
//
// The [Resolver] is itself a [segment.Provider], so it fits any store. For each lookup it applies, in order: the
// normalisation of the segment, the cache (a bounded LRU with a TTL for members and a shorter one for non
// members), the breaker of the segment, and one coalesced call to the store for all the lookups that are
// neither cached nor already being asked by another caller.
//
// Only a Member and a NotMember answer is cached. Unknown and errors never are. The resolver never applies a
// failure policy: a lookup it could not answer stays Unknown and the engine applies the policy.
package resolver
