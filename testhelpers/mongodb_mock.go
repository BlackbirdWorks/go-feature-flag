package testhelpers

// MongoFindResultString is a MongoDB find result with two flags.
const MongoFindResultString = `[{"flag":"test-flag","variations":{"true_var":true,"false_var":false},"targeting":` +
	`[{"query":"key eq \"random-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"fal` +
	`se_var"},"trackEvents":false},{"flag":"test-flag2","variations":{"true_var":true,"false_var":false},"targeting"` +
	`:[{"query":"key eq \"not-a-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"fal` +
	`se_var"},"trackEvents":false}]`

// MongoMissingFlagKey is a MongoDB find result in which one flag has no key.
const MongoMissingFlagKey = `[{"flag":"test-flag","variations":{"true_var":true,"false_var":false},"targeting":[{` +
	`"query":"key eq \"random-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false` +
	`_var"},"trackEvents":false},{"variations":{"true_var":true,"false_var":false},"targeting":[{"query":"key eq \"n` +
	`ot-a-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false_var"},"trackEvents"` +
	`:false}]`

// MissingFlagKeyResult is the expected flag configuration for MongoMissingFlagKey.
const MissingFlagKeyResult = `{"test-flag":{"variations":{"true_var":true,"false_var":false},"targeting":[{"query` +
	`":"key eq \"random-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false_var"}` +
	`,"trackEvents":false}}`

// MongoFindResultFlagNoStr is a MongoDB find result in which a flag key is not a string.
const MongoFindResultFlagNoStr = `[{"flag":123456,"variations":{"true_var":true,"false_var":false},"targeting":[{` +
	`"query":"key eq \"random-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false` +
	`_var"},"trackEvents":false},{"flag":"test-flag","variations":{"true_var":true,"false_var":false},"targeting":[{` +
	`"query":"key eq \"random-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false` +
	`_var"},"trackEvents":false}]`

// FlagKeyNotStringResult is the expected flag configuration for MongoFindResultFlagNoStr.
const FlagKeyNotStringResult = MissingFlagKeyResult

// QueryResult is the expected flag configuration of the MongoDB query tests.
const QueryResult = `{"test-flag":{"variations":{"true_var":true,"false_var":false},"targeting":[{"query":"key eq` +
	` \"random-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false_var"},"trackEv` +
	`ents":false},"test-flag2":{"variations":{"true_var":true,"false_var":false},"targeting":[{"query":"key eq \"not` +
	`-a-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false_var"},"trackEvents":false}}`
