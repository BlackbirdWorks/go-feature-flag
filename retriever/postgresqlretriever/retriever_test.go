//go:build docker

package postgresqlretriever_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	testcontainerPostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/blackbirdworks/semaphore/retriever"
	"github.com/blackbirdworks/semaphore/retriever/postgresqlretriever"
)

func TestPostgreSQLRetriever(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		files     []string
		tableName string
		columns   map[string]string
		flagset   string
		assertErr assert.ErrorAssertionFunc
		want      string
	}{
		{
			name: "valid set of flag",
			files: []string{
				"sql/init.sql",
				"sql/insert_data.sql",
			},
			tableName: "go_feature_flag",
			assertErr: assert.NoError,
			want:      "response/valid.json",
		},
		{
			name: "invalid table name",
			files: []string{
				"sql/init.sql",
				"sql/insert_data.sql",
			},
			tableName: "invalid_table_name",
			assertErr: assert.Error,
		},
		{
			name: "invalid column names",
			files: []string{
				"sql/init.sql",
				"sql/insert_data.sql",
			},
			assertErr: assert.Error,
			columns: map[string]string{
				"flag_name": "invalid_flag_name",
				"flagset":   "invalid_flagset",
				"config":    "invalid_config",
			},
		},
		{
			name: "valid set of flag with flagset",
			files: []string{
				"sql/init.sql",
				"sql/insert_data.sql",
				"sql/insert_alternative_flagset.sql",
			},
			tableName: "go_feature_flag",
			assertErr: assert.NoError,
			flagset:   "team-A",
			want:      "response/valid_alternative_flagset.json",
		},
		{
			name: "valid set of flag with empty flagset",
			files: []string{
				"sql/init.sql",
				"sql/insert_data.sql",
			},
			tableName: "go_feature_flag",
			assertErr: assert.NoError,
			flagset:   "empty-flagset",
			want:      "response/empty-flagset.json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			connectionString := startPostgreSQLAndAddData(t, tt.files)

			r := postgresqlretriever.Retriever{
				URI:     connectionString,
				Table:   tt.tableName,
				Columns: tt.columns,
			}
			require.NoError(t, r.Init(context.TODO(), nil, &tt.flagset))
			defer func() {
				assert.NoError(t, r.Shutdown(context.TODO()))
			}()

			assert.Equal(t, retriever.RetrieverReady, r.Status())
			got, err := r.Retrieve(context.TODO())
			tt.assertErr(t, err)
			if err != nil {
				return
			}

			want, err := os.ReadFile(filepath.Join("testdata", tt.want))
			require.NoError(t, err)
			assert.JSONEq(t, string(want), string(got))
		})
	}
}

// startPostgreSQLAndAddData starts a PostgreSQL container, removed when the test ends, and loads the files in it.
func startPostgreSQLAndAddData(t *testing.T, files []string) string {
	t.Helper()
	ctx := context.TODO()
	postgreSQLContainer, err := testcontainerPostgres.Run(ctx, "postgres:15-alpine")
	require.NoError(t, err)

	connectionString, err := postgreSQLContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, postgreSQLContainer.Terminate(context.TODO())) })

	// Retry connection with backoff
	var conn *pgx.Conn
	maxRetries := 5
	for i := range maxRetries {
		conn, err = pgx.Connect(ctx, connectionString)
		if err == nil {
			break
		}
		if i < maxRetries-1 {
			time.Sleep(time.Duration(i+1) * time.Second)
		}
	}
	require.NoError(t, err, "Failed to connect to database after %d retries", maxRetries)
	defer func() {
		if conn != nil {
			conn.Close(ctx)
		}
	}()

	for _, file := range files {
		content, readFileErr := os.ReadFile(filepath.Join("testdata", file))
		require.NoError(t, readFileErr)

		// Execute the SQL content
		_, readFileErr = conn.Exec(ctx, string(content))
		assert.NoError(t, readFileErr)
	}

	return connectionString
}

