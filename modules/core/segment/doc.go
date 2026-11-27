// Package segment holds the model of the named segments that the targeting rules of a flag test an attribute against
// (targetingKey in segment("beta-users")): the definitions, the seam that answers the membership questions of a
// request, the failure policies and an in-memory provider.
//
// The membership data never lives in the flags. A Provider answers, for the attribute values of one evaluation,
// whether each is a member of its segment, is not, or whether that could not be determined. The engine turns those
// three-valued answers into the result of a rule (see flagdef) and applies the failure policy of the segment.
package segment
