package bigqueryexporter

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"text/template"

	"cloud.google.com/go/bigquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"

	"github.com/blackbirdworks/semaphore/exporter"
)

const (
	amount                   = "amount"
	bad                      = "bad"
	checkout                 = "checkout"
	country                  = "country"
	fakeDataset              = "fake-dataset"
	fakeDatasetFeatureTable  = "fake-dataset.feature-table"
	fakeDatasetTrackingTable = "fake-dataset.tracking-table"
	fakeProject              = "fake-project"
	feature                  = "feature"
	featureTable             = "feature-table"
	flagKey                  = "flag-key"
	putRowsFailed            = "put rows failed"
	sERVER                   = "SERVER"
	tracking                 = "tracking"
	trackingTable            = "tracking-table"
	unsupportedTypeFunc      = "unsupported type: func"
	user                     = "user"
	userKey                  = "user-key"
	variationA               = "variation-a"
)

var (
	errClientInitFailed  = errors.New("client init failed")
	errEnsureTableFailed = errors.New("ensure table failed")
	errPutRowsFailed     = errors.New(putRowsFailed)
	errBoom              = errors.New("boom")
)

func TestExporter_IsBulk(t *testing.T) {
	t.Parallel()

	e := &Exporter{}

	assert.True(t, e.IsBulk(), "BigQuery exporter is a bulk one")
}

func TestExporter_ExportFeatureEvents(t *testing.T) {
	t.Parallel()

	ctx := context.TODO()
	client := newMockBigQueryClient()
	exp := &Exporter{
		ProjectID:     fakeProject,
		DatasetID:     fakeDataset,
		TableName:     featureTable,
		newClientFunc: mockNewClientFunc(client),
	}
	events := []exporter.ExportableEvent{
		exporter.FeatureEvent{
			Kind:         feature,
			ContextKind:  user,
			UserKey:      userKey,
			CreationDate: 1617970547,
			Key:          flagKey,
			Variation:    variationA,
			Value:        map[string]any{"enabled": true},
			Default:      false,
			Version:      "v1",
			Source:       sERVER,
			Metadata:     map[string]any{"provider": "go"},
		},
	}

	err := exp.Export(ctx, nil, events)

	require.NoError(t, err)
	assert.Empty(t, client.ensureTableCalls)
	assert.Len(t, client.inserters, 1)
	assert.Len(t, client.inserters[fakeDatasetFeatureTable].puts, 1)

	rows, ok := client.inserters[fakeDatasetFeatureTable].puts[0].([]rowSaver)
	require.True(t, ok)
	assert.Len(t, rows, 1)
	assert.Equal(t, map[string]bigquery.Value{
		"kind":          feature,
		"context_kind":  user,
		"user_key":      userKey,
		"creation_date": int64(1617970547),
		"key":           flagKey,
		"variation":     variationA,
		"value":         `{"enabled":true}`,
		"default":       false,
		"version":       "v1",
		"source":        sERVER,
		"metadata":      `{"provider":"go"}`,
	}, rows[0].values)
	assert.Equal(t, "flag-key|user-key|1617970547", rows[0].insertID)
}

func TestExporter_ExportTrackingEvents(t *testing.T) {
	t.Parallel()

	ctx := context.TODO()
	client := newMockBigQueryClient()
	exp := &Exporter{
		ProjectID:     fakeProject,
		DatasetID:     fakeDataset,
		TableName:     trackingTable,
		newClientFunc: mockNewClientFunc(client),
	}
	events := []exporter.ExportableEvent{
		exporter.TrackingEvent{
			Kind:              tracking,
			ContextKind:       user,
			UserKey:           userKey,
			CreationDate:      1617970547,
			Key:               checkout,
			TrackingDetails:   map[string]any{amount: 42},
			EvaluationContext: map[string]any{country: "FR"},
		},
	}

	err := exp.Export(ctx, nil, events)

	require.NoError(t, err)
	assert.Empty(t, client.ensureTableCalls)
	assert.Len(t, client.inserters, 1)
	assert.Len(t, client.inserters[fakeDatasetTrackingTable].puts, 1)

	rows, ok := client.inserters[fakeDatasetTrackingTable].puts[0].([]rowSaver)
	require.True(t, ok)
	assert.Len(t, rows, 1)
	assert.Equal(t, map[string]bigquery.Value{
		"kind":               tracking,
		"context_kind":       user,
		"user_key":           userKey,
		"creation_date":      int64(1617970547),
		"key":                checkout,
		"tracking_details":   `{"amount":42}`,
		"evaluation_context": `{"country":"FR"}`,
	}, rows[0].values)
	assert.Equal(t, "checkout|user-key|1617970547", rows[0].insertID)
}