// TestRetrieverErrorHandling tests various error conditions in the PostgreSQL retriever.
func TestRetrieverErrorHandling(t *testing.T) {
	t.Parallel()

	t.Run("Init - Invalid connection URI", func(t *testing.T) {
		t.Parallel()

		r := postgresqlretriever.Retriever{
			URI:   "invalid-connection-string",
			Table: "test_table",
		}

		err := r.Init(context.Background(), nil, nil)
		require.Error(t, err)
		assert.Equal(t, retriever.RetrieverError, r.Status())
	})

	t.Run("Init - Connection timeout", func(t *testing.T) {
		t.Parallel()

		// Use a non-routable IP address to simulate connection timeout
		r := postgresqlretriever.Retriever{
			URI:   "postgres://user:pass@192.0.2.1:5432/db", // RFC5737 test address
			Table: "test_table",
		}

		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()

		err := r.Init(ctx, nil, nil)
		require.Error(t, err)
		assert.Equal(t, retriever.RetrieverError, r.Status())
	})

	t.Run("Init - Ping failure", func(t *testing.T) {
		t.Parallel()

		// This test uses a malformed URI that allows connection but fails on ping
		r := postgresqlretriever.Retriever{
			URI:   "postgres://nonexistentuser:wrongpass@localhost:9999/nonexistentdb",
			Table: "test_table",
		}

		err := r.Init(context.Background(), nil, nil)
		require.Error(t, err)
		assert.Equal(t, retriever.RetrieverError, r.Status())
	})

	t.Run("Retrieve - nil connection", func(t *testing.T) {
		t.Parallel()

		r := postgresqlretriever.Retriever{
			URI:   "postgres://user:pass@localhost:5432/db",
			Table: "test_table",
		}
		// Don't call Init() to ensure conn is nil

		_, err := r.Retrieve(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "database connection pool is not initialized")
	})

	t.Run("Status - nil receiver", func(t *testing.T) {
		t.Parallel()

		var r *postgresqlretriever.Retriever
		status := r.Status()
		assert.Equal(t, retriever.RetrieverNotReady, status)
	})

	t.Run("Status - empty status", func(t *testing.T) {
		t.Parallel()

		r := &postgresqlretriever.Retriever{}
		status := r.Status()
		assert.Equal(t, retriever.RetrieverNotReady, status)
	})

	t.Run("Shutdown - nil connection", func(t *testing.T) {
		t.Parallel()

		r := postgresqlretriever.Retriever{
			URI:   "postgres://user:pass@localhost:5432/db",
			Table: "test_table",
		}
		// Don't call Init() to ensure conn is nil

		err := r.Shutdown(context.Background())
		require.NoError(t, err) // Should not error when connection is nil
		assert.Equal(t, retriever.RetrieverNotReady, r.Status())
	})
}

// TestRetrieverQueryErrors tests query-related error conditions using a real PostgreSQL container.
func TestRetrieverQueryErrors(t *testing.T) {
	t.Parallel()

	connectionString := startPostgreSQLAndAddData(t, []string{"sql/init.sql", "sql/insert_data.sql"})

	t.Run("Query execution error - invalid table", func(t *testing.T) {
		t.Parallel()

		r := postgresqlretriever.Retriever{
			URI:   connectionString,
			Table: "nonexistent_table",
		}

		err := r.Init(context.Background(), nil, nil)
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, r.Shutdown(context.Background()))
		}()

		_, err = r.Retrieve(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute query")
	})

	t.Run("Query execution error - invalid columns", func(t *testing.T) {
		t.Parallel()

		r := postgresqlretriever.Retriever{
			URI:   connectionString,
			Table: "go_feature_flag",
			Columns: map[string]string{
				"flag_name": "nonexistent_column",
				"config":    "config",
			},
		}

		err := r.Init(context.Background(), nil, nil)
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, r.Shutdown(context.Background()))
		}()

		_, err = r.Retrieve(context.Background())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute query")
	})

	t.Run("Context cancellation during query", func(t *testing.T) {
		t.Parallel()

		r := postgresqlretriever.Retriever{
			URI:   connectionString,
			Table: "go_feature_flag",
		}

		err := r.Init(context.Background(), nil, nil)
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, r.Shutdown(context.Background()))
		}()

		// Create a context that is immediately cancelled
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err = r.Retrieve(ctx)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to execute query")
	})
}

// TestRetrieverDataHandlingErrors tests data processing error conditions.
func TestRetrieverDataHandlingErrors(t *testing.T) {
	t.Parallel()

	// Create a test container with some test data
	connectionString := startPostgreSQLAndAddData(t, []string{"sql/init.sql"})

	// Insert some test data
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, connectionString)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close(ctx) })

	// Insert a row with malformed JSON that will cause unmarshaling issues
	// Using a JSON string that contains control characters that may cause issues
	_, err = conn.Exec(ctx, `
		INSERT INTO go_feature_flag (flag_name, flagset, config) 
		VALUES ('problematic_flag', 'default', '{"key": "value\nwith\nnewlines"}')
	`)
	require.NoError(t, err)

	// Insert a row with valid JSON for comparison
	_, err = conn.Exec(ctx, `
		INSERT INTO go_feature_flag (flag_name, flagset, config) 
		VALUES ('valid_flag', 'default', '{"valid": "json"}')
	`)
	require.NoError(t, err)

	t.Run("Data processing with special characters", func(t *testing.T) {
		t.Parallel()

		r := postgresqlretriever.Retriever{
			URI:   connectionString,
			Table: "go_feature_flag",
		}

		initErr := r.Init(context.Background(), nil, nil)
		require.NoError(t, initErr)
		defer func() {
			assert.NoError(t, r.Shutdown(context.Background()))
		}()

		// This should handle the data gracefully
		result, initErr := r.Retrieve(context.Background())
		require.NoError(t, initErr)

		// Should contain both flags since the JSON is valid
		assert.Contains(t, string(result), "valid_flag")
		assert.Contains(t, string(result), "problematic_flag")
	})

	t.Run("Context timeout during query execution", func(t *testing.T) {
		t.Parallel()

		r := postgresqlretriever.Retriever{
			URI:   connectionString,
			Table: "go_feature_flag",
		}

		initErr := r.Init(context.Background(), nil, nil)
		require.NoError(t, initErr)
		defer func() {
			assert.NoError(t, r.Shutdown(context.Background()))
		}()

		// Create a context with a very short timeout to cause timeout errors
		shortCtx, cancel := context.WithTimeout(context.Background(), 1*time.Microsecond)
		defer cancel()

		// This should cause a timeout error during query execution
		_, initErr = r.Retrieve(shortCtx)
		// We expect either success (if query completes quickly) or timeout error
		if initErr != nil {
			assert.Contains(t, initErr.Error(), "failed to execute query")
		}
	})
}

