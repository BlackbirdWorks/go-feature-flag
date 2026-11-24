package testutils

const MongoFindResultString = `[{"flag":"test-flag","variations":{"true_var":true,"false_var":false},"targeting":` +
	`[{"query":"key eq \"random-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"fal` +
	`se_var"},"trackEvents":false},{"flag":"test-flag2","variations":{"true_var":true,"false_var":false},"targeting"` +
	`:[{"query":"key eq \"not-a-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"fal` +
	`se_var"},"trackEvents":false}]`

const MongoMissingFlagKey = `[{"flag":"test-flag","variations":{"true_var":true,"false_var":false},"targeting":[{` +
	`"query":"key eq \"random-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false` +
	`_var"},"trackEvents":false},{"variations":{"true_var":true,"false_var":false},"targeting":[{"query":"key eq \"n` +
	`ot-a-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false_var"},"trackEvents"` +
	`:false}]`

const MissingFlagKeyResult = `{"test-flag":{"variations":{"true_var":true,"false_var":false},"targeting":[{"query` +
	`":"key eq \"random-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false_var"}` +
	`,"trackEvents":false}}`

const MongoFindResultFlagNoStr = `[{"flag":123456,"variations":{"true_var":true,"false_var":false},"targeting":[{` +
	`"query":"key eq \"random-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false` +
	`_var"},"trackEvents":false},{"flag":"test-flag","variations":{"true_var":true,"false_var":false},"targeting":[{` +
	`"query":"key eq \"random-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false` +
	`_var"},"trackEvents":false}]`

const FlagKeyNotStringResult = MissingFlagKeyResult

const QueryResult = `{"test-flag":{"variations":{"true_var":true,"false_var":false},"targeting":[{"query":"key eq` +
	` \"random-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false_var"},"trackEv` +
	`ents":false},"test-flag2":{"variations":{"true_var":true,"false_var":false},"targeting":[{"query":"key eq \"not` +
	`-a-key\"","percentage":{"true_var":0,"false_var":100}}],"defaultRule":{"variation":"false_var"},"trackEvents":f` +
	`alse}}`
