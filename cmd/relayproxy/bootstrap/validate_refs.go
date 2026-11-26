package bootstrap

import (
	"reflect"
	"strconv"
	"strings"
)

// commonExporterKeys returns the keys that are valid for every exporter kind.
func commonExporterKeys() []string {
	return []string{"kind", "meta", "flushInterval", "maxEventInMemory", "eventType"}
}

type exporterSpec struct {
	connType string
	allowed  []string
}

// exporterKind returns the keys of an exporter kind and the connection type it needs.
func exporterKind(kind string) (exporterSpec, bool) {
	formats := []string{"filename", "csvTemplate", "format", "parquetCompressionCodec", "path"}

	switch kind {
	case ExporterFile:
		return exporterSpec{allowed: append([]string{"outputDir"}, formats[:4]...)}, true
	case ExporterWebhook:
		return exporterSpec{allowed: []string{keyEndpointURL, keySecret, "headers"}}, true
	case ExporterLog:
		return exporterSpec{allowed: []string{"logFormat"}}, true
	case ExporterS3:
		return exporterSpec{connType: TypeS3, allowed: append([]string{keyConnection}, formats...)}, true
	case ExporterKinesis:
		return exporterSpec{connType: TypeKinesis, allowed: []string{keyConnection, "streamArn", "streamName"}}, true
	case ExporterGoogleStorage:
		return exporterSpec{allowed: append([]string{"bucket", keyGoogleCredentials}, formats...)}, true
	case ExporterSQS:
		return exporterSpec{connType: TypeSQS, allowed: []string{keyConnection, "queueUrl"}}, true
	default:
		return exporterKindCloud(kind)
	}
}

// exporterKindCloud is the second half of [exporterKind].
func exporterKindCloud(kind string) (exporterSpec, bool) {
	formats := []string{"filename", "csvTemplate", "format", "parquetCompressionCodec", "path"}

	switch kind {
	case ExporterKafka:
		return exporterSpec{allowed: []string{keyKafka}}, true
	case ExporterPubSub:
		return exporterSpec{allowed: []string{keyProjectID, "topic", keyGoogleCredentials}}, true
	case ExporterBigQuery:
		return exporterSpec{allowed: []string{keyProjectID, "datasetId", "tableName", keyGoogleCredentials}}, true
	case ExporterRedshift:
		return exporterSpec{connType: TypeRedshift, allowed: []string{keyConnection, "database", "clusterIdentifier",
			"workgroupName", "dbUser", "secretArn", "tableName", "autoMigrate"}}, true
	case ExporterAzure:
		return exporterSpec{allowed: append([]string{"accountName", "accountKey", "container"}, formats...)}, true
	case ExporterOTel:
		return exporterSpec{allowed: []string{"tracerName"}}, true
	}

	return exporterSpec{}, false
}

// connectionRef checks a reference to a connection by name and returns the connection.
func (v *validator) connectionRef(path, name, wantType string) (Connection, bool) {
	if name == "" {
		v.failf(path, ErrMissingValue, "name a connection of type %s", wantType)

		return Connection{}, false
	}

	conn, ok := v.cfg.Connections[name]
	if !ok {
		v.failf(path, ErrDanglingReference, "no connection is named %q", name)

		return Connection{}, false
	}

	if conn.Type() != wantType {
		v.failf(path, ErrWrongConnectionType, "connection %q is not of type %s", name, wantType)

		return Connection{}, false
	}

	return conn, true
}

func (v *validator) exporter(path string, exp *Exporter) {
	spec, ok := exporterKind(exp.Kind)
	if !ok {
		if exp.Kind == "" {
			v.fail(path+".kind", ErrMissingValue)
		} else {
			v.failf(path+".kind", ErrUnknownType, "%q", exp.Kind)
		}

		return
	}

	v.allowed(path, reflect.ValueOf(*exp), append(commonExporterKeys(), spec.allowed...), nil)

	v.headers(path, exp.Headers)

	for _, field := range []struct {
		name   string
		secret Secret
	}{
		{keyEndpointURL, exp.EndpointURL}, {keySecret, exp.Secret},
		{keyGoogleCredentials, exp.GoogleCredentials}, {"accountKey", exp.AccountKey},
	} {
		v.secret(path+"."+field.name, field.secret)
	}

	v.httpURL(path+".queueUrl", exp.QueueURL)

	if exp.MaxEventInMemory != nil && *exp.MaxEventInMemory < 1 {
		v.failf(path+".maxEventInMemory", ErrInvalidValue, "expected an integer of 1 or more")
	}

	if exp.Kafka != nil {
		for i, address := range exp.Kafka.Addresses {
			v.hostPort(path+".kafka.addresses["+strconv.Itoa(i)+"]", address)
		}
	}

	var conn Connection

	if spec.connType != "" {
		conn, _ = v.connectionRef(path+".connection", exp.Connection, spec.connType)
	}

	v.exporterRequired(path, exp, conn)

	if exp.Format != "" {
		v.oneOf(path+".format", exp.Format, "json", "csv", "parquet")
	}
}