// TestRetrieverEdgeCases tests additional edge cases and boundary conditions.
func TestRetrieverEdgeCases(t *testing.T) {
	t.Parallel()

	connectionString := startPostgreSQLAndAddData(t, []string{"sql/init.sql"})

	t.Run("Empty result set", func(t *testing.T) {
		t.Parallel()

		r := postgresqlretriever.Retriever{
			URI:   connectionString,
			Table: "go_feature_flag", // Empty table
		}

		err := r.Init(context.Background(), nil, nil)
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, r.Shutdown(context.Background()))
		}()

		result, err := r.Retrieve(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "{}", string(result))
	})

	t.Run("Flagset filtering with no matches", func(t *testing.T) {
		t.Parallel()

		// Insert some data first, in a table of its own: the sibling subtests run in parallel
		// and one of them expects go_feature_flag to stay empty.
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, connectionString)
		require.NoError(t, err)
		defer conn.Close(ctx)

		_, err = conn.Exec(ctx, `CREATE TABLE flagset_filter_flags (LIKE go_feature_flag INCLUDING ALL)`)
		require.NoError(t, err)
		_, err = conn.Exec(ctx, `
			INSERT INTO flagset_filter_flags (flag_name, flagset, config) 
			VALUES ('test_flag', 'production', '{"enabled": true}')
		`)
		require.NoError(t, err)

		// Query with a flagset that doesn't exist
		nonExistentFlagset := "non-existent-flagset"
		r := postgresqlretriever.Retriever{
			URI:   connectionString,
			Table: "flagset_filter_flags",
		}

		err = r.Init(context.Background(), nil, &nonExistentFlagset)
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, r.Shutdown(context.Background()))
		}()

		result, err := r.Retrieve(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "{}", string(result))
	})

	t.Run("Multiple Init calls", func(t *testing.T) {
		t.Parallel()

		r := postgresqlretriever.Retriever{
			URI:   connectionString,
			Table: "go_feature_flag",
		}

		// First init should succeed
		err := r.Init(context.Background(), nil, nil)
		require.NoError(t, err)
		assert.Equal(t, retriever.RetrieverReady, r.Status())

		// Second init should also succeed (should reuse connection)
		err = r.Init(context.Background(), nil, nil)
		require.NoError(t, err)
		assert.Equal(t, retriever.RetrieverReady, r.Status())

		defer func() {
			assert.NoError(t, r.Shutdown(context.Background()))
		}()
	})

	t.Run("Custom column names", func(t *testing.T) {
		t.Parallel()

		// Create a table with different column names
		ctx := context.Background()
		conn, err := pgx.Connect(ctx, connectionString)
		require.NoError(t, err)
		defer conn.Close(ctx)

		_, err = conn.Exec(ctx, `
			CREATE TABLE custom_flags (
				name TEXT PRIMARY KEY,
				environment TEXT,
				configuration JSONB
			)
		`)
		require.NoError(t, err)

		_, err = conn.Exec(ctx, `
			INSERT INTO custom_flags (name, environment, configuration) 
			VALUES ('custom_flag', 'dev', '{"enabled": false}')
		`)
		require.NoError(t, err)

		r := postgresqlretriever.Retriever{
			URI:   connectionString,
			Table: "custom_flags",
			Columns: map[string]string{
				"flag_name": "name",
				"flagset":   "environment",
				"config":    "configuration",
			},
		}

		err = r.Init(context.Background(), nil, nil)
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, r.Shutdown(context.Background()))
		}()

		result, err := r.Retrieve(context.Background())
		require.NoError(t, err)
		assert.Contains(t, string(result), "custom_flag")
	})
}

