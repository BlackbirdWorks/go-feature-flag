package bootstrap

const (
	// maxPort is the highest TCP port.
	maxPort = 65535

	// nullTag is the YAML tag of a null scalar.
	nullTag = "!!null"
	// jsonString is the JSON Schema type of a string.
	jsonString = "string"

	authPath = "auth"

	// YAML tags of the scalars.
	tagStr = "!!str"
	tagInt = "!!int"

	keyKafka        = "kafka"
	keyWebhookURL   = "webhookUrl"
	keyAccessKeyID  = "accessKeyId"
	keyTokenFile    = "tokenFile"
	keySecretKey    = "secretAccessKey"
	keyProjectID    = "projectId"
	keyFormat       = "format"
	defS3Connection = "S3Connection"

	keyMeta = "meta"
	keyKind = "kind"
	keyType = "type"

	keyConnection        = "connection"
	keyEndpointURL       = "endpointUrl"
	keyGoogleCredentials = "googleCredentials"
	keyRoleARN           = "roleArn"
	keySecret            = "secret"
)