func TestExporter_ExportMixedBatchReturnsError(t *testing.T) {
	t.Parallel()

	ctx := context.TODO()
	client := newMockBigQueryClient()
	exp := &Exporter{
		ProjectID:     fakeProject,
		DatasetID:     fakeDataset,
		TableName:     "events-table",
		newClientFunc: mockNewClientFunc(client),
	}
	events := []exporter.ExportableEvent{
		exporter.FeatureEvent{
			Kind: feature, ContextKind: user, UserKey: userKey, CreationDate: 1617970547, Key: flagKey,
			Variation: variationA, Value: true, Default: false, Version: "v1", Source: sERVER,
		},
		exporter.TrackingEvent{
			Kind: tracking, ContextKind: user, UserKey: userKey, CreationDate: 1617970548, Key: checkout,
			TrackingDetails: map[string]any{amount: 42}, EvaluationContext: map[string]any{country: "FR"},
		},
		exporter.FeatureEvent{
			Kind: feature, ContextKind: user, UserKey: "user-key-2", CreationDate: 1617970549, Key: "flag-key-2",
			Variation: "variation-b", Value: false, Default: true, Version: "v2", Source: sERVER,
		},
	}

	err := exp.Export(ctx, nil, events)

	require.ErrorContains(t, err, "mixed event types")
	assert.Empty(t, client.inserters)
}

func TestExporter_AutoMigrate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		tableName            string
		events               []exporter.ExportableEvent
		wantEnsureTableCalls []ensureTableCall
		autoMigrate          bool
	}{
		{
			name:        "AutoMigrate true ensures feature table",
			autoMigrate: true,
			tableName:   featureTable,
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{Kind: feature, Value: true},
			},
			wantEnsureTableCalls: []ensureTableCall{
				{dataset: fakeDataset, table: featureTable, schema: featureEventSchema()},
			},
		},
		{
			name:        "AutoMigrate true ensures tracking table",
			autoMigrate: true,
			tableName:   trackingTable,
			events: []exporter.ExportableEvent{
				exporter.TrackingEvent{Kind: tracking},
			},
			wantEnsureTableCalls: []ensureTableCall{
				{dataset: fakeDataset, table: trackingTable, schema: trackingEventSchema()},
			},
		},
		{
			name:        "AutoMigrate false does not ensure tables",
			autoMigrate: false,
			tableName:   featureTable,
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{Kind: feature, Value: true},
			},
			wantEnsureTableCalls: nil,
		},
		{
			name:        "AutoMigrate true skips empty batches",
			autoMigrate: true,
			tableName:   featureTable,
			events:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.TODO()
			client := newMockBigQueryClient()
			exp := &Exporter{
				ProjectID:     fakeProject,
				DatasetID:     fakeDataset,
				TableName:     tt.tableName,
				AutoMigrate:   tt.autoMigrate,
				newClientFunc: mockNewClientFunc(client),
			}

			err := exp.Export(ctx, nil, tt.events)

			require.NoError(t, err)
			assert.Equal(t, tt.wantEnsureTableCalls, client.ensureTableCalls)
		})
	}
}

func TestExporter_DefaultTableName(t *testing.T) {
	t.Parallel()

	ctx := context.TODO()
	client := newMockBigQueryClient()
	exp := &Exporter{
		ProjectID:     fakeProject,
		DatasetID:     fakeDataset,
		newClientFunc: mockNewClientFunc(client),
	}
	events := []exporter.ExportableEvent{
		exporter.FeatureEvent{Kind: feature, Value: true},
	}

	err := exp.Export(ctx, nil, events)

	require.NoError(t, err)
	assert.Len(t, client.inserters["fake-dataset."+defaultTableName].puts, 1)
}

func TestExporter_ExportSkipsEmptyBatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		events         []exporter.ExportableEvent
		wantClientInit bool
	}{
		{
			name:           "empty event list",
			events:         nil,
			wantClientInit: false,
		},
		{
			name: "unsupported event type",
			events: []exporter.ExportableEvent{
				unsupportedEvent{key: "ignored"},
			},
			wantClientInit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			initCalled := false
			exp := &Exporter{
				ProjectID: fakeProject,
				DatasetID: fakeDataset,
				newClientFunc: func(
					context.Context,
					string,
					...option.ClientOption,
				) (bigQueryClient, error) {
					initCalled = true

					return newMockBigQueryClient(), nil
				},
			}

			err := exp.Export(context.TODO(), nil, tt.events)

			require.NoError(t, err)
			assert.Equal(t, tt.wantClientInit, initCalled)
			assert.Nil(t, exp.client)
		})
	}
}

func TestExporter_ExportErrors(t *testing.T) {
	t.Parallel()

	clientErr := errClientInitFailed
	ensureErr := errEnsureTableFailed
	putErr := errPutRowsFailed

	tests := []struct {
		newClientErr        error
		client              *mockBigQueryClient
		name                string
		tableName           string
		wantErrContains     string
		events              []exporter.ExportableEvent
		autoMigrate         bool
		wantNewClientCalled bool
	}{
		{
			name: "client initialization error",
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{Kind: feature, Key: flagKey, Value: true},
			},
			newClientErr:        clientErr,
			wantErrContains:     "client init failed",
			wantNewClientCalled: true,
		},
		{
			name: "feature value marshal error",
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{Kind: feature, Value: func() {}},
			},
			wantErrContains: unsupportedTypeFunc,
		},
		{
			name: "feature metadata marshal error",
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{
					Kind:     feature,
					Value:    true,
					Metadata: map[string]any{bad: func() {}},
				},
			},
			wantErrContains: unsupportedTypeFunc,
		},
		{
			name: "tracking details marshal error",
			events: []exporter.ExportableEvent{
				exporter.TrackingEvent{
					Kind:            tracking,
					TrackingDetails: map[string]any{bad: func() {}},
				},
			},
			wantErrContains: unsupportedTypeFunc,
		},
		{
			name: "tracking evaluation context marshal error",
			events: []exporter.ExportableEvent{
				exporter.TrackingEvent{
					Kind:              tracking,
					TrackingDetails:   map[string]any{amount: 42},
					EvaluationContext: map[string]any{bad: func() {}},
				},
			},
			wantErrContains: unsupportedTypeFunc,
		},
		{
			name:        "auto migrate returns error",
			autoMigrate: true,
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{Kind: feature, Key: flagKey, Value: true},
			},
			client:              &mockBigQueryClient{ensureTableErr: ensureErr},
			wantErrContains:     "ensure table failed",
			wantNewClientCalled: true,
		},
		{
			name: "feature insert returns error",
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{Kind: feature, Key: flagKey, Value: true},
			},
			client:              &mockBigQueryClient{putErr: putErr},
			wantErrContains:     putRowsFailed,
			wantNewClientCalled: true,
		},
		{
			name: "tracking insert returns error",
			events: []exporter.ExportableEvent{
				exporter.TrackingEvent{
					Kind:              tracking,
					Key:               checkout,
					TrackingDetails:   map[string]any{amount: 42},
					EvaluationContext: map[string]any{country: "FR"},
				},
			},
			client: &mockBigQueryClient{
				putErrByTable: map[string]error{fakeDatasetTrackingTable: putErr},
			},
			tableName:           trackingTable,
			wantErrContains:     putRowsFailed,
			wantNewClientCalled: true,
		},
		{
			name: "mixed event types returns error",
			events: []exporter.ExportableEvent{
				exporter.FeatureEvent{Kind: feature, Key: flagKey, Value: true},
				exporter.TrackingEvent{Kind: tracking, Key: checkout},
			},
			wantErrContains:     "mixed event types",
			wantNewClientCalled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := tt.client
			if client == nil {
				client = newMockBigQueryClient()
			}

			client.ensureInitialized()

			newClientCalled := false

			tableName := tt.tableName
			if tableName == "" {
				tableName = featureTable
			}

			exp := &Exporter{
				ProjectID:   fakeProject,
				DatasetID:   fakeDataset,
				TableName:   tableName,
				AutoMigrate: tt.autoMigrate,
				newClientFunc: func(
					context.Context,
					string,
					...option.ClientOption,
				) (bigQueryClient, error) {
					newClientCalled = true

					if tt.newClientErr != nil {
						return nil, tt.newClientErr
					}

					return client, nil
				},
			}

			err := exp.Export(context.TODO(), nil, tt.events)

			require.ErrorContains(t, err, tt.wantErrContains)
			assert.Equal(t, tt.wantNewClientCalled, newClientCalled)
		})
	}
}