// TestRetrieverShutdownErrors tests shutdown error conditions.
func TestRetrieverShutdownErrors(t *testing.T) {
	t.Parallel()

	connectionString := startPostgreSQLAndAddData(t, []string{"sql/init.sql"})

	t.Run("Shutdown with active connection", func(t *testing.T) {
		t.Parallel()

		r := postgresqlretriever.Retriever{
			URI:   connectionString,
			Table: "go_feature_flag",
		}

		err := r.Init(context.Background(), nil, nil)
		require.NoError(t, err)

		// Shutdown should succeed
		err = r.Shutdown(context.Background())
		require.NoError(t, err)

		assert.Equal(t, retriever.RetrieverNotReady, r.Status())

		// Second shutdown should also succeed (should not error on nil connection)
		err = r.Shutdown(context.Background())
		require.NoError(t, err)
		assert.Equal(t, retriever.RetrieverNotReady, r.Status())
	})

	t.Run("Shutdown with cancelled context", func(t *testing.T) {
		t.Parallel()

		r := postgresqlretriever.Retriever{
			URI:   connectionString,
			Table: "go_feature_flag",
		}

		err := r.Init(context.Background(), nil, nil)
		require.NoError(t, err)

		// Create a cancelled context
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		// Shutdown with cancelled context should still succeed
		err = r.Shutdown(ctx)
		require.NoError(t, err)
		assert.Equal(t, retriever.RetrieverNotReady, r.Status())
	})
}

func TestPostgreSQLRetriever_OutputFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
	}{
		{
			name: "returns json format hint for manager parser selection",
			want: "json",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := postgresqlretriever.Retriever{}
			assert.Equal(t, tt.want, r.OutputFormat())
		})
	}
}

// TestRetrieverShutdownOfASharedPool checks that the retrievers that share a pool registry only release their
// own reference: the pool stays open for the others until the last one shuts down.
func TestRetrieverShutdownOfASharedPool(t *testing.T) {
	t.Parallel()

	connectionString := startPostgreSQLAndAddData(t, []string{"sql/init.sql", "sql/insert_data.sql"})

	pools := postgresqlretriever.NewPoolRegistry()
	first := &postgresqlretriever.Retriever{URI: connectionString, Table: "go_feature_flag", Pools: pools}
	second := &postgresqlretriever.Retriever{URI: connectionString, Table: "go_feature_flag", Pools: pools}
	require.NoError(t, first.Init(context.Background(), nil, nil))
	require.NoError(t, second.Init(context.Background(), nil, nil))
	want, err := second.Retrieve(context.Background())
	require.NoError(t, err)
	// the test holds a reference of its own: the pool is only closed when the last reference is released, which
	// shows how many references the shutdowns released
	pool, err := pools.GetPool(context.Background(), connectionString)
	require.NoError(t, err)

	tests := []struct {
		name            string
		shutdown        *postgresqlretriever.Retriever
		release         bool // the test releases its own reference after the shutdown
		other           *postgresqlretriever.Retriever
		wantStatus      retriever.Status
		wantOtherStatus retriever.Status
		wantOtherErr    string
		wantPoolOpen    bool
	}{
		{
			name: "the first retriever leaves, the second keeps the pool", shutdown: first, other: second,
			wantStatus: retriever.RetrieverNotReady, wantOtherStatus: retriever.RetrieverReady, wantPoolOpen: true,
		},
		{
			name: "the second retriever leaves, the reference of the test keeps the pool", shutdown: second,
			other: first, wantStatus: retriever.RetrieverNotReady, wantOtherStatus: retriever.RetrieverNotReady,
			wantOtherErr: "database connection pool is not initialized", wantPoolOpen: true,
		},
		{
			name: "the last reference is released, the pool is closed", release: true, shutdown: first, other: first,
			wantStatus: retriever.RetrieverNotReady, wantOtherStatus: retriever.RetrieverNotReady,
			wantOtherErr: "database connection pool is not initialized", wantPoolOpen: false,
		},
	}
	// sequential: each case depends on the previous shutdowns
	for _, tt := range tests {
		require.NoError(t, tt.shutdown.Shutdown(context.Background()), tt.name)
		if tt.release {
			pools.ReleasePool(context.Background(), connectionString)
		}
		assert.Equal(t, tt.wantStatus, tt.shutdown.Status(), tt.name)
		assert.Equal(t, tt.wantOtherStatus, tt.other.Status(), tt.name)
		assert.Equal(t, tt.wantPoolOpen, pool.Ping(context.Background()) == nil, tt.name)

		got, retrieveErr := tt.other.Retrieve(context.Background())
		if tt.wantOtherErr != "" {
			require.EqualError(t, retrieveErr, tt.wantOtherErr, tt.name)

			continue
		}
		require.NoError(t, retrieveErr, tt.name)
		assert.JSONEq(t, string(want), string(got), tt.name)
	}
}