func (v *validator) require(path string, set bool, what string) {
	if !set {
		v.failf(path, ErrMissingValue, "%s", what)
	}
}

func (v *validator) exporterRequired(path string, exp *Exporter, conn Connection) {
	switch exp.Kind {
	case ExporterS3:
		if conn.S3 != nil {
			v.require(path+".connection", conn.S3.Bucket != "", "the connection sets no bucket")
		}
	case ExporterKinesis:
		stream := exp.StreamARN != "" || exp.StreamName != "" || (conn.Kinesis != nil && conn.Kinesis.Stream != "")
		v.require(path+".streamName", stream, "set streamName or streamArn here or stream on the connection")
	case ExporterGoogleStorage:
		v.require(path+".bucket", exp.Bucket != "", "")
	case ExporterSQS:
		queue := exp.QueueURL != "" || (conn.SQS != nil && conn.SQS.QueueURL != "")
		v.require(path+".queueUrl", queue, "set it here or on the connection")
	default:
		v.exporterRequiredCloud(path, exp)
	}
}

// exporterRequiredCloud is the second half of [validator.exporterRequired].
func (v *validator) exporterRequiredCloud(path string, exp *Exporter) {
	switch exp.Kind {
	case ExporterFile:
		v.require(path+".outputDir", exp.OutputDir != "", "")
	case ExporterWebhook:
		v.require(path+".endpointUrl", exp.EndpointURL.IsSet(), "")
	case ExporterKafka:
		v.require(path+".kafka", exp.Kafka != nil && exp.Kafka.Topic != "" && len(exp.Kafka.Addresses) > 0,
			"topic and addresses are required")
	case ExporterPubSub:
		v.require(path+".projectId", exp.ProjectID != "", "")
		v.require(path+".topic", exp.Topic != "", "")
	case ExporterBigQuery:
		v.require(path+".projectId", exp.ProjectID != "", "")
		v.require(path+".datasetId", exp.DatasetID != "", "")
	case ExporterRedshift:
		v.redshift(path, exp)
	case ExporterAzure:
		v.require(path+".container", exp.Container != "", "")
		v.require(path+".accountName", exp.AccountName != "", "")
	}
}

func (v *validator) redshift(path string, exp *Exporter) {
	v.require(path+".database", exp.Database != "", "")

	if (exp.ClusterIdentifier == "") == (exp.WorkgroupName == "") {
		v.failf(path, ErrInvalidValue, "exactly one of clusterIdentifier or workgroupName is required")
	}

	if exp.DBUser != "" && exp.WorkgroupName != "" {
		v.failf(path+".dbUser", ErrNotAllowed, "cannot be used with workgroupName")
	}

	if exp.DBUser != "" && exp.SecretARN != "" {
		v.failf(path+".dbUser", ErrNotAllowed, "dbUser and secretArn are mutually exclusive")
	}

	if exp.TableName != "" && !tableNameRE.MatchString(exp.TableName) {
		v.failf(path+".tableName", ErrInvalidValue, "not a valid table name")
	}
}

// headers checks the secrets of a header map, in the order of the names.
func (v *validator) headers(path string, headers map[string]Secret) {
	for _, name := range sortedKeys(headers) {
		v.secret(path+".headers."+name, headers[name])
	}
}

func (v *validator) notifier(path string, notif *Notifier) {
	var allowed, required []string

	switch notif.Kind {
	case NotifierSlack, NotifierMicrosoftTeams, NotifierDiscord:
		allowed, required = []string{keyWebhookURL}, []string{keyWebhookURL}
	case NotifierWebhook:
		allowed, required = []string{keyEndpointURL, keySecret, "headers"}, []string{keyEndpointURL}
	case "":
		v.fail(path+".kind", ErrMissingValue)

		return
	default:
		v.failf(path+".kind", ErrUnknownType, "%q", notif.Kind)

		return
	}

	v.allowed(path, reflect.ValueOf(*notif), append([]string{"kind", "meta"}, allowed...), required)

	v.headers(path, notif.Headers)

	v.secret(path+".webhookUrl", notif.WebhookURL)
	v.secret(path+".endpointUrl", notif.EndpointURL)
	v.secret(path+".secret", notif.Secret)
}

func (v *validator) controlPlane() {
	plane := v.cfg.ControlPlane
	if plane == nil {
		return
	}

	if conn, ok := v.connectionRef("controlPlane.connection", plane.Connection, TypeS3); ok && conn.S3.Bucket == "" {
		v.failf("controlPlane.connection", ErrMissingValue, "connection %q sets no bucket", plane.Connection)
	}

	manifest := plane.Manifest
	v.require("controlPlane.manifest.key", strings.TrimSpace(manifest.Key) != "", "")

	if strings.TrimSpace(manifest.Channel) == "" {
		v.failf("controlPlane.manifest.channel", ErrInvalidValue, "expected a channel name")
	}

	v.jitter("controlPlane.manifest.jitter", *manifest.Jitter)

	if *manifest.MaxObjects < 1 {
		v.failf("controlPlane.manifest.maxObjects", ErrInvalidValue, "expected a positive integer")
	}
}