func TestExporter_ExportChunksRows(t *testing.T) {
	t.Parallel()

	client := newMockBigQueryClient()
	exp := &Exporter{
		ProjectID:     fakeProject,
		DatasetID:     fakeDataset,
		TableName:     featureTable,
		newClientFunc: mockNewClientFunc(client),
	}

	events := make([]exporter.ExportableEvent, 0, maxRowsPerInsert+3)
	for i := range maxRowsPerInsert + 3 {
		events = append(events, exporter.FeatureEvent{
			Kind:         feature,
			UserKey:      fmt.Sprintf("user-%d", i),
			CreationDate: int64(i),
			Key:          flagKey,
			Value:        true,
		})
	}

	err := exp.Export(context.TODO(), nil, events)

	require.NoError(t, err)

	puts := client.inserters[fakeDatasetFeatureTable].puts
	require.Len(t, puts, 2)
	rows0, ok := puts[0].([]rowSaver)
	require.True(t, ok)
	rows1, ok := puts[1].([]rowSaver)
	require.True(t, ok)
	assert.Len(t, rows0, maxRowsPerInsert)
	assert.Len(t, rows1, 3)
}

func TestExporter_InitClientOnce(t *testing.T) {
	t.Parallel()

	client := newMockBigQueryClient()

	var callCount atomic.Int32

	exp := &Exporter{
		ProjectID: fakeProject,
		DatasetID: fakeDataset,
		newClientFunc: func(
			context.Context,
			string,
			...option.ClientOption,
		) (bigQueryClient, error) {
			callCount.Add(1)

			return client, nil
		},
	}

	const goroutines = 50

	errs := make(chan error, goroutines)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			errs <- exp.initClient(context.TODO())
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	assert.Equal(t, int32(1), callCount.Load())
	assert.Same(t, client, exp.client)
}

func TestExporter_InitClientPassesCredentialsOptions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		credentials       []byte
		wantClientOptions int
	}{
		{
			name:              "application default credentials",
			wantClientOptions: 0,
		},
		{
			name:              "explicit credentials",
			credentials:       []byte(`{"type":"authorized_user"}`),
			wantClientOptions: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				gotProjectID     string
				gotClientOptions int
			)

			client := newMockBigQueryClient()
			exp := &Exporter{
				ProjectID:         fakeProject,
				DatasetID:         fakeDataset,
				TableName:         featureTable,
				GoogleCredentials: tt.credentials,
				newClientFunc: func(
					_ context.Context,
					projectID string,
					opts ...option.ClientOption,
				) (bigQueryClient, error) {
					gotProjectID = projectID
					gotClientOptions = len(opts)

					return client, nil
				},
			}

			err := exp.Export(context.TODO(), nil, []exporter.ExportableEvent{
				exporter.FeatureEvent{Kind: feature, Value: true},
			})

			require.NoError(t, err)
			assert.Equal(t, fakeProject, gotProjectID)
			assert.Equal(t, tt.wantClientOptions, gotClientOptions)
		})
	}
}

func TestCredentialsType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		want  option.CredentialsType
		creds []byte
	}{
		{
			name:  "authorized user",
			creds: []byte(`{"type":"authorized_user"}`),
			want:  option.AuthorizedUser,
		},
		{
			name:  "impersonated service account",
			creds: []byte(`{"type":"impersonated_service_account"}`),
			want:  option.ImpersonatedServiceAccount,
		},
		{
			name:  "external account",
			creds: []byte(`{"type":"external_account"}`),
			want:  option.ExternalAccount,
		},
		{
			name:  "service account",
			creds: []byte(`{"type":"service_account"}`),
			want:  option.ServiceAccount,
		},
		{
			name:  "missing type",
			creds: []byte(`{"client_email":"test@example.com"}`),
			want:  option.ServiceAccount,
		},
		{
			name:  "invalid json",
			creds: []byte(`{`),
			want:  option.ServiceAccount,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, credentialsType(tt.creds))
		})
	}
}

func TestRowSaver_Save(t *testing.T) {
	t.Parallel()

	row := rowSaver{
		values:   map[string]bigquery.Value{colKey: flagKey},
		insertID: "flag-key|user-key|123",
	}

	values, insertID, err := row.Save()

	require.NoError(t, err)
	assert.Equal(t, row.values, values)
	assert.Equal(t, row.insertID, insertID)
}

func TestIsAlreadyExists(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err  error
		name string
		want bool
	}{
		{
			name: "google already exists error",
			err:  &googleapi.Error{Code: 409},
			want: true,
		},
		{
			name: "wrapped google already exists error",
			err:  fmt.Errorf("wrapped: %w", &googleapi.Error{Code: 409}),
			want: true,
		},
		{
			name: "different google error",
			err:  &googleapi.Error{Code: 403},
			want: false,
		},
		{
			name: "non google error",
			err:  errBoom,
			want: false,
		},
		{
			name: "nil error",
			err:  nil,
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, isAlreadyExists(tt.err))
		})
	}
}

type unsupportedEvent struct {
	key string
}

func (unsupportedEvent) GetUserKey() string {
	return userKey
}

func (u unsupportedEvent) GetKey() string {
	return u.key
}

func (unsupportedEvent) GetCreationDate() int64 {
	return 123
}

func (unsupportedEvent) FormatInCSV(_ *template.Template) ([]byte, error) {
	return nil, nil
}

func (unsupportedEvent) FormatInJSON() ([]byte, error) {
	return nil, nil
}

type ensureTableCall struct {
	dataset string
	table   string
	schema  bigquery.Schema
}

type mockBigQueryClient struct {
	ensureTableErr   error
	putErr           error
	inserters        map[string]*mockInserter
	putErrByTable    map[string]error
	ensureTableCalls []ensureTableCall
}

func newMockBigQueryClient() *mockBigQueryClient {
	client := &mockBigQueryClient{}
	client.ensureInitialized()

	return client
}

func (m *mockBigQueryClient) Put(ctx context.Context, dataset, table string, src any) error {
	return m.Inserter(dataset, table).Put(ctx, src)
}

func (m *mockBigQueryClient) Inserter(dataset, table string) *mockInserter {
	m.ensureInitialized()

	key := dataset + "." + table
	if m.inserters[key] == nil {
		m.inserters[key] = &mockInserter{err: m.putErr}
		if m.putErrByTable != nil {
			m.inserters[key].err = m.putErrByTable[key]
		}
	}

	return m.inserters[key]
}

func (m *mockBigQueryClient) EnsureTable(
	_ context.Context,
	dataset string,
	table string,
	schema bigquery.Schema,
) error {
	if m.ensureTableErr != nil {
		return m.ensureTableErr
	}

	m.ensureTableCalls = append(m.ensureTableCalls, ensureTableCall{
		dataset: dataset,
		table:   table,
		schema:  schema,
	})

	return nil
}

func (*mockBigQueryClient) Close() error {
	return nil
}

func (m *mockBigQueryClient) ensureInitialized() {
	if m.inserters == nil {
		m.inserters = make(map[string]*mockInserter)
	}
}

type mockInserter struct {
	err  error
	puts []any
}

func (m *mockInserter) Put(_ context.Context, src any) error {
	if m.err != nil {
		return m.err
	}

	m.puts = append(m.puts, src)

	return nil
}

func mockNewClientFunc(
	client bigQueryClient,
) func(context.Context, string, ...option.ClientOption) (bigQueryClient, error) {
	return func(_ context.Context, _ string, _ ...option.ClientOption) (bigQueryClient, error) {
		return client, nil
	}
}
